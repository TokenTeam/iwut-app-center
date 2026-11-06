package mongo

import (
	"context"
	"fmt"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
)

const (
	scriptReviewMigrationID        = "0036_script_review_submission"
	scriptReviewIDIndex            = "uq_script_reviews_review_id"
	scriptReviewVersionIndex       = "uq_script_reviews_snapshot_version_id"
	scriptReviewPolicyVersionIndex = "uq_script_review_policies_version"
)

func (m *Migrator) applyScriptReviewMigration(ctx context.Context) error {
	if err := m.ensureValidatedCollection(ctx, scriptReviewsCollection, scriptCollectionValidator(bson.D{
		scriptUUIDField("reviewId"), {Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{domain.ReviewPending}}}},
		scriptAuditActorProperty("submittedBy"), scriptAuditDateProperty("submittedAt"),
		{Key: "snapshot", Value: scriptNestedObject(scriptReviewSnapshotFields())},
		{Key: "policyBinding", Value: scriptNestedObject(scriptPolicyFields())},
	})); err != nil {
		return err
	}
	if err := m.ensureValidatedCollection(ctx, scriptReviewPoliciesCollection, scriptCollectionValidator(append(scriptPolicyFields(),
		bson.E{Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{scriptPolicyActive, scriptPolicyRetired}}}},
		scriptPositiveLongField("coordinationRevision"),
	))); err != nil {
		return err
	}
	if err := m.ensureValidatedCollection(ctx, scriptVersionAuditsCollection, scriptVersionAuditValidator(
		bson.A{scriptDraftCreated, scriptDraftReplaced, scriptDraftSubmitted},
	)); err != nil {
		return err
	}
	if _, err := m.database.Collection(scriptReviewsCollection).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "reviewId", Value: 1}}, Options: options.Index().SetName(scriptReviewIDIndex).SetUnique(true)},
		{Keys: bson.D{{Key: "snapshot.versionId", Value: 1}}, Options: options.Index().SetName(scriptReviewVersionIndex).SetUnique(true)},
	}); err != nil {
		return fmt.Errorf("create script review indexes: %w", err)
	}
	policies := m.database.Collection(scriptReviewPoliciesCollection)
	if _, err := policies.Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys: bson.D{{Key: "version", Value: 1}}, Options: options.Index().SetName(scriptReviewPolicyVersionIndex).SetUnique(true),
	}); err != nil {
		return fmt.Errorf("create script policy index: %w", err)
	}
	initial := domain.InitialReviewPolicy()
	seed := scriptReviewPolicyDocument{
		Binding: scriptReviewPolicyBinding(initial),
		Status:  scriptPolicyActive, CoordinationRevision: 1,
	}
	if _, err := policies.UpdateOne(ctx, bson.M{"version": initial.Version},
		bson.M{"$setOnInsert": seed}, options.UpdateOne().SetUpsert(true)); err != nil {
		return fmt.Errorf("seed formal script review policy: %w", err)
	}
	var persisted scriptReviewPolicyDocument
	if err := policies.FindOne(ctx, bson.M{"version": initial.Version}).Decode(&persisted); err != nil {
		return fmt.Errorf("read formal script review policy seed: %w", err)
	}
	policy, err := persisted.Binding.policy()
	if err != nil || policy.Version != initial.Version || !slices.Equal(policy.Checks, initial.Checks) ||
		persisted.CoordinationRevision < 1 || (persisted.Status != scriptPolicyActive && persisted.Status != scriptPolicyRetired) {
		return domain.ErrReviewPolicyUnavailable
	}
	return nil
}

func scriptReviewSnapshotFields() bson.D {
	return bson.D{
		scriptResourceIDProperty(), scriptUUIDField("versionId"),
		scriptReviewTextField("versionLabel", 64),
		{Key: "contentFormat", Value: bson.D{{Key: "enum", Value: bson.A{domain.JSONFormat}}}},
		scriptReviewTextField("contentText", domain.MaxContentBytes),
		scriptDigestField(), scriptPositiveLongField("sourceRevision"), scriptContributorsField(),
		scriptReviewTextField("resourceName", 128), scriptReviewTextField("resourcePurpose", 2048),
		scriptPositiveLongField("resourceRevision"),
	}
}

func scriptPolicyFields() bson.D {
	return bson.D{
		scriptReviewTextField("version", 128),
		{Key: "checks", Value: bson.D{
			{Key: "bsonType", Value: "array"}, {Key: "minItems", Value: 1},
			{Key: "items", Value: scriptNestedObject(bson.D{
				scriptReviewTextField("id", 64), scriptReviewTextField("title", 256), scriptReviewTextField("description", 4096),
				{Key: "mandatory", Value: bson.D{{Key: "bsonType", Value: "bool"}}},
			})},
		}},
	}
}

func scriptNestedObject(fields bson.D) bson.D {
	required := make(bson.A, 0, len(fields))
	for _, field := range fields {
		required = append(required, field.Key)
	}
	return bson.D{
		{Key: "bsonType", Value: "object"}, {Key: "additionalProperties", Value: false},
		{Key: "required", Value: required}, {Key: "properties", Value: fields},
	}
}

func scriptPositiveLongField(name string) bson.E {
	return bson.E{Key: name, Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}}
}

func scriptReviewTextField(name string, maximum int) bson.E {
	return bson.E{Key: name, Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: maximum}}}
}
