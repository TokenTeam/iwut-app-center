package mongo

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
)

type decidableReview struct {
	applicationID shared.ApplicationID
	versionID     reviewdomain.ApplicationVersionID
	reviewID      reviewdomain.ApplicationReviewID
	adminID       shared.AuthID
}

func TestApplicationReviewDecisionRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-REV-012 BR-REV-013 BR-REV-014 approve persists decision, version lifecycle and audit atomically", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-approve", "decision-approve", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 123456789, time.UTC)
		persistedDecidedAt := decidedAt.Truncate(time.Millisecond)
		decision := integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v2", "auth-reviewer", decidedAt)

		result, err := repository.Decide(t.Context(), candidate, "auth-reviewer", decision)
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if result.Review().Status() != reviewdomain.ReviewStatusApproved ||
			result.Version().ReviewStatus() != reviewdomain.ReviewDecisionApproved ||
			result.Version().Revision() != 3 || result.Version().UpdatedBy() != "auth-reviewer" ||
			!result.Version().UpdatedAt().Equal(persistedDecidedAt) ||
			!result.Review().Decision().DecidedAt().Equal(persistedDecidedAt) {
			t.Fatalf("unexpected decision result: review=%s version=%#v", result.Review().Status(), result.Version())
		}

		storedReview := readReviewDocument(t, database, seed.reviewID)
		if storedReview.Status != "APPROVED" || storedReview.Decision == nil {
			t.Fatalf("stored review = %#v", storedReview)
		}
		validation := storedReview.Decision.ApprovalValidation
		if storedReview.Decision.Outcome != "APPROVED" || storedReview.Decision.ReviewPolicyVersion != "review.v1" ||
			storedReview.Decision.DecidedBy != "auth-reviewer" || !storedReview.Decision.DecidedAt.Equal(persistedDecidedAt) ||
			storedReview.Decision.Reason != nil || validation == nil ||
			validation.ScopeCatalogRevision != 77 || validation.PreflightPolicyVersion != "public-https.v2" ||
			len(storedReview.Decision.ConfirmedCheckIDs) != 1 || storedReview.Decision.ConfirmedCheckIDs[0] != "content-reviewed" {
			t.Fatalf("stored decision = %#v", storedReview.Decision)
		}
		storedVersion := readVersionDocument(t, database, seed.versionID.String())
		if storedVersion.ReviewStatus != "APPROVED" || storedVersion.Revision != 3 ||
			storedVersion.UpdatedBy != "auth-reviewer" || !storedVersion.UpdatedAt.Equal(persistedDecidedAt) {
			t.Fatalf("stored version = %#v", storedVersion)
		}
		assertNoPublicationCollections(t, database)
	})

	t.Run("BR-REV-012 BR-REV-013 BR-REV-015 reject persists reason and clears approval validation", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-reject", "decision-reject", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 10, 0, 0, 0, time.UTC)
		decision := integrationRejectedDecision(t, "review.v1", "用途说明不足。", "auth-reviewer", decidedAt)

		result, err := repository.Decide(t.Context(), candidate, "auth-reviewer", decision)
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if result.Review().Status() != reviewdomain.ReviewStatusRejected || result.Version().Revision() != 3 {
			t.Fatalf("unexpected reject result: %#v", result)
		}
		storedReview := readReviewDocument(t, database, seed.reviewID)
		if storedReview.Status != "REJECTED" || storedReview.Decision == nil ||
			storedReview.Decision.ApprovalValidation != nil ||
			storedReview.Decision.Reason == nil || *storedReview.Decision.Reason != "用途说明不足。" ||
			len(storedReview.Decision.ConfirmedCheckIDs) != 0 {
			t.Fatalf("stored rejected decision = %#v", storedReview.Decision)
		}
		storedVersion := readVersionDocument(t, database, seed.versionID.String())
		if storedVersion.ReviewStatus != "REJECTED" || storedVersion.Revision != 3 || storedVersion.UpdatedBy != "auth-reviewer" {
			t.Fatalf("stored rejected version = %#v", storedVersion)
		}
	})

	t.Run("BR-REV-011 BR-REV-014 system rejection keeps reviewer authorization separate from decision audit", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-system-reject", "decision-system-reject", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 10, 30, 0, 0, time.UTC)
		decision := integrationRejectedDecision(
			t,
			"review.v1",
			reviewdomain.SystemSuspensionRejectionReason,
			"auth-system",
			decidedAt,
		)

		result, err := repository.Decide(t.Context(), candidate, "auth-reviewer", decision)
		if err != nil {
			t.Fatalf("Decide() error = %v", err)
		}
		if result.Review().Decision().DecidedBy() != "auth-system" || result.Version().UpdatedBy() != "auth-system" {
			t.Fatalf("system audit identity not preserved: review=%#v version=%#v", result.Review(), result.Version())
		}
		storedReview := readReviewDocument(t, database, seed.reviewID)
		storedVersion := readVersionDocument(t, database, seed.versionID.String())
		if storedReview.Decision == nil || storedReview.Decision.DecidedBy != "auth-system" ||
			storedVersion.UpdatedBy != "auth-system" {
			t.Fatalf("stored system rejection audit mismatch: review=%#v version=%#v", storedReview, storedVersion)
		}
	})

	t.Run("BR-REV-012 a second decision is rejected without overwriting the first", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-twice", "decision-twice", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 11, 0, 0, 0, time.UTC)
		firstDecision := integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v2", "auth-reviewer", decidedAt)
		if _, err := repository.Decide(t.Context(), candidate, "auth-reviewer", firstDecision); err != nil {
			t.Fatalf("first Decide() error = %v", err)
		}
		secondDecision := integrationRejectedDecision(t, "review.v1", "overwrite attempt", "auth-other", decidedAt.Add(time.Hour))
		if _, err := repository.Decide(t.Context(), candidate, "auth-other", secondDecision); !errors.Is(err, reviewport.ErrApplicationReviewAlreadyDecided) {
			t.Fatalf("second Decide() error = %v, want already decided", err)
		}
		storedReview := readReviewDocument(t, database, seed.reviewID)
		if storedReview.Status != "APPROVED" || storedReview.Decision == nil || storedReview.Decision.Outcome != "APPROVED" {
			t.Fatalf("second decision overwrote the first: %#v", storedReview)
		}
		storedVersion := readVersionDocument(t, database, seed.versionID.String())
		if storedVersion.ReviewStatus != "APPROVED" || storedVersion.Revision != 3 {
			t.Fatalf("second decision changed the version: %#v", storedVersion)
		}
	})

	t.Run("BR-REV-011 load rejects a conflicting reviewer for every conflict source", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-conflict", "decision-conflict", "v1")
		repository := NewApplicationReviewDecisionRepository(database)

		candidate, err := repository.LoadDecisionCandidate(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID)
		if candidate != nil || !errors.Is(err, reviewport.ErrApplicationReviewConflictOfInterest) {
			t.Fatalf("administrator conflict = (%v, %v)", candidate, err)
		}

		// The version creator and the review submitter are the administrator in
		// the seeded flow, so distinguish the creator explicitly.
		if _, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "versionId", Value: seed.versionID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "createdBy", Value: "auth-creator-only"}}}},
		); err != nil {
			t.Fatalf("set version creator: %v", err)
		}
		candidate, err = repository.LoadDecisionCandidate(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, "auth-creator-only")
		if candidate != nil || !errors.Is(err, reviewport.ErrApplicationReviewConflictOfInterest) {
			t.Fatalf("version creator conflict = (%v, %v)", candidate, err)
		}
	})

	t.Run("BR-REV-013 load rejects version content that no longer matches the snapshot", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-mismatch", "decision-mismatch", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		if _, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "versionId", Value: seed.versionID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "launchUrl", Value: "https://example.edu/apps/other"}}}},
		); err != nil {
			t.Fatalf("mutate version content: %v", err)
		}
		candidate, err := repository.LoadDecisionCandidate(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, "auth-reviewer")
		if candidate != nil || !errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent) {
			t.Fatalf("state mismatch = (%v, %v), want state inconsistent", candidate, err)
		}
	})

	t.Run("BR-REV-011 BR-REV-013 decide re-checks state and conflicts in the final transaction", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-final", "decision-final", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 12, 0, 0, 0, time.UTC)
		decision := integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v2", "auth-reviewer", decidedAt)

		// The version creator becomes the reviewer after the candidate was
		// loaded. The final transaction must reject the decision.
		if _, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "versionId", Value: seed.versionID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "createdBy", Value: "auth-reviewer"}}}},
		); err != nil {
			t.Fatalf("set conflicting creator: %v", err)
		}
		if _, err := repository.Decide(t.Context(), candidate, "auth-reviewer", decision); !errors.Is(err, reviewport.ErrApplicationReviewConflictOfInterest) {
			t.Fatalf("Decide() error = %v, want conflict of interest", err)
		}
		assertDecisionDidNotPersist(t, database, seed)
	})

	t.Run("BR-REV-011 decide re-checks the current administrator and leaves PENDING on mismatch", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-transfer", "decision-admin-transfer", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 12, 30, 0, 0, time.UTC)
		decision := integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v2", "auth-reviewer", decidedAt)

		if _, err := database.Collection(applicationsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "id", Value: seed.applicationID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-admin"}}}},
		); err != nil {
			t.Fatalf("transfer administrator: %v", err)
		}
		if _, err := repository.Decide(t.Context(), candidate, "auth-reviewer", decision); !errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent) {
			t.Fatalf("Decide() error = %v, want state inconsistent", err)
		}
		assertDecisionDidNotPersist(t, database, seed)
	})

	t.Run("BR-REV-012 concurrent reviewers allow at most one decision", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-admin-race", "decision-race", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		firstCandidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer-one")
		secondCandidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer-two")
		decidedAt := time.Date(2026, time.September, 21, 13, 0, 0, 0, time.UTC)
		decisions := []struct {
			candidate *reviewdomain.ApplicationReviewDecisionCandidate
			reviewer  shared.AuthID
			decision  *reviewdomain.ApplicationReviewDecision
		}{
			{firstCandidate, "auth-reviewer-one", integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v2", "auth-reviewer-one", decidedAt)},
			{secondCandidate, "auth-reviewer-two", integrationRejectedDecision(t, "review.v1", "concurrent rejection", "auth-reviewer-two", decidedAt)},
		}

		start := make(chan struct{})
		results := make(chan error, len(decisions))
		var group sync.WaitGroup
		for _, item := range decisions {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				_, err := repository.Decide(t.Context(), item.candidate, item.reviewer, item.decision)
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
			case errors.Is(err, reviewport.ErrApplicationReviewAlreadyDecided),
				errors.Is(err, reviewport.ErrApplicationReviewStateInconsistent):
				conflicts++
			default:
				t.Errorf("unexpected concurrent decision error: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent results = success:%d conflict:%d, want 1/1", successes, conflicts)
		}
		storedReview := readReviewDocument(t, database, seed.reviewID)
		if storedReview.Decision == nil || storedReview.Status == "PENDING" {
			t.Fatalf("concurrent decisions left review undecided: %#v", storedReview)
		}
	})

	t.Run("BR-REV-011 concurrent administrator transfer rejects the stale decision without partial state", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		seed := createDecidableReview(t, database, "auth-old-admin", "decision-transfer-race", "v1")
		repository := NewApplicationReviewDecisionRepository(database)
		candidate := loadDecisionCandidate(t, repository, seed, "auth-reviewer")
		decidedAt := time.Date(2026, time.September, 21, 14, 0, 0, 0, time.UTC)
		decision := integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v2", "auth-reviewer", decidedAt)

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
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-admin"}}}},
		)
		if err != nil || transferResult.ModifiedCount != 1 {
			t.Fatalf("stage administrator transfer: result=%#v error=%v", transferResult, err)
		}

		competingClient, lockStarted := monitoredMongoClient(t, database.Name(), "findAndModify")
		competingRepository := NewApplicationReviewDecisionRepository(competingClient.Database(database.Name()))
		raceContext, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		decideResult := make(chan error, 1)
		go func() {
			_, decideErr := competingRepository.Decide(raceContext, candidate, "auth-reviewer", decision)
			decideResult <- decideErr
		}()
		awaitMongoCommand(t, raceContext, lockStarted, "review decision application lock")
		if err := transferSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit administrator transfer: %v", err)
		}
		select {
		case decideErr := <-decideResult:
			if !errors.Is(decideErr, reviewport.ErrApplicationReviewStateInconsistent) {
				t.Fatalf("stale decision error = %v, want state inconsistent", decideErr)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for stale decision result: %v", raceContext.Err())
		}
		assertDecisionDidNotPersist(t, database, seed)
	})
}

func TestApplicationReviewDecisionRepositoryConstructionDoesNotMutateSchema(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	_ = NewApplicationReviewDecisionRepository(database)
	names, err := database.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("repository construction created collections: %v", names)
	}
}

func TestApplicationReviewDecisionValidatorIntegration(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	seed := createDecidableReview(t, database, "auth-admin-validator", "decision-validator", "v1")
	base := readReviewDocument(t, database, seed.reviewID)
	reviews := database.Collection(applicationReviewsCollectionName)

	variants := []struct {
		name       string
		mutate     func(*applicationReviewDocument)
		wantReject bool
	}{
		{name: "valid pending", mutate: func(*applicationReviewDocument) {}, wantReject: false},
		{name: "valid approved", mutate: func(document *applicationReviewDocument) {
			document.Status = "APPROVED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "APPROVED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{"content-reviewed"}, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
				ApprovalValidation: &applicationReviewApprovalValidationDocument{
					ScopeCatalogRevision: 7, PreflightPolicyVersion: "public-https.v1",
				},
			}
		}, wantReject: false},
		{name: "valid rejected", mutate: func(document *applicationReviewDocument) {
			reason := "denied"
			document.Status = "REJECTED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "REJECTED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{}, Reason: &reason, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: false},
		{name: "pending with decision", mutate: func(document *applicationReviewDocument) {
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "APPROVED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{"content-reviewed"}, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
				ApprovalValidation: &applicationReviewApprovalValidationDocument{
					ScopeCatalogRevision: 7, PreflightPolicyVersion: "public-https.v1",
				},
			}
		}, wantReject: true},
		{name: "outcome does not match status", mutate: func(document *applicationReviewDocument) {
			document.Status = "APPROVED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "REJECTED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{}, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: true},
		{name: "approved without approval validation", mutate: func(document *applicationReviewDocument) {
			document.Status = "APPROVED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "APPROVED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{"content-reviewed"}, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: true},
		{name: "rejected with confirmations", mutate: func(document *applicationReviewDocument) {
			reason := "denied"
			document.Status = "REJECTED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "REJECTED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{"content-reviewed"}, Reason: &reason, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: true},
		{name: "rejected without reason", mutate: func(document *applicationReviewDocument) {
			document.Status = "REJECTED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "REJECTED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{}, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: true},
		{name: "rejected reason at 2000 code points", mutate: func(document *applicationReviewDocument) {
			reason := strings.Repeat("测", 2000)
			document.Status = "REJECTED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "REJECTED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{}, Reason: &reason, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: false},
		{name: "rejected reason above 2000 code points", mutate: func(document *applicationReviewDocument) {
			reason := strings.Repeat("测", 2001)
			document.Status = "REJECTED"
			document.Decision = &applicationReviewDecisionDocument{
				Outcome: "REJECTED", ReviewPolicyVersion: "review.v1",
				ConfirmedCheckIDs: []string{}, Reason: &reason, DecidedBy: "auth-reviewer",
				DecidedAt: time.Date(2026, time.September, 21, 15, 0, 0, 0, time.UTC),
			}
		}, wantReject: true},
	}
	for index, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			document := base
			document.ReviewID = nextIntegrationApplicationReviewID(t).String()
			document.VersionID = nextIntegrationApplicationVersionID(t).String()
			document.Attempt = int32(index + 1)
			document.SourceVersionRevision = int64(index + 1)
			variant.mutate(&document)
			_, err := reviews.InsertOne(t.Context(), document)
			if variant.wantReject {
				if err == nil {
					t.Fatal("validator accepted an inconsistent decision document")
				}
				assertDocumentValidationFailure(t, err)
				return
			}
			if err != nil {
				t.Fatalf("validator rejected a valid document: %v", err)
			}
		})
	}
}

func createDecidableReview(t *testing.T, database *drivermongo.Database, adminID, applicationName, label string) decidableReview {
	t.Helper()
	application, version := createReviewTestVersion(t, database, adminID, applicationName, label)
	submissionRepository := NewApplicationReviewRepository(database)
	submissionCandidate := loadReviewCandidate(t, submissionRepository, application.ID(), version.ID(), adminID, 1)
	reviewID := nextIntegrationApplicationReviewID(t)
	if _, err := submissionRepository.Submit(
		t.Context(), submissionCandidate, reviewID, shared.AuthID(adminID), 41, "public-https.v1",
		time.Date(2026, time.September, 21, 8, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed submitted review: %v", err)
	}
	return decidableReview{
		applicationID: application.ID(),
		versionID:     reviewdomain.ApplicationVersionID(version.ID()),
		reviewID:      reviewID,
		adminID:       shared.AuthID(adminID),
	}
}

func loadDecisionCandidate(
	t *testing.T,
	repository *ApplicationReviewDecisionRepository,
	seed decidableReview,
	reviewerID string,
) *reviewdomain.ApplicationReviewDecisionCandidate {
	t.Helper()
	candidate, err := repository.LoadDecisionCandidate(
		t.Context(), seed.applicationID, seed.versionID, seed.reviewID, shared.AuthID(reviewerID),
	)
	if err != nil {
		t.Fatalf("load decision candidate: %v", err)
	}
	return candidate
}

func integrationApprovedDecision(
	t *testing.T,
	policyVersion string,
	checkIDs []string,
	catalogRevision int64,
	preflight string,
	decidedBy string,
	decidedAt time.Time,
) *reviewdomain.ApplicationReviewDecision {
	t.Helper()
	version := integrationReviewPolicyVersion(t, policyVersion)
	ids := make([]reviewdomain.ReviewCheckID, len(checkIDs))
	for index, value := range checkIDs {
		id, err := reviewdomain.NewReviewCheckID(value)
		if err != nil {
			t.Fatalf("create check ID: %v", err)
		}
		ids[index] = id
	}
	scopeRevision, err := reviewdomain.NewScopeCatalogRevision(catalogRevision)
	if err != nil {
		t.Fatalf("create scope catalog revision: %v", err)
	}
	preflightVersion, err := reviewdomain.NewPreflightPolicyVersion(preflight)
	if err != nil {
		t.Fatalf("create preflight policy version: %v", err)
	}
	validation, err := reviewdomain.NewApprovalValidation(scopeRevision, preflightVersion)
	if err != nil {
		t.Fatalf("create approval validation: %v", err)
	}
	decision, err := reviewdomain.NewApprovedDecision(version, ids, "", validation, shared.AuthID(decidedBy), decidedAt)
	if err != nil {
		t.Fatalf("create approved decision: %v", err)
	}
	return decision
}

func integrationRejectedDecision(
	t *testing.T,
	policyVersion string,
	reason string,
	decidedBy string,
	decidedAt time.Time,
) *reviewdomain.ApplicationReviewDecision {
	t.Helper()
	decision, err := reviewdomain.NewRejectedDecision(
		integrationReviewPolicyVersion(t, policyVersion), reason, shared.AuthID(decidedBy), decidedAt,
	)
	if err != nil {
		t.Fatalf("create rejected decision: %v", err)
	}
	return decision
}

func integrationReviewPolicyVersion(t *testing.T, value string) reviewdomain.ReviewPolicyVersion {
	t.Helper()
	version, err := reviewdomain.NewReviewPolicyVersion(value)
	if err != nil {
		t.Fatalf("create review policy version: %v", err)
	}
	return version
}

func readReviewDocument(t *testing.T, database *drivermongo.Database, reviewID reviewdomain.ApplicationReviewID) applicationReviewDocument {
	t.Helper()
	var document applicationReviewDocument
	if err := database.Collection(applicationReviewsCollectionName).
		FindOne(t.Context(), bson.D{{Key: "reviewId", Value: reviewID.String()}}).
		Decode(&document); err != nil {
		t.Fatalf("read application review %s: %v", reviewID, err)
	}
	return document
}

func assertDecisionDidNotPersist(t *testing.T, database *drivermongo.Database, seed decidableReview) {
	t.Helper()
	storedReview := readReviewDocument(t, database, seed.reviewID)
	if storedReview.Status != "PENDING" || storedReview.Decision != nil {
		t.Fatalf("failed decision left a partial review: %#v", storedReview)
	}
	storedVersion := readVersionDocument(t, database, seed.versionID.String())
	if storedVersion.ReviewStatus != "SUBMITTED" || storedVersion.Revision != 2 {
		t.Fatalf("failed decision left a partial version: %#v", storedVersion)
	}
}

func assertNoPublicationCollections(t *testing.T, database *drivermongo.Database) {
	t.Helper()
	names, err := database.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	for _, name := range names {
		if name == "publications" || name == "application_publications" {
			t.Fatalf("decision created publication collection %s", name)
		}
	}
}
