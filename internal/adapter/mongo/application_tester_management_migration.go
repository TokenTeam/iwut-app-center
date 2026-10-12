package mongo

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const testerManagementMigrationID = "0030_application_tester_management_query"
const testerMembershipTimeIndex = "ix_tester_memberships_application_joined_id"
const testerMembershipStatusTimeIndex = "ix_tester_memberships_application_status_joined_id"
const testerLinkTimeIndex = "ix_tester_links_application_created_id"
const testerLinkStatusTimeIndex = "ix_tester_links_application_status_created_id"

func testerManagementIndexModels(timeField, idField, allName, statusName string) []drivermongo.IndexModel {
	return []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: timeField, Value: -1}, {Key: idField, Value: -1}}, Options: options.Index().SetName(allName)},
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "status", Value: 1}, {Key: timeField, Value: -1}, {Key: idField, Value: -1}}, Options: options.Index().SetName(statusName)},
	}
}
func (m *Migrator) applyTesterManagementMigration(ctx context.Context) error {
	if _, err := m.database.Collection(applicationTesterMembershipsCollectionName).Indexes().CreateMany(ctx, testerManagementIndexModels("joinedAt", "membershipId", testerMembershipTimeIndex, testerMembershipStatusTimeIndex)); err != nil {
		return fmt.Errorf("create tester membership management indexes: %w", err)
	}
	if _, err := m.database.Collection(applicationTesterJoinLinksCollectionName).Indexes().CreateMany(ctx, testerManagementIndexModels("createdAt", "joinLinkId", testerLinkTimeIndex, testerLinkStatusTimeIndex)); err != nil {
		return fmt.Errorf("create tester join link management indexes: %w", err)
	}
	return nil
}
