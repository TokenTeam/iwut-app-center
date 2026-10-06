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
	gate, appErr := readApplicationAvailabilityGate(ctx, r.database, registration.ApplicationID().String())
	if appErr != nil {
		return nil, oauthport.ErrStateInconsistent
	}
	if gate != applicationGateAvailable {
		if hideUnknown {
			return nil, oauthport.ErrRuntimeUnavailable
		}
		return nil, oauthport.ErrApplicationNotFound
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
		if channel == oauthdomain.ChannelStable {
			context, createErr := oauthdomain.NewAuthorizationContext(runtime, authID, "")
			if createErr != nil {
				return nil, oauthport.ErrStateInconsistent
			}
			return context, nil
		}
		if channel == oauthdomain.ChannelGrey {
			matched, matchErr := r.greyCohortMatches(tx, runtime.ApplicationID, major, runtime.VersionID, authID)
			if matchErr != nil {
				return nil, matchErr
			}
			if !matched {
				return nil, oauthport.ErrRuntimeUnavailable
			}
			context, createErr := oauthdomain.NewAuthorizationContext(runtime, authID, "")
			if createErr != nil {
				return nil, oauthport.ErrStateInconsistent
			}
			return context, nil
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
	gate, err := readApplicationAvailabilityGate(ctx, r.database, configuration.ApplicationID.String())
	if err != nil {
		return nil, oauthport.ErrStateInconsistent
	}
	if gate != applicationGateAvailable {
		return nil, oauthport.ErrRuntimeUnavailable
	}
	var app applicationDocument
	if err = r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": configuration.ApplicationID.String()}).Decode(&app); errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, oauthport.ErrRuntimeUnavailable
	} else if err != nil {
		return nil, err
	}
	application, restoreErr := applicationFromDocument(app)
	if restoreErr != nil || application.ID() != configuration.ApplicationID {
		return nil, oauthport.ErrStateInconsistent
	}
	publication, snapshot, err := r.publishedVersion(ctx, configuration.ApplicationID, major, channel)
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
	versionID := publicationVersionForChannel(publication, channel)
	if versionID == nil {
		return nil, oauthport.ErrRuntimeUnavailable
	}
	runtime, createErr := oauthdomain.NewRuntimeConfiguration(configuration, major, application.AdminID(), *versionID, publication.Revision, redirects, required, optional, display, observedAt)
	if createErr != nil {
		return nil, oauthport.ErrStateInconsistent
	}
	return runtime, nil
}

func (r *OAuthProviderRepository) publishedVersion(ctx context.Context, applicationID shared.ApplicationID, major int32, channel oauthdomain.Channel) (applicationPublicationDocument, *reviewdomain.ApplicationVersionReviewSnapshot, error) {
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
	versionID := publicationVersionForChannel(publication, channel)
	if versionID == nil {
		return publication, nil, oauthport.ErrRuntimeUnavailable
	}
	actions := bson.A{"SET_TEST_VERSION"}
	if channel == oauthdomain.ChannelStable {
		actions = bson.A{"SET_STABLE_VERSION"}
	}
	if channel == oauthdomain.ChannelGrey {
		actions = bson.A{"SET_GREY_ROLLOUT", "INCREASE_GREY_EXPOSURE", "REPLACE_GREY_VERSION"}
	}
	var history applicationPublicationHistoryDocument
	err = r.database.Collection(applicationPublicationHistoryCollectionName).FindOne(ctx, bson.M{"publicationId": publication.PublicationID, "publicationRevision": bson.M{"$lte": publication.Revision}, "action": bson.M{"$in": actions}, "newVersionId": *versionID}, options.FindOne().SetSort(bson.D{{Key: "publicationRevision", Value: -1}})).Decode(&history)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	if history.ApplicationID != applicationID.String() || history.RPCAPIMajor != major || history.NewVersionID == nil || *history.NewVersionID != *versionID || history.ApprovedReviewID == nil {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	var version applicationVersionDocument
	err = r.database.Collection(applicationVersionsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "versionId": *versionID}).Decode(&version)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	var config applicationVersionOAuthConfigDocument
	err = r.database.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "applicationVersionId": *versionID}).Decode(&config)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	if _, err = applicationVersionFromDocument(version, config); err != nil || version.ReviewStatus != "APPROVED" || major < version.RPCApiMinVersion || major >= version.RPCApiMaxVersionExclusive {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	var review applicationReviewDocument
	err = r.database.Collection(applicationReviewsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "versionId": *versionID}, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})).Decode(&review)
	if err != nil {
		return publication, nil, missingOAuthProviderFact(err)
	}
	approved, restoreErr := applicationReviewFromDocument(review)
	if restoreErr != nil || review.ReviewID != *history.ApprovedReviewID || review.Status != "APPROVED" || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || version.Revision != review.SourceVersionRevision+2 {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	_, _, currentSnapshot, restoreErr := applicationVersionDocumentToDecisionVersion(version, config)
	if restoreErr != nil || !currentSnapshot.Equal(approved.Snapshot()) || version.UpdatedBy != review.Decision.DecidedBy || !version.UpdatedAt.Equal(review.Decision.DecidedAt) {
		return publication, nil, oauthport.ErrStateInconsistent
	}
	snapshot := approved.Snapshot()
	return publication, &snapshot, nil
}

func publicationVersionForChannel(publication applicationPublicationDocument, channel oauthdomain.Channel) *string {
	if channel == oauthdomain.ChannelStable {
		return publication.StableVersionID
	}
	if channel == oauthdomain.ChannelTest {
		return publication.TestVersionID
	}
	if channel == oauthdomain.ChannelGrey && publication.GreyRollout != nil {
		return &publication.GreyRollout.VersionID
	}
	return nil
}

func (r *OAuthProviderRepository) greyCohortMatches(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID string, authID shared.AuthID) (bool, error) {
	var document applicationPublicationDocument
	err := r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "rpcApiMajor": major}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return false, oauthport.ErrRuntimeUnavailable
	}
	if err != nil {
		return false, err
	}
	publication, restoreErr := applicationPublicationFromDocument(document)
	if restoreErr != nil {
		return false, oauthport.ErrStateInconsistent
	}
	rollout := publication.GreyRollout()
	if rollout == nil || rollout.VersionID().String() != versionID {
		return false, oauthport.ErrRuntimeVersionChanged
	}
	return rollout.Matches(authID), nil
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
		gate, err := readApplicationAvailabilityGate(tx, r.database, applicationID.String())
		if err != nil {
			return nil, oauthport.ErrStateInconsistent
		}
		if gate != applicationGateAvailable {
			return nil, oauthport.ErrApplicationNotFound
		}
		type registrationTypes struct{ public, confidential bool }
		registrations := map[oauthdomain.Channel]registrationTypes{}
		for _, channel := range []oauthdomain.Channel{oauthdomain.ChannelStable, oauthdomain.ChannelGrey, oauthdomain.ChannelTest} {
			var document applicationOAuthRegistrationDocument
			registrationErr := r.database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "channel": string(channel)}).Decode(&document)
			if errors.Is(registrationErr, drivermongo.ErrNoDocuments) {
				continue
			}
			if registrationErr != nil {
				return nil, registrationErr
			}
			restored, restoreErr := registrationFromDocument(document)
			if restoreErr != nil || restored.Channel() != channel {
				return nil, oauthport.ErrStateInconsistent
			}
			registrations[channel] = registrationTypes{restored.PublicClient() != nil, restored.ConfidentialClient() != nil}
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
			if _, restoreErr := applicationPublicationFromDocument(publication); restoreErr != nil {
				return nil, oauthport.ErrStateInconsistent
			}
			for _, channel := range []oauthdomain.Channel{oauthdomain.ChannelStable, oauthdomain.ChannelGrey, oauthdomain.ChannelTest} {
				versionID := publicationVersionForChannel(publication, channel)
				if versionID == nil {
					continue
				}
				validated, snapshot, validateErr := r.publishedVersion(tx, applicationID, publication.RPCAPIMajor, channel)
				if validateErr != nil {
					return nil, validateErr
				}
				entries = append(entries, oauthdomain.PublishedRedirectEntry{Channel: channel, RPCAPIMajor: validated.RPCAPIMajor, VersionID: *versionID, PublicationRevision: validated.Revision})
				types := registrations[channel]
				if types.public {
					for _, uri := range snapshot.OAuthRedirects().PKCERedirectURIs() {
						redirectSet[uri] = struct{}{}
					}
				}
				if types.confidential {
					for _, uri := range snapshot.OAuthRedirects().ConfidentialRedirectURIs() {
						redirectSet[uri] = struct{}{}
					}
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
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Channel != entries[j].Channel {
				return entries[i].Channel < entries[j].Channel
			}
			return entries[i].RPCAPIMajor < entries[j].RPCAPIMajor
		})
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
