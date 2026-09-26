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
)

// TestLaunchResolver authorizes and resolves only within one read-only snapshot.
// Capability failure carries domain.Error's validated MissingCapabilities list.
type TestLaunchResolver interface {
	ResolveForTester(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.TestLaunchDescriptor, error)
}
