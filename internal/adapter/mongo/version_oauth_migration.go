package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	versionOAuthRedirectMigrationID                 = "0015_version_oauth_redirects"
	applicationVersionOAuthConfigVersionUniqueIndex = "uq_application_version_oauth_configs_version_id"
)

func (migrator *Migrator) applyVersionOAuthRedirectMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionOAuthConfigsCollectionName, applicationVersionOAuthConfigValidator()); err != nil {
		return fmt.Errorf("create application version OAuth config collection: %w", err)
	}
	_, err := migrator.database.Collection(applicationVersionOAuthConfigsCollectionName).Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "applicationVersionId", Value: 1}},
		Options: options.Index().SetName(applicationVersionOAuthConfigVersionUniqueIndex).SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create application version OAuth config index: %w", err)
	}
	if err := migrator.backfillApplicationVersionOAuthConfigs(ctx); err != nil {
		return err
	}
	if err := migrator.backfillApplicationReviewOAuthSnapshots(ctx); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationReviewsCollectionName, withPublicationFence(applicationReviewRestorationValidator())); err != nil {
		return fmt.Errorf("enable OAuth redirect review snapshots: %w", err)
	}
	if err := migrator.activateVersionReviewPolicyV2(ctx); err != nil {
		return err
	}
	return nil
}

func (migrator *Migrator) backfillApplicationVersionOAuthConfigs(ctx context.Context) error {
	cursor, err := migrator.database.Collection(applicationVersionsCollectionName).Find(
		ctx,
		bson.D{},
		options.Find().SetProjection(bson.D{{Key: "versionId", Value: 1}, {Key: "applicationId", Value: 1}}),
	)
	if err != nil {
		return fmt.Errorf("list application versions for OAuth config backfill: %w", err)
	}
	defer cursor.Close(ctx)
	configs := migrator.database.Collection(applicationVersionOAuthConfigsCollectionName)
	for cursor.Next(ctx) {
		var version struct {
			VersionID     string `bson:"versionId"`
			ApplicationID string `bson:"applicationId"`
		}
		if err := cursor.Decode(&version); err != nil {
			return fmt.Errorf("decode application version for OAuth config backfill: %w", err)
		}
		document := applicationVersionOAuthConfigDocument{
			ApplicationVersionID: version.VersionID,
			ApplicationID:        version.ApplicationID,
			OAuthRedirects: oauthRedirectConfigurationDocument{
				PKCERedirectURIs:         []string{},
				ConfidentialRedirectURIs: []string{},
			},
		}
		if _, err := configs.UpdateOne(
			ctx,
			bson.D{{Key: "applicationVersionId", Value: version.VersionID}},
			bson.D{{Key: "$setOnInsert", Value: document}},
			options.UpdateOne().SetUpsert(true),
		); err != nil {
			return fmt.Errorf("backfill application version OAuth config: %w", err)
		}
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("iterate application versions for OAuth config backfill: %w", err)
	}
	return nil
}

func (migrator *Migrator) backfillApplicationReviewOAuthSnapshots(ctx context.Context) error {
	empty := oauthRedirectConfigurationDocument{PKCERedirectURIs: []string{}, ConfidentialRedirectURIs: []string{}}
	_, err := migrator.database.Collection(applicationReviewsCollectionName).UpdateMany(
		ctx,
		bson.D{{Key: "snapshot.oauthRedirects", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "snapshot.oauthRedirects", Value: empty}}}},
	)
	if err != nil {
		return fmt.Errorf("backfill application review OAuth snapshots: %w", err)
	}
	return nil
}

func (migrator *Migrator) activateVersionReviewPolicyV2(ctx context.Context) error {
	collection := migrator.database.Collection(versionReviewPoliciesCollectionName)
	v1Checks := []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed"}
	v1Result, err := collection.UpdateOne(
		ctx,
		bson.D{{Key: "version", Value: "app-version-review-v1"}, {Key: "requiredChecks", Value: v1Checks}, {Key: "status", Value: bson.D{{Key: "$in", Value: bson.A{"ACTIVE", "RETIRED"}}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "RETIRED"}}}},
	)
	if err != nil {
		return fmt.Errorf("retire version review policy v1: %w", err)
	}
	if v1Result.MatchedCount != 1 {
		return fmt.Errorf("retire version review policy v1: immutable content differs or policy is missing")
	}
	v2 := versionReviewPolicyDocument{
		Version: "app-version-review-v2",
		RequiredChecks: []string{
			"content-policy-reviewed",
			"launch-url-content-reviewed",
			"requested-access-reviewed",
			"oauth-redirects-reviewed",
		},
		Status: "ACTIVE",
	}
	_, err = collection.InsertOne(ctx, v2)
	if err != nil && !drivermongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("seed version review policy v2: %w", err)
	}
	var existing versionReviewPolicyDocument
	if err := collection.FindOne(ctx, bson.D{{Key: "version", Value: v2.Version}}).Decode(&existing); err != nil {
		return fmt.Errorf("verify version review policy v2: %w", err)
	}
	if !equalVersionReviewPolicyDocuments(existing, v2) {
		return fmt.Errorf("verify version review policy v2: immutable content differs")
	}
	return nil
}

func applicationVersionOAuthConfigValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{"applicationVersionId", "applicationId", "oauthRedirects"}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "applicationVersionId", Value: uuidV7Schema()},
				{Key: "applicationId", Value: uuidV7Schema()},
				{Key: "oauthRedirects", Value: oauthRedirectConfigurationSchema()},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{
			bson.D{{Key: "$size", Value: bson.D{{Key: "$setIntersection", Value: bson.A{"$oauthRedirects.pkceRedirectUris", "$oauthRedirects.confidentialRedirectUris"}}}}},
			0,
		}}}}},
	}}}
}

func oauthRedirectConfigurationSchema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{"pkceRedirectUris", "confidentialRedirectUris"}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "pkceRedirectUris", Value: oauthRedirectArraySchema()},
			{Key: "confidentialRedirectUris", Value: oauthRedirectArraySchema()},
		}},
	}
}

func oauthRedirectArraySchema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "array"},
		{Key: "maxItems", Value: 10},
		{Key: "uniqueItems", Value: true},
		{Key: "items", Value: bson.D{
			{Key: "bsonType", Value: "string"},
			{Key: "minLength", Value: 1},
			{Key: "maxLength", Value: 2048},
			{Key: "pattern", Value: "^https://"},
		}},
	}
}
