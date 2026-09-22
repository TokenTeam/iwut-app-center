package transport

import (
	"context"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/types/known/timestamppb"
	testermembershipv1 "iwut-app-center/api/gen/go/app_center/v1/tester_membership"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerusecase "iwut-app-center/internal/tester/usecase"
	"strings"
)

type JoinApplicationAsTesterHandler interface {
	Handle(context.Context, shared.AuthenticatedUserIdentity, testerdomain.ApplicationTesterJoinLinkID, testerusecase.JoinApplicationAsTesterCommand) (*testerdomain.JoinApplicationAsTesterResult, error)
}

type TesterMembershipService struct {
	testermembershipv1.UnimplementedTesterMembershipServer
	handler JoinApplicationAsTesterHandler
}

var _ testermembershipv1.TesterMembershipHTTPServer = (*TesterMembershipService)(nil)
var _ testermembershipv1.TesterMembershipServer = (*TesterMembershipService)(nil)

func NewTesterMembershipService(handler JoinApplicationAsTesterHandler) *TesterMembershipService {
	return &TesterMembershipService{handler: handler}
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
		Joined: result.Joined(),
		Membership: &testermembershipv1.TesterMembershipResource{
			MembershipId: membership.MembershipID().String(), ApplicationId: membership.ApplicationID().String(), TesterAuthId: membership.TesterAuthID().String(), Status: string(membership.Status()), JoinedViaJoinLinkId: membership.JoinedViaJoinLinkID().String(), JoinedAt: timestamppb.New(membership.JoinedAt()),
		},
		Capacity: &testermembershipv1.TesterCapacity{ActiveTesterCount: result.ActiveTesterCount(), TesterLimit: result.TesterLimit()},
	}, nil
}
