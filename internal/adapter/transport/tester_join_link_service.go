package transport

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"log/slog"
	"strings"

	kratostransport "github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/protobuf/types/known/timestamppb"

	testerjoinlinkv1 "iwut-app-center/api/gen/go/app_center/v1/tester_join_link"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerusecase "iwut-app-center/internal/tester/usecase"
)

type CreateOrRotateTesterJoinLinkHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, testerusecase.CreateOrRotateTesterJoinLinkCommand) (*testerusecase.CreateOrRotateTesterJoinLinkResult, error)
}

type RevokeTesterJoinLinkHandler interface {
	Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, testerdomain.ApplicationTesterJoinLinkID) (*testerdomain.RevokeTesterJoinLinkResult, error)
}

type TesterJoinLinkService struct {
	testerjoinlinkv1.UnimplementedTesterJoinLinkServer
	handler       CreateOrRotateTesterJoinLinkHandler
	revokeHandler RevokeTesterJoinLinkHandler
}

var _ testerjoinlinkv1.TesterJoinLinkHTTPServer = (*TesterJoinLinkService)(nil)
var _ testerjoinlinkv1.TesterJoinLinkServer = (*TesterJoinLinkService)(nil)

func NewTesterJoinLinkService(handler CreateOrRotateTesterJoinLinkHandler, revokeHandler RevokeTesterJoinLinkHandler) *TesterJoinLinkService {
	return &TesterJoinLinkService{handler: handler, revokeHandler: revokeHandler}
}

func (service *TesterJoinLinkService) CreateOrRotateTesterJoinLink(ctx context.Context, request *testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest) (*testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse, error) {
	if service == nil || service.handler == nil || request == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(testerdomain.ErrDeveloperIdentityRequired)
	}
	// The generated HTTP binding merges query fields after decoding the body.
	// This command's only input belongs to the body; reject nested query input
	// before it can reach the use case or rotate a link.
	if httpRequest, ok := khttp.RequestFromServerContext(ctx); ok {
		for key := range httpRequest.URL.Query() {
			if key == "command" || strings.HasPrefix(key, "command.") || key == "expected_active_join_link_id" || key == "expectedActiveJoinLinkId" {
				return nil, toTransportError(testerdomain.ErrInvalidTesterJoinLinkId)
			}
		}
	}
	applicationID, valid := shared.ParseApplicationID(request.GetApplicationId())
	if !valid {
		return nil, toTransportError(testerdomain.ErrInvalidApplicationId)
	}
	command := testerusecase.CreateOrRotateTesterJoinLinkCommand{}
	if body := request.GetCommand(); body != nil && body.ExpectedActiveJoinLinkId != nil {
		id := testerdomain.ApplicationTesterJoinLinkID(body.GetExpectedActiveJoinLinkId())
		command.ExpectedActiveJoinLinkID = &id
	}
	result, err := service.handler.Handle(ctx, identity, applicationID, command)
	if err != nil {
		return nil, toTransportError(err)
	}
	if result == nil || result.JoinLink() == nil || result.JoinURL() == "" {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	// Both transports receive the credential cache directive; the HTTP encoder
	// also sets it before writing the 201 status.
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "no-store")
	}
	link := result.JoinLink()
	response := &testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse{JoinLink: &testerjoinlinkv1.TesterJoinLinkResource{
		JoinLinkId: link.JoinLinkID().String(), ApplicationId: link.ApplicationID().String(), Status: string(link.Status()), CreatedBy: link.CreatedBy().String(), CreatedAt: timestamppb.New(link.CreatedAt()),
	}, JoinUrl: result.JoinURL()}
	if replaced := result.ReplacedJoinLinkID(); replaced != nil {
		id := replaced.String()
		response.ReplacedJoinLinkId = &id
	}
	return response, nil
}

func (service *TesterJoinLinkService) RevokeTesterJoinLink(ctx context.Context, request *testerjoinlinkv1.RevokeTesterJoinLinkRequest) (*testerjoinlinkv1.RevokeTesterJoinLinkResponse, error) {
	if service == nil || service.revokeHandler == nil || request == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(testerdomain.ErrDeveloperIdentityRequired)
	}
	if len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, invalidRevokeTesterJoinLinkRequest()
	}
	result, err := service.revokeHandler.Handle(ctx, identity, shared.ApplicationID(request.GetApplicationId()), testerdomain.ApplicationTesterJoinLinkID(request.GetJoinLinkId()))
	if err != nil {
		if errors.Is(err, testerdomain.ErrApplicationTesterJoinLinkStateInconsistent) {
			slog.ErrorContext(ctx, "application tester join link state invariant failed", "reason", ReasonApplicationTesterJoinLinkStateInconsistent)
		}
		return nil, toTransportError(err)
	}
	if result == nil || result.JoinLink() == nil {
		return nil, toTransportError(testerdomain.NewInternalError(nil))
	}
	if transporter, ok := kratostransport.FromServerContext(ctx); ok {
		transporter.ReplyHeader().Set("Cache-Control", "no-store")
	}
	link := result.JoinLink()
	resource := &testerjoinlinkv1.TesterJoinLinkRevocationResource{JoinLinkId: link.JoinLinkID().String(), ApplicationId: link.ApplicationID().String(), Status: string(link.Status()), CreatedBy: link.CreatedBy().String(), CreatedAt: timestamppb.New(link.CreatedAt())}
	if v := link.RevokedBy(); v != nil {
		s := v.String()
		resource.RevokedBy = &s
	}
	if v := link.RevokedAt(); v != nil {
		resource.RevokedAt = timestamppb.New(*v)
	}
	if v := link.RevocationReason(); v != nil {
		s := string(*v)
		resource.RevocationReason = &s
	}
	if v := link.ReplacedByJoinLinkID(); v != nil {
		s := v.String()
		resource.ReplacedByJoinLinkId = &s
	}
	return &testerjoinlinkv1.RevokeTesterJoinLinkResponse{Revoked: result.Revoked(), JoinLink: resource}, nil
}
func invalidRevokeTesterJoinLinkRequest() error {
	return transportStatus(codes.InvalidArgument, ReasonInvalidRevokeTesterJoinLinkRequest, "tester join link revocation accepts only application and join link path IDs")
}
