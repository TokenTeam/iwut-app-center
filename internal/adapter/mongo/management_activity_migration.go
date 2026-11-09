package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const managementActivityMigrationID = "0043_application_management_activity"
const activitySubmissionIndexName = "ix_management_activity_app_submission_id"
const activityDecisionIndexName = "ix_management_activity_app_decision_id"

func (m *Migrator) applyManagementActivityMigration(ctx context.Context) error {
	for source := 0; source < 2; source++ {
		collection, id, _, _ := activityFields(source)
		_, err := m.database.Collection(collection).Indexes().CreateMany(ctx, []dm.IndexModel{
			{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "submittedAt", Value: -1}, {Key: id, Value: -1}}, Options: options.Index().SetName(activitySubmissionIndexName)},
			{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "decision.outcome", Value: 1}, {Key: "decision.decidedAt", Value: -1}, {Key: id, Value: -1}}, Options: options.Index().SetName(activityDecisionIndexName)},
		})
		if err != nil {
			return err
		}
	}
	return nil
}
