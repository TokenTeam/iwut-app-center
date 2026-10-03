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
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
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
		return r.loadTestPlacementCandidate(tx, applicationID, major, versionID, adminID, expectedRevision, "TEST", false)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, err
	}
	return result.(*publicationdomain.TestPlacementCandidate), nil
}

func (r *ApplicationPublicationRepository) loadTestPlacementCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID publicationdomain.ApplicationVersionID, adminID shared.AuthID, expectedRevision *int64, oauthChannel string, lock bool) (*publicationdomain.TestPlacementCandidate, error) {
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
	if err := r.ensureApprovedPublishedProfile(ctx, applicationID); err != nil {
		return nil, err
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
	if err := r.ensureOAuthRegistration(ctx, applicationID, oauthChannel, len(publicationSnapshot.PKCERedirectURIs()) > 0, len(publicationSnapshot.ConfidentialRedirectURIs()) > 0); err != nil {
		return nil, err
	}
	var document applicationPublicationDocument
	var publication *publicationdomain.ApplicationPublication
	err = r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "rpcApiMajor", Value: major}}).Decode(&document)
	if err == nil {
		publication, err = applicationPublicationFromDocument(document)
		if err != nil {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
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
		// to accept a technical fence update on those documents. Profile approval
		// also advances Application.coordinationRevision, so the Application fence
		// serializes a concurrent currentPublishedProfileRevisionId replacement and
		// forces this transaction to retry all profile checks.
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

func (r *ApplicationPublicationRepository) ensureApprovedPublishedProfile(ctx context.Context, applicationID shared.ApplicationID) error {
	raw, err := r.database.Collection(applicationProfilesCollectionName).FindOne(
		ctx,
		bson.D{{Key: "applicationId", Value: applicationID.String()}},
	).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return publicationport.ErrApplicationProfileRequired
	}
	if err != nil {
		return err
	}
	profile, err := profileFromRaw(raw)
	if errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
		return publicationport.ErrApplicationProfileStateInconsistent
	}
	if err != nil {
		return err
	}
	if profile.ApplicationID != applicationID.String() {
		return publicationport.ErrApplicationProfileStateInconsistent
	}
	if profile.CurrentPublishedProfileRevisionID == nil {
		return publicationport.ErrApplicationProfileRequired
	}
	raw, err = r.database.Collection(applicationProfileRevisionsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "profileRevisionId", Value: *profile.CurrentPublishedProfileRevisionID}},
	).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return publicationport.ErrApplicationProfileStateInconsistent
	}
	if err != nil {
		return err
	}
	revision, err := profileRevisionFromRaw(raw)
	if errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
		return publicationport.ErrApplicationProfileStateInconsistent
	}
	if err != nil {
		return err
	}
	if revision.ApplicationID() != applicationID || revision.ProfileRevisionID().String() != *profile.CurrentPublishedProfileRevisionID || revision.ReviewStatus() != profiledomain.ReviewStatusApproved {
		return publicationport.ErrApplicationProfileStateInconsistent
	}
	return nil
}

func (r *ApplicationPublicationRepository) ensureOAuthRegistration(ctx context.Context, applicationID shared.ApplicationID, channel string, requirePublic, requireConfidential bool) error {
	if !requirePublic && !requireConfidential {
		return nil
	}
	var registration applicationOAuthRegistrationDocument
	err := r.database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "channel", Value: channel}},
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
	current, err := r.loadTestPlacementCandidate(ctx, candidate.ApplicationID(), candidate.RPCAPIMajor(), candidate.VersionID(), adminID, candidate.ExpectedPublicationRevision(), "TEST", true)
	if err != nil {
		return nil, err
	}
	if current.ReviewID() != candidate.ReviewID() || current.VersionRevision() != candidate.VersionRevision() || !current.Snapshot().Equal(candidate.Snapshot()) {
		return nil, publicationport.ErrApplicationReviewStateInconsistent
	}
	if old := candidate.Publication(); old != nil {
		actual := current.Publication()
		if actual == nil || actual.PublicationID() != old.PublicationID() || actual.Revision() != old.Revision() {
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
		updateResult, updateErr := r.database.Collection(applicationPublicationsCollectionName).ReplaceOne(ctx, bson.D{{Key: "publicationId", Value: previous.PublicationID().String()}, {Key: "revision", Value: previous.Revision()}}, publication)
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

func (r *ApplicationPublicationRepository) LoadStablePlacementCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID publicationdomain.ApplicationVersionID, adminID shared.AuthID, expectedRevision *int64) (*publicationdomain.StablePlacementCandidate, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !versionID.IsValid() || !adminID.IsValid() || major < 1 || (expectedRevision != nil && *expectedRevision < 1) {
		return nil, fmt.Errorf("load stable publication candidate: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		base, loadErr := r.loadTestPlacementCandidate(tx, applicationID, major, versionID, adminID, expectedRevision, "STABLE", false)
		if loadErr != nil {
			return nil, loadErr
		}
		return publicationdomain.NewStablePlacementCandidate(base.ApplicationID(), base.RPCAPIMajor(), base.VersionID(), base.ReviewID(), base.VersionRevision(), base.Snapshot(), base.Publication(), base.ExpectedPublicationRevision())
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, err
	}
	return result.(*publicationdomain.StablePlacementCandidate), nil
}

func (r *ApplicationPublicationRepository) SetStable(ctx context.Context, candidate *publicationdomain.StablePlacementCandidate, publicationID *publicationdomain.ApplicationPublicationID, historyID publicationdomain.ApplicationPublicationHistoryID, adminID shared.AuthID, validation publicationdomain.PublicationValidation, changedAt time.Time) (*publicationdomain.PlaceInTestResult, error) {
	if r == nil || r.database == nil || candidate == nil || !adminID.IsValid() {
		return nil, fmt.Errorf("set stable publication: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		base, loadErr := r.loadTestPlacementCandidate(tx, candidate.ApplicationID(), candidate.RPCAPIMajor(), candidate.VersionID(), adminID, candidate.ExpectedPublicationRevision(), "STABLE", true)
		if loadErr != nil {
			return nil, loadErr
		}
		current, createErr := publicationdomain.NewStablePlacementCandidate(base.ApplicationID(), base.RPCAPIMajor(), base.VersionID(), base.ReviewID(), base.VersionRevision(), base.Snapshot(), base.Publication(), base.ExpectedPublicationRevision())
		if createErr != nil {
			return nil, createErr
		}
		if current.ReviewID() != candidate.ReviewID() || current.VersionRevision() != candidate.VersionRevision() || !current.Snapshot().Equal(candidate.Snapshot()) {
			return nil, publicationport.ErrApplicationReviewStateInconsistent
		}
		if old := candidate.Publication(); old != nil {
			actual := current.Publication()
			if actual == nil || actual.PublicationID() != old.PublicationID() || actual.Revision() != old.Revision() {
				return nil, publicationport.ErrApplicationPublicationRevisionConflict
			}
		}
		changed, changeErr := current.SetStable(publicationID, historyID, adminID, validation, changedAt.UTC().Truncate(time.Millisecond))
		if changeErr != nil || !changed.Changed() {
			return changed, changeErr
		}
		if persistErr := r.persistPublicationChange(tx, current.Publication(), changed); persistErr != nil {
			return nil, persistErr
		}
		return changed, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("set stable publication transaction: %w", err)
	}
	return result.(*publicationdomain.PlaceInTestResult), nil
}

func (r *ApplicationPublicationRepository) LoadStableClearCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, adminID shared.AuthID, expectedRevision int64) (*publicationdomain.StableClearCandidate, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !adminID.IsValid() || major < 1 || expectedRevision < 1 {
		return nil, fmt.Errorf("load stable clear candidate: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadStableClearCandidate(tx, applicationID, major, adminID, expectedRevision, false)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, err
	}
	return result.(*publicationdomain.StableClearCandidate), nil
}

func (r *ApplicationPublicationRepository) loadStableClearCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, adminID shared.AuthID, expectedRevision int64, lock bool) (*publicationdomain.StableClearCandidate, error) {
	appFilter := bson.D{{Key: "id", Value: applicationID.String()}}
	var application applicationDocument
	if err := r.database.Collection(applicationsCollectionName).FindOne(ctx, appFilter).Decode(&application); errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationVersionNotFound
	} else if err != nil {
		return nil, err
	}
	if application.AdminID != adminID.String() {
		return nil, publicationport.ErrApplicationAdminRequired
	}
	var document applicationPublicationDocument
	raw, findErr := r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "rpcApiMajor", Value: major}}).Raw()
	if errors.Is(findErr, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationPublicationNotFound
	} else if findErr != nil {
		return nil, findErr
	}
	if err := bson.Unmarshal(raw, &document); err != nil {
		return nil, publicationport.ErrApplicationPublicationStateInconsistent
	}
	if grey, greyErr := raw.LookupErr("greyRollout"); greyErr == nil && grey.Type != bson.TypeNull {
		if document.StableVersionID == nil {
			return nil, publicationport.ErrApplicationPublicationStateInconsistent
		}
		return nil, publicationport.ErrStablePublicationRequiredByGrey
	}
	publication, err := applicationPublicationFromDocument(document)
	if err != nil {
		return nil, publicationport.ErrApplicationPublicationStateInconsistent
	}
	candidate, err := publicationdomain.NewStableClearCandidate(publication, expectedRevision)
	if errors.Is(err, publicationdomain.ErrApplicationPublicationRevisionConflict) {
		return nil, publicationport.ErrApplicationPublicationRevisionConflict
	}
	if err != nil {
		return nil, err
	}
	if lock {
		if err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, appFilter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Err(); err != nil {
			return nil, err
		}
	}
	return candidate, nil
}

func (r *ApplicationPublicationRepository) ClearStable(ctx context.Context, candidate *publicationdomain.StableClearCandidate, historyID publicationdomain.ApplicationPublicationHistoryID, adminID shared.AuthID, changedAt time.Time) (*publicationdomain.PlaceInTestResult, error) {
	if r == nil || r.database == nil || candidate == nil || candidate.Publication() == nil || !adminID.IsValid() {
		return nil, fmt.Errorf("clear stable publication: invalid repository input")
	}
	previous := candidate.Publication()
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		current, loadErr := r.loadStableClearCandidate(tx, previous.ApplicationID(), previous.RPCAPIMajor(), adminID, previous.Revision(), true)
		if loadErr != nil {
			return nil, loadErr
		}
		actual := current.Publication()
		if actual.PublicationID() != previous.PublicationID() || actual.Revision() != previous.Revision() {
			return nil, publicationport.ErrApplicationPublicationRevisionConflict
		}
		changed, changeErr := current.Clear(historyID, adminID, changedAt.UTC().Truncate(time.Millisecond))
		if changeErr != nil || !changed.Changed() {
			return changed, changeErr
		}
		if persistErr := r.persistPublicationChange(tx, actual, changed); persistErr != nil {
			return nil, persistErr
		}
		return changed, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("clear stable publication transaction: %w", err)
	}
	return result.(*publicationdomain.PlaceInTestResult), nil
}

func (r *ApplicationPublicationRepository) LoadGreyPlacementCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID publicationdomain.ApplicationVersionID, exposure publicationdomain.ExposureBasisPoints, adminID shared.AuthID, expectedRevision int64) (*publicationdomain.GreyPlacementCandidate, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !versionID.IsValid() || !exposure.IsValid() || !adminID.IsValid() || major < 1 || expectedRevision < 1 {
		return nil, fmt.Errorf("load grey publication candidate: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadGreyPlacementCandidate(tx, applicationID, major, versionID, exposure, adminID, expectedRevision, false)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, err
	}
	return result.(*publicationdomain.GreyPlacementCandidate), nil
}

func (r *ApplicationPublicationRepository) loadGreyPlacementCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, versionID publicationdomain.ApplicationVersionID, exposure publicationdomain.ExposureBasisPoints, adminID shared.AuthID, expectedRevision int64, lock bool) (*publicationdomain.GreyPlacementCandidate, error) {
	appFilter := bson.D{{Key: "id", Value: applicationID.String()}}
	var application applicationDocument
	if err := r.database.Collection(applicationsCollectionName).FindOne(ctx, appFilter).Decode(&application); errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationVersionNotFound
	} else if err != nil {
		return nil, err
	}
	if application.AdminID != adminID.String() {
		return nil, publicationport.ErrApplicationAdminRequired
	}
	var document applicationPublicationDocument
	err := r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "rpcApiMajor", Value: major}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationPublicationNotFound
	}
	if err != nil {
		return nil, err
	}
	publication, err := applicationPublicationFromDocument(document)
	if err != nil {
		return nil, publicationport.ErrApplicationPublicationStateInconsistent
	}
	change, err := publicationdomain.ClassifyGreyChange(publication, versionID, exposure, expectedRevision)
	if err != nil {
		return nil, mapGreyDomainError(err)
	}
	if change == publicationdomain.GreyChangeNoOp || change == publicationdomain.GreyChangeDecrease {
		candidate, createErr := publicationdomain.NewGreyReductionCandidate(applicationID, major, versionID, exposure, publication, expectedRevision)
		if createErr != nil {
			return nil, mapGreyDomainError(createErr)
		}
		if lock {
			if err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, appFilter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Err(); err != nil {
				return nil, err
			}
		}
		return candidate, nil
	}
	expected := expectedRevision
	base, err := r.loadTestPlacementCandidate(ctx, applicationID, major, versionID, adminID, &expected, "GREY", lock)
	if err != nil {
		return nil, err
	}
	candidate, err := publicationdomain.NewGreyPlacementCandidate(base, exposure)
	if err != nil {
		return nil, mapGreyDomainError(err)
	}
	return candidate, nil
}

func mapGreyDomainError(err error) error {
	switch {
	case errors.Is(err, publicationdomain.ErrApplicationPublicationNotFound):
		return publicationport.ErrApplicationPublicationNotFound
	case errors.Is(err, publicationdomain.ErrApplicationPublicationRevisionConflict):
		return publicationport.ErrApplicationPublicationRevisionConflict
	case errors.Is(err, publicationdomain.ErrGreyStableBaselineRequired):
		return publicationport.ErrGreyStableBaselineRequired
	case errors.Is(err, publicationdomain.ErrApplicationPublicationStateInconsistent):
		return publicationport.ErrApplicationPublicationStateInconsistent
	default:
		return err
	}
}

func (r *ApplicationPublicationRepository) SetGrey(ctx context.Context, candidate *publicationdomain.GreyPlacementCandidate, rolloutID *publicationdomain.GreyRolloutID, seed *publicationdomain.CohortSeed, historyID publicationdomain.ApplicationPublicationHistoryID, adminID shared.AuthID, validation *publicationdomain.PublicationValidation, changedAt time.Time) (*publicationdomain.PlaceInTestResult, error) {
	if r == nil || r.database == nil || candidate == nil || !adminID.IsValid() {
		return nil, fmt.Errorf("set grey publication: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		current, loadErr := r.loadGreyPlacementCandidate(tx, candidate.ApplicationID(), candidate.RPCAPIMajor(), candidate.VersionID(), candidate.ExposureBasisPoints(), adminID, candidate.ExpectedPublicationRevision(), true)
		if loadErr != nil {
			return nil, loadErr
		}
		old, actual := candidate.Publication(), current.Publication()
		if old == nil || actual == nil || actual.PublicationID() != old.PublicationID() || actual.Revision() != old.Revision() || current.ChangeKind() != candidate.ChangeKind() {
			return nil, publicationport.ErrApplicationPublicationRevisionConflict
		}
		if candidate.RequiresValidation() {
			before, after := candidate.ApprovedCandidate(), current.ApprovedCandidate()
			if before == nil || after == nil || after.ReviewID() != before.ReviewID() || after.VersionRevision() != before.VersionRevision() || !after.Snapshot().Equal(before.Snapshot()) {
				return nil, publicationport.ErrApplicationReviewStateInconsistent
			}
		}
		changed, changeErr := current.SetGrey(rolloutID, seed, historyID, adminID, validation, changedAt.UTC().Truncate(time.Millisecond))
		if changeErr != nil || !changed.Changed() {
			return changed, changeErr
		}
		if persistErr := r.persistPublicationChange(tx, actual, changed); persistErr != nil {
			return nil, persistErr
		}
		return changed, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("set grey publication transaction: %w", err)
	}
	return result.(*publicationdomain.PlaceInTestResult), nil
}

func (r *ApplicationPublicationRepository) LoadGreyClearCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, adminID shared.AuthID, expectedRevision int64) (*publicationdomain.GreyClearCandidate, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !adminID.IsValid() || major < 1 || expectedRevision < 1 {
		return nil, fmt.Errorf("load grey clear candidate: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadGreyClearCandidate(tx, applicationID, major, adminID, expectedRevision, false)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, err
	}
	return result.(*publicationdomain.GreyClearCandidate), nil
}

func (r *ApplicationPublicationRepository) loadGreyClearCandidate(ctx context.Context, applicationID shared.ApplicationID, major int32, adminID shared.AuthID, expectedRevision int64, lock bool) (*publicationdomain.GreyClearCandidate, error) {
	appFilter := bson.D{{Key: "id", Value: applicationID.String()}}
	var application applicationDocument
	if err := r.database.Collection(applicationsCollectionName).FindOne(ctx, appFilter).Decode(&application); errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationVersionNotFound
	} else if err != nil {
		return nil, err
	}
	if application.AdminID != adminID.String() {
		return nil, publicationport.ErrApplicationAdminRequired
	}
	var document applicationPublicationDocument
	err := r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "rpcApiMajor", Value: major}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, publicationport.ErrApplicationPublicationNotFound
	}
	if err != nil {
		return nil, err
	}
	publication, err := applicationPublicationFromDocument(document)
	if err != nil {
		return nil, publicationport.ErrApplicationPublicationStateInconsistent
	}
	candidate, err := publicationdomain.NewGreyClearCandidate(publication, expectedRevision)
	if err != nil {
		return nil, mapGreyDomainError(err)
	}
	if lock {
		if err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, appFilter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Err(); err != nil {
			return nil, err
		}
	}
	return candidate, nil
}

func (r *ApplicationPublicationRepository) ClearGrey(ctx context.Context, candidate *publicationdomain.GreyClearCandidate, historyID publicationdomain.ApplicationPublicationHistoryID, adminID shared.AuthID, changedAt time.Time) (*publicationdomain.PlaceInTestResult, error) {
	if r == nil || r.database == nil || candidate == nil || candidate.Publication() == nil || !adminID.IsValid() {
		return nil, fmt.Errorf("clear grey publication: invalid repository input")
	}
	previous := candidate.Publication()
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		current, loadErr := r.loadGreyClearCandidate(tx, previous.ApplicationID(), previous.RPCAPIMajor(), adminID, previous.Revision(), true)
		if loadErr != nil {
			return nil, loadErr
		}
		actual := current.Publication()
		if actual.PublicationID() != previous.PublicationID() || actual.Revision() != previous.Revision() {
			return nil, publicationport.ErrApplicationPublicationRevisionConflict
		}
		changed, changeErr := current.Clear(historyID, adminID, changedAt.UTC().Truncate(time.Millisecond))
		if changeErr != nil || !changed.Changed() {
			return changed, changeErr
		}
		if persistErr := r.persistPublicationChange(tx, actual, changed); persistErr != nil {
			return nil, persistErr
		}
		return changed, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, fmt.Errorf("clear grey publication transaction: %w", err)
	}
	return result.(*publicationdomain.PlaceInTestResult), nil
}

func (r *ApplicationPublicationRepository) persistPublicationChange(ctx context.Context, previous *publicationdomain.ApplicationPublication, result *publicationdomain.PlaceInTestResult) error {
	document := applicationPublicationToDocument(result.Publication())
	if previous == nil {
		if _, err := r.database.Collection(applicationPublicationsCollectionName).InsertOne(ctx, document); err != nil {
			if isPublicationPartitionDuplicate(err) {
				return publicationport.ErrApplicationPublicationAlreadyExists
			}
			return err
		}
	} else {
		updated, err := r.database.Collection(applicationPublicationsCollectionName).ReplaceOne(ctx, bson.D{{Key: "publicationId", Value: previous.PublicationID().String()}, {Key: "revision", Value: previous.Revision()}}, document)
		if err != nil {
			return err
		}
		if updated.MatchedCount != 1 {
			return publicationport.ErrApplicationPublicationRevisionConflict
		}
	}
	_, err := r.database.Collection(applicationPublicationHistoryCollectionName).InsertOne(ctx, applicationPublicationHistoryToDocument(result.History()))
	return err
}

func isPublicationPartitionDuplicate(err error) bool {
	if !drivermongo.IsDuplicateKeyError(err) {
		return false
	}
	var writeException drivermongo.WriteException
	if !errors.As(err, &writeException) {
		return false
	}
	for _, failure := range writeException.WriteErrors {
		if failure.Details.Lookup("keyPattern").Type != bson.TypeEmbeddedDocument {
			continue
		}
		pattern := failure.Details.Lookup("keyPattern").Document()
		if _, appErr := pattern.LookupErr("applicationId"); appErr == nil {
			if _, majorErr := pattern.LookupErr("rpcApiMajor"); majorErr == nil {
				return true
			}
		}
	}
	return false
}
