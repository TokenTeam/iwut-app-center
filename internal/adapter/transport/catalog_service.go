package transport

import (
	"context"
	"errors"
	"log/slog"

	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/grpc/codes"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
)

type ResolveTestLaunchTargetHandler interface {
	Execute(context.Context, shared.AuthenticatedUserIdentity, catalogusecase.ResolveTestLaunchTargetQuery) (*catalogdomain.TestLaunchDescriptor, error)
}

type CatalogService struct {
	catalogv1.UnimplementedCatalogServer
	handler ResolveTestLaunchTargetHandler
}

func NewCatalogService(handler ResolveTestLaunchTargetHandler) *CatalogService {
	return &CatalogService{handler: handler}
}

func (service *CatalogService) ResolveTestLaunchTarget(ctx context.Context, request *catalogv1.ResolveTestLaunchTargetRequest) (*catalogv1.TestLaunchDescriptor, error) {
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "private, no-store")
	}
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(catalogdomain.ErrAuthenticatedUserRequired)
	}
	if service == nil || service.handler == nil || request == nil {
		return nil, toTransportError(catalogdomain.NewInternalError(nil))
	}
	query := request.GetQuery()
	if len(request.ProtoReflect().GetUnknown()) != 0 || (query != nil && len(query.ProtoReflect().GetUnknown()) != 0) {
		return nil, invalidResolveTestLaunchRequest()
	}
	result, err := service.handler.Execute(ctx, identity, catalogusecase.ResolveTestLaunchTargetQuery{ApplicationID: request.GetApplicationId(), HostRPCAPIMajor: query.GetHostRpcApiMajor(), HostCapabilities: append([]string(nil), query.GetHostCapabilities()...)})
	if err != nil {
		if errors.Is(err, catalogdomain.ErrApplicationTestPublicationInconsistent) {
			// Alert by stable category only; never expose wrapped database facts or credentials.
			slog.ErrorContext(ctx, "application test publication invariant failed", "reason", ReasonApplicationTestPublicationInconsistent)
		}
		return nil, toTransportError(err)
	}
	if result == nil {
		return nil, toTransportError(catalogdomain.NewInternalError(nil))
	}
	capabilities := make([]string, len(result.RequiredCapabilities()))
	for i, name := range result.RequiredCapabilities() {
		capabilities[i] = string(name)
	}
	return &catalogv1.TestLaunchDescriptor{
		ApplicationId: result.ApplicationID().String(), PublicationId: result.PublicationID(), PublicationRevision: result.PublicationRevision(), RpcApiMajor: result.RPCAPIMajor(), VersionId: result.VersionID(), VersionLabel: result.VersionLabel(), LaunchUrl: result.LaunchURL(), RpcApiMinVersion: result.RPCAPIMinVersion(), RpcApiMaxVersionExclusive: result.RPCAPIMaxVersionExclusive(), RequiredCapabilities: capabilities, RequiredScopes: result.RequiredScopes(), OptionalScopes: result.OptionalScopes(),
	}, nil
}

func invalidResolveTestLaunchRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidResolveTestLaunchRequest, "test launch resolution accepts only application path ID and host context body")
}
