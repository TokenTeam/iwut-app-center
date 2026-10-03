package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"

	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	oauthport "iwut-app-center/internal/oauthclient/port"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
)

type OAuthProviderRepository struct {
	database *drivermongo.Database
	secrets  oauthport.SecretVerifier
}

func NewOAuthProviderRepository(database *drivermongo.Database, secrets oauthport.SecretVerifier) *OAuthProviderRepository {
	return &OAuthProviderRepository{database: database, secrets: secrets}
}

var _ oauthport.ProviderRepository = (*OAuthProviderRepository)(nil)

func (r *OAuthProviderRepository) snapshot(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	if r == nil || r.database == nil {
		return nil, fmt.Errorf("oauth provider repository is nil")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeOAuthProviderError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, fn, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, safeOAuthProviderError(err)
	}
	return result, nil
}

func (r *OAuthProviderRepository) GetClientConfiguration(ctx context.Context, clientID oauthdomain.ClientID) (*oauthdomain.ClientConfiguration, error) {
	result, err := r.snapshot(ctx, func(tx context.Context) (any, error) { return r.clientConfiguration(tx, clientID, false) })
	if err != nil {
		return nil, err
	}
	return result.(*oauthdomain.ClientConfiguration), nil
}

func (r *OAuthProviderRepository) clientConfiguration(ctx context.Context, clientID oauthdomain.ClientID, hideUnknown bool) (*oauthdomain.ClientConfiguration, error) {
	document, err := (&OAuthClientRepository{database: r.database}).registrationByClient(ctx, clientID)
	if errors.Is(err, oauthport.ErrClientNotFound) && hideUnknown {
		return nil, oauthport.ErrRuntimeUnavailable
	}
	if err != nil {
		return nil, err
	}
	registration, err := registrationFromDocument(document)
	if err != nil {
		return nil, oauthport.ErrStateInconsistent
	}
	identity := registration.ClientByID(clientID)
	if identity == nil {
		return nil, oauthport.ErrStateInconsistent
	}
	var credentialRevision *int64
	if identity.Type() == oauthdomain.ClientTypeConfidentialSecret {
		var credentialDocument oauthClientCredentialDocument
		err = r.database.Collection(oauthClientCredentialsCollectionName).FindOne(ctx, bson.M{"clientId": clientID.String()}).Decode(&credentialDocument)
		if err != nil {
			return nil, missingOAuthProviderFact(err)
		}
		credential, restoreErr := credentialFromDocument(credentialDocument)
		if restoreErr != nil || credential.ApplicationID() != registration.ApplicationID() {
			return nil, oauthport.ErrStateInconsistent
		}
		value := credential.Revision()
		credentialRevision = &value
	}
	configuration, err := oauthdomain.NewClientConfiguration(registration, clientID, credentialRevision)
	if err != nil {
		return nil, oauthport.ErrStateInconsistent
	}
	return configuration, nil
}

func (r *OAuthProviderRepository) VerifyClientSecret(ctx context.Context, clientID oauthdomain.ClientID, plain string, expected int64) (bool, int64, error) {
	result, err := r.snapshot(ctx, func(tx context.Context) (any, error) {
		configuration, configErr := r.clientConfiguration(tx, clientID, true)
		if errors.Is(configErr, oauthport.ErrRuntimeUnavailable) {
			return secretVerification{}, nil
		}
		if configErr != nil {
			return nil, configErr
		}
		if configuration.Type != oauthdomain.ClientTypeConfidentialSecret || configuration.Status != oauthdomain.ClientStatusActive || configuration.CredentialRevision == nil {
			return secretVerification{}, nil
		}
		var document oauthClientCredentialDocument
		if err := r.database.Collection(oauthClientCredentialsCollectionName).FindOne(tx, bson.M{"clientId": clientID.String()}).Decode(&document); err != nil {
			return nil, missingOAuthProviderFact(err)
		}
		credential, restoreErr := credentialFromDocument(document)
		if restoreErr != nil || credential.ApplicationID() != configuration.ApplicationID {
			return nil, oauthport.ErrStateInconsistent
		}
		verified := credential.Revision() == expected && r.secrets != nil && r.secrets.Verify(clientID, plain, credential.Digest())
		return secretVerification{verified, credential.Revision()}, nil
	})
	if err != nil {
		return false, 0, err
	}
	verification := result.(secretVerification)
	return verification.verified, verification.revision, nil
}

type secretVerification struct {
	verified bool
	revision int64
}

func (r *OAuthProviderRepository) ResolveRuntime(ctx context.Context, clientID oauthdomain.ClientID, channel oauthdomain.Channel, major int32, expected int64, observedAt time.Time) (*oauthdomain.RuntimeConfiguration, error) {
	result, err := r.snapshot(ctx, func(tx context.Context) (any, error) {
		return r.resolveRuntimeSnapshot(tx, clientID, channel, major, expected, observedAt)
	})
	if err != nil {
		return nil, err
	}
	return result.(*oauthdomain.RuntimeConfiguration), nil
}

func (r *OAuthProviderRepository) ResolveAuthorizationContext(ctx context.Context, clientID oauthdomain.ClientID, authID shared.AuthID, channel oauthdomain.Channel, major int32, expected int64, expectedVersion oauthdomain.RuntimeVersion, observedAt time.Time) (*oauthdomain.AuthorizationContext, error) {
	result, err := r.snapshot(ctx, func(tx context.Context) (any, error) {
		runtime, err := r.resolveRuntimeSnapshot(tx, clientID, channel, major, expected, observedAt)
		if err != nil {
			return nil, err
		}
		if !runtime.Version().Equal(expectedVersion) {
			return nil, oauthport.ErrRuntimeVersionChanged
		}
		var membership applicationTesterMembershipDocument
		err = r.database.Collection(applicationTesterMembershipsCollectionName).FindOne(tx, bson.M{"applicationId": runtime.ApplicationID.String(), "testerAuthId": authID.String(), "status": "ACTIVE"}).Decode(&membership)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, oauthport.ErrRuntimeUnavailable
		}
		if err != nil {
			return nil, err
		}
		restored, restoreErr := testerMembershipFromDocument(membership)
		if restoreErr != nil || restored.ApplicationID() != runtime.ApplicationID || restored.TesterAuthID() != authID {
			return nil, oauthport.ErrStateInconsistent
		}
		context, createErr := oauthdomain.NewAuthorizationContext(runtime, authID, restored.MembershipID().String())
		if createErr != nil {
			return nil, oauthport.ErrStateInconsistent
		}
		return context, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*oauthdomain.AuthorizationContext), nil
}

func (r *OAuthProviderRepository) resolveRuntimeSnapshot(ctx context.Context, clientID oauthdomain.ClientID, channel oauthdomain.Channel, major int32, expected int64, observedAt time.Time) (*oauthdomain.RuntimeConfiguration, error) {
	configuration, err := r.clientConfiguration(ctx, clientID, true)
	if err != nil {
		return nil, err
	}
	if configuration.Status != oauthdomain.ClientStatusActive || configuration.Channel != channel {
		return nil, oauthport.ErrRuntimeUnavailable
	}
	if configuration.RegistrationRevision != expected {
		return nil, oauthport.ErrRuntimeVersionChanged
	}
	var app applicationDocument
	if err = r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": configuration.ApplicationID.String()}).Decode(&app); err != nil {
		return nil, missingOAuthProviderFact(err)
	}
	application, restoreErr := applicationFromDocument(app)
	if restoreErr != nil || application.ID() != configuration.ApplicationID {
		return nil, oauthport.ErrStateInconsistent
	}
	publication, snapshot, err := r.publishedVersion(ctx, configuration.ApplicationID, major)
	if err != nil {
		return nil, err
	}
	redirects := snapshot.OAuthRedirects().PKCERedirectURIs()
	if configuration.Type == oauthdomain.ClientTypeConfidentialSecret {
		redirects = snapshot.OAuthRedirects().ConfidentialRedirectURIs()
	}
	if len(redirects) == 0 {
		return nil, oauthport.ErrRuntimeUnavailable
	}
	display, err := r.currentDisplay(ctx, configuration.ApplicationID)
	if err != nil {
		return nil, err
	}
	required := make([]string, len(snapshot.RequiredScopes()))
	for index, value := range snapshot.RequiredScopes() {
		required[index] = string(value)
	}
	optional := make([]string, len(snapshot.OptionalScopes()))
	for index, value := range snapshot.OptionalScopes() {
		optional[index] = string(value)
	}
	runtime, createErr := oauthdomain.NewRuntimeConfiguration(configuration, major, application.AdminID(), publication.TestVersionID, publication.Revision, redirects, required, optional, display, observedAt)
	if createErr != nil {
		return nil, oauthport.ErrStateInconsistent
	}
	return runtime, nil
}

func (r *OAuthProviderRepository) publishedVersion(ctx context.Context, applicationID shared.ApplicationID, major int32) (applicationPublicationDocument, *reviewdomain.ApplicationVersionReviewSnapshot, error) {
	var publication applicationPublicationDocument
	err := r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "rpcApiMajor": major}).Decode(&publication)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return publication, nil, oauthport.ErrRuntimeUnavailable
	}
	if err != nil {
		return publication, nil, err
	}
	if _, err = applicationPublicationFromDocument(publication); err != nil {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	var history applicationPublicationHistoryDocument
	err = r.database.Collection(applicationPublicationHistoryCollectionName).FindOne(ctx, bson.M{"publicationId": publication.PublicationID, "publicationRevision": publication.Revision}).Decode(&history)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	if history.ApplicationID != applicationID.String() || history.RPCAPIMajor != major || history.NewVersionID != publication.TestVersionID || history.Action != "SET_TEST_VERSION" {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	var version applicationVersionDocument
	err = r.database.Collection(applicationVersionsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "versionId": publication.TestVersionID}).Decode(&version)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	var config applicationVersionOAuthConfigDocument
	err = r.database.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "applicationVersionId": publication.TestVersionID}).Decode(&config)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	if _, err = applicationVersionFromDocument(version, config); err != nil || version.ReviewStatus != "APPROVED" || major < version.RPCApiMinVersion || major >= version.RPCApiMaxVersionExclusive {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	var review applicationReviewDocument
	err = r.database.Collection(applicationReviewsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "versionId": publication.TestVersionID}, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})).Decode(&review)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	approved, restoreErr := applicationReviewFromDocument(review)
	if restoreErr != nil || review.ReviewID != history.ApprovedReviewID || review.Status != "APPROVED" || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || version.Revision != review.SourceVersionRevision+2 {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	_, _, currentSnapshot, restoreErr := applicationVersionDocumentToDecisionVersion(version, config)
	if restoreErr != nil || !currentSnapshot.Equal(approved.Snapshot()) || version.UpdatedBy != review.Decision.DecidedBy || !version.UpdatedAt.Equal(review.Decision.DecidedAt) {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	snapshot := approved.Snapshot()
	return publication, &snapshot, nil
}

func (r *OAuthProviderRepository) currentDisplay(ctx context.Context, applicationID shared.ApplicationID) (oauthdomain.ApplicationDisplay, error) {
	raw, err := r.database.Collection(applicationProfilesCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String()}).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return oauthdomain.ApplicationDisplay{}, oauthport.ErrRuntimeUnavailable
	}
	if err != nil {
		return oauthdomain.ApplicationDisplay{}, err
	}
	profile, err := profileFromRaw(raw)
	if errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
		return oauthdomain.ApplicationDisplay{}, oauthport.ErrProfileStateInconsistent
	}
	if err != nil {
		return oauthdomain.ApplicationDisplay{}, oauthport.ErrProfileStateInconsistent
	}
	if profile.CurrentPublishedProfileRevisionID == nil {
		return oauthdomain.ApplicationDisplay{}, oauthport.ErrRuntimeUnavailable
	}
	raw, err = r.database.Collection(applicationProfileRevisionsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "profileRevisionId": *profile.CurrentPublishedProfileRevisionID}).Raw()
	if err != nil {
		return oauthdomain.ApplicationDisplay{}, missingProfileFact(err)
	}
	revision, err := profileRevisionFromRaw(raw)
	if err != nil || revision.ApplicationID() != applicationID || revision.ProfileRevisionID().String() != *profile.CurrentPublishedProfileRevisionID || revision.ReviewStatus() != profiledomain.ReviewStatusApproved {
		return oauthdomain.ApplicationDisplay{}, oauthport.ErrProfileStateInconsistent
	}
	var description, icon *string
	if value := revision.Description(); value != nil {
		text := value.String()
		description = &text
	}
	if value := revision.Icon(); value != nil {
		text := value.String()
		icon = &text
	}
	display, err := oauthdomain.NewApplicationDisplay(revision.ProfileRevisionID().String(), revision.DisplayName().String(), description, icon)
	if err != nil {
		return oauthdomain.ApplicationDisplay{}, oauthport.ErrProfileStateInconsistent
	}
	return display, nil
}

func (r *OAuthProviderRepository) GetPublishedRedirects(ctx context.Context, applicationID shared.ApplicationID, observedAt time.Time) (*oauthdomain.PublishedRedirectSnapshot, error) {
	result, err := r.snapshot(ctx, func(tx context.Context) (any, error) {
		if err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": applicationID.String()}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err(); errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, oauthport.ErrApplicationNotFound
		} else if err != nil {
			return nil, err
		}
		var registration applicationOAuthRegistrationDocument
		registrationErr := r.database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "channel": "TEST"}).Decode(&registration)
		var hasPublic, hasConfidential bool
		if registrationErr == nil {
			restored, restoreErr := registrationFromDocument(registration)
			if restoreErr != nil {
				return nil, oauthport.ErrStateInconsistent
			}
			hasPublic, hasConfidential = restored.PublicClient() != nil, restored.ConfidentialClient() != nil
		} else if !errors.Is(registrationErr, drivermongo.ErrNoDocuments) {
			return nil, registrationErr
		}
		cursor, err := r.database.Collection(applicationPublicationsCollectionName).Find(tx, bson.M{"applicationId": applicationID.String()}, options.Find().SetSort(bson.D{{Key: "rpcApiMajor", Value: 1}}))
		if err != nil {
			return nil, err
		}
		defer cursor.Close(tx)
		entries := []oauthdomain.PublishedRedirectEntry{}
		redirectSet := map[string]struct{}{}
		for cursor.Next(tx) {
			var publication applicationPublicationDocument
			if err := cursor.Decode(&publication); err != nil {
				return nil, oauthport.ErrStateInconsistent
			}
			validated, snapshot, err := r.publishedVersion(tx, applicationID, publication.RPCAPIMajor)
			if err != nil {
				return nil, err
			}
			entries = append(entries, oauthdomain.PublishedRedirectEntry{
				Channel: oauthdomain.ChannelTest, RPCAPIMajor: validated.RPCAPIMajor,
				VersionID: validated.TestVersionID, PublicationRevision: validated.Revision,
			})
			if hasPublic {
				for _, uri := range snapshot.OAuthRedirects().PKCERedirectURIs() {
					redirectSet[uri] = struct{}{}
				}
			}
			if hasConfidential {
				for _, uri := range snapshot.OAuthRedirects().ConfidentialRedirectURIs() {
					redirectSet[uri] = struct{}{}
				}
			}
		}
		if err := cursor.Err(); err != nil {
			return nil, err
		}
		redirects := make([]string, 0, len(redirectSet))
		for uri := range redirectSet {
			redirects = append(redirects, uri)
		}
		sort.Strings(redirects)
		return oauthdomain.NewPublishedRedirectSnapshot(applicationID, entries, redirects, observedAt)
	})
	if err != nil {
		return nil, err
	}
	return result.(*oauthdomain.PublishedRedirectSnapshot), nil
}

func missingOAuthProviderFact(err error) error {
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return oauthport.ErrStateInconsistent
	}
	return err
}

func missingProfileFact(err error) error {
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return oauthport.ErrProfileStateInconsistent
	}
	return err
}

func safeOAuthProviderError(err error) error {
	for _, stable := range []error{oauthport.ErrApplicationNotFound, oauthport.ErrClientNotFound, oauthport.ErrStateInconsistent, oauthport.ErrRuntimeUnavailable, oauthport.ErrRuntimeVersionChanged, oauthport.ErrProfileStateInconsistent} {
		if errors.Is(err, stable) {
			return err
		}
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("oauth provider persistence failed: canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("oauth provider persistence failed: deadline exceeded")
	}
	return fmt.Errorf("oauth provider persistence failed: storage failure")
}
