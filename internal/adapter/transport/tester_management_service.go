package transport

import (
	"context"
	managementv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_management"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/usecase"
)

type TesterManagementQueryHandler interface {
	Get(context.Context, shared.AuthenticatedUserIdentity, string) (*domain.TesterManagement, error)
	ListMemberships(context.Context, shared.AuthenticatedUserIdentity, usecase.TesterManagementListQuery) (*domain.TesterMembershipPage, error)
	ListJoinLinks(context.Context, shared.AuthenticatedUserIdentity, usecase.TesterManagementListQuery) (*domain.TesterJoinLinkPage, error)
}
type ApplicationTesterManagementService struct {
	managementv1.UnimplementedApplicationTesterManagementServiceServer
	handler TesterManagementQueryHandler
}

func NewApplicationTesterManagementService(handler TesterManagementQueryHandler) *ApplicationTesterManagementService {
	return &ApplicationTesterManagementService{handler: handler}
}

var _ managementv1.ApplicationTesterManagementServiceServer = (*ApplicationTesterManagementService)(nil)
var _ managementv1.ApplicationTesterManagementServiceHTTPServer = (*ApplicationTesterManagementService)(nil)

func testerManagementIdentity(ctx context.Context) (shared.AuthenticatedUserIdentity, error) {
	setPrivateNoStore(ctx)
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return identity, transportStatus(codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required")
	}
	return identity, nil
}
func (s *ApplicationTesterManagementService) GetApplicationTesterManagement(ctx context.Context, request *managementv1.GetApplicationTesterManagementRequest) (*managementv1.ApplicationTesterManagement, error) {
	identity, err := testerManagementIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(domain.ErrInvalidApplicationTesterManagementQuery)
	}
	r, err := s.handler.Get(ctx, identity, request.ApplicationId)
	if err != nil {
		return nil, toTransportError(err)
	}
	if r == nil {
		return nil, toTransportError(domain.ErrApplicationTesterManagementStateInconsistent)
	}
	result := &managementv1.ApplicationTesterManagement{ApplicationId: r.ApplicationID.String(), Capacity: &managementv1.TesterCapacity{ActiveTesterCount: r.ActiveTesterCount, TesterLimit: r.TesterLimit}, AsOf: timestamppb.New(r.AsOf)}
	if r.ActiveJoinLink != nil {
		result.ActiveJoinLink = managedLinkResponse(*r.ActiveJoinLink)
	}
	return result, nil
}
func (s *ApplicationTesterManagementService) ListApplicationTesterMemberships(ctx context.Context, request *managementv1.ListApplicationTesterMembershipsRequest) (*managementv1.MembershipPage, error) {
	identity, err := testerManagementIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(domain.ErrInvalidApplicationTesterManagementQuery)
	}
	query := request.GetQuery()
	if query == nil {
		query = &managementv1.MembershipQuery{}
	}
	status := ""
	switch query.Status {
	case managementv1.MembershipStatus_MEMBERSHIP_STATUS_UNSPECIFIED:
	case managementv1.MembershipStatus_MEMBERSHIP_STATUS_ACTIVE:
		status = "ACTIVE"
	case managementv1.MembershipStatus_MEMBERSHIP_STATUS_REMOVED:
		status = "REMOVED"
	case managementv1.MembershipStatus_MEMBERSHIP_STATUS_ALL:
		status = "ALL"
	default:
		return nil, toTransportError(domain.ErrInvalidApplicationTesterManagementQuery)
	}
	r, err := s.handler.ListMemberships(ctx, identity, usecase.TesterManagementListQuery{ApplicationID: request.ApplicationId, Status: status, PageSize: query.PageSize, PageToken: query.PageToken})
	if err != nil {
		return nil, toTransportError(err)
	}
	if r == nil {
		return nil, toTransportError(domain.ErrApplicationTesterManagementStateInconsistent)
	}
	items := make([]*managementv1.ManagedTesterMembership, len(r.Items))
	for i, m := range r.Items {
		items[i] = &managementv1.ManagedTesterMembership{MembershipId: m.MembershipID.String(), ApplicationId: m.ApplicationID.String(), TesterAuthId: m.TesterAuthID.String(), Status: managementv1.MembershipStatus_MEMBERSHIP_STATUS_ACTIVE, JoinedViaJoinLinkId: m.JoinedViaJoinLinkID.String(), JoinedAt: timestamppb.New(m.JoinedAt)}
		if m.Status == domain.MembershipStatusRemoved {
			items[i].Status = managementv1.MembershipStatus_MEMBERSHIP_STATUS_REMOVED
		}
		if m.RemovedAt != nil {
			items[i].RemovedAt = timestamppb.New(*m.RemovedAt)
		}
	}
	return &managementv1.MembershipPage{Items: items, NextPageToken: r.NextPageToken, AsOf: timestamppb.New(r.AsOf)}, nil
}
func (s *ApplicationTesterManagementService) ListApplicationTesterJoinLinks(ctx context.Context, request *managementv1.ListApplicationTesterJoinLinksRequest) (*managementv1.JoinLinkPage, error) {
	identity, err := testerManagementIdentity(ctx)
	if err != nil {
		return nil, err
	}
	if s == nil || s.handler == nil || request == nil {
		return nil, toTransportError(domain.ErrInvalidApplicationTesterManagementQuery)
	}
	query := request.GetQuery()
	if query == nil {
		query = &managementv1.JoinLinkQuery{}
	}
	status := ""
	switch query.Status {
	case managementv1.JoinLinkStatus_JOIN_LINK_STATUS_UNSPECIFIED:
	case managementv1.JoinLinkStatus_JOIN_LINK_STATUS_ACTIVE:
		status = "ACTIVE"
	case managementv1.JoinLinkStatus_JOIN_LINK_STATUS_REVOKED:
		status = "REVOKED"
	case managementv1.JoinLinkStatus_JOIN_LINK_STATUS_ALL:
		status = "ALL"
	default:
		return nil, toTransportError(domain.ErrInvalidApplicationTesterManagementQuery)
	}
	r, err := s.handler.ListJoinLinks(ctx, identity, usecase.TesterManagementListQuery{ApplicationID: request.ApplicationId, Status: status, PageSize: query.PageSize, PageToken: query.PageToken})
	if err != nil {
		return nil, toTransportError(err)
	}
	if r == nil {
		return nil, toTransportError(domain.ErrApplicationTesterManagementStateInconsistent)
	}
	items := make([]*managementv1.ManagedTesterJoinLink, len(r.Items))
	for i, l := range r.Items {
		items[i] = managedLinkResponse(l)
	}
	return &managementv1.JoinLinkPage{Items: items, NextPageToken: r.NextPageToken, AsOf: timestamppb.New(r.AsOf)}, nil
}
func managedLinkResponse(l domain.ManagedTesterJoinLink) *managementv1.ManagedTesterJoinLink {
	r := &managementv1.ManagedTesterJoinLink{JoinLinkId: l.JoinLinkID.String(), ApplicationId: l.ApplicationID.String(), Status: managementv1.JoinLinkStatus_JOIN_LINK_STATUS_ACTIVE, CreatedAt: timestamppb.New(l.CreatedAt)}
	if l.Status == domain.JoinLinkStatusRevoked {
		r.Status = managementv1.JoinLinkStatus_JOIN_LINK_STATUS_REVOKED
	}
	if l.RevokedAt != nil {
		r.RevokedAt = timestamppb.New(*l.RevokedAt)
	}
	if l.RevocationReason != nil {
		v := managementv1.RevocationReason(managementv1.RevocationReason_value["REVOCATION_REASON_"+string(*l.RevocationReason)])
		r.RevocationReason = &v
	}
	if l.ReplacedByJoinLinkID != nil {
		v := l.ReplacedByJoinLinkID.String()
		r.ReplacedByJoinLinkId = &v
	}
	return r
}
func isTesterManagementOperation(operation string) bool {
	return operation == managementv1.OperationApplicationTesterManagementServiceGetApplicationTesterManagement || operation == managementv1.OperationApplicationTesterManagementServiceListApplicationTesterMemberships || operation == managementv1.OperationApplicationTesterManagementServiceListApplicationTesterJoinLinks
}
