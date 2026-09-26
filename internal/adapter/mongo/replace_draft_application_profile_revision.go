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
	"time"
)

var _ profileport.DraftApplicationProfileRevisionRepository = (*ApplicationProfileRevisionRepository)(nil)

func (r *ApplicationProfileRevisionRepository) ReplaceDraft(ctx context.Context, app shared.ApplicationID, id profiledomain.ApplicationProfileRevisionID, admin shared.AuthID, expected int64, replacement profiledomain.DraftApplicationProfileReplacement, at time.Time) (*profiledomain.ApplicationProfileRevision, error) {
	if r == nil || r.database == nil || !app.IsValid() || !id.IsValid() || !admin.IsValid() || expected < 1 || at.IsZero() {
		return nil, fmt.Errorf("replace profile draft: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.replaceProfileDraftTransaction(tx, app, id, admin, expected, replacement, at.UTC().Truncate(time.Millisecond))
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	revision, ok := result.(*profiledomain.ApplicationProfileRevision)
	if !ok || revision == nil {
		return nil, fmt.Errorf("replace profile draft: invalid transaction result")
	}
	return revision, nil
}
func (r *ApplicationProfileRevisionRepository) replaceProfileDraftTransaction(ctx context.Context, app shared.ApplicationID, id profiledomain.ApplicationProfileRevisionID, admin shared.AuthID, expected int64, replacement profiledomain.DraftApplicationProfileReplacement, at time.Time) (*profiledomain.ApplicationProfileRevision, error) {
	raw, err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, bson.D{{Key: "id", Value: app.String()}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, profileport.ErrApplicationProfileRevisionNotFound
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
	revisions := r.database.Collection(applicationProfileRevisionsCollectionName)
	filter := bson.D{{Key: "applicationId", Value: app.String()}, {Key: "profileRevisionId", Value: id.String()}}
	raw, err = revisions.FindOne(ctx, filter).Raw()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, profileport.ErrApplicationProfileRevisionNotFound
	}
	if err != nil {
		return nil, err
	}
	current, err := profileRevisionFromRaw(raw)
	if err != nil {
		return nil, err
	}
	updated, err := current.ReplaceDraft(expected, replacement, admin, at)
	if err != nil {
		switch {
		case errors.Is(err, profiledomain.ErrApplicationProfileRevisionNotDraft):
			return nil, profileport.ErrApplicationProfileRevisionNotDraft
		case errors.Is(err, profiledomain.ErrApplicationProfileRevisionConflict):
			return nil, profileport.ErrApplicationProfileRevisionConflict
		default:
			return nil, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	_, projection, err := r.loadProfileWorkingState(ctx, app)
	if err != nil {
		return nil, err
	}
	if projection.WorkingProfileRevisionID == nil || *projection.WorkingProfileRevisionID != id.String() {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	if updated == current {
		return current, nil
	}
	doc := profileRevisionToDocument(updated)
	filter = append(filter, bson.E{Key: "reviewStatus", Value: "DRAFT"}, bson.E{Key: "revision", Value: expected})
	result, err := revisions.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{
		{Key: "displayName", Value: doc.DisplayName}, {Key: "description", Value: doc.Description}, {Key: "icon", Value: doc.Icon},
		{Key: "revision", Value: doc.Revision}, {Key: "updatedBy", Value: doc.UpdatedBy}, {Key: "updatedAt", Value: doc.UpdatedAt},
	}}})
	if err != nil {
		return nil, err
	}
	if result.MatchedCount != 1 {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	return updated, nil
}
