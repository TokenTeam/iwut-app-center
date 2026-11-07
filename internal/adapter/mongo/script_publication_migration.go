package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
)

const scriptPublicationMigrationID = "0040_script_publication_and_availability"

func scriptPublishedVersionFields() bson.D {
	return bson.D{
		scriptUUIDField("versionId"), scriptUUIDField("reviewId"), scriptUUIDField("decisionId"),
		{Key: "versionLabel", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"}}},
		{Key: "format", Value: bson.D{{Key: "enum", Value: bson.A{domain.JSONFormat}}}}, scriptDigestField(),
		{Key: "byteLength", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}, {Key: "maximum", Value: int64(domain.MaxContentBytes)}}},
	}
}

func (m *Migrator) applyScriptPublicationMigration(ctx context.Context) error {
	descriptor := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, scriptNestedObject(scriptPublishedVersionFields())}}}
	head := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, scriptUUIDField("head").Value}}}
	for _, collection := range []struct {
		name   string
		fields bson.D
	}{
		{scriptPublicationsCollection, bson.D{scriptResourceIDProperty(), scriptResourceRevisionProperty(),
			{Key: "head", Value: head}, scriptAuditActorProperty("updatedBy"), scriptAuditDateProperty("updatedAt")}},
		{scriptAvailabilityCollection, bson.D{scriptResourceIDProperty(), scriptResourceRevisionProperty(),
			{Key: "state", Value: bson.D{{Key: "enum", Value: bson.A{domain.DistributionAvailable, domain.DistributionSuspended}}}},
			scriptPositiveLongField("coordinationRevision"), scriptAuditActorProperty("updatedBy"), scriptAuditDateProperty("updatedAt")}},
		{scriptPublishedVersionsCollection, bson.D{scriptResourceIDProperty(),
			{Key: "version", Value: scriptNestedObject(scriptPublishedVersionFields())}, scriptUUIDField("firstEventId"),
			scriptAuditActorProperty("publishedBy"), scriptAuditDateProperty("publishedAt")}},
		{scriptPublicationEventsCollection, bson.D{scriptResourceIDProperty(), scriptUUIDField("eventId"), scriptPositiveLongField("revision"),
			{Key: "namespace", Value: bson.D{{Key: "enum", Value: bson.A{"PUBLICATION", "AVAILABILITY"}}}},
			{Key: "action", Value: bson.D{{Key: "enum", Value: bson.A{domain.PublishScript, domain.ClearScriptHead, domain.SuspendScript, domain.RestoreScript}}}},
			{Key: "version", Value: descriptor}, scriptAuditActorProperty("actor"), scriptAuditDateProperty("at"),
			{Key: "reason", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "maxLength", Value: 2048}}}}},
	} {
		if err := m.ensureValidatedCollection(ctx, collection.name, scriptCollectionValidator(collection.fields)); err != nil {
			return err
		}
	}
	for _, index := range []struct {
		collection, name string
		keys             bson.D
		unique           bool
	}{
		{scriptPublicationsCollection, "uq_script_publication_resource", bson.D{{Key: "resourceId", Value: 1}}, true},
		{scriptAvailabilityCollection, "uq_script_availability_resource", bson.D{{Key: "resourceId", Value: 1}}, true},
		{scriptPublishedVersionsCollection, "uq_script_published_version", bson.D{{Key: "resourceId", Value: 1}, {Key: "version.versionId", Value: 1}}, true},
		{scriptPublicationEventsCollection, "uq_script_publication_event", bson.D{{Key: "eventId", Value: 1}}, true},
		{scriptPublicationEventsCollection, "uq_script_publication_event_revision", bson.D{{Key: "resourceId", Value: 1}, {Key: "namespace", Value: 1}, {Key: "revision", Value: 1}}, true},
		{scriptPublicationEventsCollection, "ix_script_publication_history", bson.D{{Key: "resourceId", Value: 1}, {Key: "namespace", Value: 1}, {Key: "at", Value: -1}, {Key: "eventId", Value: -1}}, false},
		{scriptPublicationEventsCollection, "ix_script_published_version_events", bson.D{{Key: "resourceId", Value: 1}, {Key: "action", Value: 1}, {Key: "version.versionId", Value: 1}}, false},
	} {
		if _, err := m.database.Collection(index.collection).Indexes().CreateOne(ctx, dm.IndexModel{
			Keys: index.keys, Options: options.Index().SetName(index.name).SetUnique(index.unique).SetCollation(&options.Collation{Locale: "simple"}),
		}); err != nil {
			return err
		}
	}
	return nil
}
