package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

type UpdateDraftApplicationProfileRevisionCommand struct {
	ApplicationID     string
	ProfileRevisionID string
	ExpectedRevision  int64
	DisplayName       string
	Description       *string
	Icon              *string
}
type UpdateDraftApplicationProfileRevisionHandler struct {
	clock      port.Clock
	repository port.DraftApplicationProfileRevisionRepository
}

func NewUpdateDraftApplicationProfileRevisionHandler(clock port.Clock, repository port.DraftApplicationProfileRevisionRepository) *UpdateDraftApplicationProfileRevisionHandler {
	return &UpdateDraftApplicationProfileRevisionHandler{clock, repository}
}
func (h *UpdateDraftApplicationProfileRevisionHandler) Handle(ctx context.Context, identity DeveloperIdentity, c UpdateDraftApplicationProfileRevisionCommand) (*domain.ApplicationProfileRevision, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	app, ok := shared.ParseApplicationID(c.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidApplicationID
	}
	id, err := domain.ParseApplicationProfileRevisionID(c.ProfileRevisionID)
	if err != nil {
		return nil, domain.ErrInvalidApplicationProfileRevisionID
	}
	if c.ExpectedRevision < 1 {
		return nil, domain.ErrApplicationProfileExpectedRevisionRequired
	}
	replacement, err := domain.NewDraftApplicationProfileReplacement(c.DisplayName, c.Description, c.Icon)
	if err != nil {
		return nil, err
	}
	if h == nil || h.clock == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.ReplaceDraft(ctx, app, id, identity.AuthID, c.ExpectedRevision, replacement, at)
	if err != nil {
		for _, pair := range []struct{ port, domain error }{
			{port.ErrApplicationProfileRevisionNotFound, domain.ErrApplicationProfileRevisionNotFound},
			{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
			{port.ErrApplicationProfileRevisionNotDraft, domain.ErrApplicationProfileRevisionNotDraft},
			{port.ErrApplicationProfileRevisionConflict, domain.ErrApplicationProfileRevisionConflict},
		} {
			if errors.Is(err, pair.port) {
				return nil, pair.domain
			}
		}
		if errors.Is(err, port.ErrApplicationProfileStateInconsistent) {
			return nil, domain.NewApplicationProfileStateInconsistentError(err)
		}
		return nil, domain.NewInternalError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
