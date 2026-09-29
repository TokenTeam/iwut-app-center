package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

type DeveloperIdentity = shared.DeveloperIdentity
type DeveloperStatus = shared.DeveloperStatus

const (
	DeveloperStatusPending   = shared.DeveloperStatusPending
	DeveloperStatusApproved  = shared.DeveloperStatusApproved
	DeveloperStatusRejected  = shared.DeveloperStatusRejected
	DeveloperStatusSuspended = shared.DeveloperStatusSuspended
)

type CreateApplicationVersionCommand struct {
	ApplicationID             string
	VersionLabel              string
	LaunchURL                 string
	RPCApiMinVersion          int32
	RPCApiMaxVersionExclusive int32
	RequiredCapabilities      []string
	RequiredScopes            []string
	OptionalScopes            []string
	PKCERedirectURIs          []string
	ConfidentialRedirectURIs  []string
}

type CreateApplicationVersionHandler struct {
	scopeCatalog port.ScopeCatalog
	oauthPolicy  port.OAuthRedirectPolicy
	idGenerator  port.ApplicationVersionIDGenerator
	clock        port.Clock
	repository   port.ApplicationVersionRepository
}

func NewCreateApplicationVersionHandler(
	scopeCatalog port.ScopeCatalog,
	oauthPolicy port.OAuthRedirectPolicy,
	idGenerator port.ApplicationVersionIDGenerator,
	clock port.Clock,
	repository port.ApplicationVersionRepository,
) *CreateApplicationVersionHandler {
	return &CreateApplicationVersionHandler{
		scopeCatalog: scopeCatalog,
		oauthPolicy:  oauthPolicy,
		idGenerator:  idGenerator,
		clock:        clock,
		repository:   repository,
	}
}

func (handler *CreateApplicationVersionHandler) Handle(
	ctx context.Context,
	identity DeveloperIdentity,
	command CreateApplicationVersionCommand,
) (*domain.ApplicationVersion, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}

	applicationID, ok := shared.ParseApplicationID(command.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidApplicationID
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
	oauthRedirects, err := domain.NewOAuthRedirectConfiguration(
		append([]string{}, command.PKCERedirectURIs...),
		append([]string{}, command.ConfidentialRedirectURIs...),
	)
	if err != nil {
		return nil, err
	}
	if handler == nil || handler.oauthPolicy == nil {
		return nil, domain.NewInternalError(nil)
	}
	if err := handler.oauthPolicy.EnsureCanonical(oauthRedirects.PKCERedirectURIs(), oauthRedirects.ConfidentialRedirectURIs()); err != nil {
		if errors.Is(err, port.ErrInvalidOAuthRedirectConfiguration) {
			return nil, domain.ErrInvalidOAuthRedirectConfiguration
		}
		return nil, domain.NewInternalError(err)
	}

	if handler.scopeCatalog == nil || handler.idGenerator == nil || handler.clock == nil || handler.repository == nil {
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

	versionID, err := handler.idGenerator.NewUUIDv7()
	if err != nil {
		return nil, domain.NewInternalError(err)
	}
	createdAt := handler.clock.Now().UTC()
	draft, err := domain.NewDraftApplicationVersionWithOAuth(
		versionID,
		applicationID,
		versionLabel,
		launchURL,
		rpcAPIRange,
		requiredCapabilities,
		scopeRequest,
		oauthRedirects,
		identity.AuthID,
		createdAt,
	)
	if err != nil {
		return nil, domain.NewInternalError(err)
	}

	version, err := handler.repository.CreateDraft(ctx, identity.AuthID, draft)
	if err != nil {
		switch {
		case errors.Is(err, port.ErrApplicationNotFound):
			return nil, domain.ErrApplicationNotFound
		case errors.Is(err, port.ErrApplicationAdminRequired):
			return nil, domain.ErrApplicationAdminRequired
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
