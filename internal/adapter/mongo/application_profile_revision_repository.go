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
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"math"
)

type ApplicationProfileRevisionRepository struct{ database *drivermongo.Database }

var _ profileport.ApplicationProfileRevisionRepository = (*ApplicationProfileRevisionRepository)(nil)

func NewApplicationProfileRevisionRepository(database *drivermongo.Database) *ApplicationProfileRevisionRepository {
	return &ApplicationProfileRevisionRepository{database: database}
}
func (r *ApplicationProfileRevisionRepository) CreateDraft(ctx context.Context, expectedAdminID shared.AuthID, draft *profiledomain.DraftApplicationProfileRevision) (*profiledomain.ApplicationProfileRevision, error) {
	if r == nil || r.database == nil || !expectedAdminID.IsValid() || draft == nil || draft.CreatedBy() != expectedAdminID {
		return nil, fmt.Errorf("create profile revision: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.createProfileDraftTransaction(tx, expectedAdminID, draft)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	revision, ok := result.(*profiledomain.ApplicationProfileRevision)
	if !ok || revision == nil {
		return nil, fmt.Errorf("create profile revision: invalid transaction result")
	}
	return revision, nil
}
func (r *ApplicationProfileRevisionRepository) createProfileDraftTransaction(ctx context.Context, admin shared.AuthID, draft *profiledomain.DraftApplicationProfileRevision) (*profiledomain.ApplicationProfileRevision, error) {
	apps := r.database.Collection(applicationsCollectionName)
	filter := bson.D{{Key: "id", Value: draft.ApplicationID().String()}}
	// Every profile writer participates in the same Application write fence as
	// administrator transfers. Transaction retries re-read all protected facts.
	raw, err := apps.FindOneAndUpdate(ctx, filter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, profileport.ErrApplicationNotFound
	}
	if err != nil {
		return nil, err
	}
	if raw.Lookup("adminId").Type != bson.TypeString {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	if raw.Lookup("adminId").StringValue() != admin.String() {
		return nil, profileport.ErrApplicationAdminRequired
	}
	next, ok := raw.Lookup("nextProfileRevisionSequence").Int32OK()
	if !ok || next < 1 || next == math.MaxInt32 {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	exists, projection, err := r.loadProfileWorkingState(ctx, draft.ApplicationID())
	if err != nil {
		return nil, err
	}
	if projection.WorkingProfileRevisionID != nil {
		return nil, profileport.ErrApplicationProfileWorkRevisionAlreadyExists
	}
	revision, err := draft.AssignSequence(profiledomain.ProfileSequence(next))
	if err != nil {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	doc := profileRevisionToDocument(revision)
	revision, err = profileRevisionFromDocument(doc)
	if err != nil {
		return nil, err
	}
	if _, err = r.database.Collection(applicationProfileRevisionsCollectionName).InsertOne(ctx, doc); err != nil {
		return nil, err
	}
	update, err := apps.UpdateOne(ctx, bson.D{{Key: "id", Value: draft.ApplicationID().String()}, {Key: "adminId", Value: admin.String()}, {Key: "nextProfileRevisionSequence", Value: next}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "nextProfileRevisionSequence", Value: int32(1)}}}})
	if err != nil {
		return nil, err
	}
	if update.MatchedCount != 1 {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	profiles := r.database.Collection(applicationProfilesCollectionName)
	id := revision.ProfileRevisionID().String()
	if !exists {
		_, err = profiles.InsertOne(ctx, applicationProfileDocument{ApplicationID: draft.ApplicationID().String(), WorkingProfileRevisionID: &id})
	} else {
		result, updateErr := profiles.UpdateOne(ctx, bson.D{{Key: "applicationId", Value: draft.ApplicationID().String()}, {Key: "workingProfileRevisionId", Value: nil}}, bson.D{{Key: "$set", Value: bson.D{{Key: "workingProfileRevisionId", Value: id}}}})
		err = updateErr
		if err == nil && result.MatchedCount != 1 {
			return nil, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	if err != nil {
		return nil, err
	}
	return revision, nil
}

// Reads both sides of the work-slot invariant, rejecting orphan revisions,
// missing/foreign/dangling pointers and malformed persisted values.
func (r *ApplicationProfileRevisionRepository) loadProfileWorkingState(ctx context.Context, applicationID shared.ApplicationID) (bool, applicationProfileDocument, error) {
	var projection applicationProfileDocument
	filter := bson.D{{Key: "applicationId", Value: applicationID.String()}}
	raw, err := r.database.Collection(applicationProfilesCollectionName).FindOne(ctx, filter).Raw()
	exists := err == nil
	if exists {
		projection, err = profileFromRaw(raw)
		if err != nil {
			return false, projection, err
		}
	} else if !errors.Is(err, drivermongo.ErrNoDocuments) {
		return false, projection, err
	}
	revisions := r.database.Collection(applicationProfileRevisionsCollectionName)
	cursor, err := revisions.Find(ctx, append(filter, bson.E{Key: "reviewStatus", Value: bson.D{{Key: "$in", Value: bson.A{"DRAFT", "SUBMITTED"}}}}), options.Find().SetLimit(2))
	if err != nil {
		return false, projection, err
	}
	defer cursor.Close(ctx)
	var working []*profiledomain.ApplicationProfileRevision
	for cursor.Next(ctx) {
		revision, err := profileRevisionFromRaw(cursor.Current)
		if err != nil {
			return false, projection, err
		}
		working = append(working, revision)
	}
	if err := cursor.Err(); err != nil {
		return false, projection, err
	}
	if len(working) > 1 || len(working) == 1 && (!exists || projection.WorkingProfileRevisionID == nil || *projection.WorkingProfileRevisionID != working[0].ProfileRevisionID().String()) || len(working) == 0 && projection.WorkingProfileRevisionID != nil {
		return false, projection, profileport.ErrApplicationProfileStateInconsistent
	}
	if projection.CurrentPublishedProfileRevisionID != nil {
		raw, err := revisions.FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "profileRevisionId", Value: *projection.CurrentPublishedProfileRevisionID}}).Raw()
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return false, projection, profileport.ErrApplicationProfileStateInconsistent
		}
		if err != nil {
			return false, projection, err
		}
		published, err := profileRevisionFromRaw(raw)
		if err != nil || published.ReviewStatus() != profiledomain.ReviewStatusApproved {
			return false, projection, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	return exists, projection, nil
}

// Driver errors may contain full user-supplied profile text. Preserve stable
// classifications without retaining a printable driver cause.
func safeProfilePersistenceError(err error) error {
	for _, business := range []error{profileport.ErrApplicationNotFound, profileport.ErrApplicationAdminRequired, profileport.ErrApplicationProfileWorkRevisionAlreadyExists, profileport.ErrApplicationProfileStateInconsistent} {
		if errors.Is(err, business) {
			return business
		}
	}
	class := "storage failure"
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case drivermongo.IsDuplicateKeyError(err):
		class = "unique constraint"
	default:
		var serverError drivermongo.ServerError
		if errors.As(err, &serverError) && serverError.HasErrorCode(121) {
			class = "document validation"
		}
	}
	return fmt.Errorf("profile persistence failed: %s", class)
}
