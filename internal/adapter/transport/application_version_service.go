package transport

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	applicationversionv1 "iwut-app-center/api/gen/go/app_center/v1/application_version"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionusecase "iwut-app-center/internal/version/usecase"
)

type CreateApplicationVersionHandler interface {
	Handle(
		ctx context.Context,
		identity versionusecase.DeveloperIdentity,
		command versionusecase.CreateApplicationVersionCommand,
	) (*versiondomain.ApplicationVersion, error)
}

type ApplicationVersionService struct {
	applicationversionv1.UnimplementedApplicationVersionServer
	handler CreateApplicationVersionHandler
}

var _ applicationversionv1.ApplicationVersionHTTPServer = (*ApplicationVersionService)(nil)
var _ applicationversionv1.ApplicationVersionServer = (*ApplicationVersionService)(nil)

func NewApplicationVersionService(handler CreateApplicationVersionHandler) *ApplicationVersionService {
	return &ApplicationVersionService{handler: handler}
}

func (service *ApplicationVersionService) CreateApplicationVersion(
	ctx context.Context,
	request *applicationversionv1.CreateApplicationVersionRequest,
) (*applicationversionv1.CreateApplicationVersionResponse, error) {
	if service == nil || service.handler == nil {
		return nil, toTransportError(versiondomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(versiondomain.ErrDeveloperIdentityRequired)
	}

	version, err := service.handler.Handle(
		ctx,
		shared.DeveloperIdentity{
			AuthID:          identity.AuthID,
			DeveloperStatus: identity.DeveloperStatus,
		},
		versionusecase.CreateApplicationVersionCommand{
			ApplicationID:             request.GetApplicationId(),
			VersionLabel:              request.GetVersionLabel(),
			LaunchURL:                 request.GetLaunchUrl(),
			RPCApiMinVersion:          request.GetRpcApiMinVersion(),
			RPCApiMaxVersionExclusive: request.GetRpcApiMaxVersionExclusive(),
			RequiredCapabilities:      request.GetRequiredCapabilities(),
			RequiredScopes:            request.GetRequiredScopes(),
			OptionalScopes:            request.GetOptionalScopes(),
		},
	)
	if err != nil {
		return nil, toTransportError(err)
	}
	if version == nil {
		return nil, toTransportError(versiondomain.NewInternalError(nil))
	}

	return applicationVersionResponse(version), nil
}

func applicationVersionResponse(version *versiondomain.ApplicationVersion) *applicationversionv1.CreateApplicationVersionResponse {
	capabilityValues := version.RequiredCapabilities()
	capabilities := make([]string, len(capabilityValues))
	for index, capability := range capabilityValues {
		capabilities[index] = string(capability)
	}
	requiredValues := version.RequiredScopes()
	requiredScopes := make([]string, len(requiredValues))
	for index, scope := range requiredValues {
		requiredScopes[index] = string(scope)
	}
	optionalValues := version.OptionalScopes()
	optionalScopes := make([]string, len(optionalValues))
	for index, scope := range optionalValues {
		optionalScopes[index] = string(scope)
	}

	return &applicationversionv1.CreateApplicationVersionResponse{
		VersionId:                 version.ID().String(),
		ApplicationId:             version.ApplicationID().String(),
		Sequence:                  version.Sequence().Int32(),
		VersionLabel:              version.VersionLabel().String(),
		LaunchUrl:                 version.LaunchURL().String(),
		RpcApiMinVersion:          version.RPCApiRange().Minimum(),
		RpcApiMaxVersionExclusive: version.RPCApiRange().MaximumExclusive(),
		RequiredCapabilities:      capabilities,
		RequiredScopes:            requiredScopes,
		OptionalScopes:            optionalScopes,
		ReviewStatus:              string(version.ReviewStatus()),
		CreatedBy:                 version.CreatedBy().String(),
		CreatedAt:                 timestamppb.New(version.CreatedAt()),
		Revision:                  version.Revision(),
		UpdatedBy:                 version.UpdatedBy().String(),
		UpdatedAt:                 timestamppb.New(version.UpdatedAt()),
	}
}
