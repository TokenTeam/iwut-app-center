package usecase

import (
	"context"
	"errors"
	filterdomain "iwut-app-center/internal/filter/domain"
	filterport "iwut-app-center/internal/filter/port"
	"iwut-app-center/internal/shared"
)

type SetCommand struct {
	ExpectedRevision int64
	Rule             filterdomain.Rule
}
type ClearCommand struct{ ExpectedRevision int64 }

type Handlers struct {
	idGenerator filterport.RevisionIDGenerator
	clock       filterport.Clock
	repository  filterport.Repository
}

func NewHandlers(idGenerator filterport.RevisionIDGenerator, clock filterport.Clock, repository filterport.Repository) *Handlers {
	return &Handlers{idGenerator: idGenerator, clock: clock, repository: repository}
}

func (h *Handlers) Get(ctx context.Context, identity shared.DeveloperIdentity, rawApplicationID string) (*filterdomain.ApplicationFilter, error) {
	applicationID, err := validateRequest(identity, rawApplicationID)
	if err != nil {
		return nil, err
	}
	if h == nil || h.repository == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	filter, err := h.repository.LoadForAdmin(ctx, applicationID, identity.AuthID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if filter == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	return filter, nil
}

func (h *Handlers) Set(ctx context.Context, identity shared.DeveloperIdentity, rawApplicationID string, command SetCommand) (*filterdomain.ChangeResult, error) {
	applicationID, err := validateRequest(identity, rawApplicationID)
	if err != nil {
		return nil, err
	}
	if command.ExpectedRevision < 0 || command.Rule.Kind() == "" {
		return nil, filterdomain.ErrInvalidApplicationFilter
	}
	if h == nil || h.repository == nil || h.idGenerator == nil || h.clock == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	current, err := h.repository.LoadForAdmin(ctx, applicationID, identity.AuthID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if current == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	if current.Revision() != command.ExpectedRevision {
		return nil, filterdomain.ErrApplicationFilterRevisionConflict
	}
	if current.MatchesRule(command.Rule) {
		return filterdomain.NewChangeResult(current, false), nil
	}
	id, err := h.idGenerator.NewUUIDv7()
	if err != nil {
		return nil, filterdomain.NewInternalError(err)
	}
	next, revision, err := current.PublishRule(id, command.Rule, identity.AuthID, h.clock.Now().UTC())
	if err != nil {
		return nil, filterdomain.NewInternalError(err)
	}
	committed, err := h.repository.Commit(ctx, identity.AuthID, command.ExpectedRevision, next, revision)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if committed == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	return filterdomain.NewChangeResult(committed, true), nil
}

func (h *Handlers) Clear(ctx context.Context, identity shared.DeveloperIdentity, rawApplicationID string, command ClearCommand) (*filterdomain.ChangeResult, error) {
	applicationID, err := validateRequest(identity, rawApplicationID)
	if err != nil {
		return nil, err
	}
	if command.ExpectedRevision < 0 {
		return nil, filterdomain.ErrInvalidApplicationFilter
	}
	if h == nil || h.repository == nil || h.idGenerator == nil || h.clock == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	current, err := h.repository.LoadForAdmin(ctx, applicationID, identity.AuthID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if current == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	if current.Revision() != command.ExpectedRevision {
		return nil, filterdomain.ErrApplicationFilterRevisionConflict
	}
	if current.IsAllowAll() {
		return filterdomain.NewChangeResult(current, false), nil
	}
	id, err := h.idGenerator.NewUUIDv7()
	if err != nil {
		return nil, filterdomain.NewInternalError(err)
	}
	next, revision, err := current.PublishAllowAll(id, identity.AuthID, h.clock.Now().UTC())
	if err != nil {
		return nil, filterdomain.NewInternalError(err)
	}
	committed, err := h.repository.Commit(ctx, identity.AuthID, command.ExpectedRevision, next, revision)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if committed == nil {
		return nil, filterdomain.NewInternalError(nil)
	}
	return filterdomain.NewChangeResult(committed, true), nil
}

func validateRequest(identity shared.DeveloperIdentity, rawApplicationID string) (shared.ApplicationID, error) {
	if !identity.AuthID.IsValid() {
		return "", filterdomain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return "", filterdomain.ErrDeveloperApprovalRequired
	}
	applicationID, ok := shared.ParseApplicationID(rawApplicationID)
	if !ok {
		return "", filterdomain.ErrInvalidApplicationID
	}
	return applicationID, nil
}

func mapRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, filterport.ErrApplicationNotFound):
		return filterdomain.ErrApplicationNotFound
	case errors.Is(err, filterport.ErrApplicationAdminRequired):
		return filterdomain.ErrApplicationAdminRequired
	case errors.Is(err, filterport.ErrApplicationFilterRevisionConflict):
		return filterdomain.ErrApplicationFilterRevisionConflict
	case errors.Is(err, filterport.ErrApplicationFilterStateInconsistent):
		return filterdomain.NewStateInconsistentError(err)
	default:
		return filterdomain.NewInternalError(err)
	}
}
