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
	ErrApplicationVersionNotFound           = errors.New("application version not found")
	ErrApplicationVersionNotDraft           = errors.New("application version not draft")
	ErrApplicationVersionRevisionConflict   = errors.New("application version revision conflict")
	ErrApplicationVersionLabelAlreadyExists = errors.New("application version label already exists")
	ErrInvalidOAuthRedirectConfiguration    = errors.New("OAuth redirect configuration is invalid")
	ErrApplicationVersionNotAbandonable     = errors.New("application version not abandonable")
	ErrVersionAbandonmentNotAllowed         = errors.New("application version abandonment not allowed")
	ErrVersionAbandonmentStateInconsistent  = errors.New("application version abandonment state inconsistent")
	ErrVersionAbandonmentUnavailable        = errors.New("application version abandonment unavailable")
)

type ScopeCatalogRevision int64

type ScopeCatalog interface {
	EnsureAllRequestable(ctx context.Context, scopes []domain.ScopeName) (ScopeCatalogRevision, error)
}

type OAuthRedirectPolicy interface {
	EnsureCanonical(pkceRedirectURIs, confidentialRedirectURIs []string) error
}

type ApplicationVersionIDGenerator interface {
	NewUUIDv7() (domain.ApplicationVersionID, error)
}

type ApplicationVersionAbandonmentIDGenerator interface {
	NewAbandonmentUUIDv7() (domain.ApplicationVersionAbandonmentID, error)
}

// ApplicationVersionAbandonmentFactory is called only after the repository
// has proved that this is a first successful abandonment. Implementations must
// memoize the returned identity and time so MongoDB transaction retries reuse
// the same immutable fact.
type ApplicationVersionAbandonmentFactory func(
	sequence domain.VersionSequence,
	label domain.VersionLabel,
) (*domain.ApplicationVersionAbandonment, error)

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
	ReplaceDraft(
		ctx context.Context,
		applicationID shared.ApplicationID,
		versionID domain.ApplicationVersionID,
		expectedAdminID shared.AuthID,
		expectedRevision int64,
		replacement domain.DraftApplicationVersionReplacement,
		updatedAt time.Time,
	) (*domain.ApplicationVersion, error)
}

type ApplicationVersionAbandonmentRepository interface {
	AbandonDraft(
		ctx context.Context,
		applicationID shared.ApplicationID,
		versionID domain.ApplicationVersionID,
		expectedAdminID shared.AuthID,
		expectedRevision int64,
		factory ApplicationVersionAbandonmentFactory,
	) (*domain.AbandonApplicationVersionDraftResult, error)
}
