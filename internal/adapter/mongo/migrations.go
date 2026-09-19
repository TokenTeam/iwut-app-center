package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	migrationLedgerCollectionName          = "app_center_schema_migrations"
	applicationCreationMigrationID         = "0001_application_creation"
	applicationIDUniqueIndexName           = "uq_applications_id"
	applicationAdminNameUniqueIndexName    = "uq_applications_admin_id_name_key"
	applicationQuotaAdminIDUniqueIndexName = "uq_application_creation_quotas_admin_id"
)

type migrationRecord struct {
	ID        string    `bson:"_id"`
	AppliedAt time.Time `bson:"appliedAt"`
}

// Migrator owns explicit schema changes. Applications must invoke it as a
// deployment step; repository construction and service startup do not call it.
type Migrator struct {
	database *drivermongo.Database
}

func NewMigrator(database *drivermongo.Database) *Migrator {
	return &Migrator{database: database}
}

func (migrator *Migrator) Migrate(ctx context.Context) error {
	if migrator == nil || migrator.database == nil {
		return fmt.Errorf("run MongoDB migrations: database is nil")
	}

	if err := migrator.ensureMigrationLedger(ctx); err != nil {
		return err
	}

	ledger := migrator.database.Collection(migrationLedgerCollectionName)
	err := ledger.FindOne(ctx, bson.D{{Key: "_id", Value: applicationCreationMigrationID}}).Err()
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, drivermongo.ErrNoDocuments):
		return fmt.Errorf("read migration ledger: %w", err)
	}

	if err := migrator.applyApplicationCreationMigration(ctx); err != nil {
		return fmt.Errorf("apply migration %s: %w", applicationCreationMigrationID, err)
	}

	_, err = ledger.InsertOne(ctx, migrationRecord{
		ID:        applicationCreationMigrationID,
		AppliedAt: time.Now().UTC(),
	})
	if err != nil {
		if drivermongo.IsDuplicateKeyError(err) {
			return nil
		}
		return fmt.Errorf("record migration %s: %w", applicationCreationMigrationID, err)
	}
	return nil
}

func (migrator *Migrator) ensureMigrationLedger(ctx context.Context) error {
	err := migrator.database.CreateCollection(ctx, migrationLedgerCollectionName)
	if err == nil || isNamespaceExists(err) {
		return nil
	}
	return fmt.Errorf("create migration ledger: %w", err)
}

func (migrator *Migrator) applyApplicationCreationMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationCreationQuotasCollectionName, applicationCreationQuotaValidator()); err != nil {
		return err
	}

	applications := migrator.database.Collection(applicationsCollectionName)
	_, err := applications.Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{
			Keys:    bson.D{{Key: "id", Value: 1}},
			Options: options.Index().SetName(applicationIDUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "adminId", Value: 1},
				{Key: "nameKey", Value: 1},
			},
			Options: options.Index().SetName(applicationAdminNameUniqueIndexName).SetUnique(true),
		},
	})
	if err != nil {
		return fmt.Errorf("create application indexes: %w", err)
	}

	quotas := migrator.database.Collection(applicationCreationQuotasCollectionName)
	_, err = quotas.Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "adminId", Value: 1}},
		Options: options.Index().SetName(applicationQuotaAdminIDUniqueIndexName).SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create application quota index: %w", err)
	}
	return nil
}

func (migrator *Migrator) ensureValidatedCollection(ctx context.Context, name string, validator bson.D) error {
	err := migrator.database.CreateCollection(
		ctx,
		name,
		options.CreateCollection().
			SetValidator(validator).
			SetValidationLevel("strict").
			SetValidationAction("error"),
	)
	if err == nil {
		return nil
	}
	if !isNamespaceExists(err) {
		return fmt.Errorf("create collection %s: %w", name, err)
	}

	result := migrator.database.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: name},
		{Key: "validator", Value: validator},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	})
	if err := result.Err(); err != nil {
		return fmt.Errorf("update validator for collection %s: %w", name, err)
	}
	return nil
}

func isNamespaceExists(err error) bool {
	var commandError drivermongo.CommandError
	return errors.As(err, &commandError) && commandError.Code == 48
}

func applicationValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{
				"id", "name", "nameKey", "adminId", "createdAt",
				"nextVersionSequence", "nextProfileRevisionSequence",
			}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "id", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "pattern", Value: "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$"},
				}},
				{Key: "name", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "pattern", Value: "^[A-Za-z0-9_-]{1,50}$"},
				}},
				{Key: "nameKey", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "pattern", Value: "^[a-z0-9_-]{1,50}$"},
				}},
				{Key: "adminId", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
				}},
				{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
				{Key: "nextVersionSequence", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(1)},
				}},
				{Key: "nextProfileRevisionSequence", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(1)},
				}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$nameKey", bson.D{{Key: "$toLower", Value: "$name"}}}}}}},
	}}}
}

func applicationCreationQuotaValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{"adminId", "limit", "usedCount", "revision", "updatedAt"}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "adminId", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
				}},
				{Key: "limit", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(0)},
				}},
				{Key: "usedCount", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(0)},
				}},
				{Key: "revision", Value: bson.D{
					{Key: "bsonType", Value: "long"},
					{Key: "minimum", Value: int64(0)},
				}},
				{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$lte", Value: bson.A{"$usedCount", "$limit"}}}}},
	}}}
}
