package mongo

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func scriptDecisionCommand(t *testing.T, draft domain.Version, submission port.SubmitDraft, action string) port.DecideReview {
	t.Helper()
	input := domain.DecisionInput{ExpectedRevision: 2, PolicyVersion: domain.InitialPolicyVersion, Action: action, Reason: "requires changes"}
	if action == "APPROVE" {
		input.Reason = ""
		for _, check := range domain.InitialReviewPolicy().Checks {
			input.ConfirmedCheckIDs = append(input.ConfirmedCheckIDs, check.ID)
		}
	}
	return port.DecideReview{ResourceID: draft.ResourceID, VersionID: draft.ID, ReviewID: submission.ReviewID, Input: input,
		DecisionID: nextIntegrationApplicationID(t).String(), TaskID: nextIntegrationApplicationID(t).String(), Actor: "independent-script-reviewer", At: submission.At.Add(time.Second)}
}

func TestScriptDecisionIntegration_UCAPP044_BRSCR010011012(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, submission := newScriptSubmissionFixture(t, db)
	if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	approve := scriptDecisionCommand(t, draft, submission, "APPROVE")
	for _, actor := range []string{"author", "submitter"} {
		input := approve
		input.Actor = shared.AuthID(actor)
		if _, err := repo.DecideReview(t.Context(), input); !errors.Is(err, domain.ErrReviewConflict) {
			t.Fatalf("conflict %s: %v", actor, err)
		}
	}
	for _, check := range []struct {
		name   string
		change func(*port.DecideReview)
		want   error
	}{
		{"missing check", func(c *port.DecideReview) { c.Input.ConfirmedCheckIDs = c.Input.ConfirmedCheckIDs[:3] }, domain.ErrChecksIncomplete},
		{"unknown check", func(c *port.DecideReview) { c.Input.ConfirmedCheckIDs = []string{"unknown"} }, domain.ErrChecksIncomplete},
		{"duplicate check", func(c *port.DecideReview) {
			c.Input.ConfirmedCheckIDs = append(append([]string{}, c.Input.ConfirmedCheckIDs...), c.Input.ConfirmedCheckIDs[0])
		}, domain.ErrChecksIncomplete},
		{"revision", func(c *port.DecideReview) { c.Input.ExpectedRevision = 1 }, domain.ErrRevisionConflict},
		{"policy", func(c *port.DecideReview) { c.Input.PolicyVersion = "other" }, domain.ErrPolicyMismatch},
	} {
		t.Run(check.name, func(t *testing.T) {
			input := approve
			check.change(&input)
			if _, err := repo.DecideReview(t.Context(), input); !errors.Is(err, check.want) {
				t.Fatalf("got %v want %v", err, check.want)
			}
		})
	}
	if _, err := db.Collection(scriptReviewPoliciesCollection).UpdateOne(t.Context(), bson.M{"version": domain.InitialPolicyVersion}, bson.M{"$set": bson.M{"status": scriptPolicyRetired}}); err != nil {
		t.Fatal(err)
	}
	result, err := repo.DecideReview(t.Context(), approve)
	if err != nil || result.Revision != 3 || result.Cascade != nil || result.Decision.Outcome != domain.VersionApproved {
		t.Fatalf("retired bound policy decision: %+v %v", result, err)
	}
	query := NewScriptQueryRepository(db, ScriptQueryCursorKey{1})
	detail, err := query.GetReview(t.Context(), draft.ResourceID, draft.ID, submission.ReviewID)
	if err != nil || detail.Review.Decision == nil || detail.Review.Decision.ID != approve.DecisionID {
		t.Fatalf("terminal readback: %+v %v", detail, err)
	}
	if _, err := query.GetVersion(t.Context(), port.ScriptReadScope{Review: true}, draft.ResourceID, draft.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DecideReview(t.Context(), approve); !errors.Is(err, domain.ErrReviewDecided) {
		t.Fatal("second decision", err)
	}
	assertCollectionCount(t, db, scriptCascadeTasksCollection, 0)
}

func TestScriptDecisionAtomicityIntegration_UCAPP044_BRSCR011012(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, submission := newScriptSubmissionFixture(t, db)
	if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	reject := scriptDecisionCommand(t, draft, submission, "REJECT")
	result, err := repo.DecideReview(t.Context(), reject)
	if err != nil || result.Cascade == nil || result.Cascade.Status != "PENDING" {
		t.Fatalf("durable ACK: %+v %v", result, err)
	}
	otherRepo, otherDraft, otherSubmission := newScriptSubmissionFixture(t, db)
	if _, err := otherRepo.SubmitDraft(t.Context(), otherSubmission); err != nil {
		t.Fatal(err)
	}
	collision := scriptDecisionCommand(t, otherDraft, otherSubmission, "REJECT")
	collision.TaskID = reject.TaskID
	if _, err := otherRepo.DecideReview(t.Context(), collision); !dm.IsDuplicateKeyError(err) {
		t.Fatalf("task persistence must fail: %v", err)
	}
	stored, err := otherRepo.loadVersion(t.Context(), otherDraft.ResourceID, otherDraft.ID)
	if err != nil || stored.State != domain.VersionSubmitted || stored.Revision != 2 {
		t.Fatalf("partial script rejection: %+v %v", stored, err)
	}
	query := NewScriptQueryRepository(db, ScriptQueryCursorKey{1})
	if detail, err := query.GetReview(t.Context(), otherDraft.ResourceID, otherDraft.ID, otherSubmission.ReviewID); err != nil || detail.Review.Status != domain.ReviewPending {
		t.Fatalf("partial review: %+v %v", detail, err)
	}
	start := make(chan struct{})
	outcomes := make(chan error, 2)
	var writers sync.WaitGroup
	for _, action := range []string{"APPROVE", "REJECT"} {
		cmd := scriptDecisionCommand(t, otherDraft, otherSubmission, action)
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			_, err := otherRepo.DecideReview(t.Context(), cmd)
			outcomes <- err
		}()
	}
	close(start)
	writers.Wait()
	close(outcomes)
	successes, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			successes++
		} else if errors.Is(err, domain.ErrReviewDecided) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("double terminal %d %d", successes, conflicts)
	}
}
