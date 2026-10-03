package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
	"strings"
)

type PlaceApprovedVersionInTestSlotCommand struct {
	VersionID                   domain.ApplicationVersionID
	ExpectedPublicationRevision *int64
}
type PlaceApprovedVersionInTestSlotHandler struct {
	scopeCatalog port.ScopeCatalog
	launchPolicy port.LaunchURLSubmissionPolicy
	idGenerator  port.UUIDv7Generator
	clock        port.Clock
	repository   port.ApplicationPublicationRepository
}

func NewPlaceApprovedVersionInTestSlotHandler(catalog port.ScopeCatalog, policy port.LaunchURLSubmissionPolicy, ids port.UUIDv7Generator, clock port.Clock, repository port.ApplicationPublicationRepository) *PlaceApprovedVersionInTestSlotHandler {
	return &PlaceApprovedVersionInTestSlotHandler{catalog, policy, ids, clock, repository}
}
func (h *PlaceApprovedVersionInTestSlotHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, rpcAPIMajor int32, command PlaceApprovedVersionInTestSlotCommand) (*domain.PlaceInTestResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if rpcAPIMajor < 1 {
		return nil, domain.ErrInvalidRpcApiMajor
	}
	if !command.VersionID.IsValid() {
		return nil, domain.ErrInvalidApplicationVersionId
	}
	if command.ExpectedPublicationRevision != nil && *command.ExpectedPublicationRevision < 1 {
		return nil, domain.ErrInvalidApplicationPublicationRevision
	}
	if !applicationID.IsValid() {
		return nil, domain.ErrApplicationVersionNotFound
	}
	applicationID = shared.ApplicationID(strings.ToLower(applicationID.String()))
	command.VersionID = domain.ApplicationVersionID(strings.ToLower(command.VersionID.String()))
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadTestPlacementCandidate(ctx, applicationID, rpcAPIMajor, command.VersionID, identity.AuthID, command.ExpectedPublicationRevision)
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
	if revision < 1 {
		return nil, domain.NewInternalError(nil)
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
	value, err := h.idGenerator.NewUUIDv7()
	historyID := domain.ApplicationPublicationHistoryID(value)
	if err != nil || !historyID.IsValid() {
		return nil, domain.NewInternalError(err)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.PlaceInTest(ctx, candidate, publicationID, historyID, identity.AuthID, validation, at)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
func equalRevision(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func mapRepositoryError(err error) error {
	if errors.Is(err, port.ErrApplicationProfileStateInconsistent) || errors.Is(err, domain.ErrApplicationProfileStateInconsistent) {
		return domain.NewApplicationProfileStateInconsistentError(err)
	}
	for _, entry := range []struct {
		port   error
		domain error
	}{
		{port.ErrApplicationVersionNotFound, domain.ErrApplicationVersionNotFound},
		{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrApplicationVersionNotApproved, domain.ErrApplicationVersionNotApproved},
		{port.ErrApplicationReviewStateInconsistent, domain.ErrApplicationReviewStateInconsistent},
		{port.ErrApplicationVersionRpcApiIncompatible, domain.ErrApplicationVersionRpcApiIncompatible},
		{port.ErrApplicationPublicationAlreadyExists, domain.ErrApplicationPublicationAlreadyExists},
		{port.ErrApplicationPublicationNotFound, domain.ErrApplicationPublicationNotFound},
		{port.ErrApplicationPublicationRevisionConflict, domain.ErrApplicationPublicationRevisionConflict},
		{port.ErrOAuthClientRegistrationRequired, domain.ErrOAuthClientRegistrationRequired},
		{port.ErrApplicationProfileRequired, domain.ErrApplicationProfileRequired},
	} {
		if errors.Is(err, entry.port) || errors.Is(err, entry.domain) {
			return entry.domain
		}
	}
	return domain.NewInternalError(err)
}
