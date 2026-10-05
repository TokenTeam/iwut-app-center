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

type fakeReviewPolicyProvider struct {
	events          *[]string
	policy          *domain.VersionReviewPolicy
	err             error
	calls           int
	expectedVersion domain.ReviewPolicyVersion
	snapshot        domain.ApplicationVersionReviewSnapshot
}

func (fake *fakeReviewPolicyProvider) RequireUsable(
	_ context.Context,
	expected domain.ReviewPolicyVersion,
	snapshot domain.ApplicationVersionReviewSnapshot,
) (*domain.VersionReviewPolicy, error) {
	fake.calls++
	fake.expectedVersion = expected
	fake.snapshot = snapshot
	appendEvent(fake.events, "policy")
	return fake.policy, fake.err
}

type fakeSuspensionChecker struct {
	events    *[]string
	suspended bool
	err       error
	calls     int
	authIDs   []shared.AuthID
}

type fakeSystemPrincipalResolver struct {
	authID shared.AuthID
	err    error
	calls  int
}

func (fake *fakeSystemPrincipalResolver) ResolveReviewAutoRejection(context.Context) (shared.AuthID, error) {
	fake.calls++
	return fake.authID, fake.err
}

func (fake *fakeSuspensionChecker) BlocksApproval(_ context.Context, currentAdminID, submittedBy shared.AuthID) (bool, error) {
	fake.calls++
	fake.authIDs = []shared.AuthID{currentAdminID, submittedBy}
	appendEvent(fake.events, "suspension")
	return fake.suspended, fake.err
}

type fakeDecisionRepository struct {
	events            *[]string
	candidate         *domain.ApplicationReviewDecisionCandidate
	loadErr           error
	decideErr         error
	loadCalls         int
	decideCalls       int
	loadedReviewerID  shared.AuthID
	decidedReviewerID shared.AuthID
	decidedDecision   *domain.ApplicationReviewDecision
}

func (fake *fakeDecisionRepository) LoadDecisionCandidate(
	_ context.Context,
	_ shared.ApplicationID,
	_ domain.ApplicationVersionID,
	_ domain.ApplicationReviewID,
	reviewerID shared.AuthID,
) (*domain.ApplicationReviewDecisionCandidate, error) {
	fake.loadCalls++
	fake.loadedReviewerID = reviewerID
	appendEvent(fake.events, "candidate")
	if fake.loadErr != nil {
		return nil, fake.loadErr
	}
	return fake.candidate, nil
}

func (fake *fakeDecisionRepository) Decide(
	_ context.Context,
	candidate *domain.ApplicationReviewDecisionCandidate,
	reviewerID shared.AuthID,
	decision *domain.ApplicationReviewDecision,
) (*domain.ApplicationReviewDecisionResult, error) {
	fake.decideCalls++
	fake.decidedReviewerID = reviewerID
	fake.decidedDecision = decision
	appendEvent(fake.events, "decide")
	if fake.decideErr != nil {
		return nil, fake.decideErr
	}
	review := candidate.Review()
	switch decision.Outcome() {
	case domain.ReviewDecisionApproved:
		if _, err := review.Approve(
			decision.ReviewPolicyVersion(), decision.ConfirmedCheckIDs(), decision.Reason(),
			decision.ApprovalValidation(), decision.DecidedBy(), decision.DecidedAt(),
		); err != nil {
			return nil, err
		}
	case domain.ReviewDecisionRejected:
		if _, err := review.Reject(
			decision.ReviewPolicyVersion(), decision.Reason(), decision.DecidedBy(), decision.DecidedAt(),
		); err != nil {
			return nil, err
		}
	}
	version, err := domain.NewDecidedApplicationVersion(candidate, decision)
	if err != nil {
		return nil, err
	}
	return domain.NewApplicationReviewDecisionResult(review, version)
}

func TestDecideApplicationVersionReview_BR_REV_010_IdentityAndPermissionBeforeRepository(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		identity ReviewerIdentity
		command  DecideApplicationVersionReviewCommand
		want     error
	}{
		{name: "missing identity", identity: ReviewerIdentity{Permissions: []string{PermissionApplicationVersionReview}}, command: approveCommand(), want: domain.ErrReviewerIdentityRequired},
		{name: "missing permission", identity: ReviewerIdentity{AuthID: "auth-reviewer"}, command: approveCommand(), want: domain.ErrApplicationReviewPermissionRequired},
		{name: "invalid outcome", identity: reviewerIdentity(), command: DecideApplicationVersionReviewCommand{Outcome: "MAYBE", ExpectedPolicyVersion: "review.v1"}, want: domain.ErrInvalidApplicationReviewOutcome},
		{name: "invalid policy version", identity: reviewerIdentity(), command: DecideApplicationVersionReviewCommand{Outcome: "APPROVE", ExpectedPolicyVersion: "bad version"}, want: domain.ErrInvalidApplicationReviewPolicyVersion},
		{name: "reject with confirmations", identity: reviewerIdentity(), command: DecideApplicationVersionReviewCommand{Outcome: "REJECT", ExpectedPolicyVersion: "review.v1", ConfirmedCheckIDs: []string{"content-reviewed"}, Reason: "reason"}, want: domain.ErrInvalidApplicationReviewChecks},
		{name: "reject without reason", identity: reviewerIdentity(), command: DecideApplicationVersionReviewCommand{Outcome: "REJECT", ExpectedPolicyVersion: "review.v1"}, want: domain.ErrInvalidApplicationReviewReason},
		{name: "duplicate confirmation", identity: reviewerIdentity(), command: DecideApplicationVersionReviewCommand{Outcome: "APPROVE", ExpectedPolicyVersion: "review.v1", ConfirmedCheckIDs: []string{"content-reviewed", "content-reviewed"}}, want: domain.ErrApplicationReviewChecksIncomplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeDecisionRepository{candidate: decisionCandidate(t)}
			handler := newDecisionHandler(repository, reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), &fakeSuspensionChecker{}, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
			result, err := handler.Handle(context.Background(), test.identity, applicationID, versionID, reviewID, test.command)
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil %v", result, err, test.want)
			}
			if repository.loadCalls != 0 {
				t.Fatal("repository loaded after early rejection")
			}
		})
	}
}

func TestDecideApplicationVersionReview_BR_REV_011_ConflictOfInterestRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		adminID   shared.AuthID
		createdBy shared.AuthID
		submitter shared.AuthID
		reviewer  shared.AuthID
	}{
		{name: "current administrator", adminID: "auth-reviewer", createdBy: "auth-creator", submitter: "auth-submitter", reviewer: "auth-reviewer"},
		{name: "version creator", adminID: "auth-admin", createdBy: "auth-reviewer", submitter: "auth-submitter", reviewer: "auth-reviewer"},
		{name: "review submitter", adminID: "auth-admin", createdBy: "auth-creator", submitter: "auth-reviewer", reviewer: "auth-reviewer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeDecisionRepository{candidate: decisionCandidateForReviewer(t, test.adminID, test.createdBy, test.submitter)}
			handler := newDecisionHandler(repository, reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), &fakeSuspensionChecker{}, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
			identity := ReviewerIdentity{AuthID: test.reviewer, Permissions: []string{PermissionApplicationVersionReview}}
			result, err := handler.Handle(context.Background(), identity, applicationID, versionID, reviewID, approveCommand())
			if result != nil || !errors.Is(err, domain.ErrApplicationReviewConflictOfInterest) {
				t.Fatalf("Handle() = (%v, %v), want conflict of interest", result, err)
			}
			if repository.decideCalls != 0 {
				t.Fatal("conflicting decision reached the repository")
			}
		})
	}
}

func TestDecideApplicationVersionReview_BR_REV_016_PolicyMustBeUsable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		policy *domain.VersionReviewPolicy
		err    error
	}{
		{name: "provider reports changed", policy: nil, err: port.ErrReviewPolicyChanged},
		{name: "retired policy", policy: reviewPolicyValue(t, "review.v1", domain.ReviewPolicyStatusRetired, "content-reviewed")},
		{name: "different version", policy: reviewPolicyValue(t, "review.v2", domain.ReviewPolicyStatusActive, "content-reviewed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeDecisionRepository{candidate: decisionCandidate(t)}
			provider := &fakeReviewPolicyProvider{policy: test.policy, err: test.err}
			handler := newDecisionHandler(repository, provider, &fakeSuspensionChecker{}, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
			result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
			if result != nil || !errors.Is(err, domain.ErrApplicationReviewPolicyChanged) {
				t.Fatalf("Handle() = (%v, %v), want policy changed", result, err)
			}
			if repository.decideCalls != 0 {
				t.Fatal("unusable policy reached the repository")
			}
		})
	}
}

func TestDecideApplicationVersionReview_BR_REV_017_ApproveRequiresAllChecks(t *testing.T) {
	t.Parallel()
	repository := &fakeDecisionRepository{candidate: decisionCandidate(t)}
	handler := newDecisionHandler(repository, reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed", "scopes-reviewed"), &fakeSuspensionChecker{}, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
	result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, DecideApplicationVersionReviewCommand{
		Outcome: "APPROVE", ExpectedPolicyVersion: "review.v1", ConfirmedCheckIDs: []string{"content-reviewed"},
	})
	if result != nil || !errors.Is(err, domain.ErrApplicationReviewChecksIncomplete) {
		t.Fatalf("Handle() = (%v, %v), want checks incomplete", result, err)
	}
	if repository.decideCalls != 0 {
		t.Fatal("incomplete confirmation reached the repository")
	}
}

func TestDecideApplicationVersionReview_BR_REV_014_018_ApproveRevalidatesAndPersists(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 8)
	candidate := decisionCandidate(t)
	repository := &fakeDecisionRepository{events: &events, candidate: candidate}
	catalog := &fakeCatalog{events: &events, revision: 41}
	launchPolicy := &fakePolicy{events: &events, version: "public-https.v2"}
	suspension := &fakeSuspensionChecker{events: &events}
	policyProvider := reviewPolicyProviderWithEvents(t, &events, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed")
	now := time.Date(2026, time.September, 21, 9, 30, 0, 99, time.FixedZone("CST", 8*60*60))
	clock := &fakeClock{events: &events, now: now}
	handler := NewDecideApplicationVersionReviewHandler(
		policyProvider, suspension, catalog, launchPolicy, &fakeOAuthPolicy{events: &events},
		clock, repository, &fakeSystemPrincipalResolver{authID: "auth-system"},
	)

	result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, DecideApplicationVersionReviewCommand{
		Outcome: "APPROVE", ExpectedPolicyVersion: "review.v1", ConfirmedCheckIDs: []string{"content-reviewed"},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if want := []string{"candidate", "policy", "suspension", "oauth", "scopes", "preflight", "clock", "decide"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if policyProvider.expectedVersion != "review.v1" || !policyProvider.snapshot.Equal(candidate.Review().Snapshot()) {
		t.Fatal("policy provider was not asked with the versioned snapshot")
	}
	if want := []domain.ScopeName{"profile.basic", "schedule.read"}; !reflect.DeepEqual(catalog.scopes, want) {
		t.Fatalf("revalidated scopes = %v, want %v", catalog.scopes, want)
	}
	if launchPolicy.launchURL != "https://example.edu/app" {
		t.Fatalf("revalidated launch URL = %q", launchPolicy.launchURL)
	}
	if want := []shared.AuthID{"auth-admin", "auth-submitter"}; !reflect.DeepEqual(suspension.authIDs, want) {
		t.Fatalf("suspension check IDs = %v, want %v", suspension.authIDs, want)
	}
	if repository.decidedReviewerID != "auth-reviewer" || repository.decidedDecision.Outcome() != domain.ReviewDecisionApproved {
		t.Fatal("manual approve did not use the trusted reviewer identity")
	}
	if repository.decidedDecision.DecidedBy() != "auth-reviewer" ||
		!repository.decidedDecision.DecidedAt().Equal(now) || repository.decidedDecision.DecidedAt().Location() != time.UTC {
		t.Fatalf("decidedBy/decidedAt = (%s, %v)", repository.decidedDecision.DecidedBy(), repository.decidedDecision.DecidedAt())
	}
	validation := repository.decidedDecision.ApprovalValidation()
	if validation == nil || validation.ScopeCatalogRevision() != 41 || validation.PreflightPolicyVersion() != "public-https.v2" {
		t.Fatalf("approval validation = %#v", validation)
	}
	if result.Review().Status() != domain.ReviewStatusApproved || result.Version().Revision() != 9 ||
		result.Version().UpdatedBy() != "auth-reviewer" {
		t.Fatalf("unexpected result = %#v", result)
	}
}

func TestDecideApplicationVersionReview_BR_REV_014_018_SuspendedAutoRejectionUsesSystemIdentity(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 8)
	repository := &fakeDecisionRepository{events: &events, candidate: decisionCandidate(t)}
	catalog := &fakeCatalog{events: &events, revision: 41}
	launchPolicy := &fakePolicy{events: &events, version: "public-https.v2"}
	suspension := &fakeSuspensionChecker{events: &events, suspended: true}
	now := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	clock := &fakeClock{events: &events, now: now}
	handler := newDecisionHandlerWithSystemID(repository, reviewPolicyProviderWithEvents(t, &events, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), suspension, catalog, launchPolicy, clock, "auth-system")

	result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if want := []string{"candidate", "policy", "suspension", "clock", "decide"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if catalog.calls != 0 || launchPolicy.calls != 0 {
		t.Fatal("suspended auto rejection still called ScopeCatalog or LaunchURLSubmissionPolicy")
	}
	if repository.decidedReviewerID != "auth-reviewer" || repository.decidedDecision.DecidedBy() != "auth-system" {
		t.Fatal("auto rejection did not preserve the initiating reviewer and injected System decision actor")
	}
	if repository.decidedDecision.Outcome() != domain.ReviewDecisionRejected ||
		repository.decidedDecision.Reason() != domain.SystemEligibilityRejectionReason ||
		repository.decidedDecision.ApprovalValidation() != nil {
		t.Fatalf("auto decision = %#v", repository.decidedDecision)
	}
	if result.Review().Status() != domain.ReviewStatusRejected || result.Version().ReviewStatus() != domain.ReviewDecisionRejected {
		t.Fatal("auto rejection did not persist REJECTED")
	}
}

func TestDecideApplicationVersionReview_BR_REV_018_SuspensionUnavailableKeepsPending(t *testing.T) {
	t.Parallel()
	repository := &fakeDecisionRepository{candidate: decisionCandidate(t)}
	suspension := &fakeSuspensionChecker{err: errors.New("auth unavailable")}
	handler := newDecisionHandler(repository, reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), suspension, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
	result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
	if result != nil || !errors.Is(err, domain.ErrDeveloperStatusUnavailable) {
		t.Fatalf("Handle() = (%v, %v), want developer status unavailable", result, err)
	}
	var domainError *domain.Error
	if !errors.As(err, &domainError) || domainError.Category() != domain.ErrorCategoryDependencyUnavailable {
		t.Fatalf("category = %v, want DependencyUnavailable", err)
	}
	if repository.decideCalls != 0 {
		t.Fatal("unavailable suspension facts still wrote a decision")
	}
}

func TestDecideApplicationVersionReview_BR_REV_018_SystemPrincipalUnavailableKeepsPending(t *testing.T) {
	t.Parallel()
	repository := &fakeDecisionRepository{candidate: decisionCandidate(t)}
	resolver := &fakeSystemPrincipalResolver{err: errors.New("auth unavailable")}
	handler := NewDecideApplicationVersionReviewHandler(
		reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"),
		&fakeSuspensionChecker{suspended: true},
		&fakeCatalog{revision: 1},
		&fakePolicy{version: "public-https.v1"},
		&fakeOAuthPolicy{},
		&fakeClock{now: time.Now()},
		repository,
		resolver,
	)
	result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
	if result != nil || !errors.Is(err, domain.ErrSystemPrincipalUnavailable) {
		t.Fatalf("Handle() = (%v, %v), want system principal unavailable", result, err)
	}
	if resolver.calls != 1 || repository.decideCalls != 0 {
		t.Fatalf("resolver/decide calls = (%d, %d), want (1, 0)", resolver.calls, repository.decideCalls)
	}
}

func TestDecideApplicationVersionReview_BR_REV_018_RejectSkipsExternalChecks(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 4)
	repository := &fakeDecisionRepository{events: &events, candidate: decisionCandidate(t)}
	catalog := &fakeCatalog{events: &events, revision: 41}
	launchPolicy := &fakePolicy{events: &events, version: "public-https.v2"}
	now := time.Date(2026, time.September, 21, 9, 30, 0, 0, time.UTC)
	handler := newDecisionHandler(repository, reviewPolicyProviderWithEvents(t, &events, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), &fakeSuspensionChecker{events: &events}, catalog, launchPolicy, &fakeClock{events: &events, now: now})

	result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, DecideApplicationVersionReviewCommand{
		Outcome: "REJECT", ExpectedPolicyVersion: "review.v1", Reason: "用途说明不足。",
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if want := []string{"candidate", "policy", "clock", "decide"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if catalog.calls != 0 || launchPolicy.calls != 0 {
		t.Fatal("reject called ScopeCatalog or LaunchURLSubmissionPolicy")
	}
	if repository.decidedDecision.Outcome() != domain.ReviewDecisionRejected ||
		repository.decidedDecision.DecidedBy() != "auth-reviewer" ||
		repository.decidedDecision.Reason() != "用途说明不足。" {
		t.Fatalf("rejected decision = %#v", repository.decidedDecision)
	}
	if result.Review().Status() != domain.ReviewStatusRejected {
		t.Fatal("reject did not persist REJECTED")
	}
}

func TestDecideApplicationVersionReview_BR_REV_018_ExternalFailuresDoNotDecide(t *testing.T) {
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
		{name: "URL not reviewable", policyErr: port.ErrLaunchURLNotReviewable, want: domain.ErrApplicationLaunchURLNotReviewable, category: domain.ErrorCategoryValidation},
		{name: "URL inspection unavailable", policyErr: errors.Join(port.ErrLaunchURLInspectionUnavailable, cause), want: domain.ErrLaunchURLInspectionUnavailable, category: domain.ErrorCategoryDependencyUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeDecisionRepository{candidate: decisionCandidate(t)}
			catalog := &fakeCatalog{revision: 5, err: test.catalogErr}
			launchPolicy := &fakePolicy{version: "public-https.v1", err: test.policyErr}
			handler := NewDecideApplicationVersionReviewHandler(
				reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"),
				&fakeSuspensionChecker{}, catalog, launchPolicy, &fakeOAuthPolicy{err: test.oauthErr},
				&fakeClock{now: time.Now()}, repository, &fakeSystemPrincipalResolver{authID: "auth-system"},
			)
			result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil %v", result, err, test.want)
			}
			var domainError *domain.Error
			if !errors.As(err, &domainError) || domainError.Category() != test.category {
				t.Fatalf("category = %v, want %s", err, test.category)
			}
			if repository.decideCalls != 0 {
				t.Fatal("external failure still wrote a decision")
			}
		})
	}
}

func TestDecideApplicationVersionReview_BR_REV_012_013_RepositoryOutcomesMapStableErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		portErr  error
		want     error
		category domain.ErrorCategory
	}{
		{name: "not found", portErr: port.ErrApplicationReviewNotFound, want: domain.ErrApplicationReviewNotFound, category: domain.ErrorCategoryNotFound},
		{name: "already decided", portErr: port.ErrApplicationReviewAlreadyDecided, want: domain.ErrApplicationReviewAlreadyDecided, category: domain.ErrorCategoryConflict},
		{name: "state inconsistent", portErr: port.ErrApplicationReviewStateInconsistent, want: domain.ErrApplicationReviewStateInconsistent, category: domain.ErrorCategoryConflict},
		{name: "conflict of interest", portErr: port.ErrApplicationReviewConflictOfInterest, want: domain.ErrApplicationReviewConflictOfInterest, category: domain.ErrorCategoryAuthorization},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeDecisionRepository{loadErr: test.portErr}
			handler := newDecisionHandler(repository, reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), &fakeSuspensionChecker{}, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
			result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
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

func TestDecideApplicationVersionReview_BR_REV_012_RepositoryDecideOutcomesMapStableErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		portErr error
		want    error
	}{
		{name: "already decided by a concurrent reviewer", portErr: port.ErrApplicationReviewAlreadyDecided, want: domain.ErrApplicationReviewAlreadyDecided},
		{name: "state changed during external checks", portErr: port.ErrApplicationReviewStateInconsistent, want: domain.ErrApplicationReviewStateInconsistent},
		{name: "administrator changed conflict facts", portErr: port.ErrApplicationReviewConflictOfInterest, want: domain.ErrApplicationReviewConflictOfInterest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeDecisionRepository{candidate: decisionCandidate(t), decideErr: test.portErr}
			handler := newDecisionHandler(repository, reviewPolicy(t, "review.v1", domain.ReviewPolicyStatusActive, "content-reviewed"), &fakeSuspensionChecker{}, &fakeCatalog{revision: 1}, &fakePolicy{version: "public-https.v1"}, &fakeClock{now: time.Now()})
			result, err := handler.Handle(context.Background(), reviewerIdentity(), applicationID, versionID, reviewID, approveCommand())
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil %v", result, err, test.want)
			}
		})
	}
}

func reviewerIdentity() ReviewerIdentity {
	return ReviewerIdentity{AuthID: "auth-reviewer", Permissions: []string{PermissionApplicationVersionReview}}
}

func approveCommand() DecideApplicationVersionReviewCommand {
	return DecideApplicationVersionReviewCommand{
		Outcome: "APPROVE", ExpectedPolicyVersion: "review.v1", ConfirmedCheckIDs: []string{"content-reviewed"},
	}
}

func newDecisionHandler(
	repository port.ApplicationReviewDecisionRepository,
	policyProvider port.ReviewPolicyProvider,
	suspension port.DeveloperApprovalChecker,
	catalog port.ScopeCatalog,
	launchPolicy port.LaunchURLSubmissionPolicy,
	clock port.Clock,
) *DecideApplicationVersionReviewHandler {
	return NewDecideApplicationVersionReviewHandler(policyProvider, suspension, catalog, launchPolicy, &fakeOAuthPolicy{}, clock, repository, &fakeSystemPrincipalResolver{authID: "auth-system"})
}

func newDecisionHandlerWithSystemID(
	repository port.ApplicationReviewDecisionRepository,
	policyProvider port.ReviewPolicyProvider,
	suspension port.DeveloperApprovalChecker,
	catalog port.ScopeCatalog,
	launchPolicy port.LaunchURLSubmissionPolicy,
	clock port.Clock,
	systemAuthID shared.AuthID,
) *DecideApplicationVersionReviewHandler {
	return NewDecideApplicationVersionReviewHandler(policyProvider, suspension, catalog, launchPolicy, &fakeOAuthPolicy{}, clock, repository, &fakeSystemPrincipalResolver{authID: systemAuthID})
}

func decisionCandidate(t *testing.T) *domain.ApplicationReviewDecisionCandidate {
	t.Helper()
	return decisionCandidateForReviewer(t, "auth-admin", "auth-creator", "auth-submitter")
}

func decisionCandidateForReviewer(
	t *testing.T,
	adminID shared.AuthID,
	createdBy shared.AuthID,
	submittedBy shared.AuthID,
) *domain.ApplicationReviewDecisionCandidate {
	t.Helper()
	snapshot, err := domain.NewApplicationVersionReviewSnapshot(
		"v2", "https://example.edu/app", 1, 3,
		[]string{"camera.read.v1", "user.profile.v1"},
		[]domain.ScopeName{"profile.basic"}, []domain.ScopeName{"schedule.read"},
	)
	if err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	submission, err := domain.NewSubmissionCandidate(applicationID, versionID, 7, snapshot)
	if err != nil {
		t.Fatalf("create submission candidate: %v", err)
	}
	attempt, _ := domain.NewReviewAttempt(1)
	catalogRevision, _ := domain.NewScopeCatalogRevision(11)
	preflightPolicyVersion, _ := domain.NewPreflightPolicyVersion("public-https.v1")
	review, err := domain.NewPendingApplicationReview(
		submission, reviewID, attempt, catalogRevision, preflightPolicyVersion, submittedBy,
		time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create pending review: %v", err)
	}
	versionSnapshot := review.Snapshot()
	candidate, err := domain.NewApplicationReviewDecisionCandidate(
		review, domain.SubmittedVersionReviewStatus, 8, createdBy, &versionSnapshot, adminID,
	)
	if err != nil {
		t.Fatalf("create decision candidate: %v", err)
	}
	return candidate
}

func reviewPolicy(t *testing.T, version string, status domain.ReviewPolicyStatus, checkIDs ...string) *fakeReviewPolicyProvider {
	t.Helper()
	return &fakeReviewPolicyProvider{policy: reviewPolicyValue(t, version, status, checkIDs...)}
}

func reviewPolicyProviderWithEvents(t *testing.T, events *[]string, version string, status domain.ReviewPolicyStatus, checkIDs ...string) *fakeReviewPolicyProvider {
	t.Helper()
	return &fakeReviewPolicyProvider{events: events, policy: reviewPolicyValue(t, version, status, checkIDs...)}
}

func reviewPolicyValue(t *testing.T, version string, status domain.ReviewPolicyStatus, checkIDs ...string) *domain.VersionReviewPolicy {
	t.Helper()
	definitions := make([]domain.ReviewCheckDefinition, len(checkIDs))
	for index, value := range checkIDs {
		id, err := domain.NewReviewCheckID(value)
		if err != nil {
			t.Fatalf("create check ID: %v", err)
		}
		definition, err := domain.NewReviewCheckDefinition(id)
		if err != nil {
			t.Fatalf("create check definition: %v", err)
		}
		definitions[index] = definition
	}
	policyVersion, err := domain.NewReviewPolicyVersion(version)
	if err != nil {
		t.Fatalf("create policy version: %v", err)
	}
	policy, err := domain.NewVersionReviewPolicy(policyVersion, definitions, status)
	if err != nil {
		t.Fatalf("create policy: %v", err)
	}
	return policy
}
