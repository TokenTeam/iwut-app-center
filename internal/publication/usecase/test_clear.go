package usecase

import (
	"context"
	"strings"
	"sync"
	"time"

	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

type ClearTestSlotCommand struct{ ExpectedPublicationRevision int64 }
type ClearTestSlotHandler struct {
	ids        port.UUIDv7Generator
	clock      port.Clock
	repository port.TestPublicationClearRepository
}

func NewClearTestSlotHandler(ids port.UUIDv7Generator, clock port.Clock, repository port.TestPublicationClearRepository) *ClearTestSlotHandler {
	return &ClearTestSlotHandler{ids, clock, repository}
}

func (h *ClearTestSlotHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, app shared.ApplicationID, major int32, command ClearTestSlotCommand) (*domain.PlaceInTestResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if !app.IsValid() {
		return nil, domain.ErrInvalidApplicationID
	}
	if err := validateStableIdentityAndPartition(identity, app, major); err != nil {
		return nil, err
	}
	if command.ExpectedPublicationRevision < 1 {
		return nil, domain.ErrInvalidApplicationPublicationRevision
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	app = shared.ApplicationID(strings.ToLower(app.String()))
	var once sync.Once
	var id domain.ApplicationPublicationHistoryID
	var at time.Time
	var auditErr error
	factory := func() (domain.ApplicationPublicationHistoryID, time.Time, error) {
		once.Do(func() {
			if h.ids == nil || h.clock == nil {
				auditErr = domain.NewInternalError(nil)
				return
			}
			value, err := h.ids.NewUUIDv7()
			id = domain.ApplicationPublicationHistoryID(value)
			if err != nil || !id.IsValid() {
				auditErr = domain.NewInternalError(err)
				return
			}
			at = h.clock.Now().UTC()
			if at.IsZero() {
				auditErr = domain.NewInternalError(nil)
			}
		})
		return id, at, auditErr
	}
	result, err := h.repository.ClearTest(ctx, app, major, identity.AuthID, command.ExpectedPublicationRevision, factory)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil || result.Publication() == nil || result.Publication().ApplicationID() != app || result.Publication().RPCAPIMajor() != major || result.Publication().TestVersionIDPtr() != nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
