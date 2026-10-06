package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationClosureMigrationID     = "0022_application_closure"
	applicationClosuresCollectionName = "application_closures"
)

func (migrator *Migrator) applyApplicationClosureMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationLifecycleValidator()); err != nil {
		return err
	}
	if _, err := migrator.database.Collection(applicationsCollectionName).UpdateMany(ctx,
		bson.D{{Key: "lifecycleStatus", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "lifecycleStatus", Value: "ACTIVE"}, {Key: "lifecycleRevision", Value: int64(1)}}}},
	); err != nil {
		return fmt.Errorf("backfill application lifecycle: %w", err)
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationAdminTransfersCollectionName, applicationAdminTransferValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationTesterJoinLinksCollectionName, applicationTesterJoinLinkValidatorWithClosure()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationClosuresCollectionName, applicationClosureValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationClosuresCollectionName).Indexes().CreateMany(ctx, []m.IndexModel{
		{Keys: bson.D{{Key: "closureId", Value: 1}}, Options: options.Index().SetName("uq_application_closure_id").SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}}, Options: options.Index().SetName("uq_application_closure_application").SetUnique(true)},
		{Keys: bson.D{{Key: "highRiskProofJti", Value: 1}}, Options: options.Index().SetName("uq_application_closure_proof_jti").SetUnique(true)},
		{Keys: bson.D{{Key: "authRevocationState", Value: 1}, {Key: "nextAttemptAt", Value: 1}, {Key: "closureId", Value: 1}}, Options: options.Index().SetName("ix_application_closure_worker")},
	})
	return err
}

func applicationClosureValidator() bson.D {
	nullDate := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "bsonType", Value: "date"}}}}}
	nullString := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, nonEmptyStringSchema()}}}
	schema := publicationObjectSchema(bson.A{"closureId", "applicationId", "initiatedBy", "sourceOwnershipRevision", "sourceLifecycleRevision", "status", "highRiskProofJti", "closingStartedAt", "authRevocationState", "authReceiptId", "authAppliedAt", "closedAt", "nextAttemptAt", "attempt"}, bson.D{
		{Key: "closureId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "initiatedBy", Value: nonEmptyStringSchema()},
		{Key: "sourceOwnershipRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
		{Key: "sourceLifecycleRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
		{Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{"CLOSING", "CLOSED"}}}}, {Key: "highRiskProofJti", Value: uuidV7Schema()},
		{Key: "closingStartedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}}, {Key: "authRevocationState", Value: bson.D{{Key: "enum", Value: bson.A{"PENDING", "APPLIED"}}}},
		{Key: "authReceiptId", Value: nullString}, {Key: "authAppliedAt", Value: nullDate}, {Key: "closedAt", Value: nullDate}, {Key: "nextAttemptAt", Value: nullDate},
		{Key: "attempt", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(0)}}},
	})
	closing := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "CLOSING"}}}, bson.D{{Key: "$eq", Value: bson.A{"$authRevocationState", "PENDING"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$authReceiptId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$authAppliedAt", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$closedAt", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$nextAttemptAt", nil}}},
	}}}
	closed := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$status", "CLOSED"}}}, bson.D{{Key: "$eq", Value: bson.A{"$authRevocationState", "APPLIED"}}},
		bson.D{{Key: "$ne", Value: bson.A{"$authReceiptId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$authAppliedAt", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$closedAt", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$nextAttemptAt", nil}}},
	}}}
	return bson.D{
		{Key: "$and", Value: bson.A{
			schema,
			bson.D{{Key: "$expr", Value: bson.D{{Key: "$or", Value: bson.A{closing, closed}}}}},
		}},
	}
}
