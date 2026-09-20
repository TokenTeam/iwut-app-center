package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrScopeNotRequestable                = errors.New("scope is not requestable")
	ErrScopeCatalogUnavailable            = errors.New("scope catalog unavailable")
	ErrLaunchURLNotReviewable             = errors.New("launch URL is not reviewable")
	ErrLaunchURLInspectionUnavailable     = errors.New("launch URL inspection unavailable")
	ErrApplicationVersionNotFound         = errors.New("application version not found")
	ErrApplicationAdminRequired           = errors.New("application administrator required")
	ErrApplicationVersionNotDraft         = errors.New("application version is not a draft")
	ErrApplicationVersionRevisionConflict = errors.New("application version revision conflict")
)

type ScopeCatalog interface {
	EnsureAllRequestable(ctx context.Context, scopes []domain.ScopeName) (domain.ScopeCatalogRevision, error)
}

type LaunchURLSubmissionPolicy interface {
	Inspect(ctx context.Context, launchURL domain.LaunchURL) (domain.PreflightPolicyVersion, error)
}

type ApplicationReviewIDGenerator interface {
	NewUUIDv7() (domain.ApplicationReviewID, error)
}

type Clock interface {
	Now() time.Time
}

type ApplicationReviewRepository interface {
	LoadSubmissionCandidate(
		ctx context.Context,
		applicationID shared.ApplicationID,
		versionID domain.ApplicationVersionID,
		expectedAdminID shared.AuthID,
		expectedRevision int64,
	) (*domain.SubmissionCandidate, error)

	Submit(
		ctx context.Context,
		candidate *domain.SubmissionCandidate,
		reviewID domain.ApplicationReviewID,
		expectedAdminID shared.AuthID,
		scopeCatalogRevision domain.ScopeCatalogRevision,
		preflightPolicyVersion domain.PreflightPolicyVersion,
		submittedAt time.Time,
	) (*domain.ReviewSubmissionResult, error)
}
