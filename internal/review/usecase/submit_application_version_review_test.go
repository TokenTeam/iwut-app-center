package usecase

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

const (
	applicationID shared.ApplicationID        = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"
	versionID     domain.ApplicationVersionID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c22"
	reviewID      domain.ApplicationReviewID  = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c23"
)

type fakeRepository struct {
	events                   *[]string
	candidate                *domain.SubmissionCandidate
	loadErr                  error
	submitErr                error
	loadCalls                int
	submitCalls              int
	loadedApplicationID      shared.ApplicationID
	loadedVersionID          domain.ApplicationVersionID
	loadedAdminID            shared.AuthID
	loadedRevision           int64
	submittedReviewID        domain.ApplicationReviewID
	submittedAdminID         shared.AuthID
	submittedCatalogRevision domain.ScopeCatalogRevision
	submittedPolicyVersion   domain.PreflightPolicyVersion
	submittedAt              time.Time
}

func (fake *fakeRepository) LoadSubmissionCandidate(_ context.Context, applicationID shared.ApplicationID, versionID domain.ApplicationVersionID, adminID shared.AuthID, revision int64) (*domain.SubmissionCandidate, error) {
	fake.loadCalls++
	fake.loadedApplicationID, fake.loadedVersionID, fake.loadedAdminID, fake.loadedRevision = applicationID, versionID, adminID, revision
	appendEvent(fake.events, "candidate")
	if fake.loadErr != nil {
		return nil, fake.loadErr
	}
	return fake.candidate, nil
}

func (fake *fakeRepository) Submit(_ context.Context, candidate *domain.SubmissionCandidate, id domain.ApplicationReviewID, adminID shared.AuthID, catalogRevision domain.ScopeCatalogRevision, policyVersion domain.PreflightPolicyVersion, submittedAt time.Time) (*domain.ReviewSubmissionResult, error) {
	fake.submitCalls++
	fake.submittedReviewID, fake.submittedAdminID = id, adminID
	fake.submittedCatalogRevision, fake.submittedPolicyVersion, fake.submittedAt = catalogRevision, policyVersion, submittedAt
	appendEvent(fake.events, "submit")
	if fake.submitErr != nil {
		return nil, fake.submitErr
	}
	attempt, _ := domain.NewReviewAttempt(1)
	review, _ := domain.NewPendingApplicationReview(candidate, id, attempt, catalogRevision, policyVersion, adminID, submittedAt)
	version, _ := domain.NewSubmittedApplicationVersion(candidate, adminID, submittedAt)
	return domain.NewReviewSubmissionResult(review, version)
}

type fakeCatalog struct {
	events   *[]string
	revision domain.ScopeCatalogRevision
	err      error
	calls    int
	scopes   []domain.ScopeName
}

func (fake *fakeCatalog) EnsureAllRequestable(_ context.Context, scopes []domain.ScopeName) (domain.ScopeCatalogRevision, error) {
	fake.calls++
	fake.scopes = append([]domain.ScopeName{}, scopes...)
	appendEvent(fake.events, "scopes")
	return fake.revision, fake.err
}

type fakePolicy struct {
	events    *[]string
	version   domain.PreflightPolicyVersion
	err       error
	calls     int
	launchURL domain.LaunchURL
}

type fakeOAuthPolicy struct {
	events       *[]string
	err          error
	calls        int
	pkce         []string
	confidential []string
}

func (fake *fakeOAuthPolicy) Validate(pkce, confidential []string) error {
	fake.calls++
	fake.pkce = append([]string{}, pkce...)
	fake.confidential = append([]string{}, confidential...)
	appendEvent(fake.events, "oauth")
	return fake.err
}

func (fake *fakePolicy) Inspect(_ context.Context, launchURL domain.LaunchURL) (domain.PreflightPolicyVersion, error) {
	fake.calls++
	fake.launchURL = launchURL
	appendEvent(fake.events, "preflight")
	return fake.version, fake.err
}

type fakeIDGenerator struct {
	events *[]string
	id     domain.ApplicationReviewID
	err    error
	calls  int
}

func (fake *fakeIDGenerator) NewUUIDv7() (domain.ApplicationReviewID, error) {
	fake.calls++
	appendEvent(fake.events, "id")
	return fake.id, fake.err
}

type fakeClock struct {
	events *[]string
	now    time.Time
	calls  int
}

func (fake *fakeClock) Now() time.Time {
	fake.calls++
	appendEvent(fake.events, "clock")
	return fake.now
}

func TestSubmitApplicationVersionReview_BR_REV_001_003_004_005_006_007_009_Success(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 6)
	candidate := submissionCandidate(t)
	repository := &fakeRepository{events: &events, candidate: candidate}
	catalog := &fakeCatalog{events: &events, revision: 31}
	policy := &fakePolicy{events: &events, version: "public-https.v1"}
	idGenerator := &fakeIDGenerator{events: &events, id: reviewID}
	now := time.Date(2026, time.September, 20, 20, 0, 0, 99, time.FixedZone("CST", 8*60*60))
	clock := &fakeClock{events: &events, now: now}
	oauthPolicy := &fakeOAuthPolicy{events: &events}
	handler := NewSubmitApplicationVersionReviewHandler(catalog, policy, oauthPolicy, idGenerator, clock, repository)

	result, err := handler.Handle(context.Background(), approvedIdentity(), applicationID, versionID, SubmitApplicationVersionReviewCommand{ExpectedRevision: 7})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if want := []string{"candidate", "oauth", "scopes", "preflight", "id", "clock", "submit"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if repository.loadedApplicationID != applicationID || repository.loadedVersionID != versionID || repository.loadedAdminID != "auth-1" || repository.loadedRevision != 7 {
		t.Fatal("candidate load did not receive typed identity and optimistic concurrency inputs")
	}
	if want := []domain.ScopeName{"profile.basic", "schedule.read"}; !reflect.DeepEqual(catalog.scopes, want) {
		t.Fatalf("validated scopes = %v, want %v", catalog.scopes, want)
	}
	if policy.launchURL != "https://example.edu/app" {
		t.Fatalf("inspected launch URL = %q", policy.launchURL)
	}
	if repository.submittedReviewID != reviewID || repository.submittedCatalogRevision != 31 || repository.submittedPolicyVersion != "public-https.v1" || repository.submittedAdminID != "auth-1" {
		t.Fatal("final submit did not receive external validation and system identity facts")
	}
	if !repository.submittedAt.Equal(now) || repository.submittedAt.Location() != time.UTC {
		t.Fatalf("submittedAt = %v, want injected UTC time", repository.submittedAt)
	}
	if result.Review().ReviewID() != reviewID || result.Version().Revision() != 8 || result.Version().ReviewStatus() != "SUBMITTED" {
		t.Fatal("unexpected submission result")
	}
}

func TestSubmitApplicationVersionReview_BR_REV_001_003_RejectsIdentityAndRevisionBeforeDependencies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		identity DeveloperIdentity
		revision int64
		want     error
	}{
		{name: "missing identity", identity: DeveloperIdentity{DeveloperStatus: DeveloperStatusApproved}, revision: 7, want: domain.ErrDeveloperIdentityRequired},
		{name: "unapproved developer", identity: DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: shared.DeveloperStatusPending}, revision: 7, want: domain.ErrDeveloperApprovalRequired},
		{name: "missing revision", identity: approvedIdentity(), revision: 0, want: domain.ErrApplicationVersionRevisionRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeRepository{candidate: submissionCandidate(t)}
			catalog := &fakeCatalog{revision: 1}
			policy := &fakePolicy{version: "v1"}
			idGenerator := &fakeIDGenerator{id: reviewID}
			clock := &fakeClock{now: time.Now()}
			oauthPolicy := &fakeOAuthPolicy{}
			handler := NewSubmitApplicationVersionReviewHandler(catalog, policy, oauthPolicy, idGenerator, clock, repository)
			result, err := handler.Handle(context.Background(), test.identity, applicationID, versionID, SubmitApplicationVersionReviewCommand{ExpectedRevision: test.revision})
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil %v", result, err, test.want)
			}
			if repository.loadCalls != 0 || catalog.calls != 0 || policy.calls != 0 || oauthPolicy.calls != 0 || idGenerator.calls != 0 || clock.calls != 0 {
				t.Fatal("dependency called after early rejection")
			}
		})
	}
}

func TestSubmitApplicationVersionReview_BR_REV_001_002_003_RepositoryOutcomesMapStableErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		portError error
		want      error
		category  domain.ErrorCategory
	}{
		{name: "not found or mismatched", portError: port.ErrApplicationVersionNotFound, want: domain.ErrApplicationVersionNotFound, category: domain.ErrorCategoryNotFound},
		{name: "not current admin", portError: port.ErrApplicationAdminRequired, want: domain.ErrApplicationAdminRequired, category: domain.ErrorCategoryAuthorization},
		{name: "not draft", portError: port.ErrApplicationVersionNotDraft, want: domain.ErrApplicationVersionNotDraft, category: domain.ErrorCategoryConflict},
		{name: "revision conflict", portError: port.ErrApplicationVersionRevisionConflict, want: domain.ErrApplicationVersionRevisionConflict, category: domain.ErrorCategoryConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeRepository{loadErr: test.portError}
			handler := validHandler(repository, &fakeCatalog{revision: 1}, &fakePolicy{version: "v1"})
			result, err := handler.Handle(context.Background(), approvedIdentity(), applicationID, versionID, SubmitApplicationVersionReviewCommand{ExpectedRevision: 7})
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil %v", result, err, test.want)
			}
			var domainError *domain.Error
			if !errors.As(err, &domainError) || domainError.Category() != test.category {
				t.Fatalf("category = %v, want %s", err, test.category)
			}
		})
	}
}

func TestSubmitApplicationVersionReview_BR_REV_006_007_ExternalFailuresDoNotSubmit(t *testing.T) {
	t.Parallel()
	cause := errors.New("dependency down")
	tests := []struct {
		name       string
		catalogErr error
		policyErr  error
		oauthErr   error
		want       error
		category   domain.ErrorCategory
	}{
		{name: "scope no longer requestable", catalogErr: port.ErrScopeNotRequestable, want: domain.ErrInvalidApplicationScope, category: domain.ErrorCategoryValidation},
		{name: "scope catalog unavailable", catalogErr: errors.Join(port.ErrScopeCatalogUnavailable, cause), want: domain.ErrScopeCatalogUnavailable, category: domain.ErrorCategoryDependencyUnavailable},
		{name: "OAuth redirect not reviewable", oauthErr: port.ErrOAuthRedirectNotReviewable, want: domain.ErrInvalidOAuthRedirectConfiguration, category: domain.ErrorCategoryValidation},
		{name: "URL not public HTTPS", policyErr: port.ErrLaunchURLNotReviewable, want: domain.ErrApplicationLaunchURLNotReviewable, category: domain.ErrorCategoryValidation},
		{name: "DNS unavailable", policyErr: errors.Join(port.ErrLaunchURLInspectionUnavailable, cause), want: domain.ErrLaunchURLInspectionUnavailable, category: domain.ErrorCategoryDependencyUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeRepository{candidate: submissionCandidate(t)}
			catalog := &fakeCatalog{revision: 5, err: test.catalogErr}
			policy := &fakePolicy{version: "v1", err: test.policyErr}
			handler := NewSubmitApplicationVersionReviewHandler(catalog, policy, &fakeOAuthPolicy{err: test.oauthErr}, &fakeIDGenerator{id: reviewID}, &fakeClock{now: time.Now()}, repository)
			result, err := handler.Handle(context.Background(), approvedIdentity(), applicationID, versionID, SubmitApplicationVersionReviewCommand{ExpectedRevision: 7})
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil %v", result, err, test.want)
			}
			var domainError *domain.Error
			if !errors.As(err, &domainError) || domainError.Category() != test.category {
				t.Fatalf("category = %v, want %s", err, test.category)
			}
			if repository.submitCalls != 0 {
				t.Fatal("final Submit called after external failure")
			}
		})
	}
}

func approvedIdentity() DeveloperIdentity {
	return DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved}
}
func appendEvent(events *[]string, value string) {
	if events != nil {
		*events = append(*events, value)
	}
}
func validHandler(repository *fakeRepository, catalog *fakeCatalog, policy *fakePolicy) *SubmitApplicationVersionReviewHandler {
	return NewSubmitApplicationVersionReviewHandler(catalog, policy, &fakeOAuthPolicy{}, &fakeIDGenerator{id: reviewID}, &fakeClock{now: time.Now()}, repository)
}
func submissionCandidate(t *testing.T) *domain.SubmissionCandidate {
	t.Helper()
	snapshot, err := domain.NewApplicationVersionReviewSnapshot("v2", "https://example.edu/app", 1, 3, []string{"camera.read.v1", "user.profile.v1"}, []domain.ScopeName{"profile.basic"}, []domain.ScopeName{"schedule.read"})
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	candidate, err := domain.NewSubmissionCandidate(applicationID, versionID, 7, snapshot)
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	return candidate
}
