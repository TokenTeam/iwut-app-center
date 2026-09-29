package mongo

import (
	"context"
	"errors"
	"fmt"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

// ApplicationReviewDecisionRepository owns the one-time decision of a PENDING
// ApplicationReview and the atomic ApplicationVersion lifecycle/audit
// transition.
type ApplicationReviewDecisionRepository struct {
	database *drivermongo.Database
}

var _ reviewport.ApplicationReviewDecisionRepository = (*ApplicationReviewDecisionRepository)(nil)

func NewApplicationReviewDecisionRepository(database *drivermongo.Database) *ApplicationReviewDecisionRepository {
	return &ApplicationReviewDecisionRepository{database: database}
}

func (repository *ApplicationReviewDecisionRepository) LoadDecisionCandidate(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
	reviewerID shared.AuthID,
) (*reviewdomain.ApplicationReviewDecisionCandidate, error) {
	if repository == nil || repository.database == nil {
		return nil, fmt.Errorf("load application review decision candidate: MongoDB database is nil")
	}
	if !applicationID.IsValid() || !versionID.IsValid() || !reviewID.IsValid() || !reviewerID.IsValid() {
		return nil, fmt.Errorf("load application review decision candidate: invalid repository input")
	}

	adminID, err := repository.readApplicationAdmin(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	reviewDocument, err := repository.readDecisionReview(ctx, applicationID, versionID, reviewID)
	if err != nil {
		return nil, err
	}
	review, err := applicationReviewFromDocument(reviewDocument)
	if err != nil {
		return nil, fmt.Errorf("load application review decision candidate: %w", err)
	}
	if review.Status() != reviewdomain.ReviewStatusPending || review.HasDecision() {
		return nil, reviewport.ErrApplicationReviewAlreadyDecided
	}

	versionDocument, err := repository.readDecisionVersion(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	oauthDocument, err := repository.readDecisionVersionOAuthConfig(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	candidate, err := decisionCandidateFromDocuments(review, versionDocument, oauthDocument, adminID)
	if err != nil {
		return nil, err
	}
	if candidate.ReviewerConflicts(reviewerID) {
		return nil, reviewport.ErrApplicationReviewConflictOfInterest
	}
	return candidate, nil
}

func (repository *ApplicationReviewDecisionRepository) Decide(
	ctx context.Context,
	candidate *reviewdomain.ApplicationReviewDecisionCandidate,
	reviewerID shared.AuthID,
	decision *reviewdomain.ApplicationReviewDecision,
) (*reviewdomain.ApplicationReviewDecisionResult, error) {
	if repository == nil || repository.database == nil {
		return nil, fmt.Errorf("decide application review: MongoDB database is nil")
	}
	if candidate == nil || candidate.Review() == nil || !reviewerID.IsValid() || decision == nil {
		return nil, fmt.Errorf("decide application review: invalid repository input")
	}

	session, err := repository.database.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("start application review decision transaction: %w", err)
	}
	defer session.EndSession(ctx)

	transactionOptions := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())
	result, err := session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		return repository.decideTransaction(transactionContext, candidate, reviewerID, decision)
	}, transactionOptions)
	if err != nil {
		switch {
		case errors.Is(err, reviewport.ErrApplicationReviewNotFound):
			return nil, reviewport.ErrApplicationReviewNotFound
		case errors.Is(err, reviewport.ErrApplicationReviewAlreadyDecided):
			return nil, reviewport.ErrApplicationReviewAlreadyDecided
		case errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent):
			return nil, reviewport.ErrApplicationReviewStateInconsistent
		case errors.Is(err, reviewport.ErrApplicationReviewConflictOfInterest):
			return nil, reviewport.ErrApplicationReviewConflictOfInterest
		default:
			return nil, fmt.Errorf("decide application review transaction: %w", err)
		}
	}
	decisionResult, ok := result.(*reviewdomain.ApplicationReviewDecisionResult)
	if !ok || decisionResult == nil {
		return nil, fmt.Errorf("decide application review transaction: invalid transaction result")
	}
	return decisionResult, nil
}

func (repository *ApplicationReviewDecisionRepository) decideTransaction(
	ctx context.Context,
	candidate *reviewdomain.ApplicationReviewDecisionCandidate,
	reviewerID shared.AuthID,
	decision *reviewdomain.ApplicationReviewDecision,
) (*reviewdomain.ApplicationReviewDecisionResult, error) {
	reviewReference := candidate.Review()
	applicationID := reviewReference.ApplicationID()
	versionID := reviewReference.VersionID()
	reviewID := reviewReference.ReviewID()

	// The conditional $inc on the adapter-only coordinationRevision is a real
	// write and therefore a document-level fence: a concurrent administrator
	// transfer on the same Application conflicts and forces a retry that
	// re-reads the current administrator. Reading the same document also gives
	// the current adminId used by the final conflict-of-interest check.
	currentAdminID, err := repository.lockAndReadApplicationAdmin(ctx, applicationID)
	if err != nil {
		return nil, err
	}

	reviewDocument, err := repository.readDecisionReview(ctx, applicationID, versionID, reviewID)
	if err != nil {
		return nil, err
	}
	currentReview, err := applicationReviewFromDocument(reviewDocument)
	if err != nil {
		return nil, fmt.Errorf("decide application review: %w", err)
	}
	if currentReview.Status() != reviewdomain.ReviewStatusPending || currentReview.HasDecision() {
		return nil, reviewport.ErrApplicationReviewAlreadyDecided
	}
	if !currentReview.Snapshot().Equal(reviewReference.Snapshot()) ||
		currentReview.SourceVersionRevision() != reviewReference.SourceVersionRevision() ||
		currentReview.SubmittedBy() != reviewReference.SubmittedBy() {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}

	versionDocument, err := repository.readDecisionVersion(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	oauthDocument, err := repository.readDecisionVersionOAuthConfig(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	if versionDocument.ReviewStatus != reviewdomain.SubmittedVersionReviewStatus ||
		versionDocument.Revision != currentReview.SourceVersionRevision()+1 {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	currentVersionRevision, versionCreatedBy, currentVersionSnapshot, err := applicationVersionDocumentToDecisionVersion(versionDocument, oauthDocument)
	if err != nil {
		return nil, fmt.Errorf("decide application review: %w", err)
	}
	if !currentVersionSnapshot.Equal(currentReview.Snapshot()) {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}

	// The final transaction re-confirms the administrator fact the candidate
	// was loaded with, so a decision never commits against a stale administrator
	// or a stale suspension observation.
	if currentAdminID != candidate.CurrentAdminID() {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	if reviewerID == currentAdminID || reviewerID == versionCreatedBy ||
		reviewerID == currentReview.SubmittedBy() {
		return nil, reviewport.ErrApplicationReviewConflictOfInterest
	}

	decisionDocument, err := applicationReviewDecisionToDocument(decision)
	if err != nil {
		return nil, fmt.Errorf("decide application review: %w", err)
	}
	var updatedReviewDocument applicationReviewDocument
	err = repository.database.Collection(applicationReviewsCollectionName).FindOneAndUpdate(
		ctx,
		bson.D{
			{Key: "reviewId", Value: reviewID.String()},
			{Key: "applicationId", Value: applicationID.String()},
			{Key: "versionId", Value: versionID.String()},
			{Key: "status", Value: string(reviewdomain.ReviewStatusPending)},
			{Key: "decision", Value: nil},
		},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "status", Value: decision.Outcome().String()},
			{Key: "decision", Value: decisionDocument},
		}}},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedReviewDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, reviewport.ErrApplicationReviewAlreadyDecided
	}
	if err != nil {
		return nil, fmt.Errorf("write application review decision: %w", err)
	}

	var updatedVersionDocument applicationVersionDocument
	err = repository.database.Collection(applicationVersionsCollectionName).FindOneAndUpdate(
		ctx,
		bson.D{
			{Key: "versionId", Value: versionID.String()},
			{Key: "applicationId", Value: applicationID.String()},
			{Key: "reviewStatus", Value: reviewdomain.SubmittedVersionReviewStatus},
			{Key: "revision", Value: currentVersionRevision},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "reviewStatus", Value: decision.Outcome().String()},
				{Key: "updatedBy", Value: decision.DecidedBy().String()},
				{Key: "updatedAt", Value: decision.DecidedAt().UTC()},
			}},
			{Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}},
		},
		options.FindOneAndUpdate().SetReturnDocument(options.After),
	).Decode(&updatedVersionDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	if err != nil {
		return nil, fmt.Errorf("write decided application version: %w", err)
	}

	decidedReview, err := applicationReviewFromDocument(updatedReviewDocument)
	if err != nil {
		return nil, fmt.Errorf("decide application review: %w", err)
	}
	persistedDecision := decidedReview.Decision()
	if persistedDecision == nil || updatedVersionDocument.ReviewStatus != persistedDecision.Outcome().String() ||
		updatedVersionDocument.Revision != currentVersionRevision+1 ||
		updatedVersionDocument.UpdatedBy != persistedDecision.DecidedBy().String() ||
		!updatedVersionDocument.UpdatedAt.Equal(persistedDecision.DecidedAt()) {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	// MongoDB stores datetimes at millisecond precision. Build the returned
	// Version summary from the persisted decision so Review and Version audit
	// timestamps remain exactly equal even when the injected clock has finer
	// precision.
	versionResult, err := reviewdomain.NewDecidedApplicationVersion(candidate, persistedDecision)
	if err != nil {
		return nil, fmt.Errorf("decide application review: %w", err)
	}
	result, err := reviewdomain.NewApplicationReviewDecisionResult(decidedReview, versionResult)
	if err != nil {
		return nil, fmt.Errorf("decide application review: %w", err)
	}
	return result, nil
}

func (repository *ApplicationReviewDecisionRepository) readApplicationAdmin(
	ctx context.Context,
	applicationID shared.ApplicationID,
) (shared.AuthID, error) {
	var document struct {
		AdminID string `bson:"adminId"`
	}
	err := repository.database.Collection(applicationsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "id", Value: applicationID.String()}},
		options.FindOne().SetProjection(bson.D{{Key: "adminId", Value: 1}}),
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return "", reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read application administrator for review decision: %w", err)
	}
	adminID := shared.AuthID(document.AdminID)
	if !adminID.IsValid() {
		return "", fmt.Errorf("read application administrator for review decision: invalid administrator")
	}
	return adminID, nil
}

func (repository *ApplicationReviewDecisionRepository) lockAndReadApplicationAdmin(
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
		options.FindOneAndUpdate().
			SetProjection(bson.D{{Key: "adminId", Value: 1}}).
			SetReturnDocument(options.Before),
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return "", reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock application for review decision: %w", err)
	}
	adminID := shared.AuthID(document.AdminID)
	if !adminID.IsValid() {
		return "", fmt.Errorf("lock application for review decision: invalid administrator")
	}
	return adminID, nil
}

func (repository *ApplicationReviewDecisionRepository) readDecisionReview(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
) (applicationReviewDocument, error) {
	var document applicationReviewDocument
	err := repository.database.Collection(applicationReviewsCollectionName).FindOne(
		ctx,
		bson.D{
			{Key: "reviewId", Value: reviewID.String()},
			{Key: "applicationId", Value: applicationID.String()},
			{Key: "versionId", Value: versionID.String()},
		},
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return applicationReviewDocument{}, reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return applicationReviewDocument{}, fmt.Errorf("read application review for decision: %w", err)
	}
	return document, nil
}

func (repository *ApplicationReviewDecisionRepository) readDecisionVersion(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
) (applicationVersionDocument, error) {
	var document applicationVersionDocument
	err := repository.database.Collection(applicationVersionsCollectionName).FindOne(
		ctx,
		bson.D{
			{Key: "versionId", Value: versionID.String()},
			{Key: "applicationId", Value: applicationID.String()},
		},
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return applicationVersionDocument{}, reviewport.ErrApplicationReviewNotFound
	}
	if err != nil {
		return applicationVersionDocument{}, fmt.Errorf("read application version for decision: %w", err)
	}
	return document, nil
}

func (repository *ApplicationReviewDecisionRepository) readDecisionVersionOAuthConfig(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
) (applicationVersionOAuthConfigDocument, error) {
	var document applicationVersionOAuthConfigDocument
	err := repository.database.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "applicationVersionId", Value: versionID.String()}, {Key: "applicationId", Value: applicationID.String()}},
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return applicationVersionOAuthConfigDocument{}, reviewport.ErrApplicationReviewStateInconsistent
	}
	if err != nil {
		return applicationVersionOAuthConfigDocument{}, fmt.Errorf("read application version OAuth config for decision: %w", err)
	}
	return document, nil
}

func decisionCandidateFromDocuments(
	review *reviewdomain.ApplicationReview,
	versionDocument applicationVersionDocument,
	oauthDocument applicationVersionOAuthConfigDocument,
	adminID shared.AuthID,
) (*reviewdomain.ApplicationReviewDecisionCandidate, error) {
	if versionDocument.ReviewStatus != reviewdomain.SubmittedVersionReviewStatus {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	revision, createdBy, snapshot, err := applicationVersionDocumentToDecisionVersion(versionDocument, oauthDocument)
	if err != nil {
		return nil, fmt.Errorf("load application review decision candidate: %w", err)
	}
	if revision != review.SourceVersionRevision()+1 || !snapshot.Equal(review.Snapshot()) {
		return nil, reviewport.ErrApplicationReviewStateInconsistent
	}
	candidate, err := reviewdomain.NewApplicationReviewDecisionCandidate(
		review,
		versionDocument.ReviewStatus,
		revision,
		createdBy,
		&snapshot,
		adminID,
	)
	if err != nil {
		return nil, fmt.Errorf("load application review decision candidate: %w", err)
	}
	return candidate, nil
}
