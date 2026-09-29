package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

type ApplicationPublicationRepository struct{ database *drivermongo.Database }

func NewApplicationPublicationRepository(database *drivermongo.Database) *ApplicationPublicationRepository {
	return &ApplicationPublicationRepository{database}
}

func (r *ApplicationPublicationRepository) LoadTestPlacementCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID publicationdomain.ApplicationVersionID, adminID shared.AuthID, expectedRevision *int64) (*publicationdomain.TestPlacementCandidate, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !versionID.IsValid() || !adminID.IsValid() || major < 1 || (expectedRevision != nil && *expectedRevision < 1) {
		return nil, fmt.Errorf("load publication candidate: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadTestPlacementCandidate(tx, applicationID, major, versionID, adminID, expectedRevision, false)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, err
	}
	return result.(*publicationdomain.TestPlacementCandidate), nil
}

func (r *ApplicationPublicationRepository) loadTestPlacementCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID publicationdomain.ApplicationVersionID, adminID shared.AuthID, expectedRevision *int64, lock bool) (*publicationdomain.TestPlacementCandidate, error) {
	var application applicationDocument
	appFilter := bson.D{{Key: "id", Value: applicationID.String()}}
	err := r.database.Collection(applicationsCollectionName).FindOne(ctx, appFilter).Decode(&application)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return nil, err
	}
	// Check path ownership before administrator authorization to hide foreign versions.
	var version applicationVersionDocument
	versionFilter := bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "versionId", Value: versionID.String()}}
	err = r.database.Collection(applicationVersionsCollectionName).FindOne(ctx, versionFilter).Decode(&version)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return nil, err
	}
	if application.AdminID != adminID.String() {
		return nil, publicationport.ErrApplicationAdminRequired
	}
	if version.ReviewStatus != "APPROVED" {
		return nil, publicationport.ErrApplicationVersionNotApproved
	}
	var review applicationReviewDocument
	reviewFilter := bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "versionId", Value: versionID.String()}}
	err = r.database.Collection(applicationReviewsCollectionName).FindOne(ctx, reviewFilter, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})).Decode(&review)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	if err != nil {
		return nil, err
	}
	if review.Status != "APPROVED" {
		return nil, publicationport.ErrApplicationVersionNotApproved
	}
	var oauthConfig applicationVersionOAuthConfigDocument
	err = r.database.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(ctx, bson.D{{Key: "applicationVersionId", Value: versionID.String()}, {Key: "applicationId", Value: applicationID.String()}}).Decode(&oauthConfig)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	if err != nil {
		return nil, err
	}
	approved, err := applicationReviewFromDocument(review)
	if err != nil || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || version.Revision != review.SourceVersionRevision+2 {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	_, _, snapshot, err := applicationVersionDocumentToDecisionVersion(version, oauthConfig)
	if err != nil || !snapshot.Equal(approved.Snapshot()) {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	publicationSnapshot, err := publicationdomain.NewApplicationVersionReviewSnapshot(
		review.Snapshot.VersionLabel,
		publicationdomain.LaunchURL(review.Snapshot.LaunchURL),
		review.Snapshot.RPCApiMinVersion,
		review.Snapshot.RPCApiMaxVersionExclusive,
		review.Snapshot.RequiredCapabilities,
		publicationScopeNames(review.Snapshot.RequiredScopes),
		publicationScopeNames(review.Snapshot.OptionalScopes),
		publicationdomain.OAuthRedirectConfiguration{
			PKCE:         append([]string{}, review.Snapshot.OAuthRedirects.PKCERedirectURIs...),
			Confidential: append([]string{}, review.Snapshot.OAuthRedirects.ConfidentialRedirectURIs...),
		},
	)
	if err != nil {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	if err := r.ensureTestOAuthRegistration(ctx, applicationID, len(publicationSnapshot.PKCERedirectURIs()) > 0, len(publicationSnapshot.ConfidentialRedirectURIs()) > 0); err != nil {
		return nil, err
	}
	var document applicationPublicationDocument
	var publication *publicationdomain.ApplicationPublication
	err = r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "rpcApiMajor", Value: major}}).Decode(&document)
	if err == nil {
		publication, err = applicationPublicationFromDocument(document)
		if err != nil {
			return nil, fmt.Errorf("read publication: %w", err)
		}
	} else if !errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, err
	}
	candidate, err := publicationdomain.NewTestPlacementCandidate(applicationID, major, versionID, publicationdomain.ApplicationReviewID(review.ReviewID), version.Revision, *publicationSnapshot, publication, expectedRevision)
	switch {
	case errors.Is(err, publicationdomain.ErrApplicationVersionRpcApiIncompatible):
		return nil, publicationport.ErrApplicationVersionRpcApiIncompatible
	case errors.Is(err, publicationdomain.ErrApplicationPublicationAlreadyExists):
		return nil, publicationport.ErrApplicationPublicationAlreadyExists
	case errors.Is(err, publicationdomain.ErrApplicationPublicationNotFound):
		return nil, publicationport.ErrApplicationPublicationNotFound
	case errors.Is(err, publicationdomain.ErrApplicationPublicationRevisionConflict):
		return nil, publicationport.ErrApplicationPublicationRevisionConflict
	}
	if err != nil {
		return nil, err
	}
	if lock {
		// A snapshot read alone cannot protect eligibility from concurrent writes.
		// Touch each authoritative source inside this transaction after validation;
		// writes after our snapshot force a full transaction retry and revalidation.
		// Existing invalid/revoked states are rejected before any validator is asked
		// to accept a technical fence update on those documents.
		for _, source := range []struct {
			collection     string
			filter, update bson.D
		}{
			{applicationsCollectionName, appFilter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}},
			{applicationVersionsCollectionName, versionFilter, publicationFenceUpdate()},
			{applicationReviewsCollectionName, bson.D{{Key: "reviewId", Value: review.ReviewID}}, publicationFenceUpdate()},
		} {
			if err := r.database.Collection(source.collection).FindOneAndUpdate(ctx, source.filter, source.update).Err(); err != nil {
				return nil, err
			}
		}
	}
	return candidate, nil
}

func (r *ApplicationPublicationRepository) ensureTestOAuthRegistration(ctx context.Context, applicationID shared.ApplicationID, requirePublic, requireConfidential bool) error {
	if !requirePublic && !requireConfidential {
		return nil
	}
	var registration applicationOAuthRegistrationDocument
	err := r.database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "channel", Value: "TEST"}},
	).Decode(&registration)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return publicationport.ErrOAuthClientRegistrationRequired
	}
	if err != nil {
		return err
	}
	if requirePublic && registration.PublicClient == nil {
		return publicationport.ErrOAuthClientRegistrationRequired
	}
	if requireConfidential {
		if registration.ConfidentialClient == nil {
			return publicationport.ErrOAuthClientRegistrationRequired
		}
		err = r.database.Collection(oauthClientCredentialsCollectionName).FindOne(
			ctx,
			bson.D{{Key: "clientId", Value: registration.ConfidentialClient.ClientID}, {Key: "applicationId", Value: applicationID.String()}},
			options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}}),
		).Err()
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return publicationport.ErrOAuthClientRegistrationRequired
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func publicationFenceUpdate() bson.D {
	return bson.D{{Key: "$inc", Value: bson.D{{Key: "publicationCoordinationRevision", Value: int64(1)}}}}
}
func publicationScopeNames(values []string) []publicationdomain.ScopeName {
	result := make([]publicationdomain.ScopeName, len(values))
	for i, v := range values {
		result[i] = publicationdomain.ScopeName(v)
	}
	return result
}

func (r *ApplicationPublicationRepository) PlaceInTest(ctx context.Context, candidate *publicationdomain.TestPlacementCandidate, publicationID *publicationdomain.ApplicationPublicationID, historyID publicationdomain.ApplicationPublicationHistoryID, adminID shared.AuthID, validation publicationdomain.PublicationValidation, changedAt time.Time) (*publicationdomain.PlaceInTestResult, error) {
	if r == nil || r.database == nil || candidate == nil || !adminID.IsValid() {
		return nil, fmt.Errorf("place publication: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.placeInTestTransaction(tx, candidate, publicationID, historyID, adminID, validation, changedAt.UTC().Truncate(time.Millisecond))
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("place publication transaction: %w", err)
	}
	return result.(*publicationdomain.PlaceInTestResult), nil
}

func (r *ApplicationPublicationRepository) placeInTestTransaction(ctx context.Context, candidate *publicationdomain.TestPlacementCandidate, publicationID *publicationdomain.ApplicationPublicationID, historyID publicationdomain.ApplicationPublicationHistoryID, adminID shared.AuthID, validation publicationdomain.PublicationValidation, changedAt time.Time) (*publicationdomain.PlaceInTestResult, error) {
	current, err := r.loadTestPlacementCandidate(ctx, candidate.ApplicationID(), candidate.RPCAPIMajor(), candidate.VersionID(), adminID, candidate.ExpectedPublicationRevision(), true)
	if err != nil {
		return nil, err
	}
	if current.ReviewID() != candidate.ReviewID() || current.VersionRevision() != candidate.VersionRevision() || !current.Snapshot().Equal(candidate.Snapshot()) {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	if old := candidate.Publication(); old != nil {
		actual := current.Publication()
		if actual == nil || actual.PublicationID() != old.PublicationID() || actual.TestVersionID() != old.TestVersionID() {
			return nil, publicationport.ErrApplicationPublicationRevisionConflict
		}
	}
	result, err := current.PlaceInTest(publicationID, historyID, adminID, validation, changedAt)
	if err != nil {
		return nil, err
	}
	if !result.Changed() {
		return result, nil
	}
	publication := applicationPublicationToDocument(result.Publication())
	if current.Publication() == nil {
		_, err = r.database.Collection(applicationPublicationsCollectionName).InsertOne(ctx, publication)
		if drivermongo.IsDuplicateKeyError(err) {
			// Only a partition collision is a business conflict; generated ID collisions
			// and history ID collisions remain internal persistence failures.
			var writeException drivermongo.WriteException
			if errors.As(err, &writeException) {
				for _, failure := range writeException.WriteErrors {
					if failure.Details.Lookup("keyPattern").Type == bson.TypeEmbeddedDocument {
						pattern := failure.Details.Lookup("keyPattern").Document()
						if _, e := pattern.LookupErr("applicationId"); e == nil {
							return nil, publicationport.ErrApplicationPublicationAlreadyExists
						}
					}
				}
			}
		}
	} else {
		previous := current.Publication()
		updateResult, updateErr := r.database.Collection(applicationPublicationsCollectionName).ReplaceOne(ctx, bson.D{{Key: "publicationId", Value: previous.PublicationID().String()}, {Key: "revision", Value: previous.Revision()}, {Key: "testVersionId", Value: previous.TestVersionID().String()}}, publication)
		err = updateErr
		if err == nil && updateResult.MatchedCount != 1 {
			return nil, publicationport.ErrApplicationPublicationRevisionConflict
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err = r.database.Collection(applicationPublicationHistoryCollectionName).InsertOne(ctx, applicationPublicationHistoryToDocument(result.History())); err != nil {
		return nil, err
	}
	return result, nil
}
