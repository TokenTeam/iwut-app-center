package usecase

import (
	"context"
	"sync"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type ApplicationArchiveCommand struct {
	ApplicationID           shared.ApplicationID
	ExpectedArchiveRevision int64
}

type ApplicationArchivalHandlers struct {
	repository port.ApplicationArchivalRepository
	ids        port.ApplicationArchiveEventIDGenerator
	clock      port.Clock
}

func NewApplicationArchivalHandlers(repository port.ApplicationArchivalRepository, ids port.ApplicationArchiveEventIDGenerator, clock port.Clock) *ApplicationArchivalHandlers {
	return &ApplicationArchivalHandlers{repository: repository, ids: ids, clock: clock}
}

func (h *ApplicationArchivalHandlers) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, applicationID shared.ApplicationID) (domain.ApplicationArchiveState, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationArchiveState{}, domain.ErrUserIdentityRequired
	}
	if !applicationID.IsValid() {
		return domain.ApplicationArchiveState{}, domain.ErrInvalidApplicationArchiveRequest
	}
	if h == nil || h.repository == nil {
		return domain.ApplicationArchiveState{}, domain.ErrApplicationArchiveUnavailable
	}
	return h.repository.GetArchiveState(ctx, applicationID, identity.AuthID)
}

func (h *ApplicationArchivalHandlers) Archive(ctx context.Context, identity shared.AuthenticatedUserIdentity, command ApplicationArchiveCommand) (domain.ApplicationArchiveState, error) {
	return h.set(ctx, identity, command, domain.ApplicationArchiveArchived)
}

func (h *ApplicationArchivalHandlers) Unarchive(ctx context.Context, identity shared.AuthenticatedUserIdentity, command ApplicationArchiveCommand) (domain.ApplicationArchiveState, error) {
	return h.set(ctx, identity, command, domain.ApplicationArchiveUnarchived)
}

func (h *ApplicationArchivalHandlers) set(ctx context.Context, identity shared.AuthenticatedUserIdentity, command ApplicationArchiveCommand, target domain.ApplicationArchiveStatus) (domain.ApplicationArchiveState, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationArchiveState{}, domain.ErrUserIdentityRequired
	}
	if !command.ApplicationID.IsValid() || command.ExpectedArchiveRevision < 1 {
		return domain.ApplicationArchiveState{}, domain.ErrInvalidApplicationArchiveRequest
	}
	if h == nil || h.repository == nil || h.ids == nil || h.clock == nil {
		return domain.ApplicationArchiveState{}, domain.ErrApplicationArchiveUnavailable
	}
	var once sync.Once
	var eventID domain.ApplicationArchiveEventID
	var occurredAt time.Time
	var eventErr error
	return h.repository.SetArchiveState(ctx, command.ApplicationID, identity.AuthID, target, command.ExpectedArchiveRevision, func() (domain.ApplicationArchiveEventID, time.Time, error) {
		once.Do(func() {
			eventID, eventErr = h.ids.NewUUIDv7()
			if eventErr != nil || !eventID.IsValid() {
				eventErr = domain.ErrApplicationArchiveUnavailable
				return
			}
			occurredAt = h.clock.Now().UTC()
		})
		return eventID, occurredAt, eventErr
	})
}
