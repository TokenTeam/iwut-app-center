package mongo

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const myTestApplicationMigrationID = "0031_my_test_application_query"
const myTestMembershipScanIndex = "ix_tester_memberships_self_status_application_membership"

func (r *Migrator) applyMyTestApplicationMigration(ctx context.Context) error {
	_, err := r.database.Collection(applicationTesterMembershipsCollectionName).Indexes().CreateOne(ctx, m.IndexModel{Keys: bson.D{{Key: "testerAuthId", Value: 1}, {Key: "status", Value: 1}, {Key: "applicationId", Value: 1}, {Key: "membershipId", Value: 1}}, Options: options.Index().SetName(myTestMembershipScanIndex).SetCollation(&options.Collation{Locale: "simple"})})
	if err != nil {
		return fmt.Errorf("create own tester membership scan index: %w", err)
	}
	return nil
}
