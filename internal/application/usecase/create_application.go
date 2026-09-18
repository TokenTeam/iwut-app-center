package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
)

type DeveloperStatus string

const (
	DeveloperStatusPending   DeveloperStatus = "PENDING"
	DeveloperStatusApproved  DeveloperStatus = "APPROVED"
	DeveloperStatusRejected  DeveloperStatus = "REJECTED"
	DeveloperStatusSuspended DeveloperStatus = "SUSPENDED"
)

type DeveloperIdentity struct {
	AuthID          domain.AuthID
	DeveloperStatus DeveloperStatus
}

type CreateApplicationCommand struct {
	Name string
}

type CreateApplicationHandler struct {
	idGenerator port.ApplicationIDGenerator
	clock       port.Clock
	quotaPolicy port.ApplicationQuotaPolicy
	repository  port.ApplicationRepository
}

func NewCreateApplicationHandler(
	idGenerator port.ApplicationIDGenerator,
	clock port.Clock,
	quotaPolicy port.ApplicationQuotaPolicy,
	repository port.ApplicationRepository,
) *CreateApplicationHandler {
	return &CreateApplicationHandler{
		idGenerator: idGenerator,
		clock:       clock,
		quotaPolicy: quotaPolicy,
		repository:  repository,
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

	if handler == nil || handler.idGenerator == nil || handler.clock == nil || handler.quotaPolicy == nil || handler.repository == nil {
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

	limit, err := handler.quotaPolicy.LimitFor(ctx, identity.AuthID)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	if limit < 0 {
		return nil, domain.NewInternalError(nil)
	}

	err = handler.repository.CreateWithinQuota(ctx, application, limit)
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
