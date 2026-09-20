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
	migrationLedgerCollectionName             = "app_center_schema_migrations"
	applicationCreationMigrationID            = "0001_application_creation"
	applicationIDUniqueIndexName              = "uq_applications_id"
	applicationAdminNameUniqueIndexName       = "uq_applications_admin_id_name_key"
	applicationQuotaAdminIDUniqueIndexName    = "uq_application_creation_quotas_admin_id"
	applicationVersionMigrationID             = "0002_application_version"
	applicationVersionIDUniqueIndexName       = "uq_application_versions_version_id"
	applicationVersionSequenceUniqueIndexName = "uq_application_versions_application_id_sequence"
	applicationVersionLabelUniqueIndexName    = "uq_application_versions_application_id_version_label"
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

	migrations := []struct {
		id    string
		apply func(context.Context) error
	}{
		{id: applicationCreationMigrationID, apply: migrator.applyApplicationCreationMigration},
		{id: applicationVersionMigrationID, apply: migrator.applyApplicationVersionMigration},
	}
	for _, migration := range migrations {
		if err := migrator.applyMigration(ctx, migration.id, migration.apply); err != nil {
			return err
		}
	}
	return nil
}

func (migrator *Migrator) applyMigration(ctx context.Context, id string, apply func(context.Context) error) error {
	ledger := migrator.database.Collection(migrationLedgerCollectionName)
	err := ledger.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Err()
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, drivermongo.ErrNoDocuments):
		return fmt.Errorf("read migration ledger for %s: %w", id, err)
	}

	if err := apply(ctx); err != nil {
		return fmt.Errorf("apply migration %s: %w", id, err)
	}
	_, err = ledger.InsertOne(ctx, migrationRecord{ID: id, AppliedAt: time.Now().UTC()})
	if err == nil || drivermongo.IsDuplicateKeyError(err) {
		return nil
	}
	return fmt.Errorf("record migration %s: %w", id, err)
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

func (migrator *Migrator) applyApplicationVersionMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionsCollectionName, applicationVersionValidator()); err != nil {
		return err
	}

	versions := migrator.database.Collection(applicationVersionsCollectionName)
	_, err := versions.Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{
			Keys:    bson.D{{Key: "versionId", Value: 1}},
			Options: options.Index().SetName(applicationVersionIDUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "applicationId", Value: 1},
				{Key: "sequence", Value: 1},
			},
			Options: options.Index().SetName(applicationVersionSequenceUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "applicationId", Value: 1},
				{Key: "versionLabel", Value: 1},
			},
			Options: options.Index().
				SetName(applicationVersionLabelUniqueIndexName).
				SetUnique(true).
				SetCollation(&options.Collation{Locale: "simple"}),
		},
	})
	if err != nil {
		return fmt.Errorf("create application version indexes: %w", err)
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

// applicationVersionValidator is a storage-level defense in depth. It enforces
// facts MongoDB can express reliably (shape, byte limits, set relationships,
// RPC ordering, and initial lifecycle/audit equality). Full URL address-class
// and Unicode domain validation remains in version/domain and in the document
// mapper; this schema is not a replacement for those constructors.
func applicationVersionValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{
				"versionId", "applicationId", "sequence", "versionLabel", "launchUrl",
				"rpcApiMinVersion", "rpcApiMaxVersionExclusive", "requiredCapabilities",
				"requiredScopes", "optionalScopes", "reviewStatus", "createdBy", "createdAt",
				"revision", "updatedBy", "updatedAt",
			}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "versionId", Value: uuidV7Schema()},
				{Key: "applicationId", Value: uuidV7Schema()},
				{Key: "sequence", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(1)},
				}},
				{Key: "versionLabel", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
					{Key: "maxLength", Value: 50},
					{Key: "pattern", Value: "^[^\\x00-\\x1F\\x7F\\s](?:[^\\x00-\\x1F\\x7F]*[^\\x00-\\x1F\\x7F\\s])?$"},
				}},
				{Key: "launchUrl", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
					{Key: "pattern", Value: "^https?://"},
				}},
				{Key: "rpcApiMinVersion", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(1)},
				}},
				{Key: "rpcApiMaxVersionExclusive", Value: bson.D{{Key: "bsonType", Value: "int"}}},
				{Key: "requiredCapabilities", Value: stringSetSchema("^[a-z][a-z0-9]*(?:\\.[a-z][a-z0-9]*)*\\.v[1-9][0-9]*$")},
				{Key: "requiredScopes", Value: stringSetSchema("")},
				{Key: "optionalScopes", Value: stringSetSchema("")},
				{Key: "reviewStatus", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "enum", Value: bson.A{"DRAFT"}},
				}},
				{Key: "createdBy", Value: nonEmptyStringSchema()},
				{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
				{Key: "revision", Value: bson.D{
					{Key: "bsonType", Value: "long"},
					{Key: "enum", Value: bson.A{int64(1)}},
				}},
				{Key: "updatedBy", Value: nonEmptyStringSchema()},
				{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{"$rpcApiMaxVersionExclusive", "$rpcApiMinVersion"}}},
			bson.D{{Key: "$lte", Value: bson.A{bson.D{{Key: "$strLenBytes", Value: "$launchUrl"}}, 2048}}},
			bson.D{{Key: "$eq", Value: bson.A{
				bson.D{{Key: "$size", Value: bson.D{{Key: "$setIntersection", Value: bson.A{"$requiredScopes", "$optionalScopes"}}}}},
				0,
			}}},
			bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}},
			bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}},
		}}}}},
	}}}
}

func uuidV7Schema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "string"},
		{Key: "pattern", Value: "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$"},
	}
}

func nonEmptyStringSchema() bson.D {
	return bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}
}

func stringSetSchema(pattern string) bson.D {
	itemSchema := bson.D{{Key: "bsonType", Value: "string"}}
	if pattern != "" {
		itemSchema = append(itemSchema, bson.E{Key: "pattern", Value: pattern})
	}
	return bson.D{
		{Key: "bsonType", Value: "array"},
		{Key: "uniqueItems", Value: true},
		{Key: "items", Value: itemSchema},
	}
}
