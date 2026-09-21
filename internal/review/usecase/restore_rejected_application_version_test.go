package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

type fakeRestorationRepository struct {
	result           *domain.RestoreRejectedVersionResult
	err              error
	calls            int
	applicationID    shared.ApplicationID
	versionID        domain.ApplicationVersionID
	reviewID         domain.ApplicationReviewID
	adminID          shared.AuthID
	expectedRevision int64
	restoredAt       time.Time
}

func (fake *fakeRestorationRepository) RestoreDraft(
	_ context.Context,
	applicationID shared.ApplicationID,
	versionID domain.ApplicationVersionID,
	reviewID domain.ApplicationReviewID,
	adminID shared.AuthID,
	expectedRevision int64,
	restoredAt time.Time,
) (*domain.RestoreRejectedVersionResult, error) {
	fake.calls++
	fake.applicationID, fake.versionID, fake.reviewID = applicationID, versionID, reviewID
	fake.adminID, fake.expectedRevision, fake.restoredAt = adminID, expectedRevision, restoredAt
	return fake.result, fake.err
}

func TestRestoreRejectedApplicationVersion_BR_REV_021_023_028_Success(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 21, 14, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	repository := &fakeRestorationRepository{result: restorationResult(t, now.UTC())}
	clock := &fakeClock{now: now}
	handler := NewRestoreRejectedApplicationVersionHandler(clock, repository)

	result, err := handler.Handle(
		t.Context(), approvedIdentity(), applicationID, versionID, reviewID,
		RestoreRejectedApplicationVersionCommand{ExpectedVersionRevision: 9},
	)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if result == nil || repository.calls != 1 || repository.applicationID != applicationID || repository.versionID != versionID ||
		repository.reviewID != reviewID || repository.adminID != approvedIdentity().AuthID || repository.expectedRevision != 9 ||
		!repository.restoredAt.Equal(now) || repository.restoredAt.Location() != time.UTC || clock.calls != 1 {
		t.Fatalf("repository call = %#v, clock calls = %d", repository, clock.calls)
	}
}

func TestRestoreRejectedApplicationVersion_BR_REV_021_023_ValidatesBeforeRepository(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		identity shared.DeveloperIdentity
		appID    shared.ApplicationID
		verID    domain.ApplicationVersionID
		revID    domain.ApplicationReviewID
		revision int64
		want     error
	}{
		{name: "identity", identity: shared.DeveloperIdentity{}, appID: applicationID, verID: versionID, revID: reviewID, revision: 9, want: domain.ErrDeveloperIdentityRequired},
		{name: "approval", identity: shared.DeveloperIdentity{AuthID: "auth-admin", DeveloperStatus: shared.DeveloperStatusPending}, appID: applicationID, verID: versionID, revID: reviewID, revision: 9, want: domain.ErrDeveloperApprovalRequired},
		{name: "revision", identity: approvedIdentity(), appID: applicationID, verID: versionID, revID: reviewID, revision: 0, want: domain.ErrApplicationVersionRevisionRequired},
		{name: "path", identity: approvedIdentity(), appID: "bad", verID: versionID, revID: reviewID, revision: 9, want: domain.ErrApplicationReviewNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRestorationRepository{}
			clock := &fakeClock{now: time.Now()}
			handler := NewRestoreRejectedApplicationVersionHandler(clock, repository)
			result, err := handler.Handle(t.Context(), test.identity, test.appID, test.verID, test.revID, RestoreRejectedApplicationVersionCommand{ExpectedVersionRevision: test.revision})
			if result != nil || !errors.Is(err, test.want) || repository.calls != 0 || clock.calls != 0 {
				t.Fatalf("Handle() = (%v, %v), repository=%d clock=%d, want %v", result, err, repository.calls, clock.calls, test.want)
			}
		})
	}
}

func TestRestoreRejectedApplicationVersion_BR_REV_021_022_023_026_MapsRepositoryOutcomes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  error
		want error
	}{
		{name: "not found", got: port.ErrApplicationReviewNotFound, want: domain.ErrApplicationReviewNotFound},
		{name: "admin", got: port.ErrApplicationAdminRequired, want: domain.ErrApplicationAdminRequired},
		{name: "latest", got: port.ErrApplicationReviewNotLatest, want: domain.ErrApplicationReviewNotLatest},
		{name: "already", got: port.ErrApplicationReviewAlreadyRestored, want: domain.ErrApplicationReviewAlreadyRestored},
		{name: "rejected", got: port.ErrApplicationVersionNotRejected, want: domain.ErrApplicationVersionNotRejected},
		{name: "revision", got: port.ErrApplicationVersionRevisionConflict, want: domain.ErrApplicationVersionRevisionConflict},
		{name: "state", got: port.ErrApplicationReviewStateInconsistent, want: domain.ErrApplicationReviewStateInconsistent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRestorationRepository{err: test.got}
			handler := NewRestoreRejectedApplicationVersionHandler(&fakeClock{now: time.Now()}, repository)
			result, err := handler.Handle(t.Context(), approvedIdentity(), applicationID, versionID, reviewID, RestoreRejectedApplicationVersionCommand{ExpectedVersionRevision: 9})
			if result != nil || !errors.Is(err, test.want) || repository.calls != 1 {
				t.Fatalf("Handle() = (%v, %v), calls=%d, want %v", result, err, repository.calls, test.want)
			}
		})
	}
}

func restorationResult(t *testing.T, restoredAt time.Time) *domain.RestoreRejectedVersionResult {
	t.Helper()
	snapshot, _ := domain.NewApplicationVersionReviewSnapshot("v1", "https://example.edu/app", 1, 3, []string{}, []domain.ScopeName{}, []domain.ScopeName{})
	candidate, _ := domain.NewSubmissionCandidate(applicationID, versionID, 7, snapshot)
	attempt, _ := domain.NewReviewAttempt(1)
	catalogRevision, _ := domain.NewScopeCatalogRevision(1)
	preflightPolicy, _ := domain.NewPreflightPolicyVersion("public-https.v1")
	review, _ := domain.NewPendingApplicationReview(candidate, reviewID, attempt, catalogRevision, preflightPolicy, "auth-admin", restoredAt.Add(-2*time.Hour))
	reviewPolicy, _ := domain.NewReviewPolicyVersion("review.v1")
	if _, err := review.Reject(reviewPolicy, "reason", "auth-reviewer", restoredAt.Add(-time.Hour)); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if _, err := review.RecordDraftRestoration("auth-admin", restoredAt, 10); err != nil {
		t.Fatalf("RecordDraftRestoration() error = %v", err)
	}
	version, _ := domain.NewRestoredApplicationVersion(applicationID, versionID, 10, "auth-admin", restoredAt)
	result, err := domain.NewRestoreRejectedVersionResult(review, version)
	if err != nil {
		t.Fatalf("NewRestoreRejectedVersionResult() error = %v", err)
	}
	return result
}
