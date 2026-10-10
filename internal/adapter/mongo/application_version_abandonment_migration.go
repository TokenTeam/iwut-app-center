package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationVersionAbandonmentMigrationID       = "0028_application_version_abandonment"
	applicationVersionAbandonmentsCollectionName   = "application_version_abandonments"
	applicationVersionAbandonmentIDIndexName       = "uq_application_version_abandonments_abandonment_id"
	applicationVersionAbandonmentVersionIndexName  = "uq_application_version_abandonments_version_id"
	applicationVersionAbandonmentSequenceIndexName = "uq_application_version_abandonments_application_sequence"
	applicationVersionAbandonmentAuditIndexName    = "ix_application_version_abandonments_application_abandoned_at"
)

func (migrator *Migrator) applyApplicationVersionAbandonmentMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionAbandonmentsCollectionName, applicationVersionAbandonmentValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationVersionAbandonmentsCollectionName).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "abandonmentId", Value: 1}}, Options: options.Index().SetName(applicationVersionAbandonmentIDIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "versionId", Value: 1}}, Options: options.Index().SetName(applicationVersionAbandonmentVersionIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetName(applicationVersionAbandonmentSequenceIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "abandonedAt", Value: 1}}, Options: options.Index().SetName(applicationVersionAbandonmentAuditIndexName)},
	})
	if err != nil {
		return fmt.Errorf("create application version abandonment indexes: %w", err)
	}
	return nil
}

func applicationVersionAbandonmentValidator() bson.D {
	return publicationObjectSchema(
		bson.A{"abandonmentId", "applicationId", "versionId", "sequence", "versionLabel", "sourceRevision", "abandonedBy", "abandonedAt"},
		bson.D{
			{Key: "abandonmentId", Value: uuidV7Schema()},
			{Key: "applicationId", Value: uuidV7Schema()},
			{Key: "versionId", Value: uuidV7Schema()},
			{Key: "sequence", Value: positiveIntSchema()},
			{Key: "versionLabel", Value: bson.D{
				{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 50},
				{Key: "pattern", Value: "^[^\\x00-\\x1F\\x7F\\s](?:[^\\x00-\\x1F\\x7F]*[^\\x00-\\x1F\\x7F\\s])?$"},
			}},
			{Key: "sourceRevision", Value: positiveLongSchema()},
			{Key: "abandonedBy", Value: nonEmptyStringSchema()},
			{Key: "abandonedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		},
	)
}
