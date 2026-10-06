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
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

var _ pp.ApplicationProfileReviewRepository = (*ApplicationProfileRevisionRepository)(nil)

func (r *ApplicationProfileRevisionRepository) SubmitDraft(ctx context.Context, app shared.ApplicationID, id pd.ApplicationProfileRevisionID, admin shared.AuthID, expected int64, reviewID pd.ApplicationProfileReviewID, at time.Time) (*pd.ApplicationProfileSubmission, error) {
	if r == nil || r.database == nil || !app.IsValid() || !id.IsValid() || !admin.IsValid() || expected < 1 || !reviewID.IsValid() || at.IsZero() {
		return nil, fmt.Errorf("submit profile draft: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.submitProfileDraftTransaction(tx, app, id, admin, expected, reviewID, at.UTC().Truncate(time.Millisecond))
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	submission, ok := result.(*pd.ApplicationProfileSubmission)
	if !ok || submission == nil {
		return nil, fmt.Errorf("submit profile draft: invalid transaction result")
	}
	return submission, nil
}

func (r *ApplicationProfileRevisionRepository) submitProfileDraftTransaction(ctx context.Context, app shared.ApplicationID, id pd.ApplicationProfileRevisionID, admin shared.AuthID, expected int64, reviewID pd.ApplicationProfileReviewID, at time.Time) (*pd.ApplicationProfileSubmission, error) {
	// The shared Application write fence serializes submission, editing, creation
	// and administrator transfer. Retrying the transaction reloads every fact.
	raw, err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, bson.M{"id": app.String(), "lifecycleStatus": "ACTIVE"}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}}).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, pp.ErrApplicationProfileRevisionNotFound
	}
	if err != nil {
		return nil, err
	}
	if raw.Lookup("adminId").Type != bson.TypeString {
		return nil, pp.ErrApplicationProfileStateInconsistent
	}
	if raw.Lookup("adminId").StringValue() != admin.String() {
		return nil, pp.ErrApplicationAdminRequired
	}
	revisions := r.database.Collection(applicationProfileRevisionsCollectionName)
	filter := bson.M{"applicationId": app.String(), "profileRevisionId": id.String()}
	raw, err = revisions.FindOne(ctx, filter).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, pp.ErrApplicationProfileRevisionNotFound
	}
	if err != nil {
		return nil, err
	}
	current, err := profileRevisionForSubmission(raw)
	if err != nil {
		return nil, err
	}
	if current.ReviewStatus() != pd.ReviewStatusDraft {
		return nil, pp.ErrApplicationProfileRevisionNotDraft
	}
	if current.Revision() != expected {
		return nil, pp.ErrApplicationProfileRevisionConflict
	}
	_, projection, err := r.loadProfileWorkingState(ctx, app)
	if err != nil {
		return nil, err
	}
	if projection.WorkingProfileRevisionID == nil || *projection.WorkingProfileRevisionID != id.String() {
		return nil, pp.ErrApplicationProfileStateInconsistent
	}
	reviews := r.database.Collection(applicationProfileReviewsCollectionName)
	err = reviews.FindOne(ctx, bson.M{"profileRevisionId": id.String(), "$or": bson.A{bson.M{"status": "PENDING"}, bson.M{"sourceRevision": expected}}}).Err()
	if err == nil {
		return nil, pp.ErrApplicationProfileStateInconsistent
	}
	if !errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, err
	}
	var attempt int32 = 1
	latest, err := reviews.FindOne(ctx, bson.M{"profileRevisionId": id.String()}, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})).Raw()
	if err == nil {
		last, ok := latest.Lookup("attempt").Int32OK()
		appID, validApp := latest.Lookup("applicationId").StringValueOK()
		if !ok || last < 1 || last == math.MaxInt32 || !validApp || appID != app.String() {
			return nil, pp.ErrApplicationProfileStateInconsistent
		}
		attempt = last + 1
	} else if !errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, err
	}
	submission, err := current.SubmitDraft(expected, reviewID, attempt, admin, at)
	if err != nil {
		if errors.Is(err, pd.ErrInvalidApplicationProfileContent) {
			return nil, pp.ErrInvalidApplicationProfileContent
		}
		return nil, pp.ErrApplicationProfileStateInconsistent
	}
	if _, err = reviews.InsertOne(ctx, profileReviewToDocument(submission.Review)); err != nil {
		return nil, err
	}
	filter["reviewStatus"] = "DRAFT"
	filter["revision"] = expected
	result, err := revisions.UpdateOne(ctx, filter, bson.M{"$set": bson.M{"reviewStatus": "SUBMITTED", "revision": submission.ProfileRevision.Revision(), "updatedBy": admin.String(), "updatedAt": at}})
	if err != nil {
		return nil, err
	}
	if result.MatchedCount != 1 {
		return nil, pp.ErrApplicationProfileStateInconsistent
	}
	// The projection remains byte-for-byte unchanged, including publication. A
	// concurrent profile writer is excluded by the same Application write fence.
	return submission, nil
}

// UC015 classifies invalid stored content separately from structural/audit
// corruption. Validate metadata first, without normalizing or repairing text.
func profileRevisionForSubmission(raw bson.Raw) (*pd.ApplicationProfileRevision, error) {
	doc, err := profileRevisionDocumentFromRaw(raw)
	if err != nil {
		return nil, err
	}
	metadata := doc
	metadata.DisplayName, metadata.Description, metadata.Icon = "valid", nil, nil
	if _, err = profileRevisionFromDocument(metadata); err != nil {
		return nil, err
	}
	if err = pd.ValidateStoredApplicationProfileContent(doc.DisplayName, doc.Description, doc.Icon); err != nil {
		return nil, pp.ErrInvalidApplicationProfileContent
	}
	return profileRevisionFromDocument(doc)
}

type applicationProfileReviewSnapshotDocument struct {
	DisplayName string  `bson:"displayName"`
	Description *string `bson:"description"`
	Icon        *string `bson:"icon"`
}
type applicationProfileReviewDocument struct {
	ProfileReviewID   string                                   `bson:"profileReviewId"`
	ApplicationID     string                                   `bson:"applicationId"`
	ProfileRevisionID string                                   `bson:"profileRevisionId"`
	Attempt           int32                                    `bson:"attempt"`
	SourceRevision    int64                                    `bson:"sourceRevision"`
	Status            string                                   `bson:"status"`
	Snapshot          applicationProfileReviewSnapshotDocument `bson:"snapshot"`
	SubmittedBy       string                                   `bson:"submittedBy"`
	SubmittedAt       time.Time                                `bson:"submittedAt"`
	Decision          any                                      `bson:"decision"`
}

func profileReviewToDocument(review *pd.ApplicationProfileReview) applicationProfileReviewDocument {
	snapshot := review.Snapshot()
	doc := applicationProfileReviewDocument{ProfileReviewID: review.ProfileReviewID().String(), ApplicationID: review.ApplicationID().String(), ProfileRevisionID: review.ProfileRevisionID().String(), Attempt: review.Attempt(), SourceRevision: review.SourceRevision(), Status: string(review.Status()), SubmittedBy: review.SubmittedBy().String(), SubmittedAt: review.SubmittedAt().UTC().Truncate(time.Millisecond), Snapshot: applicationProfileReviewSnapshotDocument{DisplayName: snapshot.DisplayName().String()}}
	if d := snapshot.Description(); d != nil {
		value := d.String()
		doc.Snapshot.Description = &value
	}
	if i := snapshot.Icon(); i != nil {
		value := i.String()
		doc.Snapshot.Icon = &value
	}
	doc.Decision = profileDecisionToDocument(review.Decision())
	return doc
}
