package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
)

var (
	ErrScopeNotRequestable                  = errors.New("scope is not requestable")
	ErrScopeCatalogUnavailable              = errors.New("scope catalog unavailable")
	ErrApplicationNotFound                  = errors.New("application not found")
	ErrApplicationAdminRequired             = errors.New("application administrator required")
	ErrApplicationVersionLabelAlreadyExists = errors.New("application version label already exists")
)

type ScopeCatalogRevision int64

type ScopeCatalog interface {
	EnsureAllRequestable(ctx context.Context, scopes []domain.ScopeName) (ScopeCatalogRevision, error)
}

type ApplicationVersionIDGenerator interface {
	NewUUIDv7() (domain.ApplicationVersionID, error)
}

type Clock interface {
	Now() time.Time
}

// ApplicationVersionRepository owns the atomic administrator check, sequence
// allocation, label uniqueness check and draft insertion.
type ApplicationVersionRepository interface {
	CreateDraft(
		ctx context.Context,
		expectedAdminID shared.AuthID,
		draft *domain.DraftApplicationVersion,
	) (*domain.ApplicationVersion, error)
}
