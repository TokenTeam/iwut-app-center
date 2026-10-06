package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const applicationReviewsCollectionName = "application_reviews"

type ApplicationReviewRepository struct {
	database *drivermongo.Database
}

var _ reviewport.ApplicationReviewRepository = (*ApplicationReviewRepository)(nil)

func NewApplicationReviewRepository(database *drivermongo.Database) *ApplicationReviewRepository {
	return &ApplicationReviewRepository{database: database}
}

func (repository *ApplicationReviewRepository) LoadSubmissionCandidate(
	ctx context.Context,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
) (*reviewdomain.SubmissionCandidate, error) {
	if err := repository.validateInput(applicationID, versionID, expectedAdminID, expectedRevision); err != nil {
		return nil, err
	}
	if err := repository.checkAdministrator(ctx, applicationID, expectedAdminID); err != nil {
		return nil, err
	}
	document, err := repository.loadVersion(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	if document.ReviewStatus != "DRAFT" {
		return nil, reviewport.ErrApplicationVersionNotDraft
	}
	if document.Revision != expectedRevision {
		return nil, reviewport.ErrApplicationVersionRevisionConflict
	}
	oauthDocument, err := repository.loadVersionOAuthConfig(ctx, applicationID, versionID)
	if err != nil {
		return nil, err
	}
	candidate, err := applicationVersionDocumentToSubmissionCandidate(document, oauthDocument)
	if err != nil {
		return nil, fmt.Errorf("load review submission candidate: %w", err)
	}
	return candidate, nil
}

func (repository *ApplicationReviewRepository) Submit(
	ctx context.Context,
	candidate *reviewdomain.SubmissionCandidate,
	reviewID reviewdomain.ApplicationReviewID,
	expectedAdminID shared.AuthID,
	scopeCatalogRevision reviewdomain.ScopeCatalogRevision,
	preflightPolicyVersion reviewdomain.PreflightPolicyVersion,
	submittedAt time.Time,
) (*reviewdomain.ReviewSubmissionResult, error) {
	if repository == nil || repository.database == nil {
		return nil, fmt.Errorf("submit application review: MongoDB database is nil")
	}
	if candidate == nil || !reviewID.IsValid() || !expectedAdminID.IsValid() || scopeCatalogRevision < 1 ||
		submittedAt.IsZero() {
		return nil, fmt.Errorf("submit application review: invalid repository input")
	}
	if _, err := reviewdomain.NewPreflightPolicyVersion(preflightPolicyVersion.String()); err != nil {
		return nil, fmt.Errorf("submit application review: invalid preflight policy version")
	}

	session, err := repository.database.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("start application review submission transaction: %w", err)
	}
	defer session.EndSession(ctx)

	transactionOptions := options.Transaction().
		SetReadConcern(readconcern.Snapshot()).
		SetWriteConcern(writeconcern.Majority())
	result, err := session.WithTransaction(ctx, func(transactionContext context.Context) (any, error) {
		return repository.submitTransaction(
			transactionContext, candidate, reviewID, expectedAdminID,
			scopeCatalogRevision, preflightPolicyVersion, submittedAt.UTC(),
		)
	}, transactionOptions)
	if err != nil {
		switch {
		case errors.Is(err, reviewport.ErrApplicationVersionNotFound):
			return nil, reviewport.ErrApplicationVersionNotFound
		case errors.Is(err, reviewport.ErrApplicationAdminRequired):
			return nil, reviewport.ErrApplicationAdminRequired
		case errors.Is(err, reviewport.ErrApplicationVersionNotDraft):
			return nil, reviewport.ErrApplicationVersionNotDraft
		case errors.Is(err, reviewport.ErrApplicationVersionRevisionConflict):
			return nil, reviewport.ErrApplicationVersionRevisionConflict
		default:
			return nil, fmt.Errorf("submit application review transaction: %w", err)
		}
	}
	submission, ok := result.(*reviewdomain.ReviewSubmissionResult)
	if !ok || submission == nil {
		return nil, fmt.Errorf("submit application review transaction: invalid transaction result")
	}
	return submission, nil
}

func (repository *ApplicationReviewRepository) submitTransaction(
	ctx context.Context,
	candidate *reviewdomain.SubmissionCandidate,
	reviewID reviewdomain.ApplicationReviewID,
	expectedAdminID shared.AuthID,
	scopeCatalogRevision reviewdomain.ScopeCatalogRevision,
	preflightPolicyVersion reviewdomain.PreflightPolicyVersion,
	submittedAt time.Time,
) (*reviewdomain.ReviewSubmissionResult, error) {
	if err := repository.lockAdministrator(ctx, candidate.ApplicationID(), expectedAdminID); err != nil {
		return nil, err
	}
	document, err := repository.loadVersion(ctx, candidate.ApplicationID(), candidate.VersionID())
	if err != nil {
		return nil, err
	}
	if document.ReviewStatus != "DRAFT" {
		return nil, reviewport.ErrApplicationVersionNotDraft
	}
	if document.Revision != candidate.Revision() {
		return nil, reviewport.ErrApplicationVersionRevisionConflict
	}
	oauthDocument, err := repository.loadVersionOAuthConfig(ctx, candidate.ApplicationID(), candidate.VersionID())
	if err != nil {
		return nil, err
	}
	currentCandidate, err := applicationVersionDocumentToSubmissionCandidate(document, oauthDocument)
	if err != nil {
		return nil, fmt.Errorf("validate current review submission candidate: %w", err)
	}
	if !submissionCandidatesEqual(currentCandidate, candidate) {
		return nil, reviewport.ErrApplicationVersionRevisionConflict
	}

	attempt, err := repository.nextAttempt(ctx, candidate.VersionID())
	if err != nil {
		return nil, err
	}
	review, err := reviewdomain.NewPendingApplicationReview(
		candidate, reviewID, attempt, scopeCatalogRevision, preflightPolicyVersion, expectedAdminID, submittedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create pending application review: %w", err)
	}
	reviewDocument, err := applicationReviewToDocument(review)
	if err != nil {
		return nil, err
	}
	if _, err := repository.database.Collection(applicationReviewsCollectionName).InsertOne(ctx, reviewDocument); err != nil {
		return nil, fmt.Errorf("insert application review: %w", err)
	}

	updateResult, err := repository.database.Collection(applicationVersionsCollectionName).UpdateOne(
		ctx,
		bson.D{
			{Key: "versionId", Value: candidate.VersionID().String()},
			{Key: "applicationId", Value: candidate.ApplicationID().String()},
			{Key: "reviewStatus", Value: "DRAFT"},
			{Key: "revision", Value: candidate.Revision()},
		},
		bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "reviewStatus", Value: "SUBMITTED"},
				{Key: "updatedBy", Value: expectedAdminID.String()},
				{Key: "updatedAt", Value: submittedAt},
			}},
			{Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("update submitted application version: %w", err)
	}
	if updateResult.MatchedCount != 1 || updateResult.ModifiedCount != 1 {
		return nil, reviewport.ErrApplicationVersionRevisionConflict
	}

	submittedVersion, err := reviewdomain.NewSubmittedApplicationVersion(candidate, expectedAdminID, submittedAt)
	if err != nil {
		return nil, fmt.Errorf("create submitted application version result: %w", err)
	}
	result, err := reviewdomain.NewReviewSubmissionResult(review, submittedVersion)
	if err != nil {
		return nil, fmt.Errorf("create application review submission result: %w", err)
	}
	return result, nil
}

// lockAdministrator turns the final ownership check into a document write in
// the submission transaction. The conditional $inc on the adapter-only
// coordinationRevision is a real write, so a concurrent administrator transfer
// on the same Application document conflicts and forces a retry that
// re-evaluates the administrator filter. A same-value $set would not reliably
// produce that write conflict, letting an old administrator commit against a
// stale snapshot. It is never nextVersionSequence, which belongs to UC-APP-002.
func (repository *ApplicationReviewRepository) lockAdministrator(
	ctx context.Context,
	applicationID shared.ApplicationID,
	expectedAdminID shared.AuthID,
) error {
	var document struct {
		AdminID string `bson:"adminId"`
	}
	err := repository.database.Collection(applicationsCollectionName).FindOneAndUpdate(
		ctx,
		bson.D{{Key: "id", Value: applicationID.String()}, {Key: "adminId", Value: expectedAdminID.String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}},
		options.FindOneAndUpdate().SetProjection(bson.D{{Key: "adminId", Value: 1}}).SetReturnDocument(options.Before),
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return repository.classifyMissingAdministrator(ctx, applicationID)
	}
	if err != nil {
		return fmt.Errorf("lock application administrator: %w", err)
	}
	return nil
}

func (repository *ApplicationReviewRepository) validateInput(
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
) error {
	if repository == nil || repository.database == nil {
		return fmt.Errorf("load application review submission candidate: MongoDB database is nil")
	}
	if !applicationID.IsValid() || !versionID.IsValid() || !expectedAdminID.IsValid() || expectedRevision < 1 {
		return fmt.Errorf("load application review submission candidate: invalid repository input")
	}
	return nil
}

func (repository *ApplicationReviewRepository) checkAdministrator(
	ctx context.Context,
	applicationID shared.ApplicationID,
	expectedAdminID shared.AuthID,
) error {
	var document struct {
		AdminID string `bson:"adminId"`
	}
	err := repository.database.Collection(applicationsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "id", Value: applicationID.String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}},
		options.FindOne().SetProjection(bson.D{{Key: "adminId", Value: 1}}),
	).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return reviewport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return fmt.Errorf("read application administrator: %w", err)
	}
	if document.AdminID != expectedAdminID.String() {
		return reviewport.ErrApplicationAdminRequired
	}
	return nil
}

func (repository *ApplicationReviewRepository) classifyMissingAdministrator(ctx context.Context, applicationID shared.ApplicationID) error {
	err := repository.database.Collection(applicationsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "id", Value: applicationID.String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}},
		options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}}),
	).Err()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return reviewport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return fmt.Errorf("classify application administrator: %w", err)
	}
	return reviewport.ErrApplicationAdminRequired
}

func (repository *ApplicationReviewRepository) loadVersion(
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
		return applicationVersionDocument{}, reviewport.ErrApplicationVersionNotFound
	}
	if err != nil {
		return applicationVersionDocument{}, fmt.Errorf("read application version: %w", err)
	}
	return document, nil
}

func (repository *ApplicationReviewRepository) loadVersionOAuthConfig(
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
		return applicationVersionOAuthConfigDocument{}, fmt.Errorf("read application version OAuth config: missing dependent configuration")
	}
	if err != nil {
		return applicationVersionOAuthConfigDocument{}, fmt.Errorf("read application version OAuth config: %w", err)
	}
	return document, nil
}

func (repository *ApplicationReviewRepository) nextAttempt(ctx context.Context, versionID reviewdomain.ApplicationVersionID) (reviewdomain.ReviewAttempt, error) {
	var latest struct {
		Attempt int32 `bson:"attempt"`
	}
	err := repository.database.Collection(applicationReviewsCollectionName).FindOne(
		ctx,
		bson.D{{Key: "versionId", Value: versionID.String()}},
		options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}}).SetProjection(bson.D{{Key: "attempt", Value: 1}}),
	).Decode(&latest)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return reviewdomain.NewReviewAttempt(1)
	}
	if err != nil {
		return 0, fmt.Errorf("read latest application review attempt: %w", err)
	}
	if latest.Attempt == math.MaxInt32 {
		return 0, fmt.Errorf("allocate application review attempt: attempt exhausted")
	}
	attempt, err := reviewdomain.NewReviewAttempt(latest.Attempt + 1)
	if err != nil {
		return 0, fmt.Errorf("allocate application review attempt: %w", err)
	}
	return attempt, nil
}

func submissionCandidatesEqual(left, right *reviewdomain.SubmissionCandidate) bool {
	if left == nil || right == nil || left.ApplicationID() != right.ApplicationID() ||
		left.VersionID() != right.VersionID() || left.Revision() != right.Revision() {
		return false
	}
	leftSnapshot := left.Snapshot()
	rightSnapshot := right.Snapshot()
	return leftSnapshot.VersionLabel() == rightSnapshot.VersionLabel() &&
		leftSnapshot.LaunchURL() == rightSnapshot.LaunchURL() &&
		leftSnapshot.RPCAPIMinVersion() == rightSnapshot.RPCAPIMinVersion() &&
		leftSnapshot.RPCAPIMaxVersionExclusive() == rightSnapshot.RPCAPIMaxVersionExclusive() &&
		slices.Equal(leftSnapshot.RequiredCapabilities(), rightSnapshot.RequiredCapabilities()) &&
		slices.Equal(leftSnapshot.RequiredScopes(), rightSnapshot.RequiredScopes()) &&
		slices.Equal(leftSnapshot.OptionalScopes(), rightSnapshot.OptionalScopes()) &&
		leftSnapshot.OAuthRedirects().Equal(rightSnapshot.OAuthRedirects())
}
