package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

type UpdateDraftApplicationVersionCommand struct {
	ExpectedRevision          int64
	VersionLabel              string
	LaunchURL                 string
	RPCApiMinVersion          int32
	RPCApiMaxVersionExclusive int32
	RequiredCapabilities      []string
	RequiredScopes            []string
	OptionalScopes            []string
}

type UpdateDraftApplicationVersionHandler struct {
	scopeCatalog port.ScopeCatalog
	clock        port.Clock
	repository   port.ApplicationVersionRepository
}

func NewUpdateDraftApplicationVersionHandler(
	scopeCatalog port.ScopeCatalog,
	clock port.Clock,
	repository port.ApplicationVersionRepository,
) *UpdateDraftApplicationVersionHandler {
	return &UpdateDraftApplicationVersionHandler{scopeCatalog: scopeCatalog, clock: clock, repository: repository}
}

func (handler *UpdateDraftApplicationVersionHandler) Handle(
	ctx context.Context,
	identity DeveloperIdentity,
	applicationID shared.ApplicationID,
	versionID domain.ApplicationVersionID,
	command UpdateDraftApplicationVersionCommand,
) (*domain.ApplicationVersion, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if !applicationID.IsValid() {
		return nil, domain.ErrInvalidApplicationID
	}
	if !versionID.IsValid() {
		return nil, domain.ErrApplicationVersionNotFound
	}
	if command.ExpectedRevision < 1 {
		return nil, domain.ErrApplicationVersionRevisionRequired
	}

	versionLabel, err := domain.NewVersionLabel(command.VersionLabel)
	if err != nil {
		return nil, err
	}
	launchURL, err := domain.NewLaunchURL(command.LaunchURL)
	if err != nil {
		return nil, err
	}
	rpcAPIRange, err := domain.NewRPCApiRange(command.RPCApiMinVersion, command.RPCApiMaxVersionExclusive)
	if err != nil {
		return nil, err
	}
	requiredCapabilities, err := domain.NewCapabilitySet(command.RequiredCapabilities)
	if err != nil {
		return nil, err
	}
	scopeRequest, err := domain.NewScopeRequest(command.RequiredScopes, command.OptionalScopes)
	if err != nil {
		return nil, err
	}
	replacement, err := domain.NewDraftApplicationVersionReplacement(
		versionLabel, launchURL, rpcAPIRange, requiredCapabilities, scopeRequest,
	)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}

	if handler == nil || handler.scopeCatalog == nil || handler.clock == nil || handler.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	_, err = handler.scopeCatalog.EnsureAllRequestable(ctx, scopeRequest.All())
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

	updatedAt := handler.clock.Now().UTC()
	version, err := handler.repository.ReplaceDraft(
		ctx, applicationID, versionID, identity.AuthID, command.ExpectedRevision, replacement, updatedAt,
	)
	if err != nil {
		switch {
		case errors.Is(err, port.ErrApplicationVersionNotFound):
			return nil, domain.ErrApplicationVersionNotFound
		case errors.Is(err, port.ErrApplicationAdminRequired):
			return nil, domain.ErrApplicationAdminRequired
		case errors.Is(err, port.ErrApplicationVersionNotDraft):
			return nil, domain.ErrApplicationVersionNotDraft
		case errors.Is(err, port.ErrApplicationVersionRevisionConflict):
			return nil, domain.ErrApplicationVersionRevisionConflict
		case errors.Is(err, port.ErrApplicationVersionLabelAlreadyExists):
			return nil, domain.ErrApplicationVersionLabelAlreadyExists
		default:
			return nil, domain.NewInternalError(err)
		}
	}
	if version == nil {
		return nil, domain.NewInternalError(nil)
	}
	return version, nil
}
