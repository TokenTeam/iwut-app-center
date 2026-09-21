package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

func TestApplicationReviewDecision_BR_REV_012_PendingReviewDecidesExactlyOnce(t *testing.T) {
	t.Parallel()
	review := pendingReviewForDecision(t)
	policyVersion := mustPolicyVersion(t, "review.v1")
	validation := mustApprovalValidation(t, 31, "public-https.v1")
	decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)

	decision, err := review.Approve(
		policyVersion, []ReviewCheckID{"content-reviewed"}, "", validation, "auth-reviewer", decidedAt,
	)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if decision.Outcome() != ReviewDecisionApproved || review.Status() != ReviewStatusApproved || !review.HasDecision() {
		t.Fatalf("approved review = status:%s decision:%#v", review.Status(), review.Decision())
	}
	if _, err := review.Approve(policyVersion, []ReviewCheckID{"content-reviewed"}, "", validation, "auth-reviewer", decidedAt); !errors.Is(err, ErrApplicationReviewAlreadyDecided) {
		t.Fatalf("second Approve() error = %v, want already decided", err)
	}
	if _, err := review.Reject(policyVersion, "no longer relevant", "auth-reviewer", decidedAt); !errors.Is(err, ErrApplicationReviewAlreadyDecided) {
		t.Fatalf("Reject() after Approve() error = %v, want already decided", err)
	}
	if review.Status() != ReviewStatusApproved || review.Decision().Outcome() != ReviewDecisionApproved {
		t.Fatal("a rejected second decision mutated the stored decision")
	}
}

func TestApplicationReviewDecision_BR_REV_015_RejectRequiresValidReason(t *testing.T) {
	t.Parallel()
	policyVersion := mustPolicyVersion(t, "review.v1")
	decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	tests := []struct {
		name    string
		reason  string
		wantErr bool
	}{
		{name: "valid", reason: "请求的课表读取权限未说明用途。", wantErr: false},
		{name: "empty", reason: "", wantErr: true},
		{name: "whitespace only", reason: "   ", wantErr: true},
		{name: "leading whitespace", reason: " reason", wantErr: true},
		{name: "trailing whitespace", reason: "reason ", wantErr: true},
		{name: "control character", reason: "reason\nvalue", wantErr: true},
		{name: "too long", reason: string(make([]rune, 2001)), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			review := pendingReviewForDecision(t)
			decision, err := review.Reject(policyVersion, test.reason, "auth-reviewer", decidedAt)
			if test.wantErr {
				if decision != nil || !errors.Is(err, ErrInvalidApplicationReviewReason) {
					t.Fatalf("Reject() = (%v, %v), want nil invalid reason", decision, err)
				}
				return
			}
			if err != nil || decision == nil {
				t.Fatalf("Reject() error = %v", err)
			}
			if decision.Reason() != test.reason || decision.Outcome() != ReviewDecisionRejected ||
				decision.ApprovalValidation() != nil || len(decision.ConfirmedCheckIDs()) != 0 {
				t.Fatalf("rejected decision = %#v", decision)
			}
		})
	}
}

func TestApplicationReviewDecision_BR_REV_015_SystemRejectionReasonIsValidAndApproveReasonIsOptional(t *testing.T) {
	t.Parallel()
	if err := ValidateReviewReason(SystemSuspensionRejectionReason); err != nil {
		t.Fatalf("system rejection reason is invalid: %v", err)
	}
	review := pendingReviewForDecision(t)
	policyVersion := mustPolicyVersion(t, "review.v1")
	validation := mustApprovalValidation(t, 31, "public-https.v1")
	decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)

	if _, err := review.Approve(policyVersion, []ReviewCheckID{"content-reviewed"}, "note", validation, "auth-reviewer", decidedAt); err != nil {
		t.Fatalf("Approve() with note error = %v", err)
	}
	if got := review.Decision().Reason(); got != "note" {
		t.Fatalf("reason = %q, want note", got)
	}
}

func TestVersionReviewPolicy_BR_REV_016_017_ValidateConfirmation(t *testing.T) {
	t.Parallel()
	policy := mustPolicy(t, "review.v1", ReviewPolicyStatusActive, "content-reviewed", "launch-url-reviewed", "scopes-reviewed")

	normalized, err := policy.ValidateConfirmation([]ReviewCheckID{"scopes-reviewed", "content-reviewed", "launch-url-reviewed"})
	if err != nil {
		t.Fatalf("ValidateConfirmation() error = %v", err)
	}
	want := []ReviewCheckID{"content-reviewed", "launch-url-reviewed", "scopes-reviewed"}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("normalized = %v, want %v", normalized, want)
	}

	tests := []struct {
		name      string
		confirmed []ReviewCheckID
	}{
		{name: "missing required", confirmed: []ReviewCheckID{"content-reviewed"}},
		{name: "unknown", confirmed: []ReviewCheckID{"content-reviewed", "launch-url-reviewed", "unknown-check"}},
		{name: "duplicate", confirmed: []ReviewCheckID{"content-reviewed", "content-reviewed", "launch-url-reviewed", "scopes-reviewed"}},
		{name: "empty", confirmed: []ReviewCheckID{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.ValidateConfirmation(test.confirmed)
			if got != nil || !errors.Is(err, ErrApplicationReviewChecksIncomplete) {
				t.Fatalf("ValidateConfirmation() = (%v, %v), want nil incomplete", got, err)
			}
		})
	}

	if policy.Usable() != true || policy.Version() != "review.v1" || policy.Status() != ReviewPolicyStatusActive {
		t.Fatal("active policy is not usable")
	}
	retired := mustPolicy(t, "review.v0", ReviewPolicyStatusRetired, "content-reviewed")
	if retired.Usable() {
		t.Fatal("retired policy reported usable")
	}
}

func TestApplicationReviewDecision_BR_REV_012_016_InvalidInputs(t *testing.T) {
	t.Parallel()
	if _, err := NewReviewPolicyVersion("bad version"); !errors.Is(err, ErrInvalidApplicationReviewPolicyVersion) {
		t.Fatalf("invalid policy version error = %v", err)
	}
	if _, err := NewReviewAction("MAYBE"); !errors.Is(err, ErrInvalidApplicationReviewOutcome) {
		t.Fatalf("invalid action error = %v", err)
	}
	policyVersion := mustPolicyVersion(t, "review.v1")
	decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	review := pendingReviewForDecision(t)
	if _, err := review.Approve(policyVersion, []ReviewCheckID{"content-reviewed"}, "", nil, "auth-reviewer", decidedAt); !errors.Is(err, ErrInternal) {
		t.Fatalf("Approve() without approval validation error = %v, want internal", err)
	}
	reviewAgain := pendingReviewForDecision(t)
	if _, err := reviewAgain.Reject(policyVersion, "reason", "auth-reviewer", time.Time{}); !errors.Is(err, ErrInternal) {
		t.Fatalf("Reject() without decidedAt error = %v, want internal", err)
	}
}

func TestApplicationReviewDecision_BR_REV_012_013_DecisionIsImmutable(t *testing.T) {
	t.Parallel()
	review := pendingReviewForDecision(t)
	policyVersion := mustPolicyVersion(t, "review.v1")
	validation := mustApprovalValidation(t, 31, "public-https.v1")
	decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	if _, err := review.Approve(policyVersion, []ReviewCheckID{"content-reviewed"}, "", validation, "auth-reviewer", decidedAt); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}

	decision := review.Decision()
	decision.confirmedCheckIDs[0] = "mutated"
	decision.reason = "mutated"
	decision.approvalValidation.scopeCatalogRevision = 999
	if got := review.Decision(); got.ConfirmedCheckIDs()[0] != "content-reviewed" || got.Reason() != "" ||
		got.ApprovalValidation().ScopeCatalogRevision() != 31 {
		t.Fatalf("decision getter exposed mutable state: %#v", got)
	}
}

func TestApplicationReviewDecisionCandidate_BR_REV_011_013_ConflictsAndState(t *testing.T) {
	t.Parallel()
	review := pendingReviewForDecision(t)
	snapshot := review.Snapshot()
	candidate, err := NewApplicationReviewDecisionCandidate(review, SubmittedVersionReviewStatus, 8, "auth-creator", &snapshot, "auth-admin")
	if err != nil {
		t.Fatalf("NewApplicationReviewDecisionCandidate() error = %v", err)
	}
	if candidate.ReviewerConflicts("auth-admin") != true ||
		candidate.ReviewerConflicts("auth-creator") != true ||
		candidate.ReviewerConflicts("auth-submitter") != true ||
		candidate.ReviewerConflicts("auth-reviewer") != false {
		t.Fatal("conflict of interest checks are wrong")
	}
	if candidate.VersionRevision() != 8 || candidate.CurrentAdminID() != "auth-admin" {
		t.Fatalf("candidate facts = revision:%d admin:%s", candidate.VersionRevision(), candidate.CurrentAdminID())
	}
	if want := []ScopeName{"profile.basic", "schedule.read"}; !reflect.DeepEqual(candidate.AllScopes(), want) {
		t.Fatalf("candidate scopes = %v, want %v", candidate.AllScopes(), want)
	}

	otherSnapshot, err := NewApplicationVersionReviewSnapshot("v3", "https://example.edu/other", 1, 3, []string{"camera.read.v1", "user.profile.v1"}, []ScopeName{"profile.basic"}, []ScopeName{"schedule.read"})
	if err != nil {
		t.Fatalf("create other snapshot: %v", err)
	}
	tests := []struct {
		name      string
		status    string
		revision  int64
		snapshot  *ApplicationVersionReviewSnapshot
		createdBy shared.AuthID
		adminID   shared.AuthID
	}{
		{name: "wrong status", status: "DRAFT", revision: 8, snapshot: &snapshot, createdBy: "auth-creator", adminID: "auth-admin"},
		{name: "wrong revision", status: SubmittedVersionReviewStatus, revision: 7, snapshot: &snapshot, createdBy: "auth-creator", adminID: "auth-admin"},
		{name: "different snapshot", status: SubmittedVersionReviewStatus, revision: 8, snapshot: otherSnapshot, createdBy: "auth-creator", adminID: "auth-admin"},
		{name: "missing creator", status: SubmittedVersionReviewStatus, revision: 8, snapshot: &snapshot, createdBy: "", adminID: "auth-admin"},
		{name: "missing admin", status: SubmittedVersionReviewStatus, revision: 8, snapshot: &snapshot, createdBy: "auth-creator", adminID: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := NewApplicationReviewDecisionCandidate(review, test.status, test.revision, test.createdBy, test.snapshot, test.adminID)
			if got != nil || !errors.Is(err, ErrInternal) {
				t.Fatalf("candidate = (%v, %v), want nil internal", got, err)
			}
		})
	}
}

func TestApplicationReviewDecisionResult_BR_REV_013_014_VersionRevisionAndAudit(t *testing.T) {
	t.Parallel()
	review := pendingReviewForDecision(t)
	snapshot := review.Snapshot()
	candidate, err := NewApplicationReviewDecisionCandidate(review, SubmittedVersionReviewStatus, 8, "auth-creator", &snapshot, "auth-admin")
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	policyVersion := mustPolicyVersion(t, "review.v1")
	validation := mustApprovalValidation(t, 31, "public-https.v1")
	decidedAt := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	decision, err := review.Approve(policyVersion, []ReviewCheckID{"content-reviewed"}, "", validation, "auth-reviewer", decidedAt)
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	version, err := NewDecidedApplicationVersion(candidate, decision)
	if err != nil {
		t.Fatalf("NewDecidedApplicationVersion() error = %v", err)
	}
	if version.ReviewStatus() != ReviewDecisionApproved || version.Revision() != 9 ||
		version.UpdatedBy() != "auth-reviewer" || !version.UpdatedAt().Equal(decidedAt) {
		t.Fatalf("decided version = %#v", version)
	}
	result, err := NewApplicationReviewDecisionResult(review, version)
	if err != nil {
		t.Fatalf("NewApplicationReviewDecisionResult() error = %v", err)
	}
	if result.Review().Status() != ReviewStatusApproved || result.Version().Revision() != 9 {
		t.Fatal("unexpected decision result")
	}
}

func pendingReviewForDecision(t *testing.T) *ApplicationReview {
	t.Helper()
	candidate := validCandidate(t)
	attempt, _ := NewReviewAttempt(1)
	catalogRevision, _ := NewScopeCatalogRevision(11)
	preflightPolicyVersion, _ := NewPreflightPolicyVersion("public-https.v1")
	review, err := NewPendingApplicationReview(
		candidate, testReviewID, attempt, catalogRevision, preflightPolicyVersion,
		"auth-submitter", time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create pending review: %v", err)
	}
	return review
}

func mustPolicyVersion(t *testing.T, value string) ReviewPolicyVersion {
	t.Helper()
	version, err := NewReviewPolicyVersion(value)
	if err != nil {
		t.Fatalf("create policy version: %v", err)
	}
	return version
}

func mustApprovalValidation(t *testing.T, revision int64, preflight string) *ApprovalValidation {
	t.Helper()
	scopeCatalogRevision, err := NewScopeCatalogRevision(revision)
	if err != nil {
		t.Fatalf("create scope catalog revision: %v", err)
	}
	policyVersion, err := NewPreflightPolicyVersion(preflight)
	if err != nil {
		t.Fatalf("create preflight policy version: %v", err)
	}
	validation, err := NewApprovalValidation(scopeCatalogRevision, policyVersion)
	if err != nil {
		t.Fatalf("create approval validation: %v", err)
	}
	return validation
}

func mustPolicy(t *testing.T, version string, status ReviewPolicyStatus, checkIDs ...string) *VersionReviewPolicy {
	t.Helper()
	definitions := make([]ReviewCheckDefinition, len(checkIDs))
	for index, value := range checkIDs {
		id, err := NewReviewCheckID(value)
		if err != nil {
			t.Fatalf("create check ID: %v", err)
		}
		definition, err := NewReviewCheckDefinition(id)
		if err != nil {
			t.Fatalf("create check definition: %v", err)
		}
		definitions[index] = definition
	}
	policy, err := NewVersionReviewPolicy(mustPolicyVersion(t, version), definitions, status)
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	return policy
}
