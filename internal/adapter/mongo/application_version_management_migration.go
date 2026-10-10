package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func (migrator *Migrator) applyApplicationVersionManagementMigration(ctx context.Context) error {
	_, err := migrator.database.Collection(applicationVersionsCollectionName).Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys: bson.D{
			{Key: "applicationId", Value: 1},
			{Key: "reviewStatus", Value: 1},
			{Key: "sequence", Value: -1},
			{Key: "versionId", Value: -1},
		},
		Options: options.Index().SetName(applicationVersionManagementIndexName),
	})
	if err != nil {
		return fmt.Errorf("create application version management query index: %w", err)
	}
	return nil
}
