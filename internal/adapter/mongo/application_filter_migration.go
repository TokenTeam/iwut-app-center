package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationFilterMigrationID                = "0018_application_filter"
	applicationFilterApplicationUniqueIndexName = "uq_application_filters_application_id"
	applicationFilterRevisionIDUniqueIndexName  = "uq_application_filter_revisions_revision_id"
	applicationFilterRevisionSequenceIndexName  = "uq_application_filter_revisions_application_id_sequence"
)

func (migrator *Migrator) applyApplicationFilterMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationFiltersCollectionName, applicationFilterValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationFilterRevisionsCollectionName, applicationFilterRevisionValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationFiltersCollectionName).Indexes().CreateOne(ctx, drivermongo.IndexModel{Keys: bson.D{{Key: "applicationId", Value: 1}}, Options: options.Index().SetName(applicationFilterApplicationUniqueIndexName).SetUnique(true)})
	if err != nil {
		return fmt.Errorf("create application filter index: %w", err)
	}
	_, err = migrator.database.Collection(applicationFilterRevisionsCollectionName).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "filterRevisionId", Value: 1}}, Options: options.Index().SetName(applicationFilterRevisionIDUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetName(applicationFilterRevisionSequenceIndexName).SetUnique(true)},
	})
	if err != nil {
		return fmt.Errorf("create application filter revision indexes: %w", err)
	}
	return nil
}

func applicationFilterValidator() bson.D {
	return bson.D{{Key: "$jsonSchema", Value: bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "additionalProperties", Value: false},
		{Key: "required", Value: bson.A{"_id", "applicationId", "currentFilterRevisionId", "revision", "nextSequence", "updatedBy", "updatedAt"}},
		{Key: "properties", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
			{Key: "applicationId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
			{Key: "currentFilterRevisionId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
			{Key: "revision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
			{Key: "nextSequence", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(2)}}},
			{Key: "updatedBy", Value: bson.D{{Key: "bsonType", Value: "string"}}},
			{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}},
	}}}
}
func applicationFilterRevisionValidator() bson.D {
	base := bson.D{{Key: "$jsonSchema", Value: bson.D{
		{Key: "bsonType", Value: "object"}, {Key: "additionalProperties", Value: false},
		{Key: "required", Value: bson.A{"_id", "filterRevisionId", "applicationId", "sequence", "schemaVersion", "mode", "publishedBy", "publishedAt"}},
		{Key: "properties", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}}, {Key: "filterRevisionId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
			{Key: "applicationId", Value: bson.D{{Key: "bsonType", Value: "string"}}}, {Key: "sequence", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
			{Key: "schemaVersion", Value: bson.D{{Key: "enum", Value: bson.A{"profile-filter-v1"}}}}, {Key: "mode", Value: bson.D{{Key: "enum", Value: bson.A{"RULE", "ALLOW_ALL"}}}},
			{Key: "rule", Value: bson.D{{Key: "bsonType", Value: "object"}}}, {Key: "publishedBy", Value: bson.D{{Key: "bsonType", Value: "string"}}}, {Key: "publishedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}},
	}}}
	return bson.D{{Key: "$and", Value: bson.A{base, bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "mode", Value: "ALLOW_ALL"}, {Key: "rule", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "mode", Value: "RULE"}, {Key: "rule", Value: bson.D{{Key: "$type", Value: "object"}}}},
	}}}}}}
}
