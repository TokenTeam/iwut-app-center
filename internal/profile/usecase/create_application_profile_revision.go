package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

type DeveloperIdentity = shared.DeveloperIdentity
type CreateApplicationProfileRevisionCommand struct {
	ApplicationID string
	DisplayName   string
	Description   *string
	Icon          *string
}
type CreateApplicationProfileRevisionHandler struct {
	idGenerator port.ApplicationProfileRevisionIDGenerator
	clock       port.Clock
	repository  port.ApplicationProfileRevisionRepository
}

func NewCreateApplicationProfileRevisionHandler(idGenerator port.ApplicationProfileRevisionIDGenerator, clock port.Clock, repository port.ApplicationProfileRevisionRepository) *CreateApplicationProfileRevisionHandler {
	return &CreateApplicationProfileRevisionHandler{idGenerator, clock, repository}
}
func (h *CreateApplicationProfileRevisionHandler) Handle(ctx context.Context, identity DeveloperIdentity, command CreateApplicationProfileRevisionCommand) (*domain.ApplicationProfileRevision, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	applicationID, ok := shared.ParseApplicationID(command.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidApplicationID
	}
	name, err := domain.NewApplicationDisplayName(command.DisplayName)
	if err != nil {
		return nil, err
	}
	var description *domain.ApplicationDescription
	var icon *domain.ApplicationIcon
	if command.Description != nil {
		v, err := domain.NewApplicationDescription(*command.Description)
		if err != nil {
			return nil, err
		}
		description = &v
	}
	if command.Icon != nil {
		v, err := domain.NewApplicationIcon(*command.Icon)
		if err != nil {
			return nil, err
		}
		icon = &v
	}
	if h == nil || h.idGenerator == nil || h.clock == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	id, err := h.idGenerator.NewUUIDv7()
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	draft, err := domain.NewDraftApplicationProfileRevision(id, applicationID, name, description, icon, identity.AuthID, h.clock.Now().UTC())
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	revision, err := h.repository.CreateDraft(ctx, identity.AuthID, draft)
	if err != nil {
		switch {
		case errors.Is(err, port.ErrApplicationNotFound):
			return nil, domain.ErrApplicationNotFound
		case errors.Is(err, port.ErrApplicationAdminRequired):
			return nil, domain.ErrApplicationAdminRequired
		case errors.Is(err, port.ErrApplicationProfileWorkRevisionAlreadyExists):
			return nil, domain.ErrApplicationProfileWorkRevisionAlreadyExists
		case errors.Is(err, port.ErrApplicationProfileStateInconsistent):
			return nil, domain.NewApplicationProfileStateInconsistentError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if revision == nil {
		return nil, domain.NewInternalError(nil)
	}
	return revision, nil
}
