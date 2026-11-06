package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
)

const (
	scriptVersionMigrationID = "0035_script_version_drafts"
	scriptVersionIDIndex     = "uq_script_versions_version_id"
	scriptVersionLabelIndex  = "uq_script_versions_resource_id_version_label"
	scriptVersionAuditIndex  = "uq_script_version_audits_version_id_revision"
	scriptUUIDv7Pattern      = "^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"
)

func (m *Migrator) applyScriptVersionMigration(ctx context.Context) error {
	if err := m.ensureValidatedCollection(ctx, scriptVersionsCollection, scriptVersionDocumentValidator()); err != nil {
		return err
	}
	if err := m.ensureValidatedCollection(ctx, scriptVersionAuditsCollection, scriptVersionAuditValidator(bson.A{scriptDraftCreated, scriptDraftReplaced})); err != nil {
		return err
	}
	if _, err := m.database.Collection(scriptVersionsCollection).Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "versionId", Value: 1}}, Options: options.Index().SetName(scriptVersionIDIndex).SetUnique(true)},
		{Keys: bson.D{{Key: "resourceId", Value: 1}, {Key: "versionLabel", Value: 1}},
			Options: options.Index().SetName(scriptVersionLabelIndex).SetUnique(true).SetCollation(&options.Collation{Locale: "simple"})},
	}); err != nil {
		return fmt.Errorf("create script version indexes: %w", err)
	}
	_, err := m.database.Collection(scriptVersionAuditsCollection).Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "versionId", Value: 1}, {Key: "revision", Value: 1}},
		Options: options.Index().SetName(scriptVersionAuditIndex).SetUnique(true),
	})
	return err
}

func scriptVersionDocumentValidator() bson.D {
	return scriptCollectionValidator(bson.D{
		scriptResourceIDProperty(), scriptUUIDField("versionId"), scriptResourceRevisionProperty(),
		{Key: "versionLabel", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$"}}},
		{Key: "contentFormat", Value: bson.D{{Key: "enum", Value: bson.A{domain.JSONFormat}}}},
		{Key: "contentText", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: domain.MaxContentBytes}}},
		scriptDigestField(),
		{Key: "state", Value: bson.D{{Key: "enum", Value: bson.A{domain.VersionDraft, domain.VersionSubmitted, domain.VersionApproved, domain.VersionRejected}}}},
		scriptContributorsField(),
		scriptAuditActorProperty("createdBy"), scriptAuditDateProperty("createdAt"),
		scriptAuditActorProperty("updatedBy"), scriptAuditDateProperty("updatedAt"),
	})
}

func scriptVersionAuditValidator(actions bson.A) bson.D {
	return scriptCollectionValidator(bson.D{
		scriptResourceIDProperty(), scriptUUIDField("versionId"), scriptResourceRevisionProperty(), scriptDigestField(),
		{Key: "action", Value: bson.D{{Key: "enum", Value: actions}}},
		scriptAuditActorProperty("actor"), scriptAuditDateProperty("changedAt"),
	})
}

func scriptUUIDField(name string) bson.E {
	return bson.E{Key: name, Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: scriptUUIDv7Pattern}}}
}

func scriptDigestField() bson.E {
	return bson.E{Key: "digest", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[0-9a-f]{64}$"}}}
}

func scriptContributorsField() bson.E {
	return bson.E{Key: "contributors", Value: bson.D{
		{Key: "bsonType", Value: "array"}, {Key: "minItems", Value: 1}, {Key: "uniqueItems", Value: true},
		{Key: "items", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}},
	}}
}
