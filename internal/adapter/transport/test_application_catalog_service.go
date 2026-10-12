package transport

import (
	"context"
	"errors"
	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/test_application_catalog"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	cd "iwut-app-center/internal/catalog/domain"
	cu "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
	"log/slog"
)

const ListMyTestApplicationsGRPCMethod = v1.OperationTestApplicationCatalogServiceListMyTestApplications
const GetMyTestApplicationGRPCMethod = v1.OperationTestApplicationCatalogServiceGetMyTestApplication

type MyTestApplicationsHandler interface {
	List(context.Context, shared.AuthenticatedUserIdentity, cu.ListMyTestApplicationsQuery) (*cd.MyTestApplicationPage, error)
	Get(context.Context, shared.AuthenticatedUserIdentity, cu.GetMyTestApplicationQuery) (*cd.MyTestApplicationDetail, error)
}
type TestApplicationCatalogService struct {
	v1.UnimplementedTestApplicationCatalogServiceServer
	handler MyTestApplicationsHandler
}

func NewTestApplicationCatalogService(handler MyTestApplicationsHandler) *TestApplicationCatalogService {
	return &TestApplicationCatalogService{handler: handler}
}
func (s *TestApplicationCatalogService) ListMyTestApplications(ctx context.Context, request *v1.ListMyTestApplicationsRequest) (*v1.MyTestApplicationPage, error) {
	setPrivateNoStore(ctx)
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, transportStatus(codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required")
	}
	if s == nil || s.handler == nil {
		return nil, toTransportError(cd.ErrMyTestApplicationUnavailable)
	}
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 || request.Query == nil || len(request.Query.ProtoReflect().GetUnknown()) != 0 || !myTestRuntimeValid(request.Query.Runtime) {
		return nil, toTransportError(cd.ErrInvalidMyTestApplicationQuery)
	}
	q := request.Query
	page, err := s.handler.List(ctx, identity, cu.ListMyTestApplicationsQuery{Runtime: cu.CatalogRuntimeQuery{HostRPCAPIMajor: q.Runtime.HostRpcApiMajor, HostCapabilities: q.Runtime.HostCapabilities}, PageSize: q.PageSize, PageToken: q.PageToken})
	if err != nil {
		return nil, myTestTransportError(ctx, err)
	}
	if page == nil {
		return nil, toTransportError(cd.ErrMyTestApplicationStateInconsistent)
	}
	items := make([]*v1.MyTestApplicationItem, len(page.Items))
	for i, item := range page.Items {
		items[i] = myTestItemResource(item)
	}
	return &v1.MyTestApplicationPage{Items: items, NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}, nil
}
func (s *TestApplicationCatalogService) GetMyTestApplication(ctx context.Context, request *v1.GetMyTestApplicationRequest) (*v1.MyTestApplicationDetail, error) {
	setPrivateNoStore(ctx)
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, transportStatus(codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required")
	}
	if s == nil || s.handler == nil {
		return nil, toTransportError(cd.ErrMyTestApplicationUnavailable)
	}
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 || !myTestRuntimeValid(request.Runtime) {
		return nil, toTransportError(cd.ErrInvalidMyTestApplicationQuery)
	}
	d, err := s.handler.Get(ctx, identity, cu.GetMyTestApplicationQuery{ApplicationID: request.ApplicationId, Runtime: cu.CatalogRuntimeQuery{HostRPCAPIMajor: request.Runtime.HostRpcApiMajor, HostCapabilities: request.Runtime.HostCapabilities}})
	if err != nil {
		return nil, myTestTransportError(ctx, err)
	}
	if d == nil || d.Item == nil {
		return nil, toTransportError(cd.ErrMyTestApplicationStateInconsistent)
	}
	return &v1.MyTestApplicationDetail{Item: myTestItemResource(d.Item), AsOf: timestamppb.New(d.AsOf)}, nil
}
func myTestRuntimeValid(r *v1.RuntimeQuery) bool {
	return r != nil && len(r.ProtoReflect().GetUnknown()) == 0
}
func myTestTransportError(ctx context.Context, err error) error {
	if errors.Is(err, cd.ErrAuthenticatedUserRequired) {
		return transportStatus(codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required")
	}
	if errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
		slog.ErrorContext(ctx, "test participation state invariant failed", "reason", "ERROR_REASON_MY_TEST_APPLICATION_STATE_INCONSISTENT")
	}
	return toTransportError(err)
}
func myTestItemResource(item *cd.MyTestApplicationItem) *v1.MyTestApplicationItem {
	if item == nil {
		return nil
	}
	resource := &v1.MyTestApplicationItem{ApplicationId: item.ApplicationID.String(), Membership: &v1.OwnMembership{MembershipId: item.Membership.MembershipID.String(), JoinedAt: timestamppb.New(item.Membership.JoinedAt)}, MissingCapabilities: []string{}}
	if p := item.Profile; p != nil {
		resource.Profile = &v1.PublicProfile{ProfileRevisionId: p.RevisionID(), DisplayName: p.DisplayName(), Description: p.Description(), Icon: p.Icon()}
	}
	switch item.Availability {
	case cd.MyTestReady:
		resource.Availability = v1.TestAvailability_TEST_AVAILABILITY_READY
	case cd.MyTestProfileRequired:
		resource.Availability = v1.TestAvailability_TEST_AVAILABILITY_PROFILE_REQUIRED
	case cd.MyTestTargetUnavailable:
		resource.Availability = v1.TestAvailability_TEST_AVAILABILITY_TEST_TARGET_UNAVAILABLE
	case cd.MyTestCapabilitiesInsufficient:
		resource.Availability = v1.TestAvailability_TEST_AVAILABILITY_HOST_CAPABILITIES_INSUFFICIENT
	}
	for _, c := range item.MissingCapabilities {
		resource.MissingCapabilities = append(resource.MissingCapabilities, c.String())
	}
	if d := item.LaunchTarget; d != nil {
		caps := []string{}
		for _, c := range d.RequiredCapabilities() {
			caps = append(caps, c.String())
		}
		resource.LaunchTarget = &catalogv1.TestLaunchDescriptor{ApplicationId: d.ApplicationID().String(), PublicationId: d.PublicationID(), PublicationRevision: d.PublicationRevision(), RpcApiMajor: d.RPCAPIMajor(), VersionId: d.VersionID(), VersionLabel: d.VersionLabel(), LaunchUrl: d.LaunchURL(), RpcApiMinVersion: d.RPCAPIMinVersion(), RpcApiMaxVersionExclusive: d.RPCAPIMaxVersionExclusive(), RequiredCapabilities: caps, RequiredScopes: d.RequiredScopes(), OptionalScopes: d.OptionalScopes()}
	}
	return resource
}
