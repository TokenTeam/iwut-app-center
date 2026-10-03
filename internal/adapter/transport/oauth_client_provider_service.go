package transport

import (
	"context"
	"errors"
	"log/slog"

	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/shared"
)

type OAuthClientProviderHandlers interface {
	GetClientConfiguration(context.Context, domain.ClientID) (*domain.ClientConfiguration, error)
	VerifyClientSecret(context.Context, domain.ClientID, string, int64) (bool, int64, error)
	ResolveRuntime(context.Context, domain.ClientID, domain.Channel, int32, int64) (*domain.RuntimeConfiguration, error)
	ResolveAuthorizationContext(context.Context, domain.ClientID, shared.AuthID, domain.Channel, int32, int64, domain.RuntimeVersion) (*domain.AuthorizationContext, error)
	GetPublishedRedirects(context.Context, shared.ApplicationID) (*domain.PublishedRedirectSnapshot, error)
}

type OAuthClientProviderService struct {
	oauthclientv1.UnimplementedOAuthClientProviderServiceServer
	handlers OAuthClientProviderHandlers
}

func NewOAuthClientProviderService(handlers OAuthClientProviderHandlers) *OAuthClientProviderService {
	return &OAuthClientProviderService{handlers: handlers}
}

func (service *OAuthClientProviderService) GetClientConfiguration(ctx context.Context, request *oauthclientv1.GetClientConfigurationRequest) (*oauthclientv1.GetClientConfigurationResponse, error) {
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(domain.NewInternalError(nil))
	}
	configuration, err := service.handlers.GetClientConfiguration(ctx, domain.ClientID(request.GetClientId()))
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	return &oauthclientv1.GetClientConfigurationResponse{Configuration: clientConfigurationResource(configuration)}, nil
}

func (service *OAuthClientProviderService) VerifyClientSecret(ctx context.Context, request *oauthclientv1.VerifyClientSecretRequest) (*oauthclientv1.VerifyClientSecretResponse, error) {
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(domain.NewInternalError(nil))
	}
	verified, revision, err := service.handlers.VerifyClientSecret(ctx, domain.ClientID(request.GetClientId()), request.GetClientSecret(), request.GetExpectedCredentialRevision())
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	return &oauthclientv1.VerifyClientSecretResponse{Verified: verified, CredentialRevision: revision}, nil
}

func (service *OAuthClientProviderService) ResolveClientRuntimeConfiguration(ctx context.Context, request *oauthclientv1.ResolveClientRuntimeConfigurationRequest) (*oauthclientv1.ResolveClientRuntimeConfigurationResponse, error) {
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(domain.NewInternalError(nil))
	}
	channel, err := oauthChannel(request.GetChannel())
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	runtime, err := service.handlers.ResolveRuntime(ctx, domain.ClientID(request.GetClientId()), channel, request.GetRpcApiMajor(), request.GetExpectedRegistrationRevision())
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	return &oauthclientv1.ResolveClientRuntimeConfigurationResponse{Runtime: runtimeResource(runtime)}, nil
}

func (service *OAuthClientProviderService) ResolveAuthorizationContext(ctx context.Context, request *oauthclientv1.ResolveAuthorizationContextRequest) (*oauthclientv1.ResolveAuthorizationContextResponse, error) {
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(domain.NewInternalError(nil))
	}
	channel, err := oauthChannel(request.GetChannel())
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	expected := request.GetExpectedRuntimeVersion()
	if expected == nil {
		return nil, providerTransportError(ctx, domain.ErrInvalidOAuthProviderRequest)
	}
	result, err := service.handlers.ResolveAuthorizationContext(ctx, domain.ClientID(request.GetClientId()), shared.AuthID(request.GetAuthId()), channel, request.GetRpcApiMajor(), request.GetExpectedRegistrationRevision(), domain.RuntimeVersion{VersionID: expected.GetVersionId(), PublicationRevision: expected.GetPublicationRevision(), ProfileRevisionID: expected.GetProfileRevisionId(), AdminAuthID: shared.AuthID(expected.GetAdminAuthId())})
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	resource := &oauthclientv1.AuthorizationContext{Runtime: runtimeResource(result.Runtime), AuthId: result.AuthID.String()}
	if result.TesterMembershipID != "" {
		value := result.TesterMembershipID
		resource.TesterMembershipId = &value
	}
	return &oauthclientv1.ResolveAuthorizationContextResponse{Context: resource}, nil
}

func (service *OAuthClientProviderService) GetApplicationPublishedRedirects(ctx context.Context, request *oauthclientv1.GetApplicationPublishedRedirectsRequest) (*oauthclientv1.GetApplicationPublishedRedirectsResponse, error) {
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(domain.NewInternalError(nil))
	}
	applicationID, ok := shared.ParseApplicationID(request.GetApplicationId())
	if !ok {
		return nil, providerTransportError(ctx, domain.ErrInvalidApplicationID)
	}
	snapshot, err := service.handlers.GetPublishedRedirects(ctx, applicationID)
	if err != nil {
		return nil, providerTransportError(ctx, err)
	}
	return &oauthclientv1.GetApplicationPublishedRedirectsResponse{Snapshot: publishedRedirectSnapshotResource(snapshot)}, nil
}

func providerTransportError(ctx context.Context, err error) error {
	if errors.Is(err, domain.ErrOAuthClientStateInconsistent) || errors.Is(err, domain.ErrApplicationProfileStateInconsistent) {
		reason := ReasonOAuthClientStateInconsistent
		if errors.Is(err, domain.ErrApplicationProfileStateInconsistent) {
			reason = ReasonApplicationProfileStateInconsistent
		}
		slog.ErrorContext(ctx, "OAuth provider state invariant failed", "reason", reason)
	}
	return toTransportError(err)
}

func clientConfigurationResource(configuration *domain.ClientConfiguration) *oauthclientv1.OAuthClientConfiguration {
	if configuration == nil {
		return nil
	}
	resource := &oauthclientv1.OAuthClientConfiguration{ClientId: configuration.ClientID.String(), ApplicationId: configuration.ApplicationID.String(), Type: clientTypeResource(configuration.Type), Channel: oauthChannelResource(configuration.Channel), Status: clientStatusResource(configuration.Status), RegistrationRevision: configuration.RegistrationRevision, AuthorizationEpoch: configuration.AuthorizationEpoch}
	if configuration.TokenEndpointAuth == domain.TokenEndpointAuthMethodClientSecretBasic {
		resource.TokenEndpointAuthMethod = oauthclientv1.TokenEndpointAuthMethod_TOKEN_ENDPOINT_AUTH_METHOD_CLIENT_SECRET_BASIC
	} else {
		resource.TokenEndpointAuthMethod = oauthclientv1.TokenEndpointAuthMethod_TOKEN_ENDPOINT_AUTH_METHOD_NONE
	}
	if configuration.CredentialRevision != nil {
		value := *configuration.CredentialRevision
		resource.CredentialRevision = &value
	}
	return resource
}

func runtimeResource(runtime *domain.RuntimeConfiguration) *oauthclientv1.RuntimeConfiguration {
	if runtime == nil {
		return nil
	}
	return &oauthclientv1.RuntimeConfiguration{ClientId: runtime.ClientID.String(), ApplicationId: runtime.ApplicationID.String(), Type: clientTypeResource(runtime.Type), Channel: oauthChannelResource(runtime.Channel), RpcApiMajor: runtime.RPCAPIMajor, RegistrationRevision: runtime.RegistrationRevision, AuthorizationEpoch: runtime.AuthorizationEpoch, AdminAuthId: runtime.AdminAuthID.String(), VersionId: runtime.VersionID, PublicationRevision: runtime.PublicationRevision, RedirectUris: append([]string(nil), runtime.RedirectURIs...), RequiredScopes: append([]string(nil), runtime.RequiredScopes...), OptionalScopes: append([]string(nil), runtime.OptionalScopes...), Display: displayResource(runtime.Display), ObservedAt: timestamppb.New(runtime.ObservedAt), ValidUntil: timestamppb.New(runtime.ValidUntil)}
}

func displayResource(display domain.ApplicationDisplay) *oauthclientv1.ApplicationDisplay {
	resource := &oauthclientv1.ApplicationDisplay{ProfileRevisionId: display.ProfileRevisionID, DisplayName: display.DisplayName, Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}
	if display.Description != nil {
		resource.Description = structpb.NewStringValue(*display.Description)
	}
	if display.Icon != nil {
		resource.Icon = structpb.NewStringValue(*display.Icon)
	}
	return resource
}

func publishedRedirectSnapshotResource(snapshot *domain.PublishedRedirectSnapshot) *oauthclientv1.PublishedRedirectSnapshot {
	if snapshot == nil {
		return nil
	}
	entries := make([]*oauthclientv1.PublishedRedirectEntry, len(snapshot.Entries))
	for index, entry := range snapshot.Entries {
		entries[index] = &oauthclientv1.PublishedRedirectEntry{Channel: oauthChannelResource(entry.Channel), RpcApiMajor: entry.RPCAPIMajor, VersionId: entry.VersionID, PublicationRevision: entry.PublicationRevision}
	}
	return &oauthclientv1.PublishedRedirectSnapshot{ApplicationId: snapshot.ApplicationID.String(), Entries: entries, RedirectUris: append([]string(nil), snapshot.RedirectURIs...), ObservedAt: timestamppb.New(snapshot.ObservedAt), ValidUntil: timestamppb.New(snapshot.ValidUntil)}
}

func clientTypeResource(value domain.ClientType) oauthclientv1.OAuthClientType {
	if value == domain.ClientTypeConfidentialSecret {
		return oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET
	}
	return oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_PUBLIC_PKCE
}

func clientStatusResource(value domain.ClientStatus) oauthclientv1.OAuthClientStatus {
	if value == domain.ClientStatusDisabled {
		return oauthclientv1.OAuthClientStatus_OAUTH_CLIENT_STATUS_DISABLED
	}
	return oauthclientv1.OAuthClientStatus_OAUTH_CLIENT_STATUS_ACTIVE
}
