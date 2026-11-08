package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const platformApplicationQueryMigrationID = "0042_platform_application_query"
const platformApplicationQueryIndexName = "ix_applications_platform_name_id"
const platformApplicationAdminQueryIndexName = "ix_applications_platform_admin_name_id"

func (m *Migrator) applyPlatformApplicationQueryMigration(ctx context.Context) error {
	_, err := m.database.Collection(applicationsCollectionName).Indexes().CreateMany(ctx, []dm.IndexModel{
		{Keys: bson.D{{Key: "nameKey", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetName(platformApplicationQueryIndexName)},
		{Keys: bson.D{{Key: "adminId", Value: 1}, {Key: "nameKey", Value: 1}, {Key: "id", Value: 1}}, Options: options.Index().SetName(platformApplicationAdminQueryIndexName)},
	})
	return err
}
