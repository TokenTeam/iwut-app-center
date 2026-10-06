package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const applicationAdminTransferMigrationID = "0021_application_admin_transfer"

func (migrator *Migrator) applyApplicationAdminTransferMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationOwnershipValidator()); err != nil {
		return err
	}
	if _, err := migrator.database.Collection(applicationsCollectionName).UpdateMany(ctx,
		bson.D{{Key: "ownershipRevision", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "ownershipRevision", Value: int64(1)}}}},
	); err != nil {
		return fmt.Errorf("backfill application ownership revision: %w", err)
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationTesterJoinLinksCollectionName, applicationTesterJoinLinkValidatorWithAdminTransfer(true)); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, ownerOperations, accountOwnerExitOperationValidator(true)); err != nil {
		return err
	}
	if _, err := migrator.database.Collection(ownerOperations).UpdateMany(ctx,
		bson.D{{Key: "blocker", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.A{bson.D{{Key: "$set", Value: bson.D{{Key: "blocker", Value: bson.D{{Key: "$cond", Value: bson.A{"$blocked", 1, 0}}}}}}}},
	); err != nil {
		return fmt.Errorf("backfill owner-exit blocker: %w", err)
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationAdminTransfersCollectionName, applicationAdminTransferValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationAdminTransfersCollectionName).Indexes().CreateMany(ctx, []m.IndexModel{
		{Keys: bson.D{{Key: "transferId", Value: 1}}, Options: options.Index().SetName("uq_application_admin_transfer_id").SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}}, Options: options.Index().SetName("uq_application_admin_transfer_pending").SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "status", Value: "PENDING"}})},
		{Keys: bson.D{{Key: "toAdminId", Value: 1}, {Key: "status", Value: 1}, {Key: "expiresAt", Value: 1}}, Options: options.Index().SetName("ix_application_admin_transfer_target_status_expiry")},
		{Keys: bson.D{{Key: "fromAdminId", Value: 1}, {Key: "requestedAt", Value: 1}, {Key: "transferId", Value: 1}}, Options: options.Index().SetName("ix_application_admin_transfer_source_audit")},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "requestedAt", Value: 1}, {Key: "transferId", Value: 1}}, Options: options.Index().SetName("ix_application_admin_transfer_application_audit")},
	})
	return err
}

func applicationAdminTransferValidator() bson.D {
	nullableString := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, nonEmptyStringSchema()}}}
	nullableDate := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "bsonType", Value: "date"}}}}}
	pending := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "PENDING"}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolvedAt", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolvedBy", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolutionCause", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$confidentialCredentialHandling", nil}}},
	}}}
	accepted := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "ACCEPTED"}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedAt", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedBy", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolutionCause", nil}}}, bson.D{{Key: "$in", Value: bson.A{"$confidentialCredentialHandling", bson.A{"KEEP", "ROTATE"}}}},
	}}}
	rejected := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "REJECTED"}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedAt", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedBy", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolutionCause", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$confidentialCredentialHandling", nil}}},
	}}}
	cancelled := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "CANCELLED"}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedAt", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedBy", nil}}}, bson.D{{Key: "$in", Value: bson.A{"$resolutionCause", bson.A{"EXPLICIT", "APPLICATION_CLOSURE"}}}}, bson.D{{Key: "$eq", Value: bson.A{"$confidentialCredentialHandling", nil}}},
	}}}
	expired := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "EXPIRED"}}}, bson.D{{Key: "$ne", Value: bson.A{"$resolvedAt", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolvedBy", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$resolutionCause", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$confidentialCredentialHandling", nil}}},
	}}}
	schema := publicationObjectSchema(bson.A{"transferId", "applicationId", "fromAdminId", "toAdminId", "sourceOwnershipRevision", "status", "requestedAt", "expiresAt", "resolvedAt", "resolvedBy", "resolutionCause", "confidentialCredentialHandling"}, bson.D{
		{Key: "transferId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
		{Key: "fromAdminId", Value: nonEmptyStringSchema()}, {Key: "toAdminId", Value: nonEmptyStringSchema()},
		{Key: "sourceOwnershipRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
		{Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{"PENDING", "ACCEPTED", "REJECTED", "CANCELLED", "EXPIRED"}}}},
		{Key: "requestedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}}, {Key: "expiresAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		{Key: "resolvedAt", Value: nullableDate}, {Key: "resolvedBy", Value: nullableString},
		{Key: "resolutionCause", Value: bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "enum", Value: bson.A{"EXPLICIT", "APPLICATION_CLOSURE"}}}}}}},
		{Key: "confidentialCredentialHandling", Value: bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "enum", Value: bson.A{"KEEP", "ROTATE"}}}}}}},
	})
	expression := bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{"$fromAdminId", "$toAdminId"}}},
		bson.D{{Key: "$lt", Value: bson.A{"$requestedAt", "$expiresAt"}}},
		bson.D{{Key: "$or", Value: bson.A{pending, accepted, rejected, cancelled, expired}}},
	}}}}}
	return bson.D{{Key: "$and", Value: bson.A{schema, expression}}}
}
