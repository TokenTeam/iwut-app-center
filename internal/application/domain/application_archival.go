package domain

import (
	"errors"

	"iwut-app-center/internal/shared"
)

type ApplicationArchiveEventID string

func (id ApplicationArchiveEventID) String() string { return string(id) }
func (id ApplicationArchiveEventID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ApplicationArchiveState struct {
	ApplicationID              shared.ApplicationID
	LifecycleStatus            ApplicationLifecycleStatus
	PlatformAvailabilityStatus PlatformAvailabilityStatus
	ArchiveStatus              ApplicationArchiveStatus
	ArchiveRevision            int64
}

var (
	ErrInvalidApplicationArchiveRequest = errors.New("invalid application archive request")
	ErrApplicationArchiveNotFound       = errors.New("application archive not found")
	ErrApplicationArchiveConflict       = errors.New("application archive conflict")
	ErrApplicationArchiveInconsistent   = errors.New("application archive state inconsistent")
	ErrApplicationArchiveUnavailable    = errors.New("application archive unavailable")
)
