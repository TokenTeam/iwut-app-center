package mongo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	applicationdomain "iwut-app-center/internal/application/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var applicationReviewIDCounter atomic.Uint64

func TestApplicationReviewRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-REV-003 BR-REV-004 BR-REV-005 BR-REV-009 submits immutable snapshot and version transition atomically", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, version := createReviewTestVersion(t, database, "auth-review", "review-success", "v1")
		repository := NewApplicationReviewRepository(database)
		candidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-review", 1)
		// MongoDB stores BSON datetimes with millisecond precision, so the audit
		// instant under test must be millisecond-aligned.
		submittedAt := time.Date(2026, time.September, 20, 10, 11, 12, 321_000_000, time.UTC)

		result, err := repository.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), "auth-review", 41, "public-https.v1", submittedAt)
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		if result.Review().Attempt() != 1 || result.Review().SourceVersionRevision() != 1 ||
			result.Review().Status() != reviewdomain.ReviewStatusPending || result.Review().HasDecision() || result.Review().HasDraftRestoration() {
			t.Fatalf("unexpected review result: %#v", result.Review())
		}
		if result.Version().ReviewStatus() != "SUBMITTED" || result.Version().Revision() != 2 ||
			result.Version().UpdatedBy() != "auth-review" || !result.Version().UpdatedAt().Equal(submittedAt) {
			t.Fatalf("unexpected version result: %#v", result.Version())
		}

		storedVersion := readVersionDocument(t, database, version.ID().String())
		if storedVersion.ReviewStatus != "SUBMITTED" || storedVersion.Revision != 2 || storedVersion.UpdatedBy != "auth-review" || !storedVersion.UpdatedAt.Equal(submittedAt) {
			t.Fatalf("stored version lifecycle/audit = %#v", storedVersion)
		}
		var storedReview applicationReviewDocument
		if err := database.Collection(applicationReviewsCollectionName).FindOne(t.Context(), bson.D{{Key: "versionId", Value: version.ID().String()}}).Decode(&storedReview); err != nil {
			t.Fatalf("read stored review: %v", err)
		}
		if storedReview.Attempt != 1 || storedReview.SourceVersionRevision != 1 || storedReview.Status != "PENDING" ||
			storedReview.Decision != nil || storedReview.DraftRestoration != nil || storedReview.ScopeCatalogRevision != 41 ||
			storedReview.PreflightPolicyVersion != "public-https.v1" || storedReview.SubmittedBy != "auth-review" ||
			!storedReview.SubmittedAt.Equal(submittedAt) {
			t.Fatalf("stored review fields = %#v", storedReview)
		}
		assertSnapshotMatchesVersionDocument(t, storedReview.Snapshot, integrationApplicationVersionDocumentForRead(t, version))
	})

	t.Run("BR-REV-009 review insert failure rolls back version transition", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, firstVersion := createReviewTestVersion(t, database, "auth-rollback-review", "rollback-one", "v1")
		_, secondVersion := createReviewTestVersion(t, database, "auth-rollback-review", "rollback-two", "v2")
		repository := NewApplicationReviewRepository(database)
		collidingReviewID := nextIntegrationApplicationReviewID(t)
		first := loadReviewCandidate(t, repository, firstVersion.ApplicationID(), firstVersion.ID(), "auth-rollback-review", 1)
		if _, err := repository.Submit(t.Context(), first, collidingReviewID, "auth-rollback-review", 1, "v1", time.Now().UTC()); err != nil {
			t.Fatalf("seed review: %v", err)
		}
		second := loadReviewCandidate(t, repository, secondVersion.ApplicationID(), secondVersion.ID(), "auth-rollback-review", 1)
		if _, err := repository.Submit(t.Context(), second, collidingReviewID, "auth-rollback-review", 1, "v1", time.Now().UTC()); err == nil || !drivermongo.IsDuplicateKeyError(err) {
			t.Fatalf("duplicate review ID error = %v, want retained duplicate-key cause", err)
		}
		stored := readVersionDocument(t, database, secondVersion.ID().String())
		if stored.ReviewStatus != "DRAFT" || stored.Revision != 1 || stored.UpdatedBy != stored.CreatedBy || !stored.UpdatedAt.Equal(stored.CreatedAt) {
			t.Fatalf("failed submission partially changed version: %#v", stored)
		}
		assertCollectionCount(t, database, applicationReviewsCollectionName, 1)
	})

	t.Run("BR-REV-001 BR-REV-009 coordination revision advances on success and rolls back on failure", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application, version := createReviewTestVersion(t, database, "auth-review-fence", "review-fence", "v1")
		repository := NewApplicationReviewRepository(database)
		candidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-review-fence", 1)
		assertApplicationCoordinationRevision(t, database, application.ID(), 0)

		result, err := repository.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), "auth-review-fence", 1, "v1", time.Now().UTC())
		if err != nil {
			t.Fatalf("submit review: %v", err)
		}
		if result.Version().ReviewStatus() != "SUBMITTED" {
			t.Fatalf("submitted version = %#v, want SUBMITTED", result.Version())
		}
		assertApplicationCoordinationRevision(t, database, application.ID(), 1)

		// A repeat against the now-SUBMITTED Version must fail without leaving
		// the authorization fence increment behind.
		if _, err := repository.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), "auth-review-fence", 1, "v1", time.Now().UTC()); !errors.Is(err, reviewport.ErrApplicationVersionNotDraft) {
			t.Fatalf("repeat submission error = %v, want not draft", err)
		}
		assertApplicationCoordinationRevision(t, database, application.ID(), 1)
	})

	t.Run("BR-REV-002 BR-REV-003 BR-REV-005 BR-REV-009 concurrent same-revision submissions allow at most one", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, version := createReviewTestVersion(t, database, "auth-review-race", "review-race", "v1")
		repository := NewApplicationReviewRepository(database)
		candidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-review-race", 1)
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				_, err := repository.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), "auth-review-race", 1, "v1", time.Now().UTC())
				results <- err
			}()
		}
		close(start)
		group.Wait()
		close(results)
		var successes, conflicts int
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, reviewport.ErrApplicationVersionNotDraft), errors.Is(err, reviewport.ErrApplicationVersionRevisionConflict):
				conflicts++
			default:
				t.Errorf("unexpected concurrent submit error: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent results = success:%d conflict:%d, want 1/1", successes, conflicts)
		}
		assertCollectionCount(t, database, applicationReviewsCollectionName, 1)
		stored := readVersionDocument(t, database, version.ID().String())
		if stored.ReviewStatus != "SUBMITTED" || stored.Revision != 2 {
			t.Fatalf("stored version = status:%s revision:%d", stored.ReviewStatus, stored.Revision)
		}
	})

	t.Run("BR-REV-003 BR-REV-004 BR-REV-009 concurrent draft update cannot submit stale content", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, version := createReviewTestVersion(t, database, "auth-draft-race", "draft-race", "v1")
		repository := NewApplicationReviewRepository(database)
		candidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-draft-race", 1)

		updateSession, err := client.StartSession()
		if err != nil {
			t.Fatalf("start draft update session: %v", err)
		}
		defer updateSession.EndSession(context.Background())
		if err := updateSession.StartTransaction(); err != nil {
			t.Fatalf("start draft update transaction: %v", err)
		}
		updatedAt := time.Now().UTC()
		updateResult, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
			drivermongo.NewSessionContext(t.Context(), updateSession),
			bson.D{{Key: "versionId", Value: version.ID().String()}, {Key: "revision", Value: int64(1)}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "launchUrl", Value: "https://new.example.edu/app"}, {Key: "updatedBy", Value: "auth-draft-race"}, {Key: "updatedAt", Value: updatedAt}}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}},
		)
		if err != nil || updateResult.ModifiedCount != 1 {
			t.Fatalf("stage draft update: result=%#v error=%v", updateResult, err)
		}

		competingClient, updateStarted := monitoredMongoClient(t, database.Name(), "update")
		competingRepository := NewApplicationReviewRepository(competingClient.Database(database.Name()))
		raceContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		submitResult := make(chan error, 1)
		go func() {
			_, submitErr := competingRepository.Submit(raceContext, candidate, nextIntegrationApplicationReviewID(t), "auth-draft-race", 1, "v1", time.Now().UTC())
			submitResult <- submitErr
		}()
		awaitMongoCommand(t, raceContext, updateStarted, "review version update")
		if err := updateSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit draft update: %v", err)
		}
		select {
		case submitErr := <-submitResult:
			if !errors.Is(submitErr, reviewport.ErrApplicationVersionRevisionConflict) {
				t.Fatalf("stale submission error = %v, want revision conflict", submitErr)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for stale submission result: %v", raceContext.Err())
		}
		assertCollectionCount(t, database, applicationReviewsCollectionName, 0)
		stored := readVersionDocument(t, database, version.ID().String())
		if stored.Revision != 2 || stored.ReviewStatus != "DRAFT" || stored.LaunchURL != "https://new.example.edu/app" {
			t.Fatalf("stored draft after race = %#v", stored)
		}
	})

	t.Run("BR-REV-001 BR-REV-009 concurrent administrator transfer rejects old administrator", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, version := createReviewTestVersion(t, database, "auth-old-review", "admin-race", "v1")
		repository := NewApplicationReviewRepository(database)
		candidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-old-review", 1)

		transferSession, err := client.StartSession()
		if err != nil {
			t.Fatalf("start transfer session: %v", err)
		}
		defer transferSession.EndSession(context.Background())
		if err := transferSession.StartTransaction(); err != nil {
			t.Fatalf("start transfer transaction: %v", err)
		}
		transferResult, err := database.Collection(applicationsCollectionName).UpdateOne(
			drivermongo.NewSessionContext(t.Context(), transferSession),
			bson.D{{Key: "id", Value: version.ApplicationID().String()}, {Key: "adminId", Value: "auth-old-review"}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-review"}}}},
		)
		if err != nil || transferResult.ModifiedCount != 1 {
			t.Fatalf("stage administrator transfer: result=%#v error=%v", transferResult, err)
		}

		competingClient, lockStarted := monitoredMongoClient(t, database.Name(), "findAndModify")
		competingRepository := NewApplicationReviewRepository(competingClient.Database(database.Name()))
		raceContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		submitResult := make(chan error, 1)
		go func() {
			_, submitErr := competingRepository.Submit(raceContext, candidate, nextIntegrationApplicationReviewID(t), "auth-old-review", 1, "v1", time.Now().UTC())
			submitResult <- submitErr
		}()
		awaitMongoCommand(t, raceContext, lockStarted, "administrator lock")
		if err := transferSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit administrator transfer: %v", err)
		}
		select {
		case submitErr := <-submitResult:
			if !errors.Is(submitErr, reviewport.ErrApplicationAdminRequired) {
				t.Fatalf("old administrator submission error = %v, want admin required", submitErr)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for old administrator result: %v", raceContext.Err())
		}
		assertCollectionCount(t, database, applicationReviewsCollectionName, 0)
		stored := readVersionDocument(t, database, version.ID().String())
		if stored.ReviewStatus != "DRAFT" || stored.Revision != 1 {
			t.Fatalf("transfer race changed version: %#v", stored)
		}
	})

	t.Run("BR-REV-005 second legal submission increments attempt and preserves first snapshot", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, version := createReviewTestVersion(t, database, "auth-attempt", "attempts", "v1")
		repository := NewApplicationReviewRepository(database)
		firstCandidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-attempt", 1)
		firstResult, err := repository.Submit(t.Context(), firstCandidate, nextIntegrationApplicationReviewID(t), "auth-attempt", 1, "v1", time.Now().UTC())
		if err != nil {
			t.Fatalf("first submit: %v", err)
		}
		firstSnapshot := firstResult.Review().Snapshot()

		_, err = database.Collection(applicationReviewsCollectionName).UpdateOne(
			t.Context(), bson.D{{Key: "reviewId", Value: firstResult.Review().ReviewID().String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REJECTED"}}}},
			options.UpdateOne().SetBypassDocumentValidation(true),
		)
		if err != nil {
			t.Fatalf("simulate prior rejected decision: %v", err)
		}
		_, err = database.Collection(applicationVersionsCollectionName).UpdateOne(
			t.Context(), bson.D{{Key: "versionId", Value: version.ID().String()}, {Key: "revision", Value: int64(2)}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: "DRAFT"}, {Key: "versionLabel", Value: "v2"}, {Key: "updatedAt", Value: time.Now().UTC()}}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}},
		)
		if err != nil {
			t.Fatalf("simulate legal draft restoration/edit: %v", err)
		}
		secondCandidate := loadReviewCandidate(t, repository, version.ApplicationID(), version.ID(), "auth-attempt", 3)
		secondResult, err := repository.Submit(t.Context(), secondCandidate, nextIntegrationApplicationReviewID(t), "auth-attempt", 2, "v2", time.Now().UTC())
		if err != nil {
			t.Fatalf("second submit: %v", err)
		}
		if secondResult.Review().Attempt() != 2 || secondResult.Review().SourceVersionRevision() != 3 || secondResult.Review().Snapshot().VersionLabel() != "v2" {
			t.Fatalf("second review = %#v", secondResult.Review())
		}
		var storedFirst applicationReviewDocument
		if err := database.Collection(applicationReviewsCollectionName).FindOne(t.Context(), bson.D{{Key: "reviewId", Value: firstResult.Review().ReviewID().String()}}).Decode(&storedFirst); err != nil {
			t.Fatalf("read first review: %v", err)
		}
		if storedFirst.Snapshot.VersionLabel != firstSnapshot.VersionLabel() || storedFirst.Snapshot.LaunchURL != string(firstSnapshot.LaunchURL()) {
			t.Fatalf("first snapshot was overwritten: %#v", storedFirst.Snapshot)
		}
		assertCollectionCount(t, database, applicationReviewsCollectionName, 2)
	})

	t.Run("BR-REV-009 application and version mismatch is hidden as not found", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		_, firstVersion := createReviewTestVersion(t, database, "auth-mismatch", "mismatch-one", "v1")
		secondApplication, _ := createReviewTestVersion(t, database, "auth-mismatch", "mismatch-two", "v2")
		repository := NewApplicationReviewRepository(database)
		candidate, err := repository.LoadSubmissionCandidate(t.Context(), secondApplication.ID(), reviewdomain.ApplicationVersionID(firstVersion.ID()), "auth-mismatch", 1)
		if candidate != nil || !errors.Is(err, reviewport.ErrApplicationVersionNotFound) {
			t.Fatalf("mismatched LoadSubmissionCandidate() = (%v, %v), want nil not found", candidate, err)
		}
	})
}

func TestApplicationReviewMigrationIntegration_ValidatorsAndUniqueIndexes(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	application, version := createReviewTestVersion(t, database, "auth-review-schema", "review-schema", "v1")
	repository := NewApplicationReviewRepository(database)
	candidate := loadReviewCandidate(t, repository, application.ID(), version.ID(), "auth-review-schema", 1)
	attempt, _ := reviewdomain.NewReviewAttempt(1)
	review, _ := reviewdomain.NewPendingApplicationReview(candidate, nextIntegrationApplicationReviewID(t), attempt, 1, "v1", "auth-review-schema", time.Now().UTC())
	base, err := applicationReviewToDocument(review)
	if err != nil {
		t.Fatalf("map review: %v", err)
	}
	reviews := database.Collection(applicationReviewsCollectionName)

	// 0005 accepts one decision object, but an APPROVED decision must carry the
	// approval validation produced by the external re-checks.
	invalid := base
	invalid.ReviewID = nextIntegrationApplicationReviewID(t).String()
	invalid.VersionID = nextIntegrationApplicationVersionID(t).String()
	invalid.Attempt = 3
	invalid.SourceVersionRevision = 3
	invalid.Status = "APPROVED"
	invalid.Decision = &applicationReviewDecisionDocument{
		Outcome:             "APPROVED",
		ReviewPolicyVersion: "review.v1",
		ConfirmedCheckIDs:   []string{"content-reviewed"},
		DecidedBy:           "auth-reviewer",
		DecidedAt:           time.Date(2026, time.September, 20, 15, 0, 0, 0, time.UTC),
	}
	if _, err := reviews.InsertOne(t.Context(), invalid); err == nil {
		t.Fatal("validator accepted an APPROVED decision without approval validation")
	} else {
		assertDocumentValidationFailure(t, err)
	}
	if _, err := reviews.InsertOne(t.Context(), base); err != nil {
		t.Fatalf("insert valid base review: %v", err)
	}

	rejectedReason := "应用用途说明不足。"
	validDecision := base
	validDecision.ReviewID = nextIntegrationApplicationReviewID(t).String()
	validDecision.VersionID = nextIntegrationApplicationVersionID(t).String()
	validDecision.Attempt = 2
	validDecision.SourceVersionRevision = 2
	validDecision.Status = "REJECTED"
	validDecision.Decision = &applicationReviewDecisionDocument{
		Outcome:             "REJECTED",
		ReviewPolicyVersion: "review.v1",
		ConfirmedCheckIDs:   []string{},
		Reason:              &rejectedReason,
		DecidedBy:           "auth-reviewer",
		DecidedAt:           time.Date(2026, time.September, 20, 15, 0, 0, 0, time.UTC),
	}
	if _, err := reviews.InsertOne(t.Context(), validDecision); err != nil {
		t.Fatalf("insert valid rejected decision: %v", err)
	}

	tests := []struct {
		name      string
		indexName string
		mutate    func(*applicationReviewDocument)
	}{
		{name: "review ID", indexName: applicationReviewIDUniqueIndexName, mutate: func(document *applicationReviewDocument) {
			document.VersionID = nextIntegrationApplicationVersionID(t).String()
			document.Attempt = 2
			document.SourceVersionRevision = 2
		}},
		{name: "version attempt", indexName: applicationReviewAttemptUniqueIndexName, mutate: func(document *applicationReviewDocument) {
			document.ReviewID = nextIntegrationApplicationReviewID(t).String()
			document.SourceVersionRevision = 2
			document.Status = "REJECTED"
		}},
		{name: "version source revision", indexName: applicationReviewSourceUniqueIndexName, mutate: func(document *applicationReviewDocument) {
			document.ReviewID = nextIntegrationApplicationReviewID(t).String()
			document.Attempt = 2
			document.Status = "REJECTED"
		}},
		{name: "single pending", indexName: applicationReviewPendingUniqueIndexName, mutate: func(document *applicationReviewDocument) {
			document.ReviewID = nextIntegrationApplicationReviewID(t).String()
			document.Attempt = 2
			document.SourceVersionRevision = 2
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := base
			test.mutate(&document)
			_, err := reviews.InsertOne(t.Context(), document, options.InsertOne().SetBypassDocumentValidation(true))
			if err == nil || !drivermongo.IsDuplicateKeyError(err) || !strings.Contains(err.Error(), test.indexName) {
				t.Fatalf("duplicate error = %v, want index %s", err, test.indexName)
			}
		})
	}
}

func TestApplicationReviewRepositoryConstructionDoesNotMutateSchema(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	_ = NewApplicationReviewRepository(database)
	names, err := database.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("repository construction created collections: %v", names)
	}
}

func createReviewTestVersion(t *testing.T, database *drivermongo.Database, adminID, applicationName, label string) (*applicationdomain.Application, *versiondomain.ApplicationVersion) {
	t.Helper()
	application := createVersionTestApplication(t, database, adminID, applicationName)
	version, err := NewApplicationVersionRepository(database).CreateDraft(t.Context(), shared.AuthID(adminID), integrationApplicationVersionDraft(t, application.ID(), adminID, label))
	if err != nil {
		t.Fatalf("create review-test version: %v", err)
	}
	return application, version
}

func loadReviewCandidate(t *testing.T, repository *ApplicationReviewRepository, applicationID shared.ApplicationID, versionIDValue interface{ String() string }, adminID string, revision int64) *reviewdomain.SubmissionCandidate {
	t.Helper()
	versionID := reviewdomain.ApplicationVersionID(versionIDValue.String())
	candidate, err := repository.LoadSubmissionCandidate(t.Context(), applicationID, versionID, shared.AuthID(adminID), revision)
	if err != nil {
		t.Fatalf("load submission candidate: %v", err)
	}
	return candidate
}

func nextIntegrationApplicationReviewID(t *testing.T) reviewdomain.ApplicationReviewID {
	t.Helper()
	value := applicationReviewIDCounter.Add(1)
	id := reviewdomain.ApplicationReviewID(fmt.Sprintf("01890f49-0000-7000-8000-%012x", value))
	if !id.IsValid() {
		t.Fatalf("generated invalid review ID %q", id)
	}
	return id
}

func readVersionDocument(t *testing.T, database *drivermongo.Database, versionID string) applicationVersionDocument {
	t.Helper()
	var document applicationVersionDocument
	if err := database.Collection(applicationVersionsCollectionName).FindOne(t.Context(), bson.D{{Key: "versionId", Value: versionID}}).Decode(&document); err != nil {
		t.Fatalf("read application version %s: %v", versionID, err)
	}
	return document
}

func integrationApplicationVersionDocumentForRead(t *testing.T, version *versiondomain.ApplicationVersion) applicationVersionDocument {
	t.Helper()
	return applicationVersionDocument{VersionID: version.ID().String(), ApplicationID: version.ApplicationID().String(), VersionLabel: "v1", LaunchURL: "https://example.edu/apps/v1", RPCApiMinVersion: 1, RPCApiMaxVersionExclusive: 3, RequiredCapabilities: []string{"camera.read.v1"}, RequiredScopes: []string{"profile.basic"}, OptionalScopes: []string{"schedule.read"}}
}

func assertSnapshotMatchesVersionDocument(t *testing.T, snapshot applicationVersionReviewSnapshotDocument, version applicationVersionDocument) {
	t.Helper()
	if snapshot.VersionLabel != version.VersionLabel || snapshot.LaunchURL != version.LaunchURL || snapshot.RPCApiMinVersion != version.RPCApiMinVersion ||
		snapshot.RPCApiMaxVersionExclusive != version.RPCApiMaxVersionExclusive || fmt.Sprint(snapshot.RequiredCapabilities) != fmt.Sprint(version.RequiredCapabilities) ||
		fmt.Sprint(snapshot.RequiredScopes) != fmt.Sprint(version.RequiredScopes) || fmt.Sprint(snapshot.OptionalScopes) != fmt.Sprint(version.OptionalScopes) {
		t.Fatalf("snapshot %#v does not match version %#v", snapshot, version)
	}
}

func monitoredMongoClient(t *testing.T, databaseName, commandName string) (*drivermongo.Client, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{}, 1)
	monitor := &event.CommandMonitor{Started: func(_ context.Context, event *event.CommandStartedEvent) {
		if event.DatabaseName == databaseName && event.CommandName == commandName {
			select {
			case started <- struct{}{}:
			default:
			}
		}
	}}
	client, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(monitor))
	if err != nil {
		t.Fatalf("connect monitored MongoDB client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Disconnect(context.Background()); err != nil {
			t.Errorf("disconnect monitored client: %v", err)
		}
	})
	return client, started
}

func awaitMongoCommand(t *testing.T, ctx context.Context, started <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatalf("wait for %s: %v", description, ctx.Err())
	}
}
