package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

var _ versionport.ApplicationVersionAbandonmentRepository = (*ApplicationVersionRepository)(nil)

type abandonmentFactoryError struct{ cause error }

func (e *abandonmentFactoryError) Error() string {
	return "create application version abandonment fact"
}
func (e *abandonmentFactoryError) Unwrap() error { return e.cause }

func (repository *ApplicationVersionRepository) AbandonDraft(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID versiondomain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
	factory versionport.ApplicationVersionAbandonmentFactory,
) (*versiondomain.AbandonApplicationVersionDraftResult, error) {
	if repository == nil || repository.database == nil || !applicationID.IsValid() || !versionID.IsValid() ||
		!expectedAdminID.IsValid() || expectedRevision < 1 || factory == nil {
		return nil, fmt.Errorf("abandon application version draft: invalid repository input")
	}
	session, err := repository.database.Client().StartSession()
	if err != nil {
		return nil, versionport.ErrVersionAbandonmentUnavailable
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return repository.abandonDraftTransaction(tx, applicationID, versionID, expectedAdminID, expectedRevision, factory)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		for _, known := range []error{
			versionport.ErrApplicationVersionNotFound,
			versionport.ErrApplicationVersionRevisionConflict,
			versionport.ErrApplicationVersionNotAbandonable,
			versionport.ErrVersionAbandonmentNotAllowed,
			versionport.ErrVersionAbandonmentStateInconsistent,
		} {
			if errors.Is(err, known) {
				return nil, known
			}
		}
		var factoryErr *abandonmentFactoryError
		if errors.As(err, &factoryErr) {
			if factoryErr.cause == nil {
				return nil, factoryErr
			}
			return nil, factoryErr.cause
		}
		return nil, versionport.ErrVersionAbandonmentUnavailable
	}
	result, ok := value.(*versiondomain.AbandonApplicationVersionDraftResult)
	if !ok || result == nil || result.Abandonment == nil {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}
	return result, nil
}

func (repository *ApplicationVersionRepository) abandonDraftTransaction(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID versiondomain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
	factory versionport.ApplicationVersionAbandonmentFactory,
) (*versiondomain.AbandonApplicationVersionDraftResult, error) {
	applications := repository.database.Collection(applicationsCollectionName)
	versions := repository.database.Collection(applicationVersionsCollectionName)
	oauthConfigs := repository.database.Collection(applicationVersionOAuthConfigsCollectionName)
	abandonments := repository.database.Collection(applicationVersionAbandonmentsCollectionName)

	var application applicationDocument
	err := applications.FindOne(ctx, bson.M{"id": applicationID.String()}).Decode(&application)
	if errors.Is(err, drivermongo.ErrNoDocuments) || (err == nil && application.AdminID != expectedAdminID.String()) {
		return nil, versionport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read abandonment application: %w", err)
	}
	if application.ID != applicationID.String() || application.CoordinationRevision < 0 {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}
	if application.LifecycleStatus != "ACTIVE" {
		if application.LifecycleStatus == "CLOSING" || application.LifecycleStatus == "CLOSED" {
			return nil, versionport.ErrVersionAbandonmentNotAllowed
		}
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}

	var tombstoneDocument applicationVersionAbandonmentDocument
	tombstoneErr := abandonments.FindOne(ctx, bson.M{"versionId": versionID.String()}).Decode(&tombstoneDocument)
	if tombstoneErr != nil && !errors.Is(tombstoneErr, drivermongo.ErrNoDocuments) {
		return nil, fmt.Errorf("read application version abandonment: %w", tombstoneErr)
	}

	var versionDocument applicationVersionDocument
	versionErr := versions.FindOne(ctx, bson.M{"versionId": versionID.String()}).Decode(&versionDocument)
	if versionErr != nil && !errors.Is(versionErr, drivermongo.ErrNoDocuments) {
		return nil, fmt.Errorf("read application version: %w", versionErr)
	}
	oauthDocuments, err := findAbandonmentOAuthDocuments(ctx, oauthConfigs, versionID)
	if err != nil {
		return nil, err
	}
	hasReview, err := repository.hasAbandonmentReview(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	hasPublication, err := repository.hasAbandonmentPublicationReference(ctx, versionID)
	if err != nil {
		return nil, err
	}

	if tombstoneErr == nil {
		fact, mapErr := applicationVersionAbandonmentFromDocument(tombstoneDocument)
		if mapErr != nil {
			return nil, versionport.ErrVersionAbandonmentStateInconsistent
		}
		if fact.ApplicationID() != applicationID || fact.VersionID() != versionID {
			return nil, versionport.ErrApplicationVersionNotFound
		}
		if versionErr == nil || len(oauthDocuments) != 0 || hasReview || hasPublication {
			return nil, versionport.ErrVersionAbandonmentStateInconsistent
		}
		if fact.SourceRevision() != expectedRevision {
			return nil, versionport.ErrApplicationVersionRevisionConflict
		}
		return &versiondomain.AbandonApplicationVersionDraftResult{Abandonment: fact, Changed: false}, nil
	}

	if errors.Is(versionErr, drivermongo.ErrNoDocuments) || versionDocument.ApplicationID != applicationID.String() {
		return nil, versionport.ErrApplicationVersionNotFound
	}
	if len(oauthDocuments) != 1 || oauthDocuments[0].ApplicationID != applicationID.String() {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}
	version, mapErr := applicationVersionFromDocument(versionDocument, oauthDocuments[0])
	if mapErr != nil {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}
	if version.Revision() != expectedRevision {
		return nil, versionport.ErrApplicationVersionRevisionConflict
	}
	if version.ReviewStatus() != versiondomain.ReviewStatusDraft {
		return nil, versionport.ErrApplicationVersionNotAbandonable
	}
	if hasReview {
		return nil, versionport.ErrApplicationVersionNotAbandonable
	}
	if hasPublication {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}

	lockResult, err := applications.UpdateOne(ctx, bson.M{
		"id": applicationID.String(), "adminId": expectedAdminID.String(), "lifecycleStatus": "ACTIVE",
		"coordinationRevision": application.CoordinationRevision,
	}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return nil, fmt.Errorf("lock application version abandonment: %w", err)
	}
	if lockResult.MatchedCount != 1 {
		return nil, versionport.ErrApplicationVersionNotFound
	}

	fact, err := factory(version.Sequence(), version.VersionLabel())
	if err != nil || fact == nil {
		return nil, &abandonmentFactoryError{cause: err}
	}
	if fact.ApplicationID() != applicationID || fact.VersionID() != versionID || fact.Sequence() != version.Sequence() ||
		fact.VersionLabel() != version.VersionLabel() || fact.SourceRevision() != expectedRevision || fact.AbandonedBy() != expectedAdminID {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}
	document, err := applicationVersionAbandonmentToDocument(fact)
	if err != nil {
		return nil, &abandonmentFactoryError{cause: err}
	}
	if _, err = abandonments.InsertOne(ctx, document); err != nil {
		return nil, fmt.Errorf("insert application version abandonment: %w", err)
	}
	oauthDelete, err := oauthConfigs.DeleteOne(ctx, bson.M{"applicationVersionId": versionID.String(), "applicationId": applicationID.String()})
	if err != nil {
		return nil, fmt.Errorf("delete application version OAuth config: %w", err)
	}
	versionDelete, err := versions.DeleteOne(ctx, bson.M{
		"versionId": versionID.String(), "applicationId": applicationID.String(),
		"reviewStatus": string(versiondomain.ReviewStatusDraft), "revision": expectedRevision,
	})
	if err != nil {
		return nil, fmt.Errorf("delete application version: %w", err)
	}
	if oauthDelete.DeletedCount != 1 || versionDelete.DeletedCount != 1 {
		return nil, versionport.ErrVersionAbandonmentStateInconsistent
	}
	return &versiondomain.AbandonApplicationVersionDraftResult{Abandonment: fact, Changed: true}, nil
}

func findAbandonmentOAuthDocuments(ctx context.Context, collection *drivermongo.Collection, versionID versiondomain.ApplicationVersionID) ([]applicationVersionOAuthConfigDocument, error) {
	cursor, err := collection.Find(ctx, bson.M{"applicationVersionId": versionID.String()}, options.Find().SetLimit(2))
	if err != nil {
		return nil, fmt.Errorf("read application version OAuth configs: %w", err)
	}
	defer cursor.Close(ctx)
	var documents []applicationVersionOAuthConfigDocument
	if err := cursor.All(ctx, &documents); err != nil {
		return nil, fmt.Errorf("decode application version OAuth configs: %w", err)
	}
	return documents, nil
}

func (repository *ApplicationVersionRepository) hasAbandonmentReview(ctx context.Context, applicationID shared.ApplicationID, versionID versiondomain.ApplicationVersionID) (bool, error) {
	collection := repository.database.Collection(applicationReviewsCollectionName)
	var marker bson.M
	err := collection.FindOne(ctx, bson.M{
		"versionId":     versionID.String(),
		"applicationId": bson.M{"$ne": applicationID.String()},
	}, options.FindOne().SetProjection(bson.M{"_id": 1})).Decode(&marker)
	if err == nil {
		return false, versionport.ErrVersionAbandonmentStateInconsistent
	}
	if !errors.Is(err, drivermongo.ErrNoDocuments) {
		return false, fmt.Errorf("check application version review ownership: %w", err)
	}
	err = collection.FindOne(
		ctx, bson.M{"versionId": versionID.String()}, options.FindOne().SetProjection(bson.M{"_id": 1}),
	).Decode(&marker)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read application version reviews: %w", err)
	}
	return true, nil
}

func (repository *ApplicationVersionRepository) hasAbandonmentPublicationReference(ctx context.Context, versionID versiondomain.ApplicationVersionID) (bool, error) {
	id := versionID.String()
	publicationFilter := bson.M{"$or": bson.A{
		bson.M{"testVersionId": id}, bson.M{"stableVersionId": id}, bson.M{"greyRollout.versionId": id},
	}}
	count, err := repository.database.Collection(applicationPublicationsCollectionName).CountDocuments(ctx, publicationFilter, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("check application version publications: %w", err)
	}
	if count != 0 {
		return true, nil
	}
	historyFilter := bson.M{"$or": bson.A{bson.M{"previousVersionId": id}, bson.M{"newVersionId": id}}}
	count, err = repository.database.Collection(applicationPublicationHistoryCollectionName).CountDocuments(ctx, historyFilter, options.Count().SetLimit(1))
	if err != nil {
		return false, fmt.Errorf("check application version publication history: %w", err)
	}
	return count != 0, nil
}
