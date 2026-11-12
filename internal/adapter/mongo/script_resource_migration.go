package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	scriptResourceMigrationID    = "0034_script_resource_metadata"
	scriptResourceIDIndexName    = "uq_script_resources_resource_id"
	scriptResourceAuditIndexName = "uq_script_resource_audits_resource_id_revision"
)

func (m *Migrator) applyScriptResourceMigration(ctx context.Context) error {
	for _, schema := range []struct {
		collection string
		properties bson.D
		indexName  string
		keys       bson.D
	}{
		{scriptResourcesCollectionName, scriptResourceProperties(), scriptResourceIDIndexName, bson.D{{Key: "resourceId", Value: 1}}},
		{scriptResourceAuditsCollectionName, bson.D{
			scriptResourceIDProperty(), scriptResourceRevisionProperty(),
			{Key: "action", Value: bson.D{{Key: "enum", Value: bson.A{scriptResourceCreated, scriptResourceMetadataReplaced}}}},
			scriptAuditActorProperty("actor"), scriptAuditDateProperty("changedAt"),
		}, scriptResourceAuditIndexName, bson.D{{Key: "resourceId", Value: 1}, {Key: "revision", Value: 1}}},
	} {
		if err := m.ensureValidatedCollection(ctx, schema.collection, scriptCollectionValidator(schema.properties)); err != nil {
			return err
		}
		_, err := m.database.Collection(schema.collection).Indexes().CreateOne(ctx, drivermongo.IndexModel{
			Keys: schema.keys, Options: options.Index().SetName(schema.indexName).SetUnique(true),
		})
		if err != nil {
			return fmt.Errorf("create %s index: %w", schema.collection, err)
		}
	}
	return nil
}

func scriptCollectionValidator(fields bson.D) bson.D {
	properties := append(bson.D{{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}}}, fields...)
	required := make(bson.A, 0, len(properties))
	for _, property := range properties {
		required = append(required, property.Key)
	}
	return bson.D{{Key: "$jsonSchema", Value: bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "additionalProperties", Value: false},
		{Key: "required", Value: required},
		{Key: "properties", Value: properties},
	}}}
}

func scriptResourceIDProperty() bson.E {
	return bson.E{Key: "resourceId", Value: bson.D{
		{Key: "bsonType", Value: "string"},
		{Key: "pattern", Value: "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"},
	}}
}

func scriptResourceRevisionProperty() bson.E {
	return bson.E{Key: "revision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}}
}

func scriptAuditActorProperty(key string) bson.E {
	return bson.E{Key: key, Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}}
}

func scriptAuditDateProperty(key string) bson.E {
	return bson.E{Key: key, Value: bson.D{{Key: "bsonType", Value: "date"}}}
}

func scriptResourceProperties() bson.D {
	return bson.D{
		scriptResourceIDProperty(), scriptResourceRevisionProperty(),
		{Key: "name", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 128}}},
		{Key: "purpose", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 2048}}},
		scriptAuditActorProperty("createdBy"), scriptAuditActorProperty("updatedBy"),
		scriptAuditDateProperty("createdAt"), scriptAuditDateProperty("updatedAt"),
	}
}
