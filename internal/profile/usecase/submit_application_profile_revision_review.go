package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

type SubmitApplicationProfileRevisionReviewCommand struct {
	ApplicationID     string
	ProfileRevisionID string
	ExpectedRevision  int64
}
type SubmitApplicationProfileRevisionReviewHandler struct {
	ids        port.ApplicationProfileReviewIDGenerator
	clock      port.Clock
	repository port.ApplicationProfileReviewRepository
}

func NewSubmitApplicationProfileRevisionReviewHandler(ids port.ApplicationProfileReviewIDGenerator, clock port.Clock, repository port.ApplicationProfileReviewRepository) *SubmitApplicationProfileRevisionReviewHandler {
	return &SubmitApplicationProfileRevisionReviewHandler{ids, clock, repository}
}
func (h *SubmitApplicationProfileRevisionReviewHandler) Handle(ctx context.Context, identity DeveloperIdentity, c SubmitApplicationProfileRevisionReviewCommand) (*domain.ApplicationProfileSubmission, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	app, ok := shared.ParseApplicationID(c.ApplicationID)
	id, err := domain.ParseApplicationProfileRevisionID(c.ProfileRevisionID)
	if !ok || err != nil || c.ExpectedRevision < 1 {
		return nil, domain.ErrInvalidApplicationProfileReviewSubmission
	}
	if h == nil || h.ids == nil || h.clock == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	reviewID, err := h.ids.NewUUIDv7()
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	if !reviewID.IsValid() {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.SubmitDraft(ctx, app, id, identity.AuthID, c.ExpectedRevision, reviewID, at)
	if err != nil {
		for _, pair := range []struct{ port, domain error }{
			{port.ErrApplicationProfileRevisionNotFound, domain.ErrApplicationProfileRevisionNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
			{port.ErrApplicationProfileRevisionNotDraft, domain.ErrApplicationProfileRevisionNotDraft}, {port.ErrApplicationProfileRevisionConflict, domain.ErrApplicationProfileRevisionConflict},
			{port.ErrInvalidApplicationProfileContent, domain.ErrInvalidApplicationProfileContent},
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
	if result == nil || result.ProfileRevision == nil || result.Review == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
