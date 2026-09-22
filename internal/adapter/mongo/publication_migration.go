package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationPublicationMigrationID           = "0008_application_publication"
	applicationPublicationsCollectionName       = "application_publications"
	applicationPublicationHistoryCollectionName = "application_publication_history"
	publicationIDUniqueIndexName                = "uq_application_publications_publication_id"
	publicationPartitionUniqueIndexName         = "uq_application_publications_application_id_rpc_api_major"
	publicationHistoryIDUniqueIndexName         = "uq_application_publication_history_history_id"
	publicationHistoryRevisionUniqueIndexName   = "uq_application_publication_history_publication_id_revision"
	publicationHistoryAuditIndexName            = "ix_application_publication_history_application_id_changed_at_history_id"
)

func (migrator *Migrator) applyApplicationPublicationMigration(ctx context.Context) error {
	// These optional, adapter-only counters create actual writes on eligibility
	// sources, so MongoDB snapshot isolation cannot admit stale approvals. They
	// never advance a business revision or change reviewed content.
	for _, source := range []struct {
		name      string
		validator bson.D
	}{
		{applicationVersionsCollectionName, withPublicationFence(applicationVersionDecisionValidator())},
		{applicationReviewsCollectionName, withPublicationFence(applicationReviewRestorationValidator())},
		{applicationPublicationsCollectionName, applicationPublicationValidator()},
		{applicationPublicationHistoryCollectionName, applicationPublicationHistoryValidator()},
	} {
		if err := migrator.ensureValidatedCollection(ctx, source.name, source.validator); err != nil {
			return err
		}
	}
	for _, target := range []struct {
		name    string
		indexes []drivermongo.IndexModel
	}{
		{applicationPublicationsCollectionName, []drivermongo.IndexModel{
			{Keys: bson.D{{Key: "publicationId", Value: 1}}, Options: options.Index().SetName(publicationIDUniqueIndexName).SetUnique(true)},
			{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "rpcApiMajor", Value: 1}}, Options: options.Index().SetName(publicationPartitionUniqueIndexName).SetUnique(true)},
		}},
		{applicationPublicationHistoryCollectionName, []drivermongo.IndexModel{
			{Keys: bson.D{{Key: "historyId", Value: 1}}, Options: options.Index().SetName(publicationHistoryIDUniqueIndexName).SetUnique(true)},
			{Keys: bson.D{{Key: "publicationId", Value: 1}, {Key: "publicationRevision", Value: 1}}, Options: options.Index().SetName(publicationHistoryRevisionUniqueIndexName).SetUnique(true)},
			{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "changedAt", Value: 1}, {Key: "historyId", Value: 1}}, Options: options.Index().SetName(publicationHistoryAuditIndexName)},
		}},
	} {
		if _, err := migrator.database.Collection(target.name).Indexes().CreateMany(ctx, target.indexes); err != nil {
			return fmt.Errorf("create publication indexes: %w", err)
		}
	}
	return nil
}

func withPublicationFence(validator bson.D) bson.D {
	// All existing eligibility validators have the same $and/$jsonSchema envelope.
	clauses := validator[0].Value.(bson.A)
	schema := clauses[0].(bson.D)[0].Value.(bson.D)
	for i := range schema {
		if schema[i].Key == "properties" {
			schema[i].Value = append(schema[i].Value.(bson.D), bson.E{Key: "publicationCoordinationRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(0)}}})
		}
	}
	return validator
}

func applicationPublicationValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		publicationObjectSchema(bson.A{"publicationId", "applicationId", "rpcApiMajor", "testVersionId", "revision", "createdBy", "createdAt", "updatedBy", "updatedAt"}, bson.D{
			{Key: "publicationId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
			{Key: "rpcApiMajor", Value: positiveIntSchema()}, {Key: "testVersionId", Value: uuidV7Schema()},
			{Key: "revision", Value: positiveLongSchema()}, {Key: "createdBy", Value: nonEmptyStringSchema()},
			{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}}, {Key: "updatedBy", Value: nonEmptyStringSchema()},
			{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}),
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{"$revision", int64(1)}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}}, bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}}}}},
		}}}}},
	}}}
}

func applicationPublicationHistoryValidator() bson.D {
	nullableVersion := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, uuidV7Schema()}}}
	return bson.D{{Key: "$and", Value: bson.A{
		publicationObjectSchema(bson.A{"historyId", "publicationId", "applicationId", "rpcApiMajor", "publicationRevision", "action", "previousVersionId", "newVersionId", "approvedReviewId", "scopeCatalogRevision", "preflightPolicyVersion", "changedBy", "changedAt"}, bson.D{
			{Key: "historyId", Value: uuidV7Schema()}, {Key: "publicationId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
			{Key: "rpcApiMajor", Value: positiveIntSchema()}, {Key: "publicationRevision", Value: positiveLongSchema()},
			{Key: "action", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: bson.A{"SET_TEST_VERSION"}}}},
			{Key: "previousVersionId", Value: nullableVersion}, {Key: "newVersionId", Value: uuidV7Schema()}, {Key: "approvedReviewId", Value: uuidV7Schema()},
			{Key: "scopeCatalogRevision", Value: positiveLongSchema()}, {Key: "preflightPolicyVersion", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"}}},
			{Key: "changedBy", Value: nonEmptyStringSchema()}, {Key: "changedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}),
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", "$newVersionId"}}},
			bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$publicationRevision", int64(1)}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", nil}}}}}},
		}}}}},
	}}}
}

func publicationObjectSchema(required bson.A, properties bson.D) bson.D {
	properties = append(bson.D{{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}}}, properties...)
	return bson.D{{Key: "$jsonSchema", Value: bson.D{{Key: "bsonType", Value: "object"}, {Key: "required", Value: required}, {Key: "additionalProperties", Value: false}, {Key: "properties", Value: properties}}}}
}
func positiveIntSchema() bson.D {
	return bson.D{{Key: "bsonType", Value: "int"}, {Key: "minimum", Value: int32(1)}}
}
func positiveLongSchema() bson.D {
	return bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}
}
