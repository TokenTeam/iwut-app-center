package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationProfileReviewQueryMigrationID = "0025_application_profile_review_query"
	profileReviewQueueIndexName              = "ix_application_profile_reviews_status_submitted_at_id"
)

func (migrator *Migrator) applyApplicationProfileReviewQueryMigration(ctx context.Context) error {
	_, err := migrator.database.Collection(applicationProfileReviewsCollectionName).Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "status", Value: 1}, {Key: "submittedAt", Value: 1}, {Key: "profileReviewId", Value: 1}},
		Options: options.Index().SetName(profileReviewQueueIndexName),
	})
	return err
}
