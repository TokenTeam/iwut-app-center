package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
)

type ApplicationReviewRestorationRepository struct {
	database *drivermongo.Database
}

func NewApplicationReviewRestorationRepository(database *drivermongo.Database) *ApplicationReviewRestorationRepository {
	return &ApplicationReviewRestorationRepository{database: database}
}

func (repository *ApplicationReviewRestorationRepository) RestoreDraft(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
	expectedAdminID shared.AuthID,
	expectedVersionRevision int64,
	restoredAt time.Time,
) (*reviewdomain.RestoreRejectedVersionResult, error) {
	if repository == nil || repository.database == nil {
		return nil, fmt.Errorf("restore rejected application version: MongoDB database is nil")
	}
	if !applicationID.IsValid() || !versionID.IsValid() || !reviewID.IsValid() || !expectedAdminID.IsValid() ||
		expectedVersionRevision < 1 || restoredAt.IsZero() {
		return nil, fmt.Errorf("restore rejected application version: invalid repository input")
	}

	session, err := repository.database.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("start application review restoration transaction: %w", err)
	}
	defer session.EndSession(ctx)

	transactionOptions := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())
	result, err := session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		return repository.restoreDraftTransaction(
			transactionContext,
			applicationID,
			versionID,
			reviewID,
			expectedAdminID,
			expectedVersionRevision,
			restoredAt.UTC(),
		)
	}, transactionOptions)
	if err != nil {
		switch {
		case errors.Is(err, reviewport.ErrApplicationReviewNotFound):
			return nil, reviewport.ErrApplicationReviewNotFound
		case errors.Is(err, reviewport.ErrApplicationAdminRequired):
			return nil, reviewport.ErrApplicationAdminRequired
		case errors.Is(err, reviewport.ErrApplicationReviewNotLatest):
			return nil, reviewport.ErrApplicationReviewNotLatest
		case errors.Is(err, reviewport.ErrApplicationReviewAlreadyRestored):
			return nil, reviewport.ErrApplicationReviewAlreadyRestored
		case errors.Is(err, reviewport.ErrApplicationVersionNotRejected):
			return nil, reviewport.ErrApplicationVersionNotRejected
		case errors.Is(err, reviewport.ErrApplicationVersionRevisionConflict):
			return nil, reviewport.ErrApplicationVersionRevisionConflict
		case errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent):
			return nil, reviewport.ErrApplicationReviewStateInconsistent
		default:
			return nil, fmt.Errorf("restore application review transaction: %w", err)
		}
	}
	restoration, ok := result.(*reviewdomain.RestoreRejectedVersionResult)
	if !ok || restoration == nil {
		return nil, fmt.Errorf("restore application review transaction: invalid transaction result")
	}
	return restoration, nil
}

func (repository *ApplicationReviewRestorationRepository) restoreDraftTransaction(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
	expectedAdminID shared.AuthID,
	expectedVersionRevision int64,
	restoredAt time.Time,
) (*reviewdomain.RestoreRejectedVersionResult, error) {
	reviewDocument, err := repository.readRestorationReview(ctx, applicationID, versionID, reviewID)
	if err != nil {
		return nil, err
	}
	versionDocument, err := repository.readRestorationVersion(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	currentAdminID, err := repository.lockRestorationApplication(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	if currentAdminID != expectedAdminID {
		return nil, reviewport.ErrApplicationAdminRequired
	}

	latestAttempt, err := repository.readLatestReviewAttempt(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	if reviewDocument.Attempt != latestAttempt {
		return nil, reviewport.ErrApplicationReviewNotLatest
	}
	if reviewDocument.DraftRestoration != nil {
		return nil, reviewport.ErrApplicationReviewAlreadyRestored
	}
	if versionDocument.ReviewStatus != string(versiondomain.ReviewStatusRejected) {
		return nil, reviewport.ErrApplicationVersionNotRejected
	}
	if versionDocument.Revision != expectedVersionRevision {
		return nil, reviewport.ErrApplicationVersionRevisionConflict
	}

	review, err := applicationReviewFromDocument(reviewDocument)
	if err != nil {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	if review.Status() != reviewdomain.ReviewStatusRejected || !review.HasDecision() ||
		review.Decision().Outcome() != reviewdomain.ReviewDecisionRejected ||
		versionDocument.Revision != review.SourceVersionRevision()+2 {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	version, err := applicationVersionFromDocument(versionDocument)
	if err != nil {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	restoredVersion, err := version.RestoreDraft(expectedVersionRevision, expectedAdminID, restoredAt)
	if err != nil {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	restoration, err := review.RecordDraftRestoration(expectedAdminID, restoredAt, restoredVersion.Revision())
	if err != nil {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	restorationDocument := applicationReviewDraftRestorationDocument{
		RestoredBy:            restoration.RestoredBy().String(),
		RestoredAt:            restoration.RestoredAt().UTC(),
		ResultVersionRevision: restoration.ResultVersionRevision(),
	}

	var updatedReviewDocument applicationReviewDocument
	err = repository.database.Collection(applicationReviewsCollectionName).FindOneAndUpdate(
		ctx,
		bson.D{
			{Key: "reviewId", Value: reviewID.String()},
			{Key: "applicationId", Value: applicationID.String()},
			{Key: "versionId", Value: versionID.String()},
			{Key: "attempt", Value: latestAttempt},
			{Key: "status", Value: string(reviewdomain.ReviewStatusRejected)},
			{Key: "decision.outcome", Value: reviewdomain.ReviewDecisionRejected.String()},
			{Key: "draftRestoration", Value: nil},
		},
		bson.D{{Key: "$set", Value: bson.D{{Key: "draftRestoration", Value: restorationDocument}}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedReviewDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	if err != nil {
		return nil, fmt.Errorf("write application review draft restoration: %w", err)
	}

	var updatedVersionDocument applicationVersionDocument
	err = repository.database.Collection(applicationVersionsCollectionName).FindOneAndUpdate(
		ctx,
		bson.D{
			{Key: "versionId", Value: versionID.String()},
			{Key: "applicationId", Value: applicationID.String()},
			{Key: "reviewStatus", Value: string(versiondomain.ReviewStatusRejected)},
			{Key: "revision", Value: expectedVersionRevision},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "reviewStatus", Value: string(versiondomain.ReviewStatusDraft)},
				{Key: "updatedBy", Value: restoredVersion.UpdatedBy().String()},
				{Key: "updatedAt", Value: restoredVersion.UpdatedAt().UTC()},
			}},
			{Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedVersionDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	if err != nil {
		return nil, fmt.Errorf("write restored application version: %w", err)
	}

	updatedReview, err := applicationReviewFromDocument(updatedReviewDocument)
	if err != nil {
		return nil, fmt.Errorf("restore application review: %w", err)
	}
	versionResult, err := reviewdomain.NewRestoredApplicationVersion(
		applicationID,
		versionID,
		updatedVersionDocument.Revision,
		shared.AuthID(updatedVersionDocument.UpdatedBy),
		updatedVersionDocument.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("restore application review: %w", err)
	}
	result, err := reviewdomain.NewRestoreRejectedVersionResult(updatedReview, versionResult)
	if err != nil {
		return nil, fmt.Errorf("restore application review: %w", err)
	}
	return result, nil
}

func (repository *ApplicationReviewRestorationRepository) readRestorationReview(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
) (applicationReviewDocument, error) {
	var document applicationReviewDocument
	err := repository.database.Collection(applicationReviewsCollectionName).FindOne(ctx, bson.D{
		{Key: "reviewId", Value: reviewID.String()},
		{Key: "applicationId", Value: applicationID.String()},
		{Key: "versionId", Value: versionID.String()},
	}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return applicationReviewDocument{}, reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return applicationReviewDocument{}, fmt.Errorf("read application review for restoration: %w", err)
	}
	return document, nil
}

func (repository *ApplicationReviewRestorationRepository) readRestorationVersion(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
) (applicationVersionDocument, error) {
	var document applicationVersionDocument
	err := repository.database.Collection(applicationVersionsCollectionName).FindOne(ctx, bson.D{
		{Key: "versionId", Value: versionID.String()},
		{Key: "applicationId", Value: applicationID.String()},
	}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return applicationVersionDocument{}, reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return applicationVersionDocument{}, fmt.Errorf("read application version for restoration: %w", err)
	}
	return document, nil
}

func (repository *ApplicationReviewRestorationRepository) lockRestorationApplication(
	ctx context.Context,
	applicationID shared.ApplicationID,
) (shared.AuthID, error) {
	var document struct {
		AdminID string `bson:"adminId"`
	}
	err := repository.database.Collection(applicationsCollectionName).FindOneAndUpdate(
		ctx,
		bson.D{{Key: "id", Value: applicationID.String()}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}},
		options.FindOneAndUpdate().SetProjection(bson.D{{Key: "adminId", Value: 1}}).SetReturnDocument(options.Before),
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return "", reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock application for restoration: %w", err)
	}
	adminID := shared.AuthID(document.AdminID)
	if !adminID.IsValid() {
		return "", reviewport.ErrApplicationReviewStateInconsistent
	}
	return adminID, nil
}

func (repository *ApplicationReviewRestorationRepository) readLatestReviewAttempt(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
) (int32, error) {
	var latest struct {
		Attempt int32 `bson:"attempt"`
	}
	err := repository.database.Collection(applicationReviewsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "versionId", Value: versionID.String()}},
		options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}}).SetProjection(bson.D{{Key: "attempt", Value: 1}}),
	).Decode(&latest)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return 0, reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read latest application review for restoration: %w", err)
	}
	return latest.Attempt, nil
}

var _ reviewport.RejectedApplicationVersionRepository = (*ApplicationReviewRestorationRepository)(nil)
