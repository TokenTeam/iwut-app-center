package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	migrationLedgerCollectionName             = "app_center_schema_migrations"
	applicationCreationMigrationID            = "0001_application_creation"
	applicationIDUniqueIndexName              = "uq_applications_id"
	applicationAdminNameUniqueIndexName       = "uq_applications_admin_id_name_key"
	applicationQuotaAdminIDUniqueIndexName    = "uq_application_creation_quotas_admin_id"
	applicationVersionMigrationID             = "0002_application_version"
	applicationVersionDraftUpdateMigrationID  = "0003_application_version_draft_update"
	applicationVersionIDUniqueIndexName       = "uq_application_versions_version_id"
	applicationVersionSequenceUniqueIndexName = "uq_application_versions_application_id_sequence"
	applicationVersionLabelUniqueIndexName    = "uq_application_versions_application_id_version_label"
	applicationReviewMigrationID              = "0004_application_review_submission"
	applicationReviewDecisionMigrationID      = "0005_application_review_decision"
	applicationReviewRestorationMigrationID   = "0006_application_review_draft_restoration"
	versionReviewPolicyMigrationID            = "0007_version_review_policy"
	versionReviewPoliciesCollectionName       = "version_review_policies"
	versionReviewPolicyVersionUniqueIndexName = "uq_version_review_policies_version"
	applicationReviewIDUniqueIndexName        = "uq_application_reviews_review_id"
	applicationReviewAttemptUniqueIndexName   = "uq_application_reviews_version_id_attempt"
	applicationReviewSourceUniqueIndexName    = "uq_application_reviews_version_id_source_revision"
	applicationReviewPendingUniqueIndexName   = "uq_application_reviews_pending_version_id"
	applicationReviewQueueIndexName           = "ix_application_reviews_status_submitted_at_review_id"
)

type migrationRecord struct {
	ID        string    `bson:"_id"`
	AppliedAt time.Time `bson:"appliedAt"`
}

// Migrator owns explicit schema changes. Applications must invoke it as a
// deployment step; repository construction and service startup do not call it.
type Migrator struct {
	database *drivermongo.Database
}

func NewMigrator(database *drivermongo.Database) *Migrator {
	return &Migrator{database: database}
}

func (migrator *Migrator) Migrate(ctx context.Context) error {
	if migrator == nil || migrator.database == nil {
		return fmt.Errorf("run MongoDB migrations: database is nil")
	}

	if err := migrator.ensureMigrationLedger(ctx); err != nil {
		return err
	}

	migrations := []struct {
		id    string
		apply func(context.Context) error
	}{
		{id: applicationCreationMigrationID, apply: migrator.applyApplicationCreationMigration},
		{id: applicationVersionMigrationID, apply: migrator.applyApplicationVersionMigration},
		{id: applicationVersionDraftUpdateMigrationID, apply: migrator.applyApplicationVersionDraftUpdateMigration},
		{id: applicationReviewMigrationID, apply: migrator.applyApplicationReviewMigration},
		{id: applicationReviewDecisionMigrationID, apply: migrator.applyApplicationReviewDecisionMigration},
		{id: applicationReviewRestorationMigrationID, apply: migrator.applyApplicationReviewRestorationMigration},
		{id: versionReviewPolicyMigrationID, apply: migrator.applyVersionReviewPolicyMigration},
		{id: applicationPublicationMigrationID, apply: migrator.applyApplicationPublicationMigration},
		{id: applicationTesterJoinLinkMigrationID, apply: migrator.applyApplicationTesterJoinLinkMigration},
		{id: applicationTesterMembershipMigrationID, apply: migrator.applyApplicationTesterMembershipMigration},
		{id: applicationProfileRevisionMigrationID, apply: migrator.applyApplicationProfileRevisionMigration},
	}
	for _, migration := range migrations {
		if err := migrator.applyMigration(ctx, migration.id, migration.apply); err != nil {
			return err
		}
	}
	return nil
}

func (migrator *Migrator) applyMigration(ctx context.Context, id string, apply func(context.Context) error) error {
	ledger := migrator.database.Collection(migrationLedgerCollectionName)
	err := ledger.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Err()
	switch {
	case err == nil:
		return nil
	case !errors.Is(err, drivermongo.ErrNoDocuments):
		return fmt.Errorf("read migration ledger for %s: %w", id, err)
	}

	if err := apply(ctx); err != nil {
		return fmt.Errorf("apply migration %s: %w", id, err)
	}
	_, err = ledger.InsertOne(ctx, migrationRecord{ID: id, AppliedAt: time.Now().UTC()})
	if err == nil || drivermongo.IsDuplicateKeyError(err) {
		return nil
	}
	return fmt.Errorf("record migration %s: %w", id, err)
}

func (migrator *Migrator) ensureMigrationLedger(ctx context.Context) error {
	err := migrator.database.CreateCollection(ctx, migrationLedgerCollectionName)
	if err == nil || isNamespaceExists(err) {
		return nil
	}
	return fmt.Errorf("create migration ledger: %w", err)
}

func (migrator *Migrator) applyApplicationCreationMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationInitialValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationCreationQuotasCollectionName, applicationCreationQuotaValidator()); err != nil {
		return err
	}

	applications := migrator.database.Collection(applicationsCollectionName)
	_, err := applications.Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{
			Keys:    bson.D{{Key: "id", Value: 1}},
			Options: options.Index().SetName(applicationIDUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "adminId", Value: 1},
				{Key: "nameKey", Value: 1},
			},
			Options: options.Index().SetName(applicationAdminNameUniqueIndexName).SetUnique(true),
		},
	})
	if err != nil {
		return fmt.Errorf("create application indexes: %w", err)
	}

	quotas := migrator.database.Collection(applicationCreationQuotasCollectionName)
	_, err = quotas.Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "adminId", Value: 1}},
		Options: options.Index().SetName(applicationQuotaAdminIDUniqueIndexName).SetUnique(true),
	})
	if err != nil {
		return fmt.Errorf("create application quota index: %w", err)
	}
	return nil
}

func (migrator *Migrator) applyApplicationVersionMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionsCollectionName, applicationVersionInitialValidator()); err != nil {
		return err
	}

	versions := migrator.database.Collection(applicationVersionsCollectionName)
	_, err := versions.Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{
			Keys:    bson.D{{Key: "versionId", Value: 1}},
			Options: options.Index().SetName(applicationVersionIDUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "applicationId", Value: 1},
				{Key: "sequence", Value: 1},
			},
			Options: options.Index().SetName(applicationVersionSequenceUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{
				{Key: "applicationId", Value: 1},
				{Key: "versionLabel", Value: 1},
			},
			Options: options.Index().
				SetName(applicationVersionLabelUniqueIndexName).
				SetUnique(true).
				SetCollation(&options.Collation{Locale: "simple"}),
		},
	})
	if err != nil {
		return fmt.Errorf("create application version indexes: %w", err)
	}
	return nil
}

func (migrator *Migrator) applyApplicationVersionDraftUpdateMigration(ctx context.Context) error {
	// This migration changes both the application_versions lifecycle/audit
	// schema and the applications coordination schema. The coordination field
	// is a technical write fence: replacing a draft performs a conditional $inc
	// on it inside the same transaction that mutates the Version, so a
	// concurrent administrator transfer on the same Application document
	// conflicts and forces a retry that re-evaluates the administrator filter.
	if err := migrator.ensureValidatedCollection(ctx, applicationsCollectionName, applicationValidator()); err != nil {
		return fmt.Errorf("enable application coordination revision: %w", err)
	}
	if err := migrator.backfillApplicationCoordinationRevision(ctx); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionsCollectionName, applicationVersionDraftValidator()); err != nil {
		return fmt.Errorf("enable application version draft updates: %w", err)
	}
	return nil
}

// backfillApplicationCoordinationRevision gives every pre-existing Application
// a concrete starting revision so the first fence $inc observes a stable long
// instead of creating the field implicitly.
func (migrator *Migrator) backfillApplicationCoordinationRevision(ctx context.Context) error {
	_, err := migrator.database.Collection(applicationsCollectionName).UpdateMany(
		ctx,
		bson.D{{Key: "coordinationRevision", Value: bson.D{{Key: "$exists", Value: false}}}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "coordinationRevision", Value: int64(0)}}}},
	)
	if err != nil {
		return fmt.Errorf("backfill application coordination revision: %w", err)
	}
	return nil
}

func (migrator *Migrator) applyApplicationReviewMigration(ctx context.Context) error {
	// UC-APP-004 changes the Version validator because a successful submission
	// stores SUBMITTED, increments revision and updates the modification audit.
	// It is the only stage allowed to add SUBMITTED to the lifecycle enum.
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionsCollectionName, applicationVersionValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationReviewsCollectionName, applicationReviewValidator()); err != nil {
		return err
	}

	reviews := migrator.database.Collection(applicationReviewsCollectionName)
	_, err := reviews.Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{
			Keys:    bson.D{{Key: "reviewId", Value: 1}},
			Options: options.Index().SetName(applicationReviewIDUniqueIndexName).SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "versionId", Value: 1}, {Key: "attempt", Value: 1}},
			Options: options.Index().SetName(applicationReviewAttemptUniqueIndexName).SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "versionId", Value: 1}, {Key: "sourceVersionRevision", Value: 1}},
			Options: options.Index().SetName(applicationReviewSourceUniqueIndexName).SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "versionId", Value: 1}},
			Options: options.Index().
				SetName(applicationReviewPendingUniqueIndexName).
				SetUnique(true).
				SetPartialFilterExpression(bson.D{{Key: "status", Value: "PENDING"}}),
		},
		{
			Keys:    bson.D{{Key: "status", Value: 1}, {Key: "submittedAt", Value: 1}, {Key: "reviewId", Value: 1}},
			Options: options.Index().SetName(applicationReviewQueueIndexName),
		},
	})
	if err != nil {
		return fmt.Errorf("create application review indexes: %w", err)
	}
	return nil
}

// applyApplicationReviewDecisionMigration is the 0005 stage. It is the only
// stage allowed to add APPROVED/REJECTED to the Version lifecycle and to open
// the one-time decision object on ApplicationReview. It adds no collection.
func (migrator *Migrator) applyApplicationReviewDecisionMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationVersionsCollectionName, applicationVersionDecisionValidator()); err != nil {
		return err
	}
	if err := migrator.ensureValidatedCollection(ctx, applicationReviewsCollectionName, applicationReviewDecisionValidator()); err != nil {
		return err
	}
	return nil
}

// applyApplicationReviewRestorationMigration is the 0006 stage. It opens the
// typed one-time draftRestoration object while keeping the rejected decision
// immutable and leaving ApplicationVersion storage unchanged.
func (migrator *Migrator) applyApplicationReviewRestorationMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, applicationReviewsCollectionName, applicationReviewRestorationValidator()); err != nil {
		return err
	}
	return nil
}

func (migrator *Migrator) applyVersionReviewPolicyMigration(ctx context.Context) error {
	if err := migrator.ensureValidatedCollection(ctx, versionReviewPoliciesCollectionName, versionReviewPolicyValidator()); err != nil {
		return err
	}
	collection := migrator.database.Collection(versionReviewPoliciesCollectionName)
	if _, err := collection.Indexes().CreateOne(ctx, drivermongo.IndexModel{
		Keys:    bson.D{{Key: "version", Value: 1}},
		Options: options.Index().SetName(versionReviewPolicyVersionUniqueIndexName).SetUnique(true),
	}); err != nil {
		return fmt.Errorf("create version review policy index: %w", err)
	}

	seed := versionReviewPolicyDocument{
		Version: "app-version-review-v1",
		RequiredChecks: []string{
			"content-policy-reviewed",
			"launch-url-content-reviewed",
			"requested-access-reviewed",
		},
		Status: "ACTIVE",
	}
	_, err := collection.InsertOne(ctx, seed)
	if err != nil && !drivermongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("seed version review policy: %w", err)
	}
	var existing versionReviewPolicyDocument
	if err := collection.FindOne(ctx, bson.D{{Key: "version", Value: seed.Version}}).Decode(&existing); err != nil {
		return fmt.Errorf("verify version review policy seed: %w", err)
	}
	if !equalVersionReviewPolicyDocuments(existing, seed) {
		return fmt.Errorf("verify version review policy seed: version %q has different immutable content", seed.Version)
	}
	return nil
}

func (migrator *Migrator) ensureValidatedCollection(ctx context.Context, name string, validator bson.D) error {
	err := migrator.database.CreateCollection(
		ctx,
		name,
		options.CreateCollection().
			SetValidator(validator).
			SetValidationLevel("strict").
			SetValidationAction("error"),
	)
	if err == nil {
		return nil
	}
	if !isNamespaceExists(err) {
		return fmt.Errorf("create collection %s: %w", name, err)
	}

	result := migrator.database.RunCommand(ctx, bson.D{
		{Key: "collMod", Value: name},
		{Key: "validator", Value: validator},
		{Key: "validationLevel", Value: "strict"},
		{Key: "validationAction", Value: "error"},
	})
	if err := result.Err(); err != nil {
		return fmt.Errorf("update validator for collection %s: %w", name, err)
	}
	return nil
}

func isNamespaceExists(err error) bool {
	var commandError drivermongo.CommandError
	return errors.As(err, &commandError) && commandError.Code == 48
}

func versionReviewPolicyValidator() bson.D {
	return bson.D{{Key: "$jsonSchema", Value: bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{"version", "requiredChecks", "status"}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
			{Key: "version", Value: bson.D{
				{Key: "bsonType", Value: "string"},
				{Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"},
			}},
			{Key: "requiredChecks", Value: bson.D{
				{Key: "bsonType", Value: "array"},
				{Key: "uniqueItems", Value: true},
				{Key: "items", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}},
			}},
			{Key: "status", Value: bson.D{
				{Key: "bsonType", Value: "string"},
				{Key: "enum", Value: bson.A{"ACTIVE", "RETIRED"}},
			}},
		}},
	}}}
}

// applicationInitialValidator is the 0001 schema. It must stay byte-for-byte
// equivalent to the validator a fresh 0001 deployment established; the
// coordinationRevision fence is added by 0003, never by rewriting this stage.
func applicationInitialValidator() bson.D {
	return applicationValidatorForCoordination(false)
}

// applicationValidator is the 0003 schema. It keeps the 0001 business
// constraints and adds the adapter-only coordinationRevision write fence.
func applicationValidator() bson.D {
	return applicationValidatorForCoordination(true)
}

func applicationValidatorForCoordination(includeCoordinationRevision bool) bson.D {
	required := bson.A{
		"id", "name", "nameKey", "adminId", "createdAt",
		"nextVersionSequence", "nextProfileRevisionSequence",
	}
	properties := bson.D{
		{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
		{Key: "id", Value: bson.D{
			{Key: "bsonType", Value: "string"},
			{Key: "pattern", Value: "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$"},
		}},
		{Key: "name", Value: bson.D{
			{Key: "bsonType", Value: "string"},
			{Key: "pattern", Value: "^[A-Za-z0-9_-]{1,50}$"},
		}},
		{Key: "nameKey", Value: bson.D{
			{Key: "bsonType", Value: "string"},
			{Key: "pattern", Value: "^[a-z0-9_-]{1,50}$"},
		}},
		{Key: "adminId", Value: bson.D{
			{Key: "bsonType", Value: "string"},
			{Key: "minLength", Value: 1},
		}},
		{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		{Key: "nextVersionSequence", Value: bson.D{
			{Key: "bsonType", Value: "int"},
			{Key: "minimum", Value: int32(1)},
		}},
		{Key: "nextProfileRevisionSequence", Value: bson.D{
			{Key: "bsonType", Value: "int"},
			{Key: "minimum", Value: int32(1)},
		}},
	}
	if includeCoordinationRevision {
		required = append(required, "coordinationRevision")
		properties = append(properties, bson.E{Key: "coordinationRevision", Value: bson.D{
			{Key: "bsonType", Value: "long"},
			{Key: "minimum", Value: int64(0)},
		}})
	}

	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: required},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: properties},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$nameKey", bson.D{{Key: "$toLower", Value: "$name"}}}}}}},
	}}}
}

func applicationCreationQuotaValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{"adminId", "limit", "usedCount", "revision", "updatedAt"}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "adminId", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
				}},
				{Key: "limit", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(0)},
				}},
				{Key: "usedCount", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(0)},
				}},
				{Key: "revision", Value: bson.D{
					{Key: "bsonType", Value: "long"},
					{Key: "minimum", Value: int64(0)},
				}},
				{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$lte", Value: bson.A{"$usedCount", "$limit"}}}}},
	}}}
}

// applicationVersion validators are storage-level defense in depth. They
// enforce facts MongoDB can express reliably (shape, byte limits, set
// relationships, RPC ordering, and lifecycle/audit shape). Full URL
// address-class and Unicode domain validation remains in version/domain and in
// the document mapper; these schemas are not a replacement for those
// constructors.
//
// The lifecycle enum is advanced one migration at a time. Each stage accepts
// only states that already exist at that point in the migration chain, so a
// database upgraded through the chain ends with exactly the same validator as a
// fresh database.
func applicationVersionInitialValidator() bson.D {
	return applicationVersionValidatorForLifecycle(bson.A{"DRAFT"}, true)
}

// applicationVersionDraftValidator is the 0003 schema. It opens DRAFT to
// replacement (revision >= 1, mutable audit) but still rejects SUBMITTED and
// every later lifecycle state; SUBMITTED only becomes valid in 0004.
func applicationVersionDraftValidator() bson.D {
	return applicationVersionValidatorForLifecycle(bson.A{"DRAFT"}, false)
}

// applicationVersionValidator is the 0004 schema. It adds SUBMITTED, the only
// state UC-APP-004 stores, and still leaves APPROVED/REJECTED/REVOKED to future
// migrations.
func applicationVersionValidator() bson.D {
	return applicationVersionValidatorForLifecycle(bson.A{"DRAFT", "SUBMITTED"}, false)
}

// applicationVersionDecisionValidator is the 0005 schema. It adds the two
// decision outcomes UC-APP-005 writes and still leaves REVOKED to a future
// migration.
func applicationVersionDecisionValidator() bson.D {
	return applicationVersionValidatorForLifecycle(bson.A{"DRAFT", "SUBMITTED", "APPROVED", "REJECTED"}, false)
}

func applicationVersionValidatorForLifecycle(reviewStatuses bson.A, initialOnly bool) bson.D {
	revisionSchema := bson.D{
		{Key: "bsonType", Value: "long"},
		{Key: "minimum", Value: int64(1)},
	}
	expressions := bson.A{
		bson.D{{Key: "$gt", Value: bson.A{"$rpcApiMaxVersionExclusive", "$rpcApiMinVersion"}}},
		bson.D{{Key: "$lte", Value: bson.A{bson.D{{Key: "$strLenBytes", Value: "$launchUrl"}}, 2048}}},
		bson.D{{Key: "$eq", Value: bson.A{
			bson.D{{Key: "$size", Value: bson.D{{Key: "$setIntersection", Value: bson.A{"$requiredScopes", "$optionalScopes"}}}}},
			0,
		}}},
	}
	if initialOnly {
		revisionSchema = bson.D{
			{Key: "bsonType", Value: "long"},
			{Key: "enum", Value: bson.A{int64(1)}},
		}
		expressions = append(expressions,
			bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}},
			bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}},
		)
	} else {
		// Revision 1 is always the unedited creation audit. Any later revision
		// (replacement or lifecycle transition) may carry a different updater.
		expressions = append(expressions, bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{"$revision", int64(1)}}},
			bson.D{{Key: "$and", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{"$reviewStatus", "DRAFT"}}},
				bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}},
				bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}},
			}}},
		}}})
	}

	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{
				"versionId", "applicationId", "sequence", "versionLabel", "launchUrl",
				"rpcApiMinVersion", "rpcApiMaxVersionExclusive", "requiredCapabilities",
				"requiredScopes", "optionalScopes", "reviewStatus", "createdBy", "createdAt",
				"revision", "updatedBy", "updatedAt",
			}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "versionId", Value: uuidV7Schema()},
				{Key: "applicationId", Value: uuidV7Schema()},
				{Key: "sequence", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(1)},
				}},
				{Key: "versionLabel", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
					{Key: "maxLength", Value: 50},
					{Key: "pattern", Value: "^[^\\x00-\\x1F\\x7F\\s](?:[^\\x00-\\x1F\\x7F]*[^\\x00-\\x1F\\x7F\\s])?$"},
				}},
				{Key: "launchUrl", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
					{Key: "pattern", Value: "^https?://"},
				}},
				{Key: "rpcApiMinVersion", Value: bson.D{
					{Key: "bsonType", Value: "int"},
					{Key: "minimum", Value: int32(1)},
				}},
				{Key: "rpcApiMaxVersionExclusive", Value: bson.D{{Key: "bsonType", Value: "int"}}},
				{Key: "requiredCapabilities", Value: stringSetSchema("^[a-z][a-z0-9]*(?:\\.[a-z][a-z0-9]*)*\\.v[1-9][0-9]*$")},
				{Key: "requiredScopes", Value: stringSetSchema("")},
				{Key: "optionalScopes", Value: stringSetSchema("")},
				{Key: "reviewStatus", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "enum", Value: reviewStatuses},
				}},
				{Key: "createdBy", Value: nonEmptyStringSchema()},
				{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
				{Key: "revision", Value: revisionSchema},
				{Key: "updatedBy", Value: nonEmptyStringSchema()},
				{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: expressions}}}},
	}}}
}

// applicationReviewValidator is the 0004 schema: only a PENDING Review without
// a decision may exist.
func applicationReviewValidator() bson.D {
	return applicationReviewValidatorForLifecycle(false, false)
}

// applicationReviewDecisionValidator is the 0005 schema. It allows exactly one
// decision object on an APPROVED/REJECTED Review and keeps PENDING Reviews
// decision-free.
func applicationReviewDecisionValidator() bson.D {
	return applicationReviewValidatorForLifecycle(true, false)
}

func applicationReviewRestorationValidator() bson.D {
	return applicationReviewValidatorForLifecycle(true, true)
}

func applicationReviewValidatorForDecision(includeDecision bool) bson.D {
	return applicationReviewValidatorForLifecycle(includeDecision, false)
}

func applicationReviewValidatorForLifecycle(includeDecision, includeRestoration bool) bson.D {
	statuses := bson.A{"PENDING"}
	decisionSchema := bson.D{{Key: "bsonType", Value: "null"}}
	draftRestorationSchema := bson.D{{Key: "bsonType", Value: "null"}}
	expressions := bson.A{
		bson.D{{Key: "$gt", Value: bson.A{"$snapshot.rpcApiMaxVersionExclusive", "$snapshot.rpcApiMinVersion"}}},
		bson.D{{Key: "$lte", Value: bson.A{bson.D{{Key: "$strLenBytes", Value: "$snapshot.launchUrl"}}, 2048}}},
		bson.D{{Key: "$eq", Value: bson.A{
			bson.D{{Key: "$size", Value: bson.D{{Key: "$setIntersection", Value: bson.A{"$snapshot.requiredScopes", "$snapshot.optionalScopes"}}}}},
			0,
		}}},
	}
	if includeDecision {
		statuses = bson.A{"PENDING", "APPROVED", "REJECTED"}
		decisionSchema = bson.D{{Key: "oneOf", Value: bson.A{
			bson.D{{Key: "bsonType", Value: "null"}},
			applicationReviewDecisionSchema(),
		}}}
		expressions = append(expressions, applicationReviewDecisionConsistencyExpression())
	}
	if includeRestoration {
		draftRestorationSchema = bson.D{{Key: "oneOf", Value: bson.A{
			bson.D{{Key: "bsonType", Value: "null"}},
			applicationReviewDraftRestorationSchema(),
		}}}
		expressions = append(expressions, applicationReviewDraftRestorationConsistencyExpression())
	}

	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{
				"reviewId", "applicationId", "versionId", "attempt", "sourceVersionRevision",
				"status", "decision", "draftRestoration", "snapshot", "scopeCatalogRevision",
				"preflightPolicyVersion", "submittedBy", "submittedAt",
			}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{{Key: "bsonType", Value: "objectId"}}},
				{Key: "reviewId", Value: uuidV7Schema()},
				{Key: "applicationId", Value: uuidV7Schema()},
				{Key: "versionId", Value: uuidV7Schema()},
				{Key: "attempt", Value: bson.D{{Key: "bsonType", Value: "int"}, {Key: "minimum", Value: int32(1)}}},
				{Key: "sourceVersionRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
				{Key: "status", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: statuses}}},
				{Key: "decision", Value: decisionSchema},
				{Key: "draftRestoration", Value: draftRestorationSchema},
				{Key: "snapshot", Value: applicationReviewSnapshotSchema()},
				{Key: "scopeCatalogRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
				{Key: "preflightPolicyVersion", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"},
				}},
				{Key: "submittedBy", Value: nonEmptyStringSchema()},
				{Key: "submittedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: expressions}}}},
	}}}
}

func applicationReviewDraftRestorationSchema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{"restoredBy", "restoredAt", "resultVersionRevision"}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "restoredBy", Value: nonEmptyStringSchema()},
			{Key: "restoredAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "resultVersionRevision", Value: bson.D{
				{Key: "bsonType", Value: "long"},
				{Key: "minimum", Value: int64(1)},
			}},
		}},
	}
}

func applicationReviewDraftRestorationConsistencyExpression() bson.D {
	return bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$draftRestoration", nil}}},
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$status", "REJECTED"}}},
			bson.D{{Key: "$eq", Value: bson.A{"$decision.outcome", "REJECTED"}}},
		}}},
	}}}
}

func applicationReviewDecisionSchema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{
			"outcome", "reviewPolicyVersion", "confirmedCheckIds", "reason",
			"decidedBy", "decidedAt", "approvalValidation",
		}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "outcome", Value: bson.D{
				{Key: "bsonType", Value: "string"},
				{Key: "enum", Value: bson.A{"APPROVED", "REJECTED"}},
			}},
			{Key: "reviewPolicyVersion", Value: bson.D{
				{Key: "bsonType", Value: "string"},
				{Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"},
			}},
			{Key: "confirmedCheckIds", Value: bson.D{
				{Key: "bsonType", Value: "array"},
				{Key: "uniqueItems", Value: true},
				{Key: "items", Value: nonEmptyStringSchema()},
			}},
			{Key: "reason", Value: bson.D{{Key: "oneOf", Value: bson.A{
				bson.D{{Key: "bsonType", Value: "null"}},
				bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "minLength", Value: 1},
				},
			}}}},
			{Key: "decidedBy", Value: nonEmptyStringSchema()},
			{Key: "decidedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "approvalValidation", Value: bson.D{{Key: "oneOf", Value: bson.A{
				bson.D{{Key: "bsonType", Value: "null"}},
				bson.D{
					{Key: "bsonType", Value: "object"},
					{Key: "required", Value: bson.A{"scopeCatalogRevision", "preflightPolicyVersion"}},
					{Key: "additionalProperties", Value: false},
					{Key: "properties", Value: bson.D{
						{Key: "scopeCatalogRevision", Value: bson.D{
							{Key: "bsonType", Value: "long"},
							{Key: "minimum", Value: int64(1)},
						}},
						{Key: "preflightPolicyVersion", Value: bson.D{
							{Key: "bsonType", Value: "string"},
							{Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"},
						}},
					}},
				},
			}}}},
		}},
	}
}

// applicationReviewDecisionConsistencyExpression keeps status and decision in
// lockstep: PENDING has no decision, APPROVED/REJECTED has a decision whose
// outcome matches status, and REJECTED carries no approvalValidation.
func applicationReviewDecisionConsistencyExpression() bson.D {
	return bson.D{{Key: "$or", Value: bson.A{
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$status", "PENDING"}}},
			bson.D{{Key: "$eq", Value: bson.A{"$decision", nil}}},
		}}},
		bson.D{{Key: "$and", Value: bson.A{
			bson.D{{Key: "$ne", Value: bson.A{"$status", "PENDING"}}},
			bson.D{{Key: "$ne", Value: bson.A{"$decision", nil}}},
			bson.D{{Key: "$eq", Value: bson.A{"$decision.outcome", "$status"}}},
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{"$status", "APPROVED"}}},
				bson.D{{Key: "$ne", Value: bson.A{"$decision.approvalValidation", nil}}},
				bson.D{{Key: "$and", Value: bson.A{
					bson.D{{Key: "$eq", Value: bson.A{"$status", "REJECTED"}}},
					bson.D{{Key: "$eq", Value: bson.A{"$decision.approvalValidation", nil}}},
					bson.D{{Key: "$eq", Value: bson.A{"$decision.confirmedCheckIds", bson.A{}}}},
					bson.D{{Key: "$ne", Value: bson.A{"$decision.reason", nil}}},
				}}},
			}}},
			// BR-REV-015 counts Unicode code points, not bytes, so a multibyte
			// reason is never rejected by a byte-length limit. $cond does not
			// evaluate the length branch for the nullable APPROVED reason.
			bson.D{{Key: "$cond", Value: bson.A{
				bson.D{{Key: "$eq", Value: bson.A{"$decision.reason", nil}}},
				true,
				bson.D{{Key: "$lte", Value: bson.A{
					bson.D{{Key: "$strLenCP", Value: "$decision.reason"}},
					2000,
				}}},
			}}},
		}}},
	}}}
}

func applicationReviewSnapshotSchema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{
			"versionLabel", "launchUrl", "rpcApiMinVersion", "rpcApiMaxVersionExclusive",
			"requiredCapabilities", "requiredScopes", "optionalScopes",
		}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "versionLabel", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 50}}},
			{Key: "launchUrl", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^https://"}}},
			{Key: "rpcApiMinVersion", Value: bson.D{{Key: "bsonType", Value: "int"}, {Key: "minimum", Value: int32(1)}}},
			{Key: "rpcApiMaxVersionExclusive", Value: bson.D{{Key: "bsonType", Value: "int"}}},
			{Key: "requiredCapabilities", Value: stringSetSchema("^[a-z][a-z0-9]*(?:\\.[a-z][a-z0-9]*)*\\.v[1-9][0-9]*$")},
			{Key: "requiredScopes", Value: stringSetSchema("")},
			{Key: "optionalScopes", Value: stringSetSchema("")},
		}},
	}
}

func uuidV7Schema() bson.D {
	return bson.D{
		{Key: "bsonType", Value: "string"},
		{Key: "pattern", Value: "^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-7[0-9a-fA-F]{3}-[89aAbB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$"},
	}
}

func nonEmptyStringSchema() bson.D {
	return bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}
}

func stringSetSchema(pattern string) bson.D {
	itemSchema := bson.D{{Key: "bsonType", Value: "string"}}
	if pattern != "" {
		itemSchema = append(itemSchema, bson.E{Key: "pattern", Value: pattern})
	}
	return bson.D{
		{Key: "bsonType", Value: "array"},
		{Key: "uniqueItems", Value: true},
		{Key: "items", Value: itemSchema},
	}
}
