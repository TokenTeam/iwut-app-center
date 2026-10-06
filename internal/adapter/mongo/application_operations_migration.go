package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationOperationsMigrationID              = "0023_application_operations"
	applicationOperationEventsCollectionName      = "application_operation_events"
	applicationOperationAlertOutboxCollectionName = "application_operation_alert_outbox"
)

func (migrator *Migrator) applyApplicationOperationsMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationAvailabilityValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationsCollectionName).UpdateMany(ctx,
		bson.D{{Key: "platformAvailabilityStatus", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "platformAvailabilityStatus", Value: "AVAILABLE"}, {Key: "platformAvailabilityRevision", Value: int64(1)}, {Key: "lastPlatformOperationEventId", Value: nil}, {Key: "suspendedAt", Value: nil}, {Key: "restoredAt", Value: nil}}}},
	)
	if err != nil {
		return fmt.Errorf("backfill application availability: %w", err)
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationOperationEventsCollectionName, applicationOperationEventValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationOperationAlertOutboxCollectionName, applicationOperationAlertValidator()); err != nil {
		return err
	}
	_, err = migrator.database.Collection(applicationOperationEventsCollectionName).Indexes().CreateMany(ctx, []m.IndexModel{
		{Keys: bson.D{{Key: "eventId", Value: 1}}, Options: options.Index().SetName("uq_application_operation_event_id").SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "occurredAt", Value: 1}, {Key: "eventId", Value: 1}}, Options: options.Index().SetName("ix_application_operation_audit")},
	})
	if err != nil {
		return fmt.Errorf("create application operation event indexes: %w", err)
	}
	_, err = migrator.database.Collection(applicationOperationAlertOutboxCollectionName).Indexes().CreateMany(ctx, []m.IndexModel{
		{Keys: bson.D{{Key: "eventId", Value: 1}}, Options: options.Index().SetName("uq_application_operation_alert_event").SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "createdAt", Value: 1}}, Options: options.Index().SetName("ix_application_operation_alert_delivery")},
	})
	return err
}

func applicationOperationEventValidator() bson.D {
	return publicationObjectSchema(bson.A{"eventId", "applicationId", "actorAuthId", "action", "reason", "sourceLifecycleRevision", "beforeStatus", "afterStatus", "beforeRevision", "afterRevision", "occurredAt"}, bson.D{
		{Key: "eventId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "actorAuthId", Value: nonEmptyStringSchema()},
		{Key: "action", Value: bson.D{{Key: "enum", Value: bson.A{"SUSPEND", "RESTORE"}}}}, {Key: "reason", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 1024}}},
		{Key: "sourceLifecycleRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
		{Key: "beforeStatus", Value: bson.D{{Key: "enum", Value: bson.A{"AVAILABLE", "SUSPENDED"}}}}, {Key: "afterStatus", Value: bson.D{{Key: "enum", Value: bson.A{"AVAILABLE", "SUSPENDED"}}}},
		{Key: "beforeRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}}, {Key: "afterRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(2)}}}, {Key: "occurredAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
	})
}

func applicationOperationAlertValidator() bson.D {
	return publicationObjectSchema(bson.A{"eventId", "applicationId", "action", "severity", "status", "createdAt"}, bson.D{
		{Key: "eventId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "action", Value: bson.D{{Key: "enum", Value: bson.A{"SUSPEND", "RESTORE"}}}},
		{Key: "severity", Value: bson.D{{Key: "enum", Value: bson.A{"WARNING", "INFO"}}}}, {Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{"PENDING", "DELIVERED"}}}}, {Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
	})
}
