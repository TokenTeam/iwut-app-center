package transport

import (
	"context"
	"errors"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_archival"
	"google.golang.org/grpc/codes"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationArchivalHandlers interface {
	Get(context.Context, shared.AuthenticatedUserIdentity, shared.ApplicationID) (domain.ApplicationArchiveState, error)
	Archive(context.Context, shared.AuthenticatedUserIdentity, usecase.ApplicationArchiveCommand) (domain.ApplicationArchiveState, error)
	Unarchive(context.Context, shared.AuthenticatedUserIdentity, usecase.ApplicationArchiveCommand) (domain.ApplicationArchiveState, error)
}

type ApplicationArchivalService struct {
	pb.UnimplementedApplicationArchivalServiceServer
	handlers ApplicationArchivalHandlers
}

func NewApplicationArchivalService(handlers ApplicationArchivalHandlers) *ApplicationArchivalService {
	return &ApplicationArchivalService{handlers: handlers}
}

func archivalRequest(ctx context.Context, raw string) (shared.AuthenticatedUserIdentity, shared.ApplicationID, error) {
	identity, ok := authenticatedUserIdentityFromContext(ctx)
	if !ok {
		return shared.AuthenticatedUserIdentity{}, "", domain.ErrUserIdentityRequired
	}
	appID, ok := shared.ParseApplicationID(raw)
	if !ok {
		return shared.AuthenticatedUserIdentity{}, "", domain.ErrInvalidApplicationArchiveRequest
	}
	return identity, appID, nil
}

func (s *ApplicationArchivalService) GetApplicationArchiveState(ctx context.Context, req *pb.GetApplicationArchiveStateRequest) (*pb.GetApplicationArchiveStateResponse, error) {
	setNoStore(ctx)
	if s == nil || s.handlers == nil || req == nil {
		return nil, archivalError(domain.ErrInvalidApplicationArchiveRequest)
	}
	identity, appID, err := archivalRequest(ctx, req.GetApplicationId())
	if err != nil {
		return nil, archivalError(err)
	}
	state, err := s.handlers.Get(ctx, identity, appID)
	if err != nil {
		return nil, archivalError(err)
	}
	resource, err := archiveStateResource(state)
	if err != nil {
		return nil, archivalError(err)
	}
	return &pb.GetApplicationArchiveStateResponse{State: resource}, nil
}

func (s *ApplicationArchivalService) ArchiveApplication(ctx context.Context, req *pb.ArchiveApplicationRequest) (*pb.ArchiveApplicationResponse, error) {
	setNoStore(ctx)
	if s == nil || s.handlers == nil || req == nil || req.GetCommand() == nil {
		return nil, archivalError(domain.ErrInvalidApplicationArchiveRequest)
	}
	identity, appID, err := archivalRequest(ctx, req.GetApplicationId())
	if err != nil {
		return nil, archivalError(err)
	}
	state, err := s.handlers.Archive(ctx, identity, usecase.ApplicationArchiveCommand{ApplicationID: appID, ExpectedArchiveRevision: req.GetCommand().GetExpectedArchiveRevision()})
	if err != nil {
		return nil, archivalError(err)
	}
	resource, err := archiveStateResource(state)
	if err != nil {
		return nil, archivalError(err)
	}
	return &pb.ArchiveApplicationResponse{State: resource}, nil
}

func (s *ApplicationArchivalService) UnarchiveApplication(ctx context.Context, req *pb.UnarchiveApplicationRequest) (*pb.UnarchiveApplicationResponse, error) {
	setNoStore(ctx)
	if s == nil || s.handlers == nil || req == nil || req.GetCommand() == nil {
		return nil, archivalError(domain.ErrInvalidApplicationArchiveRequest)
	}
	identity, appID, err := archivalRequest(ctx, req.GetApplicationId())
	if err != nil {
		return nil, archivalError(err)
	}
	state, err := s.handlers.Unarchive(ctx, identity, usecase.ApplicationArchiveCommand{ApplicationID: appID, ExpectedArchiveRevision: req.GetCommand().GetExpectedArchiveRevision()})
	if err != nil {
		return nil, archivalError(err)
	}
	resource, err := archiveStateResource(state)
	if err != nil {
		return nil, archivalError(err)
	}
	return &pb.UnarchiveApplicationResponse{State: resource}, nil
}

func archiveStateResource(value domain.ApplicationArchiveState) (*pb.ApplicationArchiveState, error) {
	if !value.ApplicationID.IsValid() || value.ArchiveRevision < 1 {
		return nil, domain.ErrApplicationArchiveInconsistent
	}
	resource := &pb.ApplicationArchiveState{
		ApplicationId:              value.ApplicationID.String(),
		LifecycleStatus:            pb.ApplicationLifecycleStatus(pb.ApplicationLifecycleStatus_value["APPLICATION_LIFECYCLE_STATUS_"+string(value.LifecycleStatus)]),
		PlatformAvailabilityStatus: pb.PlatformAvailabilityStatus(pb.PlatformAvailabilityStatus_value["PLATFORM_AVAILABILITY_STATUS_"+string(value.PlatformAvailabilityStatus)]),
		ArchiveStatus:              pb.ApplicationArchiveStatus(pb.ApplicationArchiveStatus_value["APPLICATION_ARCHIVE_STATUS_"+string(value.ArchiveStatus)]),
		ArchiveRevision:            value.ArchiveRevision,
	}
	if resource.LifecycleStatus == pb.ApplicationLifecycleStatus_APPLICATION_LIFECYCLE_STATUS_UNSPECIFIED || resource.PlatformAvailabilityStatus == pb.PlatformAvailabilityStatus_PLATFORM_AVAILABILITY_STATUS_UNSPECIFIED || resource.ArchiveStatus == pb.ApplicationArchiveStatus_APPLICATION_ARCHIVE_STATUS_UNSPECIFIED {
		return nil, domain.ErrApplicationArchiveInconsistent
	}
	return resource, nil
}

func archivalError(err error) error {
	switch {
	case errors.Is(err, domain.ErrUserIdentityRequired):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_USER_IDENTITY_REQUIRED", "user identity is required")
	case errors.Is(err, domain.ErrInvalidApplicationArchiveRequest):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_ARCHIVE_REQUEST", "application archive request is invalid")
	case errors.Is(err, domain.ErrApplicationArchiveNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_APPLICATION_ARCHIVE_NOT_FOUND", "application not found")
	case errors.Is(err, domain.ErrApplicationArchiveConflict):
		return transportStatus(codes.Aborted, "ERROR_REASON_APPLICATION_ARCHIVE_CONFLICT", "application archive state changed")
	case errors.Is(err, domain.ErrApplicationNotActive):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_APPLICATION_NOT_ACTIVE", "application is not active")
	case errors.Is(err, domain.ErrApplicationArchiveInconsistent):
		return transportStatus(codes.Internal, "ERROR_REASON_APPLICATION_ARCHIVE_STATE_INCONSISTENT", "application archive state is inconsistent")
	case errors.Is(err, domain.ErrApplicationArchiveUnavailable):
		return transportStatus(codes.Unavailable, "ERROR_REASON_APPLICATION_ARCHIVE_UNAVAILABLE", "application archive operation is unavailable")
	default:
		return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "internal failure")
	}
}
