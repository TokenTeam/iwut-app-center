package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const profileManagementMigrationID = "0032_application_profile_management_query"
const profileManagementStatusIndex = "ix_profile_revisions_application_status_sequence_id"

func (r *Migrator) applyProfileManagementMigration(ctx context.Context) error {
	_, err := r.database.Collection(applicationProfileRevisionsCollectionName).Indexes().CreateOne(ctx, m.IndexModel{
		Keys:    bson.D{{Key: "applicationId", Value: 1}, {Key: "reviewStatus", Value: 1}, {Key: "sequence", Value: -1}, {Key: "profileRevisionId", Value: -1}},
		Options: options.Index().SetName(profileManagementStatusIndex).SetCollation(&options.Collation{Locale: "simple"}),
	})
	return err
}
