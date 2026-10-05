package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const accountOwnerExitMigrationID = "0020_account_owner_exit"

func (r *Migrator) applyAccountOwnerExitMigration(ctx context.Context) error {
	fence := publicationObjectSchema(bson.A{"authId", "revision", "sealedPurpose", "sealedOperationId", "pendingOperationId", "pendingPurpose"}, bson.D{
		{Key: "authId", Value: nonEmptyStringSchema()}, {Key: "revision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: 0}}},
		{Key: "sealedPurpose", Value: bson.D{{Key: "enum", Value: bson.A{0, 1, 2}}}}, {Key: "pendingPurpose", Value: bson.D{{Key: "enum", Value: bson.A{0, 1, 2}}}},
		{Key: "sealedOperationId", Value: bson.D{{Key: "bsonType", Value: "string"}}}, {Key: "pendingOperationId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
	})
	operation := publicationObjectSchema(bson.A{"authId", "operationId", "purpose", "receiptId", "decision", "cleanup", "blocked", "attempt", "nextAttemptAt"}, bson.D{
		{Key: "authId", Value: nonEmptyStringSchema()}, {Key: "operationId", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`}}},
		{Key: "purpose", Value: bson.D{{Key: "enum", Value: bson.A{1, 2}}}}, {Key: "receiptId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
		{Key: "decision", Value: bson.D{{Key: "enum", Value: bson.A{1, 2, 3}}}}, {Key: "cleanup", Value: bson.D{{Key: "enum", Value: bson.A{1, 2, 3}}}},
		{Key: "blocked", Value: bson.D{{Key: "bsonType", Value: "bool"}}}, {Key: "attempt", Value: bson.D{{Key: "bsonType", Value: "int"}, {Key: "minimum", Value: 0}, {Key: "maximum", Value: 7}}}, {Key: "nextAttemptAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
	})
	if err := r.ensureValidatedCollection(ctx, ownerFences, fence); err != nil {
		return err
	}
	if err := r.ensureValidatedCollection(ctx, ownerOperations, operation); err != nil {
		return err
	}
	if _, err := r.database.Collection(ownerFences).Indexes().CreateOne(ctx, m.IndexModel{Keys: bson.D{{Key: "authId", Value: 1}}, Options: options.Index().SetName("uq_owner_exit_fence_auth").SetUnique(true)}); err != nil {
		return err
	}
	_, err := r.database.Collection(ownerOperations).Indexes().CreateMany(ctx, []m.IndexModel{
		{Keys: bson.D{{Key: "operationId", Value: 1}}, Options: options.Index().SetName("uq_owner_exit_operation").SetUnique(true)},
		{Keys: bson.D{{Key: "authId", Value: 1}, {Key: "decision", Value: 1}}, Options: options.Index().SetName("ix_owner_exit_auth_decision")},
		{Keys: bson.D{{Key: "nextAttemptAt", Value: 1}, {Key: "operationId", Value: 1}}, Options: options.Index().SetName("ix_owner_exit_due")},
	})
	return err
}
