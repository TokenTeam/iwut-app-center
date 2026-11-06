package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const scriptQueryMigrationID = "0037_script_query_indexes"

func (m *Migrator) applyScriptQueryMigration(ctx context.Context) error {
	indexes := []struct {
		collection, name string
		keys             bson.D
	}{
		{scriptResourcesCollectionName, "ix_script_resources_created", bson.D{{Key: "createdAt", Value: -1}, {Key: "resourceId", Value: -1}}},
		{scriptVersionsCollection, "ix_script_versions_resource_created", bson.D{{Key: "resourceId", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "versionId", Value: -1}}},
		{scriptVersionsCollection, "ix_script_versions_resource_state_created", bson.D{{Key: "resourceId", Value: 1}, {Key: "state", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "versionId", Value: -1}}},
		{scriptReviewsCollection, "ix_script_reviews_status_submitted", bson.D{{Key: "status", Value: 1}, {Key: "submittedAt", Value: -1}, {Key: "reviewId", Value: -1}}},
		{scriptReviewsCollection, "ix_script_reviews_status_resource_submitted", bson.D{{Key: "status", Value: 1}, {Key: "snapshot.resourceId", Value: 1}, {Key: "submittedAt", Value: -1}, {Key: "reviewId", Value: -1}}},
	}
	for _, index := range indexes {
		_, err := m.database.Collection(index.collection).Indexes().CreateOne(ctx, dm.IndexModel{
			Keys: index.keys, Options: options.Index().SetName(index.name).SetCollation(&options.Collation{Locale: "simple"}),
		})
		if err != nil {
			return fmt.Errorf("create script query index %s: %w", index.name, err)
		}
	}
	return nil
}
