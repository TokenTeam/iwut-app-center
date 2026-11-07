package mongo

import (
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	scriptdomain "iwut-app-center/internal/script/domain"
	scriptport "iwut-app-center/internal/script/port"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

func TestScriptDependenciesIntegration_UCAPP048_BRSDP001002003004(t *testing.T) {
	ctx := t.Context()
	db := migratedIntegrationDatabase(t, integrationClient(t))
	scriptRepo, draft, scriptCommand := newScriptSubmissionFixture(t, db)
	ref := shared.ScriptReference{ResourceID: string(draft.ResourceID), VersionID: string(draft.ID)}
	app, version := createReviewTestVersion(t, db, "dependency-owner", "dependency-app", "v1")
	appWriter := NewApplicationVersionRepository(db)
	replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/dependency", nil, nil, nil).
		WithScriptReferences([]shared.ScriptReference{ref})
	if _, err := appWriter.ReplaceDraft(ctx, app.ID(), version.ID(), "dependency-owner", 1, replacement, time.Now()); !errors.Is(err, shared.ErrInvalidScriptReferences) {
		t.Fatalf("draft script selectable: %v", err)
	}
	if _, err := scriptRepo.SubmitDraft(ctx, scriptCommand); err != nil {
		t.Fatal(err)
	}
	current, err := appWriter.ReplaceDraft(ctx, app.ID(), version.ID(), "dependency-owner", 1, replacement, time.Now())
	if err != nil || current.Revision() != 2 || len(current.ScriptReferences()) != 1 {
		t.Fatalf("replace with submitted script: %v", err)
	}
	if _, err := appWriter.ReplaceDraft(ctx, app.ID(), version.ID(), "dependency-owner", 1, replacement, time.Now()); !errors.Is(err, versionport.ErrApplicationVersionRevisionConflict) {
		t.Fatalf("stale replacement: %v", err)
	}

	key, err := DeriveApplicationVersionManagementCursorKey([]byte("uc048-candidate-test"))
	if err != nil {
		t.Fatal(err)
	}
	readVersion := NewApplicationVersionManagementRepository(db, key)
	if _, err := readVersion.ListScriptCandidates(ctx, "other-user", app.ID(), 20, ""); !errors.Is(err, versionport.ErrManagementNotFound) {
		t.Fatalf("non-owner can enumerate candidates: %v", err)
	}
	page, err := readVersion.ListScriptCandidates(ctx, "dependency-owner", app.ID(), 20, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].Dependency.Digest != draft.Content.Digest || page.Items[0].Dependency.State != "SUBMITTED" {
		t.Fatalf("candidate list: %v", err)
	}
	submitter := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, submitter, app.ID(), version.ID(), "dependency-owner", 2)
	reviewID := nextIntegrationApplicationReviewID(t)
	fact := shared.FrozenScriptReference{ScriptReference: ref, Format: draft.Content.Format, Digest: draft.Content.Digest, ReviewID: string(scriptCommand.ReviewID)}
	if _, err := db.Collection(scriptDependenciesCollection).InsertOne(ctx, scriptDependencyDocument{
		AppReviewID: reviewID.String(), ApplicationID: app.ID().String(), AppVersionID: version.ID().String(), Fact: frozenScriptReferenceToDocument(fact),
	}); err != nil {
		t.Fatal(err)
	}
	now := scriptCommand.At.Add(time.Second).UTC().Truncate(time.Millisecond)
	if _, err := submitter.Submit(ctx, candidate, reviewID, "dependency-owner", 1, "v1", now); !dm.IsDuplicateKeyError(err) {
		t.Fatalf("expected rollback after reverse registration conflict: %v", err)
	}
	assertCollectionCount(t, db, applicationReviewsCollectionName, 0)
	if stored := readVersionDocument(t, db, version.ID().String()); stored.ReviewStatus != "DRAFT" || stored.Revision != 2 {
		t.Fatal("rollback changed app draft")
	}
	var scriptVersion scriptVersionDocument
	if err := db.Collection(scriptVersionsCollection).FindOne(ctx, bson.M{"versionId": ref.VersionID}).Decode(&scriptVersion); err != nil || scriptVersion.DependencyFence != nil {
		t.Fatalf("rollback changed script fence: %v", err)
	}
	if _, err := db.Collection(scriptDependenciesCollection).DeleteOne(ctx, bson.M{"appReviewId": reviewID}); err != nil {
		t.Fatal(err)
	}
	result, err := submitter.Submit(ctx, candidate, reviewID, "dependency-owner", 1, "v1", now)
	if err != nil || len(result.Review().ScriptDependencies()) != 1 || result.Review().ScriptDependencies()[0] != fact {
		t.Fatalf("snapshot did not freeze script dependency: %v", err)
	}
	assertCollectionCount(t, db, scriptDependenciesCollection, 1)
	if err := db.Collection(scriptVersionsCollection).FindOne(ctx, bson.M{"versionId": ref.VersionID}).Decode(&scriptVersion); err != nil ||
		scriptVersion.DependencyFence == nil || *scriptVersion.DependencyFence != 1 || scriptVersion.Revision != 2 {
		t.Fatalf("script fence must advance independently of script revision: %v", err)
	}
	query := NewApplicationReviewQueryRepository(db)
	versionID := reviewdomain.ApplicationVersionID(version.ID())
	related, err := query.GetScript(ctx, "app-reviewer", app.ID(), versionID, reviewID, ref.VersionID)
	if err != nil || related.Text != draft.Content.Text || len(related.Checks) != 4 {
		t.Fatalf("bound script detail: %v", err)
	}
	if _, err := query.GetScript(ctx, "app-reviewer", app.ID(), versionID, reviewID, nextIntegrationApplicationID(t).String()); !errors.Is(err, reviewport.ErrReviewQueryNotFound) {
		t.Fatalf("unrelated script became visible: %v", err)
	}
	decisionRepo := NewApplicationReviewDecisionRepository(db)
	decisionCandidate, err := decisionRepo.LoadDecisionCandidate(ctx, app.ID(), versionID, reviewID, "app-reviewer")
	if err != nil {
		t.Fatal(err)
	}
	approval := integrationApprovedDecision(t, "app-version-review-v3", formalVersionReviewPolicy().RequiredChecks, 1, "v1", "app-reviewer", now.Add(time.Second))
	if _, err := decisionRepo.Decide(ctx, decisionCandidate, "app-reviewer", approval); !errors.Is(err, shared.ErrScriptDependencyPending) {
		t.Fatalf("pending script should block approval: %v", err)
	}

	// UC044 will own the terminal transition. Seed an exact persisted decision
	// here to exercise UC048's approval consumption before UC044 is implemented.
	decisionID := seedTerminalScriptDependency(t, db, ref.VersionID, fact.ReviewID, "APPROVED", now.Add(500*time.Millisecond))

	approved, err := decisionRepo.Decide(ctx, decisionCandidate, "app-reviewer", approval)
	if err != nil {
		t.Fatal(err)
	}
	approvals := approved.Review().Decision().ApprovalValidation().ScriptApprovals()
	if len(approvals) != 1 || approvals[0] != (shared.ScriptApprovalFact{VersionID: ref.VersionID, Digest: fact.Digest, DecisionID: decisionID}) {
		t.Fatal("app approval did not record the exact script decision")
	}
}

func seedTerminalScriptDependency(t *testing.T, db *dm.Database, scriptVersionID, scriptReviewID, state string, scriptDecidedAt time.Time) string {
	t.Helper()

	var doc scriptVersionDocument
	if err := db.Collection(scriptVersionsCollection).FindOne(t.Context(), bson.M{"versionId": scriptVersionID}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	input := scriptdomain.DecisionInput{ExpectedRevision: doc.Revision, PolicyVersion: scriptdomain.InitialPolicyVersion, Action: "REJECT", Reason: "script requires changes"}
	if state == "APPROVED" {
		input.Action, input.Reason = "APPROVE", ""
		for _, check := range scriptdomain.InitialReviewPolicy().Checks {
			input.ConfirmedCheckIDs = append(input.ConfirmedCheckIDs, check.ID)
		}
	}
	result, err := NewScriptVersionRepository(db).DecideReview(t.Context(), scriptport.DecideReview{ResourceID: doc.ResourceID, VersionID: doc.VersionID,
		ReviewID: scriptdomain.ReviewID(scriptReviewID), Input: input, DecisionID: nextIntegrationApplicationID(t).String(),
		TaskID: nextIntegrationApplicationID(t).String(), Actor: "script-reviewer", At: scriptDecidedAt})
	if err != nil {
		t.Fatal(err)
	}
	return result.Decision.ID
}

func TestRejectedScriptDependenciesIntegration_UCAPP048_BRSDP003004005(t *testing.T) {
	ctx := t.Context()
	db := migratedIntegrationDatabase(t, integrationClient(t))
	scriptWriter, scriptDraft, scriptCommand := newScriptSubmissionFixture(t, db)
	if _, err := scriptWriter.SubmitDraft(ctx, scriptCommand); err != nil {
		t.Fatal(err)
	}
	ref := shared.ScriptReference{ResourceID: string(scriptDraft.ResourceID), VersionID: string(scriptDraft.ID)}
	const owner = "rejected-script-owner"
	const reviewer = "rejected-script-app-reviewer"
	app := createVersionTestApplication(t, db, owner, "rejected-script-app")
	appWriter := NewApplicationVersionRepository(db)
	createDraft := func(label string) reviewdomain.ApplicationVersionID {
		t.Helper()
		draft := integrationApplicationVersionDraft(t, app.ID(), owner, label).WithScriptReferences([]shared.ScriptReference{ref})
		version, err := appWriter.CreateDraft(ctx, owner, draft)
		if err != nil {
			t.Fatal(err)
		}
		return reviewdomain.ApplicationVersionID(version.ID())
	}
	before, after := createDraft("before"), createDraft("after")
	submitter := NewApplicationReviewRepository(db)
	reviewID := nextIntegrationApplicationReviewID(t)
	clock := scriptCommand.At.Add(time.Second).UTC().Truncate(time.Millisecond)
	first := loadReviewCandidate(t, submitter, app.ID(), before, owner, 1)
	if _, err := submitter.Submit(ctx, first, reviewID, owner, 1, "v1", clock); err != nil {
		t.Fatal(err)
	}
	seedTerminalScriptDependency(t, db, ref.VersionID, string(scriptCommand.ReviewID), "REJECTED", clock.Add(time.Second))

	second := loadReviewCandidate(t, submitter, app.ID(), after, owner, 1)
	if _, err := submitter.Submit(ctx, second, nextIntegrationApplicationReviewID(t), owner, 1, "v1", clock.Add(2*time.Second)); !errors.Is(err, shared.ErrScriptDependencyRejected) {
		t.Fatalf("rejected script registered a new review: %v", err)
	}
	decider := NewApplicationReviewDecisionRepository(db)
	candidate, err := decider.LoadDecisionCandidate(ctx, app.ID(), before, reviewID, reviewer)
	if err != nil {
		t.Fatal(err)
	}
	approve := integrationApprovedDecision(t, "app-version-review-v3", formalVersionReviewPolicy().RequiredChecks, 1, "v1", reviewer, clock.Add(3*time.Second))
	if _, err := decider.Decide(ctx, candidate, reviewer, approve); !errors.Is(err, shared.ErrScriptDependencyRejected) {
		t.Fatalf("rejected script approved: %v", err)
	}
	reject := integrationRejectedDecision(t, "app-version-review-v3", "needs changes", reviewer, clock.Add(4*time.Second))
	if _, err := decider.Decide(ctx, candidate, reviewer, reject); err != nil {
		t.Fatalf("dependency gate blocked manual rejection: %v", err)
	}
	if _, err := NewApplicationReviewRestorationRepository(db).RestoreDraft(ctx, app.ID(), before, reviewID, owner, 3, clock.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	key, err := DeriveApplicationVersionManagementCursorKey([]byte("uc048-restoration-test"))
	if err != nil {
		t.Fatal(err)
	}
	reader := NewApplicationVersionManagementRepository(db, key)
	current, err := reader.Get(ctx, owner, app.ID(), versiondomain.ApplicationVersionID(before))
	if err != nil || len(current.Version.ScriptReferences) != 1 || current.Version.ScriptReferences[0].State != "REJECTED" {
		t.Fatalf("restored draft did not retain rejected script metadata: %v", err)
	}
	replacement := integrationApplicationVersionReplacement(t, "before", "https://example.edu/fixed", nil, nil, nil)
	updated, err := appWriter.ReplaceDraft(ctx, app.ID(), versiondomain.ApplicationVersionID(before), owner, 4, replacement, clock.Add(6*time.Second))
	if err != nil || updated.Revision() != 5 || len(updated.ScriptReferences()) != 0 {
		t.Fatalf("clear script references: %v", err)
	}
	restored := loadReviewCandidate(t, submitter, app.ID(), before, owner, 5)
	resubmitted, err := submitter.Submit(ctx, restored, nextIntegrationApplicationReviewID(t), owner, 1, "v1", clock.Add(7*time.Second))
	if err != nil || resubmitted.Review().Attempt() != 2 || len(resubmitted.Review().ScriptDependencies()) != 0 {
		t.Fatalf("resubmission inherited references: %v", err)
	}
	history := readReviewDocument(t, db, reviewID)
	if len(history.Snapshot.ScriptDependencies) != 1 || history.Snapshot.ScriptDependencies[0].Reference.VersionID != ref.VersionID {
		t.Fatal("resubmission changed frozen review history")
	}
	assertCollectionCount(t, db, scriptDependenciesCollection, 1)
}
