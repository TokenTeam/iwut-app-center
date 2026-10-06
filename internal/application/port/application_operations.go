package port

import (
	"context"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type ApplicationOperationEventIDGenerator interface {
	NewUUIDv7() (domain.ApplicationOperationEventID, error)
}

type ApplicationOperationsRepository interface {
	Get(context.Context, shared.ApplicationID) (domain.ApplicationPlatformAvailability, error)
	Set(context.Context, shared.ApplicationID, shared.AuthID, domain.PlatformAvailabilityStatus, int64, int64, string, domain.ApplicationOperationEventID, time.Time) (domain.ApplicationPlatformAvailability, error)
}
