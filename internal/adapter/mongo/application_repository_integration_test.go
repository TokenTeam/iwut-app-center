package mongo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const mongoIntegrationURIEnvironment = "MONGODB_INTEGRATION_URI"

var applicationIDCounter atomic.Uint64

func TestApplicationRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-APP-003 same admin case-insensitive name is unique", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		first := integrationApplication(t, "auth-a", "Course_App")
		second := integrationApplication(t, "auth-a", "course_app")

		if err := repository.CreateWithinQuota(t.Context(), first, 10); err != nil {
			t.Fatalf("create first application: %v", err)
		}
		if err := repository.CreateWithinQuota(t.Context(), second, 10); !errors.Is(err, port.ErrApplicationNameAlreadyExists) {
			t.Fatalf("second error = %v, want name conflict", err)
		}
		assertCollectionCount(t, database, applicationsCollectionName, 1)
		assertQuota(t, database, "auth-a", 10, 1, 1)
	})

	t.Run("BR-APP-003 different admins can use same name", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		for _, adminID := range []string{"auth-a", "auth-b"} {
			if err := repository.CreateWithinQuota(t.Context(), integrationApplication(t, adminID, "Course_App"), 10); err != nil {
				t.Fatalf("create for %s: %v", adminID, err)
			}
		}
		assertCollectionCount(t, database, applicationsCollectionName, 2)
	})

	t.Run("BR-APP-005 limit ten rejects eleventh and persisted raised limit permits next", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		for index := 0; index < 10; index++ {
			name := fmt.Sprintf("application_%02d", index)
			if err := repository.CreateWithinQuota(t.Context(), integrationApplication(t, "auth-quota", name), 10); err != nil {
				t.Fatalf("create application %d: %v", index+1, err)
			}
		}
		if err := repository.CreateWithinQuota(t.Context(), integrationApplication(t, "auth-quota", "application_10"), 10); !errors.Is(err, port.ErrApplicationQuotaExceeded) {
			t.Fatalf("eleventh error = %v, want quota exceeded", err)
		}
		assertQuota(t, database, "auth-quota", 10, 10, 10)

		updateResult, err := database.Collection(applicationCreationQuotasCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "adminId", Value: "auth-quota"}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "limit", Value: int32(11)}}}},
		)
		if err != nil {
			t.Fatalf("simulate future quota adjustment: %v", err)
		}
		if updateResult.MatchedCount != 1 || updateResult.ModifiedCount != 1 {
			t.Fatalf("quota adjustment result = matched %d modified %d, want 1 and 1", updateResult.MatchedCount, updateResult.ModifiedCount)
		}

		if err := repository.CreateWithinQuota(
			t.Context(),
			integrationApplication(t, "auth-quota", "application_11"),
			domain.InitialDeveloperApplicationQuotaLimit,
		); err != nil {
			t.Fatalf("create after raising limit: %v", err)
		}
		assertQuota(t, database, "auth-quota", 11, 11, 11)
	})

	t.Run("BR-APP-005 initial limit never overwrites an existing lower limit", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		quota := applicationCreationQuotaDocument{
			AdminID: "auth-custom-quota", Limit: 1, UsedCount: 0, Revision: 0,
			UpdatedAt: time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC),
		}
		if _, err := database.Collection(applicationCreationQuotasCollectionName).InsertOne(t.Context(), quota); err != nil {
			t.Fatalf("seed adjusted quota: %v", err)
		}

		if err := repository.CreateWithinQuota(
			t.Context(),
			integrationApplication(t, "auth-custom-quota", "first"),
			domain.InitialDeveloperApplicationQuotaLimit,
		); err != nil {
			t.Fatalf("create within adjusted quota: %v", err)
		}
		if err := repository.CreateWithinQuota(
			t.Context(),
			integrationApplication(t, "auth-custom-quota", "second"),
			domain.InitialDeveloperApplicationQuotaLimit,
		); !errors.Is(err, port.ErrApplicationQuotaExceeded) {
			t.Fatalf("second error = %v, want quota exceeded", err)
		}
		assertQuota(t, database, "auth-custom-quota", 1, 1, 1)
	})

	t.Run("BR-APP-001 creation initializes both next sequences", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		application := integrationApplication(t, "auth-sequence", "sequence_app")
		if err := repository.CreateWithinQuota(t.Context(), application, 10); err != nil {
			t.Fatalf("create application: %v", err)
		}

		var document applicationDocument
		err := database.Collection(applicationsCollectionName).
			FindOne(t.Context(), bson.D{{Key: "id", Value: application.ID().String()}}).
			Decode(&document)
		if err != nil {
			t.Fatalf("read application document: %v", err)
		}
		if document.NextVersionSequence != 1 || document.NextProfileRevisionSequence != 1 {
			t.Fatalf("next sequences = (%d, %d), want (1, 1)", document.NextVersionSequence, document.NextProfileRevisionSequence)
		}
	})

	t.Run("BR-APP-003 concurrent equivalent names only create once", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		const contenders = 12
		start := make(chan struct{})
		results := make(chan error, contenders)
		var group sync.WaitGroup

		for index := 0; index < contenders; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				<-start
				name := "Concurrent_App"
				if index%2 == 1 {
					name = "concurrent_app"
				}
				results <- repository.CreateWithinQuota(t.Context(), integrationApplication(t, "auth-name-race", name), 100)
			}(index)
		}
		close(start)
		group.Wait()
		close(results)

		successes, nameConflicts := classifyConcurrentResults(t, results, port.ErrApplicationNameAlreadyExists)
		if successes != 1 || nameConflicts != contenders-1 {
			t.Fatalf("successes = %d, name conflicts = %d; want 1 and %d", successes, nameConflicts, contenders-1)
		}
		assertCollectionCount(t, database, applicationsCollectionName, 1)
		assertQuota(t, database, "auth-name-race", 100, 1, 1)
	})

	t.Run("BR-APP-005 BR-APP-006 concurrent creation cannot exceed quota", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		const contenders = 20
		const limit = 10
		start := make(chan struct{})
		results := make(chan error, contenders)
		var group sync.WaitGroup

		for index := 0; index < contenders; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				<-start
				results <- repository.CreateWithinQuota(
					t.Context(),
					integrationApplication(t, "auth-quota-race", fmt.Sprintf("race_%02d", index)),
					limit,
				)
			}(index)
		}
		close(start)
		group.Wait()
		close(results)

		successes, quotaConflicts := classifyConcurrentResults(t, results, port.ErrApplicationQuotaExceeded)
		if successes != limit || quotaConflicts != contenders-limit {
			t.Fatalf("successes = %d, quota conflicts = %d; want %d and %d", successes, quotaConflicts, limit, contenders-limit)
		}
		assertCollectionCount(t, database, applicationsCollectionName, limit)
		assertQuota(t, database, "auth-quota-race", limit, limit, limit)
	})

	t.Run("BR-APP-006 failed application insert does not consume quota", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		repository := NewApplicationRepository(database)
		collidingIDApplication := integrationApplication(t, "another-admin", "existing")
		document, err := applicationToDocument(collidingIDApplication)
		if err != nil {
			t.Fatalf("map seed application: %v", err)
		}
		if _, err := database.Collection(applicationsCollectionName).InsertOne(t.Context(), document); err != nil {
			t.Fatalf("seed colliding application: %v", err)
		}

		quota := applicationCreationQuotaDocument{
			AdminID: "auth-insert-failure", Limit: 10, UsedCount: 0, Revision: 0,
			UpdatedAt: time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC),
		}
		if _, err := database.Collection(applicationCreationQuotasCollectionName).InsertOne(t.Context(), quota); err != nil {
			t.Fatalf("seed quota: %v", err)
		}

		failedApplication := integrationApplicationWithID(t, collidingIDApplication.ID(), "auth-insert-failure", "new_name")
		err = repository.CreateWithinQuota(t.Context(), failedApplication, 10)
		if err == nil || errors.Is(err, port.ErrApplicationNameAlreadyExists) || errors.Is(err, port.ErrApplicationQuotaExceeded) {
			t.Fatalf("error = %v, want technical duplicate-ID failure", err)
		}
		assertQuota(t, database, "auth-insert-failure", 10, 0, 0)
	})

	t.Run("validators reject missing and illegal fields", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		applications := database.Collection(applicationsCollectionName)
		quotas := database.Collection(applicationCreationQuotasCollectionName)

		_, err := applications.InsertOne(t.Context(), bson.D{{Key: "id", Value: "missing-fields"}})
		assertDocumentValidationFailure(t, err)

		_, err = applications.InsertOne(t.Context(), applicationDocument{
			ID:                          nextIntegrationApplicationID(t).String(),
			Name:                        "Valid_Name",
			NameKey:                     "wrong_key",
			AdminID:                     "auth-validator",
			CreatedAt:                   time.Now().UTC(),
			NextVersionSequence:         1,
			NextProfileRevisionSequence: 1,
		})
		assertDocumentValidationFailure(t, err)

		_, err = quotas.InsertOne(t.Context(), applicationCreationQuotaDocument{
			AdminID: "auth-validator", Limit: 1, UsedCount: 2, Revision: 0, UpdatedAt: time.Now().UTC(),
		})
		assertDocumentValidationFailure(t, err)
	})

	t.Run("BR-APP-001 coordination revision fence is required and typed", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		applications := database.Collection(applicationsCollectionName)
		valid, err := applicationToDocument(integrationApplication(t, "auth-fence", "coordination_fence"))
		if err != nil {
			t.Fatalf("map application document: %v", err)
		}

		// The 0003 schema requires the adapter-only fence field, so a document
		// shaped exactly like a 0001 row is no longer writable.
		_, err = applications.InsertOne(t.Context(), legacyApplicationBSON(valid))
		assertDocumentValidationFailure(t, err)

		negative := valid
		negative.CoordinationRevision = -1
		_, err = applications.InsertOne(t.Context(), negative)
		assertDocumentValidationFailure(t, err)
	})
}

// legacyApplicationBSON renders an Application exactly as the 0001 schema
// allowed, without the adapter-only coordinationRevision field.
func legacyApplicationBSON(document applicationDocument) bson.D {
	return bson.D{
		{Key: "id", Value: document.ID},
		{Key: "name", Value: document.Name},
		{Key: "nameKey", Value: document.NameKey},
		{Key: "adminId", Value: document.AdminID},
		{Key: "createdAt", Value: document.CreatedAt},
		{Key: "nextVersionSequence", Value: document.NextVersionSequence},
		{Key: "nextProfileRevisionSequence", Value: document.NextProfileRevisionSequence},
	}
}

func TestMigratorIntegration_IsIdempotentAndCreatesNamedSchema(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	migrator := NewMigrator(database)
	if err := migrator.Migrate(t.Context()); err != nil {
		t.Fatalf("first migration: %v", err)
	}
	if err := migrator.Migrate(t.Context()); err != nil {
		t.Fatalf("second migration: %v", err)
	}

	assertCollectionCount(t, database, migrationLedgerCollectionName, 9)
	assertIndexNames(t, database.Collection(applicationsCollectionName), []string{
		"_id_", applicationIDUniqueIndexName, applicationAdminNameUniqueIndexName,
	})
	assertIndexNames(t, database.Collection(applicationTesterJoinLinksCollectionName), []string{
		"_id_", testerJoinLinkIDUniqueIndexName, testerJoinLinkTokenHashUniqueIndexName, testerJoinLinkActiveUniqueIndexName, testerJoinLinkAuditIndexName,
	})
	assertIndexNames(t, database.Collection(applicationPublicationsCollectionName), []string{
		"_id_", publicationIDUniqueIndexName, publicationPartitionUniqueIndexName,
	})
	assertIndexNames(t, database.Collection(applicationPublicationHistoryCollectionName), []string{
		"_id_", publicationHistoryIDUniqueIndexName, publicationHistoryRevisionUniqueIndexName, publicationHistoryAuditIndexName,
	})
	assertIndexNames(t, database.Collection(applicationCreationQuotasCollectionName), []string{
		"_id_", applicationQuotaAdminIDUniqueIndexName,
	})
	assertIndexNames(t, database.Collection(applicationVersionsCollectionName), []string{
		"_id_", applicationVersionIDUniqueIndexName, applicationVersionSequenceUniqueIndexName,
		applicationVersionLabelUniqueIndexName,
	})
	assertIndexNames(t, database.Collection(applicationReviewsCollectionName), []string{
		"_id_", applicationReviewIDUniqueIndexName, applicationReviewAttemptUniqueIndexName,
		applicationReviewSourceUniqueIndexName, applicationReviewPendingUniqueIndexName,
		applicationReviewQueueIndexName,
	})
}

func TestMigratorIntegration_UpgradesExisting0001DatabaseToLatest(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	migrator := NewMigrator(database)
	if err := migrator.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if err := migrator.applyMigration(t.Context(), applicationCreationMigrationID, migrator.applyApplicationCreationMigration); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	assertCollectionCount(t, database, migrationLedgerCollectionName, 1)
	if names, err := database.ListCollectionNames(t.Context(), bson.D{{Key: "name", Value: applicationVersionsCollectionName}}); err != nil {
		t.Fatalf("list collections before 0002: %v", err)
	} else if len(names) != 0 {
		t.Fatalf("application_versions exists before 0002: %v", names)
	}

	if err := migrator.Migrate(t.Context()); err != nil {
		t.Fatalf("upgrade to latest: %v", err)
	}
	assertCollectionCount(t, database, migrationLedgerCollectionName, 9)
	assertIndexNames(t, database.Collection(applicationVersionsCollectionName), []string{
		"_id_", applicationVersionIDUniqueIndexName, applicationVersionSequenceUniqueIndexName,
		applicationVersionLabelUniqueIndexName,
	})
	assertIndexNames(t, database.Collection(applicationReviewsCollectionName), []string{
		"_id_", applicationReviewIDUniqueIndexName, applicationReviewAttemptUniqueIndexName,
		applicationReviewSourceUniqueIndexName, applicationReviewPendingUniqueIndexName,
		applicationReviewQueueIndexName,
	})
	assertIndexNames(t, database.Collection(versionReviewPoliciesCollectionName), []string{
		"_id_", versionReviewPolicyVersionUniqueIndexName,
	})
	var policy versionReviewPolicyDocument
	if err := database.Collection(versionReviewPoliciesCollectionName).FindOne(
		t.Context(), bson.D{{Key: "version", Value: "app-version-review-v1"}},
	).Decode(&policy); err != nil {
		t.Fatalf("read seeded version review policy: %v", err)
	}
	if !equalVersionReviewPolicyDocuments(policy, versionReviewPolicyDocument{
		Version: "app-version-review-v1",
		RequiredChecks: []string{
			"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed",
		},
		Status: "ACTIVE",
	}) {
		t.Fatalf("seeded version review policy = %#v", policy)
	}
}

func TestMigratorIntegration_LedgerIDsAreUniqueOrderedAndExact(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)

	cursor, err := database.Collection(migrationLedgerCollectionName).Find(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	defer cursor.Close(context.Background())
	var records []migrationRecord
	if err := cursor.All(t.Context(), &records); err != nil {
		t.Fatalf("decode migration ledger: %v", err)
	}
	got := make([]string, 0, len(records))
	for _, record := range records {
		got = append(got, record.ID)
	}
	sort.Strings(got)
	want := []string{
		applicationCreationMigrationID,
		applicationVersionMigrationID,
		applicationVersionDraftUpdateMigrationID,
		applicationReviewMigrationID,
		applicationReviewDecisionMigrationID,
		applicationReviewRestorationMigrationID,
		versionReviewPolicyMigrationID,
		applicationPublicationMigrationID,
		applicationTesterJoinLinkMigrationID,
	}
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("migration ledger IDs = %v, want %v", got, want)
	}
}

// TestMigratorIntegration_FreshMatchesSequentialUpgrade proves that applying
// the chain one migration at a time to an existing database produces exactly
// the same collection validators as one fresh Migrate.
func TestMigratorIntegration_FreshMatchesSequentialUpgrade(t *testing.T) {
	client := integrationClient(t)
	freshDatabase := integrationDatabase(t, client)
	if err := NewMigrator(freshDatabase).Migrate(t.Context()); err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}

	sequentialDatabase := integrationDatabase(t, client)
	sequential := NewMigrator(sequentialDatabase)
	if err := sequential.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatalf("create sequential migration ledger: %v", err)
	}
	for _, migration := range []struct {
		id    string
		apply func(context.Context) error
	}{
		{id: applicationCreationMigrationID, apply: sequential.applyApplicationCreationMigration},
		{id: applicationVersionMigrationID, apply: sequential.applyApplicationVersionMigration},
		{id: applicationVersionDraftUpdateMigrationID, apply: sequential.applyApplicationVersionDraftUpdateMigration},
		{id: applicationReviewMigrationID, apply: sequential.applyApplicationReviewMigration},
		{id: applicationReviewDecisionMigrationID, apply: sequential.applyApplicationReviewDecisionMigration},
		{id: applicationReviewRestorationMigrationID, apply: sequential.applyApplicationReviewRestorationMigration},
		{id: versionReviewPolicyMigrationID, apply: sequential.applyVersionReviewPolicyMigration},
		{id: applicationPublicationMigrationID, apply: sequential.applyApplicationPublicationMigration},
		{id: applicationTesterJoinLinkMigrationID, apply: sequential.applyApplicationTesterJoinLinkMigration},
	} {
		if err := sequential.applyMigration(t.Context(), migration.id, migration.apply); err != nil {
			t.Fatalf("apply %s sequentially: %v", migration.id, err)
		}
	}

	for _, collectionName := range []string{
		applicationsCollectionName,
		applicationCreationQuotasCollectionName,
		applicationVersionsCollectionName,
		applicationReviewsCollectionName,
		versionReviewPoliciesCollectionName,
		applicationPublicationsCollectionName,
		applicationPublicationHistoryCollectionName,
		applicationTesterJoinLinksCollectionName,
	} {
		freshValidator := collectionValidator(t, freshDatabase, collectionName)
		sequentialValidator := collectionValidator(t, sequentialDatabase, collectionName)
		if !bytes.Equal(freshValidator, sequentialValidator) {
			t.Fatalf(
				"validator for %s differs between fresh and sequential upgrade:\nfresh=%s\nsequential=%s",
				collectionName, freshValidator, sequentialValidator,
			)
		}
	}
}

func collectionValidator(t *testing.T, database *drivermongo.Database, collectionName string) bson.Raw {
	t.Helper()
	var result struct {
		Cursor struct {
			FirstBatch []struct {
				Options bson.Raw `bson:"options"`
			} `bson:"firstBatch"`
		} `bson:"cursor"`
	}
	err := database.RunCommand(t.Context(), bson.D{
		{Key: "listCollections", Value: 1},
		{Key: "filter", Value: bson.D{{Key: "name", Value: collectionName}}},
	}).Decode(&result)
	if err != nil {
		t.Fatalf("list collection %s: %v", collectionName, err)
	}
	if len(result.Cursor.FirstBatch) != 1 {
		t.Fatalf("list collection %s returned %d entries, want 1", collectionName, len(result.Cursor.FirstBatch))
	}
	validator, ok := result.Cursor.FirstBatch[0].Options.Lookup("validator").DocumentOK()
	if !ok {
		t.Fatalf("collection %s has no validator", collectionName)
	}
	return validator
}

func TestIntegrationTopologySupportsTransactions(t *testing.T) {
	client := integrationClient(t)
	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(t.Context(), bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		t.Fatalf("run hello: %v", err)
	}
	if hello.SetName == "" {
		t.Fatal("integration MongoDB is not a replica set")
	}
}

func TestApplicationRepositoryConstructionDoesNotMutateSchema(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	_ = NewApplicationRepository(database)
	names, err := database.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("repository construction created collections: %v", names)
	}
}

func integrationClient(t *testing.T) *drivermongo.Client {
	t.Helper()
	uri := os.Getenv(mongoIntegrationURIEnvironment)
	if uri == "" {
		t.Skipf("set %s or run scripts/test-mongo-integration.sh", mongoIntegrationURIEnvironment)
	}
	client, err := drivermongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect MongoDB: %v", err)
	}
	if err := client.Ping(t.Context(), readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		t.Fatalf("ping MongoDB: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Disconnect(context.Background()); err != nil {
			t.Errorf("disconnect MongoDB: %v", err)
		}
	})
	return client
}

func integrationDatabase(t *testing.T, client *drivermongo.Client) *drivermongo.Database {
	t.Helper()
	database := client.Database("iwut_app_center_test_" + nextIntegrationApplicationID(t).String()[24:])
	t.Cleanup(func() {
		if err := database.Drop(context.Background()); err != nil {
			t.Errorf("drop integration database %s: %v", database.Name(), err)
		}
	})
	return database
}

func migratedIntegrationDatabase(t *testing.T, client *drivermongo.Client) *drivermongo.Database {
	t.Helper()
	database := integrationDatabase(t, client)
	if err := NewMigrator(database).Migrate(t.Context()); err != nil {
		t.Fatalf("migrate integration database: %v", err)
	}
	return database
}

func integrationApplication(t *testing.T, adminValue string, nameValue string) *domain.Application {
	t.Helper()
	return integrationApplicationWithID(t, nextIntegrationApplicationID(t), adminValue, nameValue)
}

func integrationApplicationWithID(
	t *testing.T,
	id domain.ApplicationID,
	adminValue string,
	nameValue string,
) *domain.Application {
	t.Helper()
	name, err := domain.NewApplicationName(nameValue)
	if err != nil {
		t.Fatalf("create application name: %v", err)
	}
	adminID, err := domain.NewAuthID(adminValue)
	if err != nil {
		t.Fatalf("create admin ID: %v", err)
	}
	application, err := domain.NewApplication(
		id,
		name,
		adminID,
		time.Date(2026, time.September, 19, 0, 0, int(applicationIDCounter.Load()%60), 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	return application
}

func nextIntegrationApplicationID(t *testing.T) domain.ApplicationID {
	t.Helper()
	value := applicationIDCounter.Add(1)
	id, err := domain.ParseApplicationID(fmt.Sprintf("01890f47-0000-7000-8000-%012x", value))
	if err != nil {
		t.Fatalf("create test application ID: %v", err)
	}
	return id
}

func classifyConcurrentResults(t *testing.T, results <-chan error, expectedConflict error) (int, int) {
	t.Helper()
	var successes int
	var conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, expectedConflict):
			conflicts++
		default:
			t.Errorf("unexpected concurrent result: %v", err)
		}
	}
	return successes, conflicts
}

func assertCollectionCount(t *testing.T, database *drivermongo.Database, collectionName string, want int) {
	t.Helper()
	count, err := database.Collection(collectionName).CountDocuments(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("count %s: %v", collectionName, err)
	}
	if count != int64(want) {
		t.Fatalf("%s count = %d, want %d", collectionName, count, want)
	}
}

func assertQuota(t *testing.T, database *drivermongo.Database, adminID string, limit int32, usedCount int32, revision int64) {
	t.Helper()
	var document applicationCreationQuotaDocument
	err := database.Collection(applicationCreationQuotasCollectionName).
		FindOne(t.Context(), bson.D{{Key: "adminId", Value: adminID}}).
		Decode(&document)
	if err != nil {
		t.Fatalf("read quota for %s: %v", adminID, err)
	}
	if document.Limit != limit || document.UsedCount != usedCount || document.Revision != revision {
		t.Fatalf(
			"quota for %s = (limit=%d, used=%d, revision=%d), want (%d, %d, %d)",
			adminID, document.Limit, document.UsedCount, document.Revision, limit, usedCount, revision,
		)
	}
}

func assertDocumentValidationFailure(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("write succeeded, want document validation failure")
	}
	var serverError drivermongo.ServerError
	if !errors.As(err, &serverError) || !serverError.HasErrorCode(121) {
		t.Fatalf("error = %v, want MongoDB document validation error 121", err)
	}
}

func assertIndexNames(t *testing.T, collection *drivermongo.Collection, want []string) {
	t.Helper()
	cursor, err := collection.Indexes().List(t.Context())
	if err != nil {
		t.Fatalf("list indexes for %s: %v", collection.Name(), err)
	}
	defer func() {
		if err := cursor.Close(context.Background()); err != nil {
			t.Errorf("close index cursor: %v", err)
		}
	}()
	var indexes []struct {
		Name string `bson:"name"`
	}
	if err := cursor.All(t.Context(), &indexes); err != nil {
		t.Fatalf("decode indexes for %s: %v", collection.Name(), err)
	}
	got := make([]string, 0, len(indexes))
	for _, index := range indexes {
		got = append(got, index.Name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("indexes for %s = %v, want %v", collection.Name(), got, want)
	}
}
