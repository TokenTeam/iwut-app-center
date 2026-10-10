package usecase

import (
	"context"
	"errors"
	"sync"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

type AbandonApplicationVersionDraftCommand struct {
	ApplicationID    string
	VersionID        string
	ExpectedRevision int64
}

type AbandonApplicationVersionDraftHandler struct {
	ids        port.ApplicationVersionAbandonmentIDGenerator
	clock      port.Clock
	repository port.ApplicationVersionAbandonmentRepository
}

func NewAbandonApplicationVersionDraftHandler(
	ids port.ApplicationVersionAbandonmentIDGenerator,
	clock port.Clock,
	repository port.ApplicationVersionAbandonmentRepository,
) *AbandonApplicationVersionDraftHandler {
	return &AbandonApplicationVersionDraftHandler{ids: ids, clock: clock, repository: repository}
}

func (h *AbandonApplicationVersionDraftHandler) Handle(
	ctx context.Context,
	identity shared.AuthenticatedUserIdentity,
	command AbandonApplicationVersionDraftCommand,
) (*domain.AbandonApplicationVersionDraftResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrUserIdentityRequired
	}
	applicationID, applicationOK := shared.ParseApplicationID(command.ApplicationID)
	versionID := domain.ApplicationVersionID(command.VersionID)
	if !applicationOK || !versionID.IsValid() || command.ExpectedRevision < 1 {
		return nil, domain.ErrInvalidApplicationVersionAbandonment
	}
	if h == nil || h.ids == nil || h.clock == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}

	var once sync.Once
	var fact *domain.ApplicationVersionAbandonment
	var factoryErr error
	var firstSequence domain.VersionSequence
	var firstLabel domain.VersionLabel
	factory := func(sequence domain.VersionSequence, label domain.VersionLabel) (*domain.ApplicationVersionAbandonment, error) {
		once.Do(func() {
			firstSequence, firstLabel = sequence, label
			id, err := h.ids.NewAbandonmentUUIDv7()
			if err != nil {
				factoryErr = err
				return
			}
			fact, factoryErr = domain.NewApplicationVersionAbandonment(
				id, applicationID, versionID, sequence, label, command.ExpectedRevision,
				identity.AuthID, h.clock.Now().UTC(),
			)
		})
		if firstSequence != sequence || firstLabel != label {
			return nil, domain.NewInternalError(nil)
		}
		return fact, factoryErr
	}

	result, err := h.repository.AbandonDraft(
		ctx, applicationID, versionID, identity.AuthID, command.ExpectedRevision, factory,
	)
	if err != nil {
		switch {
		case errors.Is(err, port.ErrApplicationVersionNotFound):
			return nil, domain.ErrApplicationVersionNotFound
		case errors.Is(err, port.ErrApplicationVersionRevisionConflict):
			return nil, domain.ErrApplicationVersionRevisionConflict
		case errors.Is(err, port.ErrApplicationVersionNotAbandonable):
			return nil, domain.ErrApplicationVersionNotAbandonable
		case errors.Is(err, port.ErrVersionAbandonmentNotAllowed):
			return nil, domain.ErrApplicationVersionAbandonmentNotAllowed
		case errors.Is(err, port.ErrVersionAbandonmentStateInconsistent):
			return nil, domain.ErrApplicationVersionAbandonmentStateInconsistent
		case errors.Is(err, port.ErrVersionAbandonmentUnavailable):
			return nil, domain.NewApplicationVersionAbandonmentUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if result == nil || result.Abandonment == nil || result.Abandonment.ApplicationID() != applicationID || result.Abandonment.VersionID() != versionID {
		return nil, domain.ErrApplicationVersionAbandonmentStateInconsistent
	}
	return result, nil
}
