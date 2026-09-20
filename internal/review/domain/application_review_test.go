package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

const (
	testApplicationID shared.ApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"
	testVersionID     ApplicationVersionID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c22"
	testReviewID      ApplicationReviewID  = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c23"
)

func TestApplicationReview_BR_REV_003_004_005_006_007_ImmutablePendingAttempt(t *testing.T) {
	t.Parallel()
	candidate := validCandidate(t)
	attempt, _ := NewReviewAttempt(2)
	catalogRevision, _ := NewScopeCatalogRevision(17)
	policyVersion, _ := NewPreflightPolicyVersion("public-https.v1")
	submittedAt := time.Date(2026, time.September, 20, 12, 30, 0, 123, time.FixedZone("CST", 8*60*60))

	review, err := NewPendingApplicationReview(
		candidate, testReviewID, attempt, catalogRevision, policyVersion, "auth-2", submittedAt,
	)
	if err != nil {
		t.Fatalf("NewPendingApplicationReview() error = %v", err)
	}
	if review.ReviewID() != testReviewID || review.ApplicationID() != testApplicationID || review.VersionID() != testVersionID {
		t.Fatalf("review identity = (%s, %s, %s)", review.ReviewID(), review.ApplicationID(), review.VersionID())
	}
	if review.Attempt() != 2 || review.SourceVersionRevision() != 7 || review.Status() != ReviewStatusPending {
		t.Fatalf("review state = (attempt=%d source=%d status=%s)", review.Attempt(), review.SourceVersionRevision(), review.Status())
	}
	if review.HasDecision() || review.HasDraftRestoration() {
		t.Fatal("new review has decision or draft restoration")
	}
	if review.ScopeCatalogRevision() != 17 || review.PreflightPolicyVersion() != "public-https.v1" || review.SubmittedBy() != "auth-2" {
		t.Fatal("review validation or submit audit fields differ from supplied system facts")
	}
	if !review.SubmittedAt().Equal(submittedAt) || review.SubmittedAt().Location() != time.UTC {
		t.Fatalf("submittedAt = %v, want same UTC instant", review.SubmittedAt())
	}

	snapshot := review.Snapshot()
	if snapshot.VersionLabel() != "v2" || snapshot.LaunchURL() != "https://example.edu/app" ||
		snapshot.RPCAPIMinVersion() != 1 || snapshot.RPCAPIMaxVersionExclusive() != 3 {
		t.Fatalf("snapshot scalar fields changed: %#v", snapshot)
	}
	capabilities := snapshot.RequiredCapabilities()
	required := snapshot.RequiredScopes()
	optional := snapshot.OptionalScopes()
	capabilities[0] = "changed.v1"
	required[0] = "changed"
	optional[0] = "changed"
	if got := review.Snapshot(); !reflect.DeepEqual(got.RequiredCapabilities(), []string{"camera.read.v1", "user.profile.v1"}) ||
		!reflect.DeepEqual(got.RequiredScopes(), []ScopeName{"profile.basic"}) ||
		!reflect.DeepEqual(got.OptionalScopes(), []ScopeName{"schedule.read"}) {
		t.Fatal("snapshot getter exposed mutable collection state")
	}
}

func TestApplicationReview_BR_REV_003_SubmissionResultIncrementsWholeVersionRevisionAndAudit(t *testing.T) {
	t.Parallel()
	candidate := validCandidate(t)
	attempt, _ := NewReviewAttempt(1)
	catalogRevision, _ := NewScopeCatalogRevision(21)
	policyVersion, _ := NewPreflightPolicyVersion("v1")
	submittedAt := time.Date(2026, time.September, 20, 4, 5, 6, 0, time.UTC)
	review, _ := NewPendingApplicationReview(candidate, testReviewID, attempt, catalogRevision, policyVersion, "auth-1", submittedAt)
	version, err := NewSubmittedApplicationVersion(candidate, "auth-1", submittedAt)
	if err != nil {
		t.Fatalf("NewSubmittedApplicationVersion() error = %v", err)
	}
	result, err := NewReviewSubmissionResult(review, version)
	if err != nil {
		t.Fatalf("NewReviewSubmissionResult() error = %v", err)
	}
	got := result.Version()
	if got.ReviewStatus() != "SUBMITTED" || got.Revision() != 8 || got.UpdatedBy() != "auth-1" || !got.UpdatedAt().Equal(submittedAt) {
		t.Fatalf("submitted version = status:%s revision:%d by:%s at:%v", got.ReviewStatus(), got.Revision(), got.UpdatedBy(), got.UpdatedAt())
	}
}

func TestApplicationReview_BR_REV_004_NormalizedSnapshotRequired(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		capabilities []string
		required     []ScopeName
		optional     []ScopeName
	}{
		{name: "unsorted capabilities", capabilities: []string{"z.v1", "a.v1"}, required: []ScopeName{}, optional: []ScopeName{}},
		{name: "duplicate required scope", capabilities: []string{}, required: []ScopeName{"profile", "profile"}, optional: []ScopeName{}},
		{name: "overlapping scopes", capabilities: []string{}, required: []ScopeName{"profile"}, optional: []ScopeName{"profile"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot, err := NewApplicationVersionReviewSnapshot("v1", "https://example.edu", 1, 2, test.capabilities, test.required, test.optional)
			if snapshot != nil || !errors.Is(err, ErrInternal) {
				t.Fatalf("snapshot = (%v, %v), want nil Internal", snapshot, err)
			}
		})
	}
}

func TestApplicationReview_BR_REV_005_AttemptStartsAtOne(t *testing.T) {
	t.Parallel()
	if _, err := NewReviewAttempt(0); !errors.Is(err, ErrInternal) {
		t.Fatalf("NewReviewAttempt(0) error = %v, want Internal", err)
	}
}

func validCandidate(t *testing.T) *SubmissionCandidate {
	t.Helper()
	snapshot, err := NewApplicationVersionReviewSnapshot(
		"v2", "https://example.edu/app", 1, 3,
		[]string{"camera.read.v1", "user.profile.v1"},
		[]ScopeName{"profile.basic"}, []ScopeName{"schedule.read"},
	)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	candidate, err := NewSubmissionCandidate(testApplicationID, testVersionID, 7, snapshot)
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	return candidate
}
