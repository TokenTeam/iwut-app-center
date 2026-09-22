package transport

import (
	"context"
	"errors"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
	testermembershipv1 "iwut-app-center/api/gen/go/app_center/v1/tester_membership"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerusecase "iwut-app-center/internal/tester/usecase"
	"log/slog"
	"strings"
)

type JoinApplicationAsTesterHandler interface {
	Handle(context.Context, shared.AuthenticatedUserIdentity, testerdomain.ApplicationTesterJoinLinkID, testerusecase.JoinApplicationAsTesterCommand) (*testerdomain.JoinApplicationAsTesterResult, error)
}

type RemoveApplicationTesterHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, testerdomain.ApplicationTesterMembershipID) (*testerdomain.RemoveApplicationTesterResult, error)
}

type TesterMembershipService struct {
	testermembershipv1.UnimplementedTesterMembershipServer
	handler       JoinApplicationAsTesterHandler
	removeHandler RemoveApplicationTesterHandler
}

var _ testermembershipv1.TesterMembershipHTTPServer = (*TesterMembershipService)(nil)
var _ testermembershipv1.TesterMembershipServer = (*TesterMembershipService)(nil)

func NewTesterMembershipService(handler JoinApplicationAsTesterHandler, removeHandler RemoveApplicationTesterHandler) *TesterMembershipService {
	return &TesterMembershipService{handler: handler, removeHandler: removeHandler}
}

func (service *TesterMembershipService) RemoveApplicationTester(ctx context.Context, request *testermembershipv1.RemoveApplicationTesterRequest) (*testermembershipv1.RemoveApplicationTesterResponse, error) {
	if service == nil || service.removeHandler == nil || request == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(testerdomain.ErrDeveloperIdentityRequired)
	}
	if len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidRemoveTesterRequest()
	}
	result, err := service.removeHandler.Handle(ctx, identity, shared.ApplicationID(request.GetApplicationId()), testerdomain.ApplicationTesterMembershipID(request.GetMembershipId()))
	if err != nil {
		if errors.Is(err, testerdomain.ErrApplicationTesterStateInconsistent) {
			// Emit a stable alert without request identifiers, credentials, or the
			// wrapped infrastructure error. Log backends can alert on this reason.
			slog.ErrorContext(ctx, "application tester state invariant failed", "reason", ReasonApplicationTesterStateInconsistent)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.Membership() == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "no-store")
	}
	return &testermembershipv1.RemoveApplicationTesterResponse{
		Removed: result.Removed(), Membership: testerMembershipResource(result.Membership()),
		Capacity:             &testermembershipv1.TesterCapacity{ActiveTesterCount: result.ActiveTesterCount(), TesterLimit: result.TesterLimit()},
		ActiveJoinLinkExists: result.ActiveJoinLinkExists(),
	}, nil
}

func invalidRemoveTesterRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidRemoveTesterRequest, "tester removal accepts only application and membership path IDs")
}

func testerMembershipResource(membership *testerdomain.ApplicationTesterMembership) *testermembershipv1.TesterMembershipResource {
	resource := &testermembershipv1.TesterMembershipResource{
		MembershipId: membership.MembershipID().String(), ApplicationId: membership.ApplicationID().String(), TesterAuthId: membership.TesterAuthID().String(), Status: string(membership.Status()), JoinedViaJoinLinkId: membership.JoinedViaJoinLinkID().String(), JoinedAt: timestamppb.New(membership.JoinedAt()),
	}
	if removedBy := membership.RemovedBy(); removedBy != nil {
		value := removedBy.String()
		resource.RemovedBy = &value
	}
	if removedAt := membership.RemovedAt(); removedAt != nil {
		resource.RemovedAt = timestamppb.New(*removedAt)
	}
	return resource
}

func (service *TesterMembershipService) JoinApplicationAsTester(ctx context.Context, request *testermembershipv1.JoinApplicationAsTesterRequest) (*testermembershipv1.JoinApplicationAsTesterResponse, error) {
	if service == nil || service.handler == nil || request == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(testerdomain.ErrAuthenticatedUserRequired)
	}
	// Kratos merges query bindings after the body. A bearer credential must only
	// come from the command body, including when a body value is also present.
	if httpRequest, ok := khttp.RequestFromServerContext(ctx); ok {
		for key := range httpRequest.URL.Query() {
			if key == "secret" || key == "command" || strings.HasPrefix(key, "command.") {
				return nil, toTransportError(testerdomain.ErrInvalidTesterJoinSecret)
			}
		}
	}
	result, err := service.handler.Handle(ctx, identity, testerdomain.ApplicationTesterJoinLinkID(request.GetJoinLinkId()), testerusecase.JoinApplicationAsTesterCommand{Secret: request.GetCommand().GetSecret()})
	if err != nil {
		return nil, toTransportError(err)
	}
	if result == nil || result.Membership() == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "no-store")
	}
	membership := result.Membership()
	return &testermembershipv1.JoinApplicationAsTesterResponse{
		Joined:     result.Joined(),
		Membership: testerMembershipResource(membership),
		Capacity:   &testermembershipv1.TesterCapacity{ActiveTesterCount: result.ActiveTesterCount(), TesterLimit: result.TesterLimit()},
	}, nil
}
