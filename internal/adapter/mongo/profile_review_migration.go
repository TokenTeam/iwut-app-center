package mongo

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationProfileReviewMigrationID     = "0012_application_profile_review"
	applicationProfileReviewsCollectionName = "application_profile_reviews"
	profileReviewIDUniqueIndexName          = "uq_application_profile_reviews_id"
	profileReviewAttemptUniqueIndexName     = "uq_application_profile_reviews_revision_attempt"
	profileReviewSourceUniqueIndexName      = "uq_application_profile_reviews_revision_source"
	profileReviewPendingUniqueIndexName     = "uq_application_profile_reviews_pending_revision"
)

func (m *Migrator) applyApplicationProfileReviewMigration(ctx context.Context) error {
	if err := m.ensureValidatedCollection(ctx, applicationProfileReviewsCollectionName, applicationProfileReviewValidator()); err != nil {
		return safeProfilePersistenceError(err)
	}
	_, err := m.database.Collection(applicationProfileReviewsCollectionName).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "profileReviewId", Value: 1}}, Options: options.Index().SetName(profileReviewIDUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "profileRevisionId", Value: 1}, {Key: "attempt", Value: 1}}, Options: options.Index().SetName(profileReviewAttemptUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "profileRevisionId", Value: 1}, {Key: "sourceRevision", Value: 1}}, Options: options.Index().SetName(profileReviewSourceUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "profileRevisionId", Value: 1}}, Options: options.Index().SetName(profileReviewPendingUniqueIndexName).SetUnique(true).SetPartialFilterExpression(bson.M{"status": "PENDING"})},
	})
	if err != nil {
		return fmt.Errorf("create profile review indexes: %w", safeProfilePersistenceError(err))
	}
	return nil
}
func applicationProfileReviewValidator() bson.D {
	snapshot := bson.D{{Key: "bsonType", Value: "object"}, {Key: "required", Value: bson.A{"displayName", "description", "icon"}}, {Key: "additionalProperties", Value: false}, {Key: "properties", Value: bson.D{
		{Key: "displayName", Value: profileTextSchema(80)}, {Key: "description", Value: profileNullableSchema(profileTextSchema(1000))}, {Key: "icon", Value: profileNullableSchema(profileTextSchema(512))},
	}}}
	return publicationObjectSchema(bson.A{"profileReviewId", "applicationId", "profileRevisionId", "attempt", "sourceRevision", "status", "snapshot", "submittedBy", "submittedAt", "decision"}, bson.D{
		{Key: "profileReviewId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "profileRevisionId", Value: uuidV7Schema()},
		{Key: "attempt", Value: positiveIntSchema()}, {Key: "sourceRevision", Value: positiveLongSchema()},
		{Key: "status", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: bson.A{"PENDING"}}}},
		{Key: "snapshot", Value: snapshot}, {Key: "submittedBy", Value: nonEmptyStringSchema()}, {Key: "submittedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}}, {Key: "decision", Value: bson.D{{Key: "bsonType", Value: "null"}}},
	})
}
