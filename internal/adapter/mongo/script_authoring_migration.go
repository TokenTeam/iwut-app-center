package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const scriptAuthoringMigrationID = "0044_script_authoring"
const scriptOwnerIndexName = "ix_script_resources_owner_created"

func (m *Migrator) applyScriptAuthoringMigration(ctx context.Context) error {
	resources := m.database.Collection(scriptResourcesCollectionName)
	// Refuse legacy resource rows without writing or inferring ownership.
	err := resources.FindOne(ctx, bson.M{"$or": bson.A{
		bson.M{"ownerAuthId": bson.M{"$not": bson.M{"$type": "string"}}},
		bson.M{"ownerAuthId": ""},
		bson.M{"$expr": bson.M{"$ne": bson.A{"$ownerAuthId", "$createdBy"}}},
		bson.M{"$expr": bson.M{"$ne": bson.A{"$ownerAuthId", "$updatedBy"}}},
	}}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if err == nil {
		return errors.New("script resources lack valid author ownership; initialize a fresh database")
	}
	if !errors.Is(err, dm.ErrNoDocuments) {
		return err
	}
	fields := append(scriptResourceProperties(), scriptAuditActorProperty("ownerAuthId"))
	if err := m.ensureValidatedCollection(ctx, scriptResourcesCollectionName, scriptCollectionValidator(fields)); err != nil {
		return err
	}
	_, err = resources.Indexes().CreateOne(ctx, dm.IndexModel{
		Keys:    bson.D{{Key: "ownerAuthId", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "resourceId", Value: -1}},
		Options: options.Index().SetName(scriptOwnerIndexName).SetCollation(&options.Collation{Locale: "simple"}),
	})
	return err
}
