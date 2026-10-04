package transport

import (
	"context"
	"errors"
	"log/slog"

	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	kratostransport "github.com/go-kratos/kratos/v2/transport"

	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
)

type ResolveLaunchTargetHandler interface {
	Execute(context.Context, shared.AuthenticatedUserIdentity, catalogusecase.ResolveLaunchTargetQuery) (*catalogdomain.LaunchTargetDescriptor, error)
}

type RuntimeResolutionService struct {
	runtimev1.UnimplementedRuntimeResolutionServiceServer
	handler ResolveLaunchTargetHandler
}

func NewRuntimeResolutionService(handler ResolveLaunchTargetHandler) *RuntimeResolutionService {
	return &RuntimeResolutionService{handler: handler}
}

func (service *RuntimeResolutionService) ResolveLaunchTarget(ctx context.Context, request *runtimev1.ResolveLaunchTargetRequest) (*runtimev1.LaunchTargetDescriptor, error) {
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "private, no-store")
	}
	if service == nil || service.handler == nil || request == nil {
		return nil, toTransportError(catalogdomain.NewInternalError(nil))
	}
	query := request.GetQuery()
	if len(request.ProtoReflect().GetUnknown()) != 0 || (query != nil && len(query.ProtoReflect().GetUnknown()) != 0) {
		return nil, invalidResolveLaunchTargetRequest()
	}
	identity, _ := authenticatedUserIdentityFromContext(ctx)
	result, err := service.handler.Execute(ctx, identity, catalogusecase.ResolveLaunchTargetQuery{
		ApplicationID: request.GetApplicationId(), HostRPCAPIMajor: query.GetHostRpcApiMajor(), HostCapabilities: append([]string(nil), query.GetHostCapabilities()...),
	})
	if err != nil {
		if errors.Is(err, catalogdomain.ErrApplicationRuntimeStateInconsistent) {
			slog.ErrorContext(ctx, "application runtime state invariant failed", "reason", ReasonApplicationRuntimeStateInconsistent)
		}
		return nil, toTransportError(err)
	}
	if result == nil {
		return nil, toTransportError(catalogdomain.NewInternalError(nil))
	}
	capabilities := make([]string, len(result.RequiredCapabilities()))
	for index, name := range result.RequiredCapabilities() {
		capabilities[index] = name.String()
	}
	return &runtimev1.LaunchTargetDescriptor{
		ApplicationId: result.ApplicationID().String(), PublicationId: result.PublicationID(), PublicationRevision: result.PublicationRevision(), Channel: runtimeLaunchChannel(result.Channel()), RpcApiMajor: result.RPCAPIMajor(), VersionId: result.VersionID(), VersionLabel: result.VersionLabel(), LaunchUrl: result.LaunchURL(), RpcApiMinVersion: result.RPCAPIMinVersion(), RpcApiMaxVersionExclusive: result.RPCAPIMaxVersionExclusive(), RequiredCapabilities: capabilities, RequiredScopes: result.RequiredScopes(), OptionalScopes: result.OptionalScopes(),
	}, nil
}

func runtimeLaunchChannel(channel catalogdomain.LaunchChannel) runtimev1.LaunchChannel {
	switch channel {
	case catalogdomain.LaunchChannelTest:
		return runtimev1.LaunchChannel_LAUNCH_CHANNEL_TEST
	case catalogdomain.LaunchChannelGrey:
		return runtimev1.LaunchChannel_LAUNCH_CHANNEL_GREY
	case catalogdomain.LaunchChannelStable:
		return runtimev1.LaunchChannel_LAUNCH_CHANNEL_STABLE
	default:
		return runtimev1.LaunchChannel_LAUNCH_CHANNEL_UNSPECIFIED
	}
}
