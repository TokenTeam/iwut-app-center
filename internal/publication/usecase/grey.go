package usecase

import (
	"context"
	"errors"
	"io"
	"strings"

	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

type SetGreyRolloutCommand struct {
	VersionID                   domain.ApplicationVersionID
	ExposureBasisPoints         int32
	ExpectedPublicationRevision int64
}

type SetGreyRolloutHandler struct {
	scopeCatalog port.ScopeCatalog
	launchPolicy port.LaunchURLSubmissionPolicy
	idGenerator  port.UUIDv7Generator
	random       port.RandomBytes
	clock        port.Clock
	repository   port.GreyPublicationRepository
}

func NewSetGreyRolloutHandler(catalog port.ScopeCatalog, policy port.LaunchURLSubmissionPolicy, ids port.UUIDv7Generator, random port.RandomBytes, clock port.Clock, repository port.GreyPublicationRepository) *SetGreyRolloutHandler {
	return &SetGreyRolloutHandler{catalog, policy, ids, random, clock, repository}
}

func (h *SetGreyRolloutHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, rpcAPIMajor int32, command SetGreyRolloutCommand) (*domain.PlaceInTestResult, error) {
	if err := validateStableIdentityAndPartition(identity, applicationID, rpcAPIMajor); err != nil {
		return nil, err
	}
	if !command.VersionID.IsValid() {
		return nil, domain.ErrInvalidApplicationVersionId
	}
	exposure, err := domain.NewExposureBasisPoints(command.ExposureBasisPoints)
	if err != nil {
		return nil, err
	}
	if command.ExpectedPublicationRevision < 1 {
		return nil, domain.ErrInvalidApplicationPublicationRevision
	}
	applicationID = shared.ApplicationID(strings.ToLower(applicationID.String()))
	command.VersionID = domain.ApplicationVersionID(strings.ToLower(command.VersionID.String()))
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadGreyPlacementCandidate(ctx, applicationID, rpcAPIMajor, command.VersionID, exposure, identity.AuthID, command.ExpectedPublicationRevision)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if candidate == nil || candidate.ApplicationID() != applicationID || candidate.RPCAPIMajor() != rpcAPIMajor || candidate.VersionID() != command.VersionID || candidate.ExposureBasisPoints() != exposure || candidate.ExpectedPublicationRevision() != command.ExpectedPublicationRevision {
		return nil, domain.NewInternalError(nil)
	}
	if candidate.IsNoOp() {
		return domain.NewNoOpResult(candidate.Publication())
	}
	if h.idGenerator == nil || h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	var validation *domain.PublicationValidation
	if candidate.RequiresValidation() {
		approved := candidate.ApprovedCandidate()
		if approved == nil || h.scopeCatalog == nil || h.launchPolicy == nil {
			return nil, domain.NewInternalError(nil)
		}
		revision, validateErr := h.scopeCatalog.EnsureAllRequestable(ctx, approved.AllScopes())
		if validateErr != nil {
			switch {
			case errors.Is(validateErr, port.ErrScopeNotRequestable):
				return nil, domain.ErrInvalidApplicationScope
			case errors.Is(validateErr, port.ErrScopeCatalogUnavailable):
				return nil, domain.NewScopeCatalogUnavailableError(validateErr)
			default:
				return nil, domain.NewInternalError(validateErr)
			}
		}
		policy, validateErr := h.launchPolicy.Inspect(ctx, approved.Snapshot().LaunchURL())
		if validateErr != nil {
			switch {
			case errors.Is(validateErr, port.ErrLaunchURLNotReviewable):
				return nil, domain.ErrApplicationLaunchURLNotReviewable
			case errors.Is(validateErr, port.ErrLaunchURLInspectionUnavailable):
				return nil, domain.NewLaunchURLInspectionUnavailableError(validateErr)
			default:
				return nil, domain.NewInternalError(validateErr)
			}
		}
		value := domain.PublicationValidation{ScopeCatalogRevision: revision, PreflightPolicyVersion: policy}
		if !value.Valid() {
			return nil, domain.NewInternalError(nil)
		}
		validation = &value
	}
	var rolloutID *domain.GreyRolloutID
	var seed *domain.CohortSeed
	if candidate.ChangeKind() == domain.GreyChangeStart {
		if h.random == nil {
			return nil, domain.NewInternalError(nil)
		}
		value, idErr := h.idGenerator.NewUUIDv7()
		id := domain.GreyRolloutID(value)
		if idErr != nil || !id.IsValid() {
			return nil, domain.NewInternalError(idErr)
		}
		rolloutID = &id
		bytes := make([]byte, 32)
		if _, randomErr := io.ReadFull(h.random, bytes); randomErr != nil {
			return nil, domain.NewInternalError(randomErr)
		}
		cohort, seedErr := domain.NewCohortSeed(bytes)
		if seedErr != nil {
			return nil, domain.NewInternalError(seedErr)
		}
		seed = &cohort
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
	result, err := h.repository.SetGrey(ctx, candidate, rolloutID, seed, historyID, identity.AuthID, validation, at)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

type ClearGreyRolloutCommand struct{ ExpectedPublicationRevision int64 }
type ClearGreyRolloutHandler struct {
	idGenerator port.UUIDv7Generator
	clock       port.Clock
	repository  port.GreyPublicationRepository
}

func NewClearGreyRolloutHandler(ids port.UUIDv7Generator, clock port.Clock, repository port.GreyPublicationRepository) *ClearGreyRolloutHandler {
	return &ClearGreyRolloutHandler{ids, clock, repository}
}
func (h *ClearGreyRolloutHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, rpcAPIMajor int32, command ClearGreyRolloutCommand) (*domain.PlaceInTestResult, error) {
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
	candidate, err := h.repository.LoadGreyClearCandidate(ctx, applicationID, rpcAPIMajor, identity.AuthID, command.ExpectedPublicationRevision)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if candidate == nil {
		return nil, domain.NewInternalError(nil)
	}
	publication := candidate.Publication()
	if publication == nil || publication.ApplicationID() != applicationID || publication.RPCAPIMajor() != rpcAPIMajor {
		return nil, domain.NewInternalError(nil)
	}
	if candidate.IsNoOp() {
		return domain.NewNoOpResult(publication)
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
	result, err := h.repository.ClearGrey(ctx, candidate, historyID, identity.AuthID, at)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
