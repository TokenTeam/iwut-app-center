package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
	"time"
)

var (
	ErrScopeNotRequestable                     = errors.New("scope is not requestable")
	ErrScopeCatalogUnavailable                 = errors.New("scope catalog unavailable")
	ErrLaunchURLNotReviewable                  = errors.New("launch URL is not reviewable")
	ErrLaunchURLInspectionUnavailable          = errors.New("launch URL inspection unavailable")
	ErrApplicationVersionNotFound              = errors.New("application version not found")
	ErrApplicationAdminRequired                = errors.New("application administrator required")
	ErrApplicationVersionNotApproved           = errors.New("application version is not approved")
	ErrApplicationReviewStateInconsistent      = errors.New("application review state is inconsistent")
	ErrApplicationVersionRpcApiIncompatible    = errors.New("application version RPC API range is incompatible")
	ErrApplicationPublicationAlreadyExists     = errors.New("application publication already exists")
	ErrApplicationPublicationNotFound          = errors.New("application publication not found")
	ErrApplicationPublicationRevisionConflict  = errors.New("application publication revision conflict")
	ErrOAuthClientRegistrationRequired         = errors.New("OAuth client registration required")
	ErrApplicationProfileRequired              = errors.New("application profile required")
	ErrApplicationProfileStateInconsistent     = errors.New("application profile state inconsistent")
	ErrStablePublicationRequiredByGrey         = errors.New("stable publication required by grey")
	ErrApplicationPublicationStateInconsistent = errors.New("application publication state inconsistent")
)

type UUIDv7Generator interface{ NewUUIDv7() (string, error) }
type Clock interface{ Now() time.Time }
type ScopeCatalog interface {
	EnsureAllRequestable(context.Context, []domain.ScopeName) (domain.ScopeCatalogRevision, error)
}
type LaunchURLSubmissionPolicy interface {
	Inspect(context.Context, domain.LaunchURL) (domain.PreflightPolicyVersion, error)
}
type ApplicationPublicationRepository interface {
	LoadTestPlacementCandidate(context.Context, shared.ApplicationID, int32, domain.ApplicationVersionID, shared.AuthID, *int64) (*domain.TestPlacementCandidate, error)
	PlaceInTest(context.Context, *domain.TestPlacementCandidate, *domain.ApplicationPublicationID, domain.ApplicationPublicationHistoryID, shared.AuthID, domain.PublicationValidation, time.Time) (*domain.PlaceInTestResult, error)
}
type StablePublicationRepository interface {
	LoadStablePlacementCandidate(context.Context, shared.ApplicationID, int32, domain.ApplicationVersionID, shared.AuthID, *int64) (*domain.StablePlacementCandidate, error)
	SetStable(context.Context, *domain.StablePlacementCandidate, *domain.ApplicationPublicationID, domain.ApplicationPublicationHistoryID, shared.AuthID, domain.PublicationValidation, time.Time) (*domain.PlaceInTestResult, error)
	LoadStableClearCandidate(context.Context, shared.ApplicationID, int32, shared.AuthID, int64) (*domain.StableClearCandidate, error)
	ClearStable(context.Context, *domain.StableClearCandidate, domain.ApplicationPublicationHistoryID, shared.AuthID, time.Time) (*domain.PlaceInTestResult, error)
}
