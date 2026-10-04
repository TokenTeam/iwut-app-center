package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationCatalogMigrationID         = "0019_application_catalog_indexes"
	applicationCatalogStableScanIndexName = "ix_application_publications_major_application_stable"
)

func (migrator *Migrator) applyApplicationCatalogMigration(ctx context.Context) error {
	_, err := migrator.database.Collection(applicationPublicationsCollectionName).Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "rpcApiMajor", Value: 1}, {Key: "applicationId", Value: 1}},
		Options: options.Index().SetName(applicationCatalogStableScanIndexName).SetPartialFilterExpression(bson.D{{Key: "stableVersionId", Value: bson.D{{Key: "$type", Value: "string"}}}}),
	})
	if err != nil {
		return fmt.Errorf("create application catalog stable scan index: %w", err)
	}
	return nil
}
