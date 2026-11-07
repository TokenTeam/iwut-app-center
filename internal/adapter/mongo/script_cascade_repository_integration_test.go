package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	application "iwut-app-center/internal/application/domain"
	review "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	script "iwut-app-center/internal/script/domain"
	scriptport "iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
)

func createScriptDependentReview(t *testing.T, db *dm.Database, refs []shared.ScriptReference, name string, at time.Time) decidableReview {
	t.Helper()
	owner := shared.AuthID("owner-" + name)
	app, version := createReviewTestVersion(t, db, owner.String(), name, "v1")
	replacement := integrationApplicationVersionReplacement(t, "v1", "https://example.edu/cascade", nil, nil, nil).WithScriptReferences(refs)
	if _, err := NewApplicationVersionRepository(db).ReplaceDraft(t.Context(), app.ID(), version.ID(), owner, 1, replacement, at); err != nil {
		t.Fatal(err)
	}
	writer := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, writer, app.ID(), version.ID(), owner.String(), 2)
	id := nextIntegrationApplicationReviewID(t)
	if _, err := writer.Submit(t.Context(), candidate, id, owner, 1, "v1", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return decidableReview{applicationID: app.ID(), versionID: review.ApplicationVersionID(version.ID()), reviewID: id, adminID: owner}
}

func readCascadeTask(t *testing.T, db *dm.Database, id string) scriptCascadeTaskDocument {
	t.Helper()
	var task scriptCascadeTaskDocument
	if err := db.Collection(scriptCascadeTasksCollection).FindOne(t.Context(), bson.M{"taskId": id}).Decode(&task); err != nil {
		t.Fatal(err)
	}
	return task
}
func drainScriptCascades(t *testing.T, db *dm.Database, at time.Time) {
	t.Helper()
	for range 20 {
		worker := NewScriptCascadeRepository(db)
		lease, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at)
		if err != nil {
			t.Fatal(err)
		}
		if lease == nil {
			return
		}
		if err := worker.AdvanceCascade(t.Context(), *lease, "auth-system-cascade", at); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("cascade did not converge within test bound")
}
func closeCascadeApplication(t *testing.T, db *dm.Database, seed decidableReview, at time.Time, complete bool) string {
	t.Helper()
	id := application.ApplicationClosureID(nextIntegrationApplicationID(t))
	proof := application.ApplicationCloseProof{Subject: seed.adminID, ApplicationID: seed.applicationID, JTI: nextIntegrationApplicationID(t).String(), AuthTime: at, IssuedAt: at, ExpiresAt: at.Add(time.Minute)}
	writer := NewApplicationClosureRepository(db)
	if _, err := writer.Start(t.Context(), seed.applicationID, seed.adminID, 1, 1, proof, id, at); err != nil {
		t.Fatal(err)
	}
	if complete {
		if _, err := writer.Complete(t.Context(), id, "auth-closure-receipt", at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return id.String()
}

func TestScriptCascadeIntegration_UCAPP049_BRSDP006007008009(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, submission := newScriptSubmissionFixture(t, db)
	if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	refs := []shared.ScriptReference{{ResourceID: string(draft.ResourceID), VersionID: string(draft.ID)}}
	at := submission.At.Add(2 * time.Second).Truncate(time.Millisecond)
	var targets []decidableReview
	for i := range 35 {
		targets = append(targets, createScriptDependentReview(t, db, refs, fmt.Sprintf("cascade%02d", i), at))
	}
	// A draft and a review without this exact dependency remain outside the task.
	unrelated := createDecidableReview(t, db, "unrelated-owner", "unrelatedapp", "v1")
	manual := targets[0]
	writer := NewApplicationReviewDecisionRepository(db)
	candidate := loadDecisionCandidate(t, writer, manual, "manual-reviewer")
	if _, err := writer.Decide(t.Context(), candidate, "manual-reviewer", integrationRejectedDecision(t, "app-version-review-v3", "manual reason", "manual-reviewer", at.Add(2*time.Second))); err != nil {
		t.Fatal(err)
	}
	closingID := closeCascadeApplication(t, db, targets[1], at.Add(2*time.Second), false)
	closedID := closeCascadeApplication(t, db, targets[2], at.Add(2*time.Second), true)
	reject := scriptDecisionCommand(t, draft, submission, "REJECT")
	reject.At = at.Add(4 * time.Second)
	result, err := repo.DecideReview(t.Context(), reject)
	if err != nil || result.Cascade.Total != 35 {
		t.Fatalf("rejection ACK: %+v %v", result, err)
	}
	worker := NewScriptCascadeRepository(db)
	lease, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(5*time.Second))
	if err != nil || lease == nil {
		t.Fatal("claim", err)
	}
	if duplicate, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(5*time.Second)); err != nil || duplicate != nil {
		t.Fatal("lease isolation", err)
	}
	// Recover a claimed task after an imagined process crash before the first target.
	recoveryAt := at.Add(time.Minute)
	recovered, err := NewScriptCascadeRepository(db).ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), recoveryAt)
	if err != nil || recovered == nil {
		t.Fatal("expired lease recovery", err)
	}
	if err := worker.AdvanceCascade(t.Context(), *lease, "auth-system-cascade", recoveryAt); err != nil {
		t.Fatal(err)
	}
	if readCascadeTask(t, db, reject.TaskID).Processed != 0 {
		t.Fatal("stale worker advanced task")
	}
	if err := worker.AdvanceCascade(t.Context(), *recovered, "auth-system-cascade", recoveryAt); err != nil {
		t.Fatal(err)
	}
	partial := readCascadeTask(t, db, reject.TaskID)
	if partial.Processed != scriptCascadeBatchSize || partial.Status != "RUNNING" {
		t.Fatalf("unbounded work: %+v", partial)
	}
	drainScriptCascades(t, db, recoveryAt)
	task := readCascadeTask(t, db, reject.TaskID)
	if task.Status != "COMPLETE" || task.Processed != 35 || task.Rejected != 32 || task.Closed != 2 || task.AlreadyTerminal != 1 {
		t.Fatalf("incorrect completion: %+v", task)
	}
	assertCollectionCount(t, db, scriptCascadeTargetsCollection, 35)
	for i, seed := range targets {
		stored := readReviewDocument(t, db, seed.reviewID)
		if i == 1 || i == 2 {
			if stored.Status != "PENDING" || stored.Decision != nil || readVersionDocument(t, db, seed.versionID.String()).Revision != 3 {
				t.Fatal("closure history changed")
			}
			var target scriptCascadeTargetDocument
			if err := db.Collection(scriptCascadeTargetsCollection).FindOne(t.Context(), bson.M{"appReviewId": seed.reviewID.String()}).Decode(&target); err != nil {
				t.Fatal(err)
			}
			want := closingID
			if i == 2 {
				want = closedID
			}
			if target.Result != "APPLICATION_CLOSED" || target.Closure == nil || target.Closure.ID != want {
				t.Fatal("missing closure proof")
			}
			continue
		}
		if stored.Status != "REJECTED" || stored.Decision == nil {
			t.Fatal("missing rejection")
		}
		if i == 0 {
			if stored.Decision.DecidedBy != "manual-reviewer" || *stored.Decision.Reason != "manual reason" {
				t.Fatal("overwrote winner")
			}
			continue
		}
		if stored.Decision.DecidedBy != "auth-system-cascade" || stored.Decision.ScriptRejection == nil || stored.Decision.ScriptRejection.DecisionID != reject.DecisionID ||
			len(stored.Decision.ConfirmedCheckIDs) != 0 || *stored.Decision.Reason != review.SystemScriptRejectionReason {
			t.Fatal("invalid system attribution")
		}
	}
	if readReviewDocument(t, db, unrelated.reviewID).Status != "PENDING" {
		t.Fatal("unrelated review changed")
	}
	detail, err := NewScriptQueryRepository(db, ScriptQueryCursorKey{1}).GetReview(t.Context(), draft.ResourceID, draft.ID, submission.ReviewID)
	if err != nil || detail.Cascade.Status != "COMPLETE" || detail.Cascade.Closed != 2 {
		t.Fatalf("progress readback: %+v %v", detail, err)
	}
	drainScriptCascades(t, db, recoveryAt.Add(time.Minute))
	assertCollectionCount(t, db, scriptCascadeTargetsCollection, 35)
	// A later close preserves a system rejection that already won.
	closeCascadeApplication(t, db, targets[3], recoveryAt.Add(2*time.Second), false)
	if readReviewDocument(t, db, targets[3].reviewID).Status != "REJECTED" {
		t.Fatal("late closure overwrote decision")
	}
}

func TestScriptCascadeFailureIntegration_UCAPP049_BRSDP008009(t *testing.T) {
	for _, damage := range []string{"missing registration", "missing closure proof", "rollback target ledger"} {
		t.Run(damage, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			repo, draft, submission := newScriptSubmissionFixture(t, db)
			if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
				t.Fatal(err)
			}
			at := submission.At.Add(3 * time.Second).Truncate(time.Millisecond)
			refs := []shared.ScriptReference{{ResourceID: string(draft.ResourceID), VersionID: string(draft.ID)}}
			target := createScriptDependentReview(t, db, refs, "damagedapp", at)
			if damage == "missing registration" {
				if _, err := db.Collection(scriptDependenciesCollection).DeleteOne(t.Context(), bson.M{"appReviewId": target.reviewID.String()}); err != nil {
					t.Fatal(err)
				}
			}
			if damage == "missing closure proof" {
				closeCascadeApplication(t, db, target, at.Add(2*time.Second), false)
				if _, err := db.Collection(applicationClosuresCollectionName).DeleteOne(t.Context(), bson.M{"applicationId": target.applicationID.String()}); err != nil {
					t.Fatal(err)
				}
			}
			reject := scriptDecisionCommand(t, draft, submission, "REJECT")
			reject.At = at.Add(3 * time.Second)
			if _, err := repo.DecideReview(t.Context(), reject); err != nil {
				t.Fatal(err)
			}
			if damage == "rollback target ledger" {
				if _, err := db.Collection(scriptCascadeTargetsCollection).InsertOne(t.Context(), scriptCascadeTargetDocument{DecisionID: reject.DecisionID, ReviewID: target.reviewID.String(), ApplicationID: target.applicationID.String(), Result: "ALREADY_TERMINAL", ProcessedAt: at}); err != nil {
					t.Fatal(err)
				}
			}
			worker := NewScriptCascadeRepository(db)
			lease, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(4*time.Second))
			if err != nil || lease == nil {
				t.Fatal(err)
			}
			if err := worker.AdvanceCascade(t.Context(), *lease, "auth-system-cascade", at.Add(4*time.Second)); err == nil {
				t.Fatal("damaged target reported success")
			}
			task := readCascadeTask(t, db, reject.TaskID)
			if task.Status == "COMPLETE" || task.Processed != 0 {
				t.Fatal("false completion")
			}
			if readReviewDocument(t, db, target.reviewID).Status != "PENDING" {
				t.Fatal("partial decision survived rollback")
			}
			if err := worker.RecordCascadeFailure(t.Context(), *lease, "CASCADE_TARGET_UNAVAILABLE", at.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
			task = readCascadeTask(t, db, reject.TaskID)
			if task.LastFailure != "CASCADE_TARGET_UNAVAILABLE" {
				t.Fatal("failure hidden")
			}
		})
	}
}

func TestScriptCascadeCompetitionIntegration_UCAPP049_BRSDP008(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	first, firstDraft, firstSubmission := newScriptSubmissionFixture(t, db)
	second, secondDraft, secondSubmission := newScriptSubmissionFixture(t, db)
	for _, f := range []struct {
		repo    *ScriptVersionRepository
		command scriptport.SubmitDraft
	}{{first, firstSubmission}, {second, secondSubmission}} {
		if _, err := f.repo.SubmitDraft(t.Context(), f.command); err != nil {
			t.Fatal(err)
		}
	}
	at := secondSubmission.At.Add(3 * time.Second).Truncate(time.Millisecond)
	refs, _ := shared.NormalizeScriptReferences([]shared.ScriptReference{{ResourceID: string(firstDraft.ResourceID), VersionID: string(firstDraft.ID)}, {ResourceID: string(secondDraft.ResourceID), VersionID: string(secondDraft.ID)}})
	seed := createScriptDependentReview(t, db, refs, "competingapp", at)
	for _, f := range []struct {
		repo    *ScriptVersionRepository
		draft   script.Version
		command scriptport.SubmitDraft
	}{{first, firstDraft, firstSubmission}, {second, secondDraft, secondSubmission}} {
		reject := scriptDecisionCommand(t, f.draft, f.command, "REJECT")
		reject.At = at.Add(2 * time.Second)
		if _, err := f.repo.DecideReview(t.Context(), reject); err != nil {
			t.Fatal(err)
		}
	}
	worker := NewScriptCascadeRepository(db)
	var leases []scriptport.CascadeLease
	for range 2 {
		lease, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(3*time.Second))
		if err != nil || lease == nil {
			t.Fatal(err)
		}
		leases = append(leases, *lease)
	}
	writer := NewApplicationReviewDecisionRepository(db)
	candidate := loadDecisionCandidate(t, writer, seed, "human-reviewer")
	rejected := integrationRejectedDecision(t, "app-version-review-v3", "human rejection", "human-reviewer", at.Add(3*time.Second))
	start := make(chan struct{})
	results := make(chan error, 3)
	var wg sync.WaitGroup
	for _, lease := range leases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- worker.AdvanceCascade(t.Context(), lease, "auth-system-cascade", at.Add(3*time.Second))
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_, err := writer.Decide(t.Context(), candidate, "human-reviewer", rejected)
		if errors.Is(err, reviewport.ErrApplicationReviewAlreadyDecided) {
			err = nil
		}
		results <- err
	}()
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	stored := readReviewDocument(t, db, seed.reviewID)
	if stored.Status != "REJECTED" || readVersionDocument(t, db, seed.versionID.String()).Revision != 4 {
		t.Fatal("multiple terminal transitions")
	}
	assertCollectionCount(t, db, scriptCascadeTargetsCollection, 2)
	// Restore, clear references and submit a fresh attempt. Old tasks remain final.
	if _, err := NewApplicationReviewRestorationRepository(db).RestoreDraft(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 4, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	replacement := integrationApplicationVersionReplacement(t, "v1", "https://example.edu/fixed", nil, nil, nil)
	if _, err := NewApplicationVersionRepository(db).ReplaceDraft(t.Context(), seed.applicationID, versiondomain.ApplicationVersionID(seed.versionID), seed.adminID, 5, replacement, at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	submitter := NewApplicationReviewRepository(db)
	fresh := loadReviewCandidate(t, submitter, seed.applicationID, seed.versionID, seed.adminID.String(), 6)
	newID := nextIntegrationApplicationReviewID(t)
	if _, err := submitter.Submit(t.Context(), fresh, newID, seed.adminID, 1, "v1", at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	drainScriptCascades(t, db, at.Add(time.Minute))
	if readReviewDocument(t, db, newID).Status != "PENDING" {
		t.Fatal("old task rejected new attempt")
	}
}

func TestScriptCascadeSystemRetryIntegration_UCAPP049_BRSDP007008(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, submission := newScriptSubmissionFixture(t, db)
	if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	at := submission.At.Add(3 * time.Second).Truncate(time.Millisecond)
	target := createScriptDependentReview(t, db, []shared.ScriptReference{{ResourceID: string(draft.ResourceID), VersionID: string(draft.ID)}}, "systemretryapp", at)
	reject := scriptDecisionCommand(t, draft, submission, "REJECT")
	reject.At = at.Add(2 * time.Second)
	if _, err := repo.DecideReview(t.Context(), reject); err != nil {
		t.Fatal(err)
	}

	worker := NewScriptCascadeRepository(db)
	lease, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(3*time.Second))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err := worker.RecordCascadeFailure(t.Context(), *lease, "SYSTEM_PRINCIPAL_UNAVAILABLE", at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	task := readCascadeTask(t, db, reject.TaskID)
	if task.Status == "COMPLETE" || task.Processed != 0 || task.LastFailure != "SYSTEM_PRINCIPAL_UNAVAILABLE" || readReviewDocument(t, db, target.reviewID).Status != "PENDING" {
		t.Fatal("failure mutated target or disappeared")
	}

	lease, err = worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(9*time.Second))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	if err := worker.AdvanceCascade(t.Context(), *lease, "auth-resolved-system", at.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	if readCascadeTask(t, db, reject.TaskID).Status != "COMPLETE" || readReviewDocument(t, db, target.reviewID).Decision.DecidedBy != "auth-resolved-system" {
		t.Fatal("recovery lost SYSTEM identity")
	}
}

func TestScriptSubmissionRejectionOrderingIntegration_UCAPP044049_BRSDP003009(t *testing.T) {
	for _, order := range []string{"submission wins", "rejection wins"} {
		t.Run(order, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			repo, draft, submission := newScriptSubmissionFixture(t, db)
			if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
				t.Fatal(err)
			}
			at := submission.At.Add(3 * time.Second).Truncate(time.Millisecond)
			app, version := createReviewTestVersion(t, db, "ordering-owner", "orderingapp", "v1")
			replacement := integrationApplicationVersionReplacement(t, "v1", "https://example.edu/order", nil, nil, nil).WithScriptReferences([]shared.ScriptReference{{ResourceID: string(draft.ResourceID), VersionID: string(draft.ID)}})
			if _, err := NewApplicationVersionRepository(db).ReplaceDraft(t.Context(), app.ID(), version.ID(), "ordering-owner", 1, replacement, at); err != nil {
				t.Fatal(err)
			}
			submitter := NewApplicationReviewRepository(db)
			candidate := loadReviewCandidate(t, submitter, app.ID(), version.ID(), "ordering-owner", 2)
			paused, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			monitored, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
				if e.CommandName == "update" && e.Command.Lookup("update").StringValue() == scriptVersionsCollection {
					once.Do(func() {
						close(paused)
						select {
						case <-resume:
						case <-ctx.Done():
						}
					})
				}
			}}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = monitored.Disconnect(context.Background()) })
			command := scriptDecisionCommand(t, draft, submission, "REJECT")
			command.At = at.Add(2 * time.Second)
			loser := make(chan error, 1)
			runSubmission := func(writer *ApplicationReviewRepository) error {
				_, err := writer.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), "ordering-owner", 1, "v1", at.Add(time.Second))
				return err
			}
			if order == "submission wins" {
				go func() {
					_, err := NewScriptVersionRepository(monitored.Database(db.Name())).DecideReview(t.Context(), command)
					loser <- err
				}()
			} else {
				go func() { loser <- runSubmission(NewApplicationReviewRepository(monitored.Database(db.Name()))) }()
			}
			select {
			case <-paused:
			case <-time.After(10 * time.Second):
				t.Fatal("writer did not reach fence")
			}
			var winnerErr error
			if order == "submission wins" {
				winnerErr = runSubmission(submitter)
			} else {
				_, winnerErr = repo.DecideReview(t.Context(), command)
			}
			close(resume)
			if winnerErr != nil {
				t.Fatal(winnerErr)
			}
			loserErr := <-loser
			if order == "submission wins" {
				if loserErr != nil {
					t.Fatal(loserErr)
				}
			} else if !errors.Is(loserErr, shared.ErrScriptDependencyRejected) {
				t.Fatalf("late submission: %v", loserErr)
			}
			drainScriptCascades(t, db, at.Add(3*time.Second))
			task := readCascadeTask(t, db, command.TaskID)
			expected := int64(0)
			if order == "submission wins" {
				expected = 1
			}
			if task.Status != "COMPLETE" || task.Total != expected || task.Rejected != expected {
				t.Fatalf("ordering lost target: %+v", task)
			}
		})
	}
}

func TestScriptCascadeClosureOrderingIntegration_UCAPP049_BRSDP006009(t *testing.T) {
	for _, order := range []string{"closure wins", "cascade wins"} {
		t.Run(order, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			repo, draft, submission := newScriptSubmissionFixture(t, db)
			if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
				t.Fatal(err)
			}
			at := submission.At.Add(3 * time.Second).Truncate(time.Millisecond)
			seed := createScriptDependentReview(t, db, []shared.ScriptReference{{ResourceID: string(draft.ResourceID), VersionID: string(draft.ID)}}, "closureorderapp", at)
			reject := scriptDecisionCommand(t, draft, submission, "REJECT")
			reject.At = at.Add(2 * time.Second)
			if _, err := repo.DecideReview(t.Context(), reject); err != nil {
				t.Fatal(err)
			}
			worker := NewScriptCascadeRepository(db)
			lease, err := worker.ClaimCascade(t.Context(), nextIntegrationApplicationID(t).String(), at.Add(3*time.Second))
			if err != nil || lease == nil {
				t.Fatal(err)
			}
			paused, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			monitored, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
				if e.CommandName == "update" && e.Command.Lookup("update").StringValue() == applicationsCollectionName {
					once.Do(func() {
						close(paused)
						select {
						case <-resume:
						case <-ctx.Done():
						}
					})
				}
			}}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = monitored.Disconnect(context.Background()) })
			closureID := application.ApplicationClosureID(nextIntegrationApplicationID(t))
			proof := application.ApplicationCloseProof{Subject: seed.adminID, ApplicationID: seed.applicationID, JTI: nextIntegrationApplicationID(t).String(), AuthTime: at, IssuedAt: at, ExpiresAt: at.Add(time.Minute)}
			closeApp := func(database *dm.Database) error {
				_, err := NewApplicationClosureRepository(database).Start(t.Context(), seed.applicationID, seed.adminID, 1, 1, proof, closureID, at.Add(3*time.Second))
				return err
			}
			loser := make(chan error, 1)
			if order == "closure wins" {
				go func() {
					loser <- NewScriptCascadeRepository(monitored.Database(db.Name())).AdvanceCascade(t.Context(), *lease, "auth-system-cascade", at.Add(3*time.Second))
				}()
			} else {
				go func() { loser <- closeApp(monitored.Database(db.Name())) }()
			}
			select {
			case <-paused:
			case <-time.After(10 * time.Second):
				t.Fatal("writer did not reach Application fence")
			}
			var winnerErr error
			if order == "closure wins" {
				winnerErr = closeApp(db)
			} else {
				winnerErr = worker.AdvanceCascade(t.Context(), *lease, "auth-system-cascade", at.Add(3*time.Second))
			}
			close(resume)
			if winnerErr != nil {
				t.Fatal(winnerErr)
			}
			if err := <-loser; err != nil {
				t.Fatal(err)
			}
			task := readCascadeTask(t, db, reject.TaskID)
			stored := readReviewDocument(t, db, seed.reviewID)
			if task.Status != "COMPLETE" {
				t.Fatal("task stuck after close competition")
			}
			if order == "closure wins" {
				if task.Closed != 1 || stored.Status != "PENDING" || stored.Decision != nil {
					t.Fatal("closed history changed")
				}
			} else if task.Rejected != 1 || stored.Status != "REJECTED" {
				t.Fatal("winning rejection lost")
			}
		})
	}
}
