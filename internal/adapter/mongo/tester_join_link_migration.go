package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationTesterJoinLinkMigrationID     = "0009_application_tester_join_link"
	applicationTesterJoinLinksCollectionName = "application_tester_join_links"
	testerJoinLinkIDUniqueIndexName          = "uq_application_tester_join_links_join_link_id"
	testerJoinLinkTokenHashUniqueIndexName   = "uq_application_tester_join_links_token_hash"
	testerJoinLinkActiveUniqueIndexName      = "uq_application_tester_join_links_active_application_id"
	testerJoinLinkAuditIndexName             = "ix_application_tester_join_links_application_id_created_at_join_link_id"
)

func (migrator *Migrator) applyApplicationTesterJoinLinkMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationTesterJoinLinksCollectionName, applicationTesterJoinLinkValidator()); err != nil {
		return err
	}
	_, err := migrator.database.Collection(applicationTesterJoinLinksCollectionName).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "joinLinkId", Value: 1}}, Options: options.Index().SetName(testerJoinLinkIDUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "tokenHash", Value: 1}}, Options: options.Index().SetName(testerJoinLinkTokenHashUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}}, Options: options.Index().SetName(testerJoinLinkActiveUniqueIndexName).SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "status", Value: "ACTIVE"}})},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "createdAt", Value: 1}, {Key: "joinLinkId", Value: 1}}, Options: options.Index().SetName(testerJoinLinkAuditIndexName)},
	})
	if err != nil {
		return fmt.Errorf("create tester join link indexes: %w", safeTesterJoinLinkError(err))
	}
	return nil
}

func applicationTesterJoinLinkValidator() bson.D {
	return applicationTesterJoinLinkValidatorWithAdminTransfer(false)
}

func applicationTesterJoinLinkValidatorWithAdminTransfer(includeAdminTransfer bool) bson.D {
	nullable := func(schema bson.D) bson.D {
		return bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, schema}}}
	}
	reasons := bson.A{"ROTATED", "MANUAL"}
	if includeAdminTransfer {
		reasons = append(reasons, "ADMIN_TRANSFER")
	}
	revokedReasons := bson.A{
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$revocationReason", "ROTATED"}}},
			bson.D{{Key: "$ne", Value: bson.A{"$replacedByJoinLinkId", nil}}},
			bson.D{{Key: "$ne", Value: bson.A{"$replacedByJoinLinkId", "$joinLinkId"}}},
		}}},
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$revocationReason", "MANUAL"}}},
			bson.D{{Key: "$eq", Value: bson.A{"$replacedByJoinLinkId", nil}}},
		}}},
	}
	if includeAdminTransfer {
		revokedReasons = append(revokedReasons, bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$revocationReason", "ADMIN_TRANSFER"}}},
			bson.D{{Key: "$eq", Value: bson.A{"$replacedByJoinLinkId", nil}}},
		}}})
	}
	return bson.D{{Key: "$and", Value: bson.A{
		publicationObjectSchema(bson.A{"joinLinkId", "applicationId", "tokenHash", "status", "createdBy", "createdAt", "revokedBy", "revokedAt", "revocationReason", "replacedByJoinLinkId"}, bson.D{
			{Key: "joinLinkId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
			{Key: "tokenHash", Value: bson.D{{Key: "bsonType", Value: "binData"}}},
			{Key: "status", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: bson.A{"ACTIVE", "REVOKED"}}}},
			{Key: "createdBy", Value: nonEmptyStringSchema()}, {Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "revokedBy", Value: nullable(nonEmptyStringSchema())}, {Key: "revokedAt", Value: nullable(bson.D{{Key: "bsonType", Value: "date"}})},
			{Key: "revocationReason", Value: nullable(bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: reasons}})},
			{Key: "replacedByJoinLinkId", Value: nullable(uuidV7Schema())},
		}),
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
			// $cond prevents $binarySize from throwing for malformed non-binary input;
			// either invalid type or invalid length is a normal validator rejection.
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$type", Value: "$tokenHash"}}, "binData"}}},
				bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$binarySize", Value: "$tokenHash"}}, 32}}}, false,
			}}},
			bson.D{{Key: "$or", Value: bson.A{
				bson.D{{Key: "$and", Value: bson.A{
					bson.D{{Key: "$eq", Value: bson.A{"$status", "ACTIVE"}}},
					bson.D{{Key: "$eq", Value: bson.A{"$revokedBy", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$revokedAt", nil}}},
					bson.D{{Key: "$eq", Value: bson.A{"$revocationReason", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$replacedByJoinLinkId", nil}}},
				}}},
				bson.D{{Key: "$and", Value: bson.A{
					bson.D{{Key: "$eq", Value: bson.A{"$status", "REVOKED"}}},
					bson.D{{Key: "$ne", Value: bson.A{"$revokedBy", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$revokedAt", nil}}},
					bson.D{{Key: "$or", Value: revokedReasons}},
				}}},
			}}},
		}}}}},
	}}}
}
