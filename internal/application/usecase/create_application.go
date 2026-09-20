package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type DeveloperStatus = shared.DeveloperStatus

const (
	DeveloperStatusPending   = shared.DeveloperStatusPending
	DeveloperStatusApproved  = shared.DeveloperStatusApproved
	DeveloperStatusRejected  = shared.DeveloperStatusRejected
	DeveloperStatusSuspended = shared.DeveloperStatusSuspended
)

type DeveloperIdentity = shared.DeveloperIdentity

type CreateApplicationCommand struct {
	Name string
}

type CreateApplicationHandler struct {
	idGenerator  port.ApplicationIDGenerator
	clock        port.Clock
	repository   port.ApplicationRepository
	initialLimit int32
}

func NewCreateApplicationHandler(
	idGenerator port.ApplicationIDGenerator,
	clock port.Clock,
	repository port.ApplicationRepository,
	initialLimit int32,
) *CreateApplicationHandler {
	return &CreateApplicationHandler{
		idGenerator:  idGenerator,
		clock:        clock,
		repository:   repository,
		initialLimit: initialLimit,
	}
}

func (handler *CreateApplicationHandler) Handle(
	ctx context.Context,
	identity DeveloperIdentity,
	command CreateApplicationCommand,
) (*domain.Application, error) {
	if identity.AuthID == "" {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}

	name, err := domain.NewApplicationName(command.Name)
	if err != nil {
		return nil, err
	}

	if handler == nil || handler.idGenerator == nil || handler.clock == nil || handler.repository == nil || handler.initialLimit < 0 {
		return nil, domain.NewInternalError(nil)
	}

	id, err := handler.idGenerator.NewUUIDv7()
	if err != nil {
		return nil, domain.NewInternalError(err)
	}

	application, err := domain.NewApplication(id, name, identity.AuthID, handler.clock.Now().UTC())
	if err != nil {
		return nil, domain.NewInternalError(err)
	}

	err = handler.repository.CreateWithinQuota(ctx, application, handler.initialLimit)
	if err != nil {
		switch {
		case errors.Is(err, port.ErrApplicationNameAlreadyExists):
			return nil, domain.ErrApplicationNameAlreadyExists
		case errors.Is(err, port.ErrApplicationQuotaExceeded):
			return nil, domain.ErrApplicationQuotaExceeded
		default:
			return nil, domain.NewInternalError(err)
		}
	}

	return application, nil
}
