package transport

import (
	"context"

	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/protobuf/types/known/timestamppb"

	oauthclientdomain "iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/shared"
)

type OAuthClientHandlers interface {
	Register(context.Context, shared.DeveloperIdentity, shared.ApplicationID, oauthclientdomain.Channel, oauthclientdomain.ClientType, *int64) (*oauthclientdomain.RegisterResult, string, error)
	GetRegistration(context.Context, shared.DeveloperIdentity, shared.ApplicationID, oauthclientdomain.Channel) (*oauthclientdomain.Registration, error)
	SetStatus(context.Context, shared.DeveloperIdentity, oauthclientdomain.ClientID, int64, oauthclientdomain.ClientStatus) (*oauthclientdomain.StatusResult, error)
	GetCredential(context.Context, shared.DeveloperIdentity, oauthclientdomain.ClientID) (*oauthclientdomain.Credential, error)
	RotateSecret(context.Context, shared.DeveloperIdentity, oauthclientdomain.ClientID, int64) (*oauthclientdomain.Credential, string, error)
}

type OAuthClientService struct {
	oauthclientv1.UnimplementedOAuthClientServiceServer
	handlers OAuthClientHandlers
}

var _ oauthclientv1.OAuthClientServiceHTTPServer = (*OAuthClientService)(nil)
var _ oauthclientv1.OAuthClientServiceServer = (*OAuthClientService)(nil)

func NewOAuthClientService(handlers OAuthClientHandlers) *OAuthClientService {
	return &OAuthClientService{handlers: handlers}
}

func (service *OAuthClientService) RegisterOAuthClient(ctx context.Context, request *oauthclientv1.RegisterOAuthClientRequest) (*oauthclientv1.RegisterOAuthClientResponse, error) {
	setNoStore(ctx)
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(oauthclientdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrDeveloperIdentityRequired)
	}
	applicationID, ok := shared.ParseApplicationID(request.GetApplicationId())
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrInvalidApplicationID)
	}
	channel, err := oauthChannel(request.GetChannel())
	if err != nil {
		return nil, toTransportError(err)
	}
	command := request.GetCommand()
	typ := oauthclientdomain.ClientType("")
	var expected *int64
	if command != nil {
		typ, err = oauthClientType(command.GetType())
		if err != nil {
			return nil, toTransportError(err)
		}
		if command.ExpectedRegistrationRevision != nil {
			value := command.GetExpectedRegistrationRevision()
			expected = &value
		}
	}
	result, secret, err := service.handlers.Register(ctx, identity, applicationID, channel, typ, expected)
	if err != nil {
		return nil, toTransportError(err)
	}
	if result == nil || result.Registration == nil {
		return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
	}
	response := &oauthclientv1.RegisterOAuthClientResponse{Registration: oauthRegistrationResource(result.Registration)}
	if result.Credential != nil {
		if secret == "" {
			return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
		}
		response.ClientSecret = &secret
	} else if secret != "" {
		return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
	}
	return response, nil
}

func (service *OAuthClientService) GetApplicationOAuthRegistration(ctx context.Context, request *oauthclientv1.GetApplicationOAuthRegistrationRequest) (*oauthclientv1.GetApplicationOAuthRegistrationResponse, error) {
	setNoStore(ctx)
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(oauthclientdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrDeveloperIdentityRequired)
	}
	applicationID, ok := shared.ParseApplicationID(request.GetApplicationId())
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrInvalidApplicationID)
	}
	channel, err := oauthChannel(request.GetChannel())
	if err != nil {
		return nil, toTransportError(err)
	}
	registration, err := service.handlers.GetRegistration(ctx, identity, applicationID, channel)
	if err != nil {
		return nil, toTransportError(err)
	}
	if registration == nil {
		return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
	}
	return &oauthclientv1.GetApplicationOAuthRegistrationResponse{Registration: oauthRegistrationResource(registration)}, nil
}

func (service *OAuthClientService) SetOAuthClientStatus(ctx context.Context, request *oauthclientv1.SetOAuthClientStatusRequest) (*oauthclientv1.SetOAuthClientStatusResponse, error) {
	setNoStore(ctx)
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(oauthclientdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrDeveloperIdentityRequired)
	}
	command := request.GetCommand()
	if command == nil {
		return nil, toTransportError(oauthclientdomain.ErrInvalidRegistrationRevision)
	}
	statusValue, err := oauthClientStatus(command.GetStatus())
	if err != nil {
		return nil, toTransportError(err)
	}
	result, err := service.handlers.SetStatus(ctx, identity, oauthclientdomain.ClientID(request.GetClientId()), command.GetExpectedRegistrationRevision(), statusValue)
	if err != nil {
		return nil, toTransportError(err)
	}
	if result == nil || result.Registration == nil {
		return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
	}
	return &oauthclientv1.SetOAuthClientStatusResponse{Changed: result.Changed, Registration: oauthRegistrationResource(result.Registration)}, nil
}

func (service *OAuthClientService) GetOAuthClientCredentialMetadata(ctx context.Context, request *oauthclientv1.GetOAuthClientCredentialMetadataRequest) (*oauthclientv1.GetOAuthClientCredentialMetadataResponse, error) {
	setNoStore(ctx)
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(oauthclientdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrDeveloperIdentityRequired)
	}
	credential, err := service.handlers.GetCredential(ctx, identity, oauthclientdomain.ClientID(request.GetClientId()))
	if err != nil {
		return nil, toTransportError(err)
	}
	if credential == nil {
		return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
	}
	return &oauthclientv1.GetOAuthClientCredentialMetadataResponse{Credential: oauthCredentialResource(credential)}, nil
}

func (service *OAuthClientService) RotateOAuthClientSecret(ctx context.Context, request *oauthclientv1.RotateOAuthClientSecretRequest) (*oauthclientv1.RotateOAuthClientSecretResponse, error) {
	setNoStore(ctx)
	if service == nil || service.handlers == nil || request == nil {
		return nil, toTransportError(oauthclientdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(oauthclientdomain.ErrDeveloperIdentityRequired)
	}
	command := request.GetCommand()
	if command == nil {
		return nil, toTransportError(oauthclientdomain.ErrInvalidCredentialRevision)
	}
	credential, secret, err := service.handlers.RotateSecret(ctx, identity, oauthclientdomain.ClientID(request.GetClientId()), command.GetExpectedCredentialRevision())
	if err != nil {
		return nil, toTransportError(err)
	}
	if credential == nil || secret == "" {
		return nil, toTransportError(oauthclientdomain.ErrOAuthClientStateInconsistent)
	}
	return &oauthclientv1.RotateOAuthClientSecretResponse{Credential: oauthCredentialResource(credential), ClientSecret: secret}, nil
}

func setNoStore(ctx context.Context) {
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "no-store")
	}
}

func oauthChannel(value oauthclientv1.OAuthChannel) (oauthclientdomain.Channel, error) {
	switch value {
	case oauthclientv1.OAuthChannel_OAUTH_CHANNEL_TEST:
		return oauthclientdomain.ChannelTest, nil
	case oauthclientv1.OAuthChannel_OAUTH_CHANNEL_GREY, oauthclientv1.OAuthChannel_OAUTH_CHANNEL_STABLE:
		return "", oauthclientdomain.ErrOAuthChannelNotEnabled
	default:
		return "", oauthclientdomain.ErrInvalidOAuthChannel
	}
}

func oauthClientType(value oauthclientv1.OAuthClientType) (oauthclientdomain.ClientType, error) {
	switch value {
	case oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_PUBLIC_PKCE:
		return oauthclientdomain.ClientTypePublicPKCE, nil
	case oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET:
		return oauthclientdomain.ClientTypeConfidentialSecret, nil
	default:
		return "", oauthclientdomain.ErrInvalidOAuthClientType
	}
}

func oauthClientStatus(value oauthclientv1.OAuthClientStatus) (oauthclientdomain.ClientStatus, error) {
	switch value {
	case oauthclientv1.OAuthClientStatus_OAUTH_CLIENT_STATUS_ACTIVE:
		return oauthclientdomain.ClientStatusActive, nil
	case oauthclientv1.OAuthClientStatus_OAUTH_CLIENT_STATUS_DISABLED:
		return oauthclientdomain.ClientStatusDisabled, nil
	default:
		return "", oauthclientdomain.ErrInvalidOAuthClientStatus
	}
}

func oauthRegistrationResource(registration *oauthclientdomain.Registration) *oauthclientv1.ApplicationOAuthRegistrationResource {
	if registration == nil {
		return nil
	}
	return &oauthclientv1.ApplicationOAuthRegistrationResource{
		ApplicationId:        registration.ApplicationID().String(),
		Channel:              oauthclientv1.OAuthChannel_OAUTH_CHANNEL_TEST,
		PublicClient:         oauthIdentityResource(registration.PublicClient()),
		ConfidentialClient:   oauthIdentityResource(registration.ConfidentialClient()),
		RegistrationRevision: registration.Revision(),
		CreatedAt:            timestamppb.New(registration.CreatedAt()),
		UpdatedAt:            timestamppb.New(registration.UpdatedAt()),
	}
}

func oauthIdentityResource(identity *oauthclientdomain.ClientIdentity) *oauthclientv1.OAuthClientIdentityResource {
	if identity == nil {
		return nil
	}
	typ := oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_PUBLIC_PKCE
	if identity.Type() == oauthclientdomain.ClientTypeConfidentialSecret {
		typ = oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET
	}
	status := oauthclientv1.OAuthClientStatus_OAUTH_CLIENT_STATUS_ACTIVE
	if identity.Status() == oauthclientdomain.ClientStatusDisabled {
		status = oauthclientv1.OAuthClientStatus_OAUTH_CLIENT_STATUS_DISABLED
	}
	return &oauthclientv1.OAuthClientIdentityResource{
		ClientId: identity.ClientID().String(), Type: typ, Status: status,
		AuthorizationEpoch: identity.AuthorizationEpoch(), CreatedBy: identity.CreatedBy().String(), CreatedAt: timestamppb.New(identity.CreatedAt()),
		StatusUpdatedBy: identity.StatusUpdatedBy().String(), StatusUpdatedAt: timestamppb.New(identity.StatusUpdatedAt()),
	}
}

func oauthCredentialResource(credential *oauthclientdomain.Credential) *oauthclientv1.OAuthClientCredentialMetadata {
	if credential == nil {
		return nil
	}
	return &oauthclientv1.OAuthClientCredentialMetadata{
		ClientId: credential.ClientID().String(), CredentialRevision: credential.Revision(),
		RotatedBy: credential.RotatedBy().String(), RotatedAt: timestamppb.New(credential.RotatedAt()),
	}
}
