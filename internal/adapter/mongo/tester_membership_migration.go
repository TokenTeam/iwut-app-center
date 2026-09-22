package mongo

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationTesterMembershipMigrationID     = "0010_application_tester_membership"
	applicationTesterMembershipsCollectionName = "application_tester_memberships"
	testerMembershipIDUniqueIndexName          = "uq_application_tester_memberships_membership_id"
	testerMembershipActiveUniqueIndexName      = "uq_application_tester_memberships_active_application_tester"
	testerMembershipApplicationAuditIndexName  = "ix_application_tester_memberships_application_status_joined_id"
	testerMembershipUserAuditIndexName         = "ix_application_tester_memberships_tester_status_joined_id"
)

func (m *Migrator) applyApplicationTesterMembershipMigration(ctx context.Context) error {
	if err := m.ensureValidatedCollection(ctx, applicationTesterMembershipsCollectionName, applicationTesterMembershipValidator()); err != nil {
		return fmt.Errorf("create tester membership collection: %w", safeTesterMembershipError(err))
	}
	_, err := m.database.Collection(applicationTesterMembershipsCollectionName).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "membershipId", Value: 1}}, Options: options.Index().SetName(testerMembershipIDUniqueIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "testerAuthId", Value: 1}}, Options: options.Index().SetName(testerMembershipActiveUniqueIndexName).SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "status", Value: "ACTIVE"}})},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "status", Value: 1}, {Key: "joinedAt", Value: 1}, {Key: "membershipId", Value: 1}}, Options: options.Index().SetName(testerMembershipApplicationAuditIndexName)},
		{Keys: bson.D{{Key: "testerAuthId", Value: 1}, {Key: "status", Value: 1}, {Key: "joinedAt", Value: 1}, {Key: "membershipId", Value: 1}}, Options: options.Index().SetName(testerMembershipUserAuditIndexName)},
	})
	if err != nil {
		return fmt.Errorf("create tester membership indexes: %w", safeTesterMembershipError(err))
	}
	return nil
}
func applicationTesterMembershipValidator() bson.D {
	nullable := func(schema bson.D) bson.D {
		return bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, schema}}}
	}
	return bson.D{{Key: "$and", Value: bson.A{
		publicationObjectSchema(bson.A{"membershipId", "applicationId", "testerAuthId", "status", "joinedViaJoinLinkId", "joinedAt", "removedBy", "removedAt"}, bson.D{
			{Key: "membershipId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "testerAuthId", Value: nonEmptyStringSchema()},
			{Key: "status", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: bson.A{"ACTIVE", "REMOVED"}}}},
			{Key: "joinedViaJoinLinkId", Value: uuidV7Schema()}, {Key: "joinedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "removedBy", Value: nullable(nonEmptyStringSchema())}, {Key: "removedAt", Value: nullable(bson.D{{Key: "bsonType", Value: "date"}})},
		}),
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$status", "ACTIVE"}}}, bson.D{{Key: "$eq", Value: bson.A{"$removedBy", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$removedAt", nil}}}}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$status", "REMOVED"}}}, bson.D{{Key: "$ne", Value: bson.A{"$removedBy", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$removedAt", nil}}}}}},
		}}}}},
	}}}
}
