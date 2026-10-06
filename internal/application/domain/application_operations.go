package domain

import (
	"errors"
	"time"

	"iwut-app-center/internal/shared"
)

type ApplicationOperationEventID string

func (id ApplicationOperationEventID) String() string { return string(id) }
func (id ApplicationOperationEventID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ApplicationPlatformAvailability struct {
	ApplicationID        shared.ApplicationID
	LifecycleStatus      ApplicationLifecycleStatus
	LifecycleRevision    int64
	Status               PlatformAvailabilityStatus
	Revision             int64
	LastOperationEventID *ApplicationOperationEventID
	SuspendedAt          *time.Time
	RestoredAt           *time.Time
}

var (
	ErrUserIdentityRequired                  = errors.New("user identity required")
	ErrApplicationOperationForbidden         = errors.New("application operation forbidden")
	ErrInvalidPlatformAvailabilityRevision   = errors.New("invalid platform availability revision")
	ErrInvalidApplicationOperationReason     = errors.New("invalid application operation reason")
	ErrApplicationAvailabilityConflict       = errors.New("application availability conflict")
	ErrApplicationOperationStateInconsistent = errors.New("application operation state inconsistent")
	ErrApplicationOperationUnavailable       = errors.New("application operation unavailable")
)
