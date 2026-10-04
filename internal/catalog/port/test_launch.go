package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrApplicationNotFound                    = errors.New("application not found")
	ErrApplicationTesterRequired              = errors.New("active application tester membership is required")
	ErrApplicationTestTargetUnavailable       = errors.New("application test target is unavailable")
	ErrApplicationTestPublicationInconsistent = errors.New("application test publication is inconsistent")
	ErrApplicationLaunchTargetUnavailable     = errors.New("application launch target is unavailable")
	ErrApplicationRuntimeStateInconsistent    = errors.New("application runtime state is inconsistent")
	ErrInvalidPageToken                       = errors.New("invalid page token")
	ErrPublicApplicationNotFound              = errors.New("public application not found")
	ErrApplicationCatalogStateInconsistent    = errors.New("application catalog state is inconsistent")
)

// TestLaunchResolver authorizes and resolves only within one read-only snapshot.
// Capability failure carries domain.Error's validated MissingCapabilities list.
type TestLaunchResolver interface {
	ResolveForTester(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.TestLaunchDescriptor, error)
}

// LaunchTargetResolver resolves one exact-major target. An empty auth ID means
// anonymous; a non-empty value has already crossed the trusted identity edge.
type LaunchTargetResolver interface {
	Resolve(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error)
}

type PublicApplicationCatalogRepository interface {
	ListPublic(context.Context, shared.AuthID, int32, []domain.CapabilityName, int32, string) (*domain.PublicApplicationCatalogPage, error)
	GetPublic(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.PublicApplicationCatalogItem, error)
}
