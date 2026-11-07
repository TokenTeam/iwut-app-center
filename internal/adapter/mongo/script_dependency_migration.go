package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/shared"
)

const scriptDependencyMigrationID = "0038_application_script_dependencies"

func (m *Migrator) applyScriptDependencyMigration(ctx context.Context) error {
	version := withPublicationFence(applicationVersionDecisionValidator())
	versionSchema := version[0].Value.(bson.A)[0].(bson.D)[0].Value.(bson.D)
	addSchemaProperty(versionSchema, "scriptReferences", scriptReferenceArray(scriptReferenceFields()))

	review := scriptDependencyReviewValidator()

	scriptVersion := scriptVersionDocumentValidator()
	addSchemaProperty(scriptVersion[0].Value.(bson.D), "dependencyFence",
		bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(0)}})
	relation := scriptCollectionValidator(append(frozenScriptReferenceFields(),
		scriptUUIDField("appReviewId"), scriptUUIDField("applicationId"), scriptUUIDField("appVersionId")))
	validators := []struct {
		name      string
		validator bson.D
	}{
		{applicationVersionsCollectionName, version},
		{applicationReviewsCollectionName, review},
		{scriptVersionsCollection, scriptVersion},
		{scriptDependenciesCollection, relation},
	}
	for _, collection := range validators {
		if err := m.ensureValidatedCollection(ctx, collection.name, collection.validator); err != nil {
			return err
		}
	}
	if _, err := m.database.Collection(scriptVersionsCollection).Indexes().CreateOne(ctx, dm.IndexModel{
		Keys: bson.D{{Key: "createdAt", Value: -1}, {Key: "versionId", Value: -1}},
		Options: options.Index().SetName("ix_script_candidate_created").SetCollation(&options.Collation{Locale: "simple"}).
			SetPartialFilterExpression(bson.M{"state": bson.M{"$in": bson.A{"SUBMITTED", "APPROVED"}}}),
	}); err != nil {
		return fmt.Errorf("create script candidate index: %w", err)
	}

	_, err := m.database.Collection(scriptDependenciesCollection).Indexes().CreateMany(ctx, []dm.IndexModel{
		{Keys: bson.D{{Key: "appReviewId", Value: 1}, {Key: "scriptVersionId", Value: 1}}, Options: options.Index().SetName("uq_app_review_script_dependency").SetUnique(true)},
		{Keys: bson.D{{Key: "scriptVersionId", Value: 1}, {Key: "appReviewId", Value: 1}}, Options: options.Index().SetName("ix_script_dependency_review")},
	})
	if err != nil {
		return fmt.Errorf("create app review script dependency indexes: %w", err)
	}
	return nil
}

func scriptDependencyReviewValidator() bson.D {
	review := withPublicationFence(applicationReviewRestorationValidator())
	reviewSchema := review[0].Value.(bson.A)[0].(bson.D)[0].Value.(bson.D)
	properties := schemaDocument(reviewSchema, "properties")
	addSchemaProperty(schemaDocument(properties, "snapshot"), "scriptDependencies", scriptReferenceArray(frozenScriptReferenceFields()))
	decision := schemaDocument(properties, "decision")[0].Value.(bson.A)[1].(bson.D)
	approval := schemaDocument(schemaDocument(decision, "properties"), "approvalValidation")[0].Value.(bson.A)[1].(bson.D)
	addSchemaProperty(approval, "scriptApprovals", scriptReferenceArray(bson.D{
		scriptUUIDField("scriptVersionId"), scriptDigestField(), scriptUUIDField("scriptDecisionId"),
	}))

	return review
}

func schemaDocument(doc bson.D, key string) bson.D {
	for _, field := range doc {
		if field.Key == key {
			return field.Value.(bson.D)
		}
	}
	panic("missing schema field " + key)
}

func addSchemaProperty(schema bson.D, key string, value bson.D) {
	for i := range schema {
		if schema[i].Key == "properties" {
			schema[i].Value = append(schema[i].Value.(bson.D), bson.E{Key: key, Value: value})
			return
		}
	}
	panic("missing schema properties")
}

func scriptReferenceFields() bson.D {
	return bson.D{scriptUUIDField("scriptResourceId"), scriptUUIDField("scriptVersionId")}
}

func frozenScriptReferenceFields() bson.D {
	return append(scriptReferenceFields(), bson.E{Key: "format", Value: bson.D{{Key: "enum", Value: bson.A{"json"}}}},
		scriptDigestField(), scriptUUIDField("scriptReviewId"))
}

func scriptReferenceArray(fields bson.D) bson.D {
	return bson.D{{Key: "bsonType", Value: "array"}, {Key: "maxItems", Value: shared.MaxScriptReferences},
		{Key: "uniqueItems", Value: true}, {Key: "items", Value: scriptNestedObject(fields)}}
}
