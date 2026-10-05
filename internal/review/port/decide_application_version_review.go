package port

import (
	"context"
	"errors"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
)

var (
	// ErrApplicationReviewNotFound hides a missing Review and every mismatched
	// path relationship.
	ErrApplicationReviewNotFound = errors.New("application review not found")
	// ErrApplicationReviewAlreadyDecided reports a Review that is no longer
	// PENDING or already carries a decision.
	ErrApplicationReviewAlreadyDecided = errors.New("application review already decided")
	// ErrApplicationReviewStateInconsistent reports a Version state/revision,
	// snapshot or administrator fact that no legal transition can produce.
	ErrApplicationReviewStateInconsistent = errors.New("application review state inconsistent")
	// ErrApplicationReviewConflictOfInterest reports a reviewer that is the
	// current administrator, the Version creator or the Review submitter.
	ErrApplicationReviewConflictOfInterest = errors.New("application review conflict of interest")
	// ErrReviewPolicyChanged reports an unknown, retired or otherwise unusable
	// ReviewPolicyVersion.
	ErrReviewPolicyChanged = errors.New("review policy changed")
	// ErrDeveloperStatusUnavailable reports that suspension facts could not be
	// read. It never means "not suspended".
	ErrDeveloperStatusUnavailable = errors.New("developer status unavailable")
	ErrSystemPrincipalUnavailable = errors.New("system principal unavailable")
)

// ReviewPolicyProvider is owned by the consuming Review capability. App Center
// implements it with its immutable local policy repository.
type ReviewPolicyProvider interface {
	RequireUsable(
		ctx context.Context,
		expectedVersion domain.ReviewPolicyVersion,
		snapshot domain.ApplicationVersionReviewSnapshot,
	) (*domain.VersionReviewPolicy, error)
}

// DeveloperApprovalChecker is a narrow port for the Auth-owned suspension
// fact. It intentionally does not expose Auth transport, tokens or claims.
type DeveloperApprovalChecker interface {
	BlocksApproval(ctx context.Context, currentAdminID, submittedBy shared.AuthID) (bool, error)
}

type SystemPrincipalResolver interface {
	ResolveReviewAutoRejection(ctx context.Context) (shared.AuthID, error)
}

// ApplicationReviewDecisionRepository owns loading a decidable candidate and
// atomically writing the one-time decision together with the Version
// lifecycle/audit transition.
type ApplicationReviewDecisionRepository interface {
	LoadDecisionCandidate(
		ctx context.Context,
		applicationID shared.ApplicationID,
		versionID domain.ApplicationVersionID,
		reviewID domain.ApplicationReviewID,
		reviewerID shared.AuthID,
	) (*domain.ApplicationReviewDecisionCandidate, error)

	Decide(
		ctx context.Context,
		candidate *domain.ApplicationReviewDecisionCandidate,
		reviewerID shared.AuthID,
		decision *domain.ApplicationReviewDecision,
	) (*domain.ApplicationReviewDecisionResult, error)
}
