package port

import (
	"context"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type ApplicationArchiveEventIDGenerator interface {
	NewUUIDv7() (domain.ApplicationArchiveEventID, error)
}

type ApplicationArchiveEventFactory func() (domain.ApplicationArchiveEventID, time.Time, error)

type ApplicationArchivalRepository interface {
	GetArchiveState(context.Context, shared.ApplicationID, shared.AuthID) (domain.ApplicationArchiveState, error)
	SetArchiveState(context.Context, shared.ApplicationID, shared.AuthID, domain.ApplicationArchiveStatus, int64, ApplicationArchiveEventFactory) (domain.ApplicationArchiveState, error)
}
