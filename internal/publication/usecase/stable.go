package usecase

import (
	"context"
	"errors"
	"strings"

	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

type SetApprovedVersionInStableSlotCommand struct {
	VersionID                   domain.ApplicationVersionID
	ExpectedPublicationRevision *int64
}

type SetApprovedVersionInStableSlotHandler struct {
	scopeCatalog port.ScopeCatalog
	launchPolicy port.LaunchURLSubmissionPolicy
	idGenerator  port.UUIDv7Generator
	clock        port.Clock
	repository   port.StablePublicationRepository
}

func NewSetApprovedVersionInStableSlotHandler(catalog port.ScopeCatalog, policy port.LaunchURLSubmissionPolicy, ids port.UUIDv7Generator, clock port.Clock, repository port.StablePublicationRepository) *SetApprovedVersionInStableSlotHandler {
	return &SetApprovedVersionInStableSlotHandler{catalog, policy, ids, clock, repository}
}

func (h *SetApprovedVersionInStableSlotHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, rpcAPIMajor int32, command SetApprovedVersionInStableSlotCommand) (*domain.PlaceInTestResult, error) {
	if err := validateStableIdentityAndPartition(identity, applicationID, rpcAPIMajor); err != nil {
		return nil, err
	}
	if !command.VersionID.IsValid() {
		return nil, domain.ErrInvalidApplicationVersionId
	}
	if command.ExpectedPublicationRevision != nil && *command.ExpectedPublicationRevision < 1 {
		return nil, domain.ErrInvalidApplicationPublicationRevision
	}
	applicationID = shared.ApplicationID(strings.ToLower(applicationID.String()))
	command.VersionID = domain.ApplicationVersionID(strings.ToLower(command.VersionID.String()))
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadStablePlacementCandidate(ctx, applicationID, rpcAPIMajor, command.VersionID, identity.AuthID, command.ExpectedPublicationRevision)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if candidate == nil || candidate.ApplicationID() != applicationID || candidate.RPCAPIMajor() != rpcAPIMajor || candidate.VersionID() != command.VersionID || !equalRevision(candidate.ExpectedPublicationRevision(), command.ExpectedPublicationRevision) {
		return nil, domain.NewInternalError(nil)
	}
	if candidate.IsNoOp() {
		return domain.NewNoOpResult(candidate.Publication())
	}
	if h.scopeCatalog == nil || h.launchPolicy == nil || h.idGenerator == nil || h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	revision, err := h.scopeCatalog.EnsureAllRequestable(ctx, candidate.AllScopes())
	if err != nil {
		switch {
		case errors.Is(err, port.ErrScopeNotRequestable):
			return nil, domain.ErrInvalidApplicationScope
		case errors.Is(err, port.ErrScopeCatalogUnavailable):
			return nil, domain.NewScopeCatalogUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	policy, err := h.launchPolicy.Inspect(ctx, candidate.Snapshot().LaunchURL())
	if err != nil {
		switch {
		case errors.Is(err, port.ErrLaunchURLNotReviewable):
			return nil, domain.ErrApplicationLaunchURLNotReviewable
		case errors.Is(err, port.ErrLaunchURLInspectionUnavailable):
			return nil, domain.NewLaunchURLInspectionUnavailableError(err)
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	validation := domain.PublicationValidation{ScopeCatalogRevision: revision, PreflightPolicyVersion: policy}
	if !validation.Valid() {
		return nil, domain.NewInternalError(nil)
	}
	var publicationID *domain.ApplicationPublicationID
	if candidate.Publication() == nil {
		value, idErr := h.idGenerator.NewUUIDv7()
		id := domain.ApplicationPublicationID(value)
		if idErr != nil || !id.IsValid() {
			return nil, domain.NewInternalError(idErr)
		}
		publicationID = &id
	}
	value, idErr := h.idGenerator.NewUUIDv7()
	historyID := domain.ApplicationPublicationHistoryID(value)
	if idErr != nil || !historyID.IsValid() {
		return nil, domain.NewInternalError(idErr)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.SetStable(ctx, candidate, publicationID, historyID, identity.AuthID, validation, at)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

type ClearStableSlotCommand struct{ ExpectedPublicationRevision int64 }

type ClearStableSlotHandler struct {
	idGenerator port.UUIDv7Generator
	clock       port.Clock
	repository  port.StablePublicationRepository
}

func NewClearStableSlotHandler(ids port.UUIDv7Generator, clock port.Clock, repository port.StablePublicationRepository) *ClearStableSlotHandler {
	return &ClearStableSlotHandler{ids, clock, repository}
}

func (h *ClearStableSlotHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, rpcAPIMajor int32, command ClearStableSlotCommand) (*domain.PlaceInTestResult, error) {
	if err := validateStableIdentityAndPartition(identity, applicationID, rpcAPIMajor); err != nil {
		return nil, err
	}
	if command.ExpectedPublicationRevision < 1 {
		return nil, domain.ErrInvalidApplicationPublicationRevision
	}
	applicationID = shared.ApplicationID(strings.ToLower(applicationID.String()))
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadStableClearCandidate(ctx, applicationID, rpcAPIMajor, identity.AuthID, command.ExpectedPublicationRevision)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if candidate == nil || candidate.Publication() == nil || candidate.Publication().ApplicationID() != applicationID || candidate.Publication().RPCAPIMajor() != rpcAPIMajor {
		return nil, domain.NewInternalError(nil)
	}
	if candidate.IsNoOp() {
		return domain.NewNoOpResult(candidate.Publication())
	}
	if h.idGenerator == nil || h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	value, idErr := h.idGenerator.NewUUIDv7()
	historyID := domain.ApplicationPublicationHistoryID(value)
	if idErr != nil || !historyID.IsValid() {
		return nil, domain.NewInternalError(idErr)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.ClearStable(ctx, candidate, historyID, identity.AuthID, at)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

func validateStableIdentityAndPartition(identity shared.DeveloperIdentity, applicationID shared.ApplicationID, rpcAPIMajor int32) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return domain.ErrDeveloperApprovalRequired
	}
	if rpcAPIMajor < 1 {
		return domain.ErrInvalidRpcApiMajor
	}
	if !applicationID.IsValid() {
		return domain.ErrApplicationVersionNotFound
	}
	return nil
}
