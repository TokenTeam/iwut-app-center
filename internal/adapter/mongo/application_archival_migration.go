package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationArchivalMigrationID         = "0026_application_archival"
	applicationArchiveEventsCollectionName = "application_archive_events"
)

func (migrator *Migrator) applyApplicationArchivalMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationArchivalValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationsCollectionName).UpdateMany(ctx,
		bson.M{"archiveStatus": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"archiveStatus": "UNARCHIVED", "archiveRevision": int64(1), "lastArchiveEventId": nil}})
	if err != nil {
		return fmt.Errorf("backfill application archival: %w", err)
	}
	if err = migrator.ensureValidatedCollection(ctx, applicationArchiveEventsCollectionName, applicationArchiveEventValidator()); err != nil {
		return err
	}
	_, err = migrator.database.Collection(applicationArchiveEventsCollectionName).Indexes().CreateMany(ctx, []m.IndexModel{
		{Keys: bson.D{{Key: "eventId", Value: 1}}, Options: options.Index().SetName("uq_application_archive_event_id").SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "occurredAt", Value: 1}, {Key: "eventId", Value: 1}}, Options: options.Index().SetName("ix_application_archive_audit")},
	})
	if err != nil {
		return fmt.Errorf("create application archive indexes: %w", err)
	}
	return nil
}

func applicationArchiveEventValidator() bson.D {
	return publicationObjectSchema(bson.A{"eventId", "applicationId", "action", "actorAuthId", "beforeStatus", "afterStatus", "beforeRevision", "afterRevision", "occurredAt"}, bson.D{
		{Key: "eventId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "actorAuthId", Value: nonEmptyStringSchema()},
		{Key: "action", Value: bson.D{{Key: "enum", Value: bson.A{"ARCHIVE", "UNARCHIVE"}}}},
		{Key: "beforeStatus", Value: bson.D{{Key: "enum", Value: bson.A{"UNARCHIVED", "ARCHIVED"}}}}, {Key: "afterStatus", Value: bson.D{{Key: "enum", Value: bson.A{"UNARCHIVED", "ARCHIVED"}}}},
		{Key: "beforeRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}}, {Key: "afterRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(2)}}},
		{Key: "occurredAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
	})
}
