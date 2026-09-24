package transport

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

// CreateApplicationHandler is the narrow UC-001 port consumed by the transport.
// Both the HTTP and gRPC entry points call this same handler.
type CreateApplicationHandler interface {
	Handle(
		ctx context.Context,
		identity usecase.DeveloperIdentity,
		command usecase.CreateApplicationCommand,
	) (*domain.Application, error)
}

// ApplicationService adapts the generated application.v1 contract to the
// protocol-independent CreateApplication use case. It implements both the HTTP
// and gRPC generated server interfaces.
type ApplicationService struct {
	applicationv1.UnimplementedApplicationServer
	handler CreateApplicationHandler
}

var _ applicationv1.ApplicationHTTPServer = (*ApplicationService)(nil)
var _ applicationv1.ApplicationServer = (*ApplicationService)(nil)

func NewApplicationService(handler CreateApplicationHandler) *ApplicationService {
	return &ApplicationService{handler: handler}
}

func (service *ApplicationService) CreateApplication(
	ctx context.Context,
	request *applicationv1.CreateApplicationRequest,
) (*applicationv1.CreateApplicationResponse, error) {
	if service == nil || service.handler == nil {
		return nil, toTransportError(domain.NewInternalError(nil))
	}

	identity, ok := developerIdentityFromContext(ctx)
	if !ok {
		return nil, toTransportError(domain.ErrDeveloperIdentityRequired)
	}

	application, err := service.handler.Handle(
		ctx,
		shared.DeveloperIdentity{
			AuthID:          identity.AuthID,
			DeveloperStatus: identity.DeveloperStatus,
		},
		usecase.CreateApplicationCommand{Name: request.GetName()},
	)
	if err != nil {
		return nil, toTransportError(err)
	}

	return &applicationv1.CreateApplicationResponse{
		Id:        application.ID().String(),
		Name:      application.Name().String(),
		AdminId:   application.AdminID().String(),
		CreatedAt: timestamppb.New(application.CreatedAt()),
	}, nil
}
