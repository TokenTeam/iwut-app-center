package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

type RestoreRejectedApplicationVersionCommand struct {
	ExpectedVersionRevision int64
}

type RestoreRejectedApplicationVersionHandler struct {
	clock      port.Clock
	repository port.RejectedApplicationVersionRepository
}

func NewRestoreRejectedApplicationVersionHandler(
	clock port.Clock,
	repository port.RejectedApplicationVersionRepository,
) *RestoreRejectedApplicationVersionHandler {
	return &RestoreRejectedApplicationVersionHandler{clock: clock, repository: repository}
}

func (handler *RestoreRejectedApplicationVersionHandler) Handle(
	ctx context.Context,
	identity shared.DeveloperIdentity,
	applicationID shared.ApplicationID,
	versionID domain.ApplicationVersionID,
	reviewID domain.ApplicationReviewID,
	command RestoreRejectedApplicationVersionCommand,
) (*domain.RestoreRejectedVersionResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if command.ExpectedVersionRevision < 1 {
		return nil, domain.ErrApplicationVersionRevisionRequired
	}
	if !applicationID.IsValid() || !versionID.IsValid() || !reviewID.IsValid() {
		return nil, domain.ErrApplicationReviewNotFound
	}
	if handler == nil || handler.clock == nil || handler.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	restoredAt := handler.clock.Now().UTC()
	if restoredAt.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := handler.repository.RestoreDraft(
		ctx,
		applicationID,
		versionID,
		reviewID,
		identity.AuthID,
		command.ExpectedVersionRevision,
		restoredAt,
	)
	if err != nil {
		return nil, mapRestorationRepositoryError(err)
	}
	if result == nil || result.Review() == nil || result.Version() == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

func mapRestorationRepositoryError(err error) error {
	switch {
	case errors.Is(err, port.ErrApplicationReviewNotFound):
		return domain.ErrApplicationReviewNotFound
	case errors.Is(err, port.ErrApplicationAdminRequired):
		return domain.ErrApplicationAdminRequired
	case errors.Is(err, port.ErrApplicationReviewNotLatest):
		return domain.ErrApplicationReviewNotLatest
	case errors.Is(err, port.ErrApplicationReviewAlreadyRestored):
		return domain.ErrApplicationReviewAlreadyRestored
	case errors.Is(err, port.ErrApplicationVersionNotRejected):
		return domain.ErrApplicationVersionNotRejected
	case errors.Is(err, port.ErrApplicationVersionRevisionConflict):
		return domain.ErrApplicationVersionRevisionConflict
	case errors.Is(err, port.ErrApplicationReviewStateInconsistent):
		return domain.ErrApplicationReviewStateInconsistent
	default:
		return domain.NewInternalError(err)
	}
}
