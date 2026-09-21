package mongo

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	versiondomain "iwut-app-center/internal/version/domain"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
)

func TestApplicationReviewRestorationRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-REV-023 BR-REV-025 BR-REV-026 BR-REV-027 restores rejected version and records immutable audit atomically", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-admin-restore", "restore-success")
		repository := NewApplicationReviewRestorationRepository(database)
		restoredAt := time.Date(2026, time.September, 21, 16, 0, 0, 0, time.UTC)

		result, err := repository.RestoreDraft(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3, restoredAt)
		if err != nil {
			t.Fatalf("RestoreDraft() error = %v", err)
		}
		if result.Review().Status() != reviewdomain.ReviewStatusRejected || result.Version().ReviewStatus() != "DRAFT" ||
			result.Version().Revision() != 4 || result.Version().UpdatedBy() != seed.adminID ||
			!result.Version().UpdatedAt().Equal(restoredAt) {
			t.Fatalf("unexpected restoration result: review=%#v version=%#v", result.Review(), result.Version())
		}
		storedReview := readReviewDocument(t, database, seed.reviewID)
		storedVersion := readVersionDocument(t, database, seed.versionID.String())
		if storedReview.Status != "REJECTED" || storedReview.Decision == nil || storedReview.Decision.Outcome != "REJECTED" ||
			storedReview.DraftRestoration == nil || storedReview.DraftRestoration.RestoredBy != seed.adminID.String() ||
			storedReview.DraftRestoration.ResultVersionRevision != 4 || !storedReview.DraftRestoration.RestoredAt.Equal(restoredAt) {
			t.Fatalf("stored review = %#v", storedReview)
		}
		if storedVersion.ReviewStatus != "DRAFT" || storedVersion.Revision != 4 || storedVersion.UpdatedBy != seed.adminID.String() ||
			!storedVersion.UpdatedAt.Equal(restoredAt) {
			t.Fatalf("stored version = %#v", storedVersion)
		}
	})

	t.Run("BR-REV-026 repeated restore keeps first audit and wins over not-rejected", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-admin-repeat", "restore-repeat")
		repository := NewApplicationReviewRestorationRepository(database)
		firstAt := time.Date(2026, time.September, 21, 16, 10, 0, 0, time.UTC)
		if _, err := repository.RestoreDraft(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3, firstAt); err != nil {
			t.Fatalf("first RestoreDraft() error = %v", err)
		}
		if result, err := repository.RestoreDraft(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 4, firstAt.Add(time.Hour)); result != nil ||
			!errors.Is(err, reviewport.ErrApplicationReviewAlreadyRestored) {
			t.Fatalf("second RestoreDraft() = (%v, %v), want already restored", result, err)
		}
		stored := readReviewDocument(t, database, seed.reviewID)
		if stored.DraftRestoration == nil || !stored.DraftRestoration.RestoredAt.Equal(firstAt) || stored.DraftRestoration.ResultVersionRevision != 4 {
			t.Fatalf("second restore changed first audit: %#v", stored.DraftRestoration)
		}
	})

	t.Run("BR-REV-026 BR-REV-027 concurrent restores allow exactly one commit", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-admin-race-restore", "restore-race")
		repository := NewApplicationReviewRestorationRepository(database)
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for index := 0; index < 2; index++ {
			group.Add(1)
			go func(offset int) {
				defer group.Done()
				<-start
				_, err := repository.RestoreDraft(
					t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3,
					time.Date(2026, time.September, 21, 16, 20+offset, 0, 0, time.UTC),
				)
				results <- err
			}(index)
		}
		close(start)
		group.Wait()
		close(results)
		var successes, conflicts int
		for err := range results {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, reviewport.ErrApplicationReviewAlreadyRestored), errors.Is(err, reviewport.ErrApplicationVersionNotRejected), errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent):
				conflicts++
			default:
				t.Errorf("unexpected concurrent restoration error: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent results = success:%d conflict:%d, want 1/1", successes, conflicts)
		}
		storedReview := readReviewDocument(t, database, seed.reviewID)
		storedVersion := readVersionDocument(t, database, seed.versionID.String())
		if storedReview.DraftRestoration == nil || storedVersion.ReviewStatus != "DRAFT" || storedVersion.Revision != 4 {
			t.Fatalf("concurrent restore left partial state: review=%#v version=%#v", storedReview, storedVersion)
		}
	})

	t.Run("BR-REV-027 explicit transaction abort rolls back review and version together", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-admin-abort-restore", "restore-abort")
		repository := NewApplicationReviewRestorationRepository(database)
		session, err := client.StartSession()
		if err != nil {
			t.Fatalf("start restoration session: %v", err)
		}
		defer session.EndSession(context.Background())
		if err := session.StartTransaction(); err != nil {
			t.Fatalf("start restoration transaction: %v", err)
		}
		sessionContext := drivermongo.NewSessionContext(t.Context(), session)
		result, err := repository.restoreDraftTransaction(
			sessionContext, seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3,
			time.Date(2026, time.September, 21, 16, 40, 0, 0, time.UTC),
		)
		if err != nil || result == nil {
			t.Fatalf("staged restoration = (%v, %v)", result, err)
		}
		if err := session.AbortTransaction(t.Context()); err != nil {
			t.Fatalf("abort restoration transaction: %v", err)
		}
		assertRestorationDidNotPersist(t, database, seed)
	})

	t.Run("BR-REV-021 checks current administrator", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-old-admin-restore", "restore-admin")
		if _, err := database.Collection(applicationsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "id", Value: seed.applicationID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-admin"}}}},
		); err != nil {
			t.Fatalf("transfer administrator: %v", err)
		}
		result, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(
			t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3, time.Now(),
		)
		if result != nil || !errors.Is(err, reviewport.ErrApplicationAdminRequired) {
			t.Fatalf("RestoreDraft() = (%v, %v), want admin required", result, err)
		}
		assertRestorationDidNotPersist(t, database, seed)
		result, err = NewApplicationReviewRestorationRepository(database).RestoreDraft(
			t.Context(), seed.applicationID, seed.versionID, seed.reviewID, "auth-new-admin", 3, time.Now(),
		)
		if err != nil || result == nil || result.Version().UpdatedBy() != "auth-new-admin" || result.Version().Revision() != 4 {
			t.Fatalf("new-admin RestoreDraft() = (%v, %v)", result, err)
		}
	})

	t.Run("BR-REV-021 concurrent administrator transfer prevents old-admin restoration", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-old-admin-restore-race", "restore-admin-race")
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
			bson.D{{Key: "id", Value: seed.applicationID.String()}, {Key: "adminId", Value: seed.adminID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-admin-restore-race"}}}},
		)
		if err != nil || transferResult.ModifiedCount != 1 {
			t.Fatalf("stage administrator transfer: result=%#v error=%v", transferResult, err)
		}

		competingClient, lockStarted := monitoredMongoClient(t, database.Name(), "findAndModify")
		competingRepository := NewApplicationReviewRestorationRepository(competingClient.Database(database.Name()))
		raceContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		restoreResult := make(chan error, 1)
		go func() {
			_, restoreErr := competingRepository.RestoreDraft(
				raceContext, seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3, time.Now(),
			)
			restoreResult <- restoreErr
		}()
		awaitMongoCommand(t, raceContext, lockStarted, "review restoration application lock")
		if err := transferSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit administrator transfer: %v", err)
		}
		select {
		case restoreErr := <-restoreResult:
			if !errors.Is(restoreErr, reviewport.ErrApplicationAdminRequired) {
				t.Fatalf("old-admin restoration error = %v, want admin required", restoreErr)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for old-admin restoration: %v", raceContext.Err())
		}
		assertRestorationDidNotPersist(t, database, seed)
	})

	t.Run("BR-REV-022 rejects historical review before other state conflicts", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-admin-historical", "restore-historical")
		latest := readReviewDocument(t, database, seed.reviewID)
		latest.ReviewID = nextIntegrationApplicationReviewID(t).String()
		latest.Attempt = 2
		latest.SourceVersionRevision = 4
		if _, err := database.Collection(applicationReviewsCollectionName).InsertOne(t.Context(), latest); err != nil {
			t.Fatalf("insert latest attempt: %v", err)
		}
		result, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(
			t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 2, time.Now(),
		)
		if result != nil || !errors.Is(err, reviewport.ErrApplicationReviewNotLatest) {
			t.Fatalf("RestoreDraft() = (%v, %v), want not latest", result, err)
		}
		assertRestorationDidNotPersist(t, database, seed)
	})

	t.Run("BR-REV-023 BR-REV-027 distinguishes non-rejected, revision and inconsistent state", func(t *testing.T) {
		t.Run("non-rejected", func(t *testing.T) {
			database := migratedIntegrationDatabase(t, client)
			seed := createDecidableReview(t, database, "auth-admin-pending-restore", "restore-pending", "v1")
			result, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(
				t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 2, time.Now(),
			)
			if result != nil || !errors.Is(err, reviewport.ErrApplicationVersionNotRejected) {
				t.Fatalf("RestoreDraft() = (%v, %v), want not rejected", result, err)
			}
		})
		t.Run("revision", func(t *testing.T) {
			database := migratedIntegrationDatabase(t, client)
			seed := createRejectedReview(t, database, "auth-admin-stale-restore", "restore-stale")
			result, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(
				t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 2, time.Now(),
			)
			if result != nil || !errors.Is(err, reviewport.ErrApplicationVersionRevisionConflict) {
				t.Fatalf("RestoreDraft() = (%v, %v), want revision conflict", result, err)
			}
			assertRestorationDidNotPersist(t, database, seed)
		})
		t.Run("inconsistent", func(t *testing.T) {
			database := migratedIntegrationDatabase(t, client)
			seed := createRejectedReview(t, database, "auth-admin-inconsistent-restore", "restore-inconsistent")
			if _, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
				t.Context(),
				bson.D{{Key: "versionId", Value: seed.versionID.String()}},
				bson.D{{Key: "$set", Value: bson.D{{Key: "revision", Value: int64(4)}}}},
			); err != nil {
				t.Fatalf("corrupt version revision: %v", err)
			}
			result, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(
				t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 4, time.Now(),
			)
			if result != nil || !errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent) {
				t.Fatalf("RestoreDraft() = (%v, %v), want state inconsistent", result, err)
			}
			stored := readReviewDocument(t, database, seed.reviewID)
			if stored.DraftRestoration != nil {
				t.Fatalf("failed restore wrote audit: %#v", stored.DraftRestoration)
			}
		})
	})

	t.Run("BR-REV-024 restored draft can be edited and submitted as the next attempt", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createRejectedReview(t, database, "auth-admin-next-attempt", "restore-next-attempt")
		if _, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(
			t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3, time.Now(),
		); err != nil {
			t.Fatalf("restore rejected version: %v", err)
		}
		replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/restored-v2", nil, nil, nil)
		updated, err := NewApplicationVersionRepository(database).ReplaceDraft(
			t.Context(), seed.applicationID, versiondomain.ApplicationVersionID(seed.versionID), seed.adminID, 4, replacement, time.Now(),
		)
		if err != nil || updated.Revision() != 5 || updated.ReviewStatus() != "DRAFT" {
			t.Fatalf("edit restored draft = (%#v, %v)", updated, err)
		}
		reviewRepository := NewApplicationReviewRepository(database)
		candidate := loadReviewCandidate(t, reviewRepository, seed.applicationID, seed.versionID, seed.adminID.String(), 5)
		secondReviewID := nextIntegrationApplicationReviewID(t)
		result, err := reviewRepository.Submit(t.Context(), candidate, secondReviewID, seed.adminID, 41, "public-https.v1", time.Now())
		if err != nil {
			t.Fatalf("submit restored draft: %v", err)
		}
		if result.Review().Attempt() != 2 || result.Version().Revision() != 6 || result.Version().ReviewStatus() != "SUBMITTED" {
			t.Fatalf("next review result = review:%#v version:%#v", result.Review(), result.Version())
		}
	})
}

func TestApplicationReviewRestorationValidatorIntegration(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	seed := createRejectedReview(t, database, "auth-admin-restore-validator", "restore-validator")
	reviews := database.Collection(applicationReviewsCollectionName)

	invalid := []struct {
		name   string
		update bson.D
	}{
		{
			name: "partial restoration",
			update: bson.D{{Key: "$set", Value: bson.D{
				{Key: "draftRestoration", Value: bson.D{{Key: "restoredBy", Value: seed.adminID.String()}}},
			}}},
		},
		{
			name: "restoration on pending",
			update: bson.D{{Key: "$set", Value: bson.D{
				{Key: "status", Value: "PENDING"},
				{Key: "decision", Value: nil},
				{Key: "draftRestoration", Value: applicationReviewDraftRestorationDocument{
					RestoredBy: seed.adminID.String(), RestoredAt: time.Now(), ResultVersionRevision: 4,
				}},
			}}},
		},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, err := reviews.UpdateOne(t.Context(), bson.D{{Key: "reviewId", Value: seed.reviewID.String()}}, test.update)
			if err == nil {
				t.Fatal("validator accepted invalid draft restoration")
			}
			assertDocumentValidationFailure(t, err)
		})
	}
}

func createRejectedReview(t *testing.T, database *drivermongo.Database, adminID, applicationName string) decidableReview {
	t.Helper()
	seed := createDecidableReview(t, database, adminID, applicationName, "v1")
	repository := NewApplicationReviewDecisionRepository(database)
	candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
	decision := integrationRejectedDecision(
		t, "review.v1", "review rejected", "auth-reviewer",
		time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
	)
	if _, err := repository.Decide(t.Context(), candidate, "auth-reviewer", decision); err != nil {
		t.Fatalf("seed rejected review: %v", err)
	}
	return seed
}

func assertRestorationDidNotPersist(t *testing.T, database *drivermongo.Database, seed decidableReview) {
	t.Helper()
	review := readReviewDocument(t, database, seed.reviewID)
	version := readVersionDocument(t, database, seed.versionID.String())
	if review.DraftRestoration != nil || review.Status != "REJECTED" || version.ReviewStatus != "REJECTED" || version.Revision != 3 {
		t.Fatalf("failed restoration left partial state: review=%#v version=%#v", review, version)
	}
}
