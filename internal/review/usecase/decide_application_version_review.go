package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

// PermissionApplicationVersionReview is the single atomic reviewer permission.
const PermissionApplicationVersionReview = "app.version.review"

// ReviewerIdentity is the minimal trusted identity the decision UseCase needs.
// Transport/Auth adapters build it; it is never taken from a request body.
type ReviewerIdentity struct {
	AuthID      shared.AuthID
	Permissions []string
}

func (identity ReviewerIdentity) HasPermission(permission string) bool {
	for _, candidate := range identity.Permissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

type DecideApplicationVersionReviewCommand struct {
	Outcome               string
	ExpectedPolicyVersion string
	ConfirmedCheckIDs     []string
	Reason                string
}

type DecideApplicationVersionReviewHandler struct {
	reviewPolicyProvider       port.ReviewPolicyProvider
	developerSuspensionChecker port.DeveloperApprovalChecker
	scopeCatalog               port.ScopeCatalog
	launchPolicy               port.LaunchURLSubmissionPolicy
	oauthPolicy                port.OAuthRedirectPolicy
	clock                      port.Clock
	repository                 port.ApplicationReviewDecisionRepository
	systemPrincipalResolver    port.SystemPrincipalResolver
}

func NewDecideApplicationVersionReviewHandler(
	reviewPolicyProvider port.ReviewPolicyProvider,
	developerSuspensionChecker port.DeveloperApprovalChecker,
	scopeCatalog port.ScopeCatalog,
	launchPolicy port.LaunchURLSubmissionPolicy,
	oauthPolicy port.OAuthRedirectPolicy,
	clock port.Clock,
	repository port.ApplicationReviewDecisionRepository,
	systemPrincipalResolver port.SystemPrincipalResolver,
) *DecideApplicationVersionReviewHandler {
	return &DecideApplicationVersionReviewHandler{
		reviewPolicyProvider:       reviewPolicyProvider,
		developerSuspensionChecker: developerSuspensionChecker,
		scopeCatalog:               scopeCatalog,
		launchPolicy:               launchPolicy,
		oauthPolicy:                oauthPolicy,
		clock:                      clock,
		repository:                 repository,
		systemPrincipalResolver:    systemPrincipalResolver,
	}
}

func (handler *DecideApplicationVersionReviewHandler) Handle(
	ctx context.Context,
	identity ReviewerIdentity,
	applicationID shared.ApplicationID,
	versionID domain.ApplicationVersionID,
	reviewID domain.ApplicationReviewID,
	command DecideApplicationVersionReviewCommand,
) (*domain.ApplicationReviewDecisionResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrReviewerIdentityRequired
	}
	if !identity.HasPermission(PermissionApplicationVersionReview) {
		return nil, domain.ErrApplicationReviewPermissionRequired
	}

	action, err := domain.NewReviewAction(command.Outcome)
	if err != nil {
		return nil, err
	}
	policyVersion, err := domain.NewReviewPolicyVersion(command.ExpectedPolicyVersion)
	if err != nil {
		return nil, err
	}
	if !applicationID.IsValid() || !versionID.IsValid() || !reviewID.IsValid() {
		return nil, domain.ErrApplicationReviewNotFound
	}
	if action == domain.ReviewActionReject {
		if len(command.ConfirmedCheckIDs) != 0 {
			return nil, domain.ErrInvalidApplicationReviewChecks
		}
		if err := domain.ValidateReviewReason(command.Reason); err != nil {
			return nil, err
		}
	} else if command.Reason != "" {
		if err := domain.ValidateReviewReason(command.Reason); err != nil {
			return nil, err
		}
	}
	confirmedCheckIDs, err := parseConfirmedCheckIDs(command.ConfirmedCheckIDs)
	if err != nil {
		return nil, err
	}
	if handler == nil || handler.reviewPolicyProvider == nil || handler.developerSuspensionChecker == nil ||
		handler.scopeCatalog == nil || handler.launchPolicy == nil || handler.oauthPolicy == nil || handler.clock == nil ||
		handler.repository == nil || handler.systemPrincipalResolver == nil {
		return nil, domain.NewInternalError(nil)
	}

	candidate, err := handler.repository.LoadDecisionCandidate(ctx, applicationID, versionID, reviewID, identity.AuthID)
	if err != nil {
		return nil, mapDecisionRepositoryError(err)
	}
	if candidate == nil || candidate.Review() == nil ||
		candidate.Review().ApplicationID() != applicationID ||
		candidate.Review().VersionID() != versionID ||
		candidate.Review().ReviewID() != reviewID {
		return nil, domain.ErrApplicationReviewNotFound
	}
	if candidate.ReviewerConflicts(identity.AuthID) {
		return nil, domain.ErrApplicationReviewConflictOfInterest
	}

	snapshot := candidate.Review().Snapshot()
	policy, err := handler.reviewPolicyProvider.RequireUsable(ctx, policyVersion, snapshot)
	if err != nil {
		if errors.Is(err, port.ErrReviewPolicyChanged) {
			return nil, domain.ErrApplicationReviewPolicyChanged
		}
		return nil, domain.NewInternalError(err)
	}
	if policy == nil || policy.Version() != policyVersion || !policy.Usable() {
		return nil, domain.ErrApplicationReviewPolicyChanged
	}

	if action == domain.ReviewActionReject {
		decidedAt := handler.clock.Now().UTC()
		if decidedAt.IsZero() {
			return nil, domain.NewInternalError(nil)
		}
		decision, err := candidate.Review().Reject(policyVersion, command.Reason, identity.AuthID, decidedAt)
		if err != nil {
			return nil, err
		}
		return handler.decide(ctx, candidate, identity.AuthID, decision)
	}

	normalizedChecks, err := policy.ValidateConfirmation(confirmedCheckIDs)
	if err != nil {
		return nil, err
	}

	suspended, err := handler.developerSuspensionChecker.BlocksApproval(
		ctx, candidate.CurrentAdminID(), candidate.Review().SubmittedBy(),
	)
	if err != nil {
		return nil, domain.NewDeveloperStatusUnavailableError(err)
	}
	if suspended {
		// A suspended current administrator or submitter is not an ordinary
		// validation failure: the System permanently rejects the attempt and
		// skips both external approval checks.
		decidedAt := handler.clock.Now().UTC()
		if decidedAt.IsZero() {
			return nil, domain.NewInternalError(nil)
		}
		systemAuthID, err := handler.systemPrincipalResolver.ResolveReviewAutoRejection(ctx)
		if err != nil || !systemAuthID.IsValid() {
			return nil, domain.NewSystemPrincipalUnavailableError(err)
		}
		decision, err := candidate.Review().Reject(
			policyVersion, domain.SystemEligibilityRejectionReason, systemAuthID, decidedAt,
		)
		if err != nil {
			return nil, err
		}
		// The persisted actor is System, but the reviewer that initiated the
		// command remains the subject of the final conflict-of-interest recheck.
		return handler.decide(ctx, candidate, identity.AuthID, decision)
	}
	redirects := snapshot.OAuthRedirects()
	if err := handler.oauthPolicy.Validate(redirects.PKCERedirectURIs(), redirects.ConfidentialRedirectURIs()); err != nil {
		if errors.Is(err, port.ErrOAuthRedirectNotReviewable) {
			return nil, domain.ErrInvalidOAuthRedirectConfiguration
		}
		return nil, domain.NewInternalError(err)
	}

	scopeCatalogRevision, err := handler.scopeCatalog.EnsureAllRequestable(ctx, candidate.AllScopes())
	if err != nil {
		switch {
		case errors.Is(err, port.ErrScopeNotRequestable):
			return nil, domain.ErrInvalidApplicationScope
		case errors.Is(err, port.ErrScopeCatalogUnavailable):
			return nil, domain.NewScopeCatalogUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if scopeCatalogRevision < 1 {
		return nil, domain.NewInternalError(nil)
	}
	preflightPolicyVersion, err := handler.launchPolicy.Inspect(ctx, snapshot.LaunchURL())
	if err != nil {
		switch {
		case errors.Is(err, port.ErrLaunchURLNotReviewable):
			return nil, domain.ErrApplicationLaunchURLNotReviewable
		case errors.Is(err, port.ErrLaunchURLInspectionUnavailable):
			return nil, domain.NewLaunchURLInspectionUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if _, err := domain.NewPreflightPolicyVersion(preflightPolicyVersion.String()); err != nil {
		return nil, domain.NewInternalError(err)
	}
	approvalValidation, err := domain.NewApprovalValidation(scopeCatalogRevision, preflightPolicyVersion)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}

	decidedAt := handler.clock.Now().UTC()
	if decidedAt.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	decision, err := candidate.Review().Approve(
		policyVersion, normalizedChecks, command.Reason, approvalValidation, identity.AuthID, decidedAt,
	)
	if err != nil {
		return nil, err
	}
	return handler.decide(ctx, candidate, identity.AuthID, decision)
}

func (handler *DecideApplicationVersionReviewHandler) decide(
	ctx context.Context,
	candidate *domain.ApplicationReviewDecisionCandidate,
	reviewerID shared.AuthID,
	decision *domain.ApplicationReviewDecision,
) (*domain.ApplicationReviewDecisionResult, error) {
	result, err := handler.repository.Decide(ctx, candidate, reviewerID, decision)
	if err != nil {
		return nil, mapDecisionRepositoryError(err)
	}
	if result == nil || result.Review() == nil || result.Version() == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

func parseConfirmedCheckIDs(values []string) ([]domain.ReviewCheckID, error) {
	seen := make(map[domain.ReviewCheckID]struct{}, len(values))
	result := make([]domain.ReviewCheckID, 0, len(values))
	for _, value := range values {
		id, err := domain.NewReviewCheckID(value)
		if err != nil {
			return nil, domain.ErrApplicationReviewChecksIncomplete
		}
		if _, exists := seen[id]; exists {
			return nil, domain.ErrApplicationReviewChecksIncomplete
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func mapDecisionRepositoryError(err error) error {
	switch {
	case errors.Is(err, port.ErrApplicationReviewNotFound):
		return domain.ErrApplicationReviewNotFound
	case errors.Is(err, port.ErrApplicationReviewAlreadyDecided):
		return domain.ErrApplicationReviewAlreadyDecided
	case errors.Is(err, port.ErrApplicationReviewStateInconsistent):
		return domain.ErrApplicationReviewStateInconsistent
	case errors.Is(err, port.ErrApplicationReviewConflictOfInterest):
		return domain.ErrApplicationReviewConflictOfInterest
	default:
		return domain.NewInternalError(err)
	}
}
