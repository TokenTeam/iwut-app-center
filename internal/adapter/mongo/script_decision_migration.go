package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
)

const scriptDecisionMigrationID = "0039_script_decisions_and_cascade"

func (m *Migrator) applyScriptDecisionMigration(ctx context.Context) error {
	reviewValidator := scriptCollectionValidator(bson.D{
		scriptUUIDField("reviewId"), {Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{domain.ReviewPending, "APPROVED", "REJECTED"}}}},
		scriptAuditActorProperty("submittedBy"), scriptAuditDateProperty("submittedAt"),
		{Key: "snapshot", Value: scriptNestedObject(scriptReviewSnapshotFields())},
		{Key: "policyBinding", Value: scriptNestedObject(scriptPolicyFields())},
	})
	addSchemaProperty(reviewValidator[0].Value.(bson.D), "decision", scriptNestedObject(bson.D{
		scriptUUIDField("decisionId"), {Key: "outcome", Value: bson.D{{Key: "enum", Value: bson.A{"APPROVED", "REJECTED"}}}},
		scriptAuditActorProperty("decidedBy"), scriptAuditDateProperty("decidedAt"), scriptReviewTextField("policyVersion", 128),
		{Key: "reason", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "maxLength", Value: 2048}}},
		{Key: "confirmedCheckIds", Value: bson.D{{Key: "bsonType", Value: "array"}, {Key: "uniqueItems", Value: true}, {Key: "items", Value: nonEmptyStringSchema()}}},
	}))
	text := bson.D{{Key: "bsonType", Value: "string"}}
	nonnegative := bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(0)}}
	tasks := scriptCollectionValidator(bson.D{
		scriptUUIDField("taskId"), scriptUUIDField("scriptResourceId"), scriptUUIDField("scriptVersionId"), scriptDigestField(),
		scriptUUIDField("scriptReviewId"), scriptUUIDField("decisionId"), scriptAuditActorProperty("triggeredBy"), scriptAuditDateProperty("decidedAt"),
		{Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{"PENDING", "RUNNING", "COMPLETE"}}}},
		{Key: "phase", Value: bson.D{{Key: "enum", Value: bson.A{"DEPENDENCIES", "REVIEWS"}}}},
		{Key: "cursor", Value: text}, {Key: "processed", Value: nonnegative}, {Key: "total", Value: nonnegative},
		{Key: "rejected", Value: nonnegative}, {Key: "alreadyTerminal", Value: nonnegative}, {Key: "closed", Value: nonnegative},
		{Key: "leaseToken", Value: text}, scriptAuditDateProperty("leaseUntil"), scriptAuditDateProperty("nextAttemptAt"),
		{Key: "lastFailure", Value: bson.D{{Key: "enum", Value: bson.A{"", "SYSTEM_PRINCIPAL_UNAVAILABLE", "CASCADE_TARGET_UNAVAILABLE"}}}},
	})
	targets := scriptCollectionValidator(bson.D{
		scriptUUIDField("scriptDecisionId"), scriptUUIDField("appReviewId"), scriptUUIDField("applicationId"), scriptAuditDateProperty("processedAt"),
		{Key: "result", Value: bson.D{{Key: "enum", Value: bson.A{"REJECTED", "ALREADY_TERMINAL", "APPLICATION_CLOSED"}}}},
	})
	addSchemaProperty(targets[0].Value.(bson.D), "closure", scriptNestedObject(bson.D{scriptUUIDField("closureId"), scriptAuditDateProperty("closingStartedAt")}))
	for _, collection := range []struct {
		name      string
		validator bson.D
	}{
		{scriptReviewsCollection, reviewValidator}, {scriptCascadeTasksCollection, tasks}, {scriptCascadeTargetsCollection, targets},
		{scriptVersionAuditsCollection, scriptVersionAuditValidator(bson.A{scriptDraftCreated, scriptDraftReplaced, scriptDraftSubmitted, "REVIEW_APPROVED", "REVIEW_REJECTED"})},
		{applicationReviewsCollectionName, scriptDependencyReviewValidator()},
	} {
		if err := m.ensureValidatedCollection(ctx, collection.name, collection.validator); err != nil {
			return err
		}
	}
	for _, index := range []struct {
		collection, name string
		keys             bson.D
		unique           bool
	}{
		{scriptCascadeTasksCollection, "uq_script_cascade_task", bson.D{{Key: "taskId", Value: 1}}, true},
		{scriptCascadeTasksCollection, "uq_script_cascade_decision", bson.D{{Key: "decisionId", Value: 1}}, true},
		{scriptCascadeTasksCollection, "ix_script_cascade_worker", bson.D{{Key: "status", Value: 1}, {Key: "nextAttemptAt", Value: 1}, {Key: "leaseUntil", Value: 1}, {Key: "taskId", Value: 1}}, false},
		{scriptCascadeTargetsCollection, "uq_script_cascade_target", bson.D{{Key: "scriptDecisionId", Value: 1}, {Key: "appReviewId", Value: 1}}, true},
		{applicationReviewsCollectionName, "ix_app_review_script_verification", bson.D{{Key: "snapshot.scriptDependencies.scriptVersionId", Value: 1}, {Key: "reviewId", Value: 1}}, false},
	} {
		if _, err := m.database.Collection(index.collection).Indexes().CreateOne(ctx, dm.IndexModel{
			Keys: index.keys, Options: options.Index().SetName(index.name).SetUnique(index.unique),
		}); err != nil {
			return err
		}
	}
	return nil
}
