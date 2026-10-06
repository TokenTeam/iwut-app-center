package transport

import (
	"context"
	"errors"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_operations"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationOperationsHandlers interface {
	Get(context.Context, shared.TrustedIdentity, shared.ApplicationID) (domain.ApplicationPlatformAvailability, error)
	Suspend(context.Context, shared.TrustedIdentity, usecase.ApplicationOperationCommand) (domain.ApplicationPlatformAvailability, error)
	Restore(context.Context, shared.TrustedIdentity, usecase.ApplicationOperationCommand) (domain.ApplicationPlatformAvailability, error)
}

type ApplicationOperationsService struct {
	pb.UnimplementedApplicationOperationsServiceServer
	handlers ApplicationOperationsHandlers
}

func NewApplicationOperationsService(handlers ApplicationOperationsHandlers) *ApplicationOperationsService {
	return &ApplicationOperationsService{handlers: handlers}
}

func operationsRequest(ctx context.Context, raw string) (shared.TrustedIdentity, shared.ApplicationID, error) {
	identity, ok := trustedIdentityFromContext(ctx)
	if !ok || !identity.AuthID.IsValid() {
		return shared.TrustedIdentity{}, "", domain.ErrUserIdentityRequired
	}
	appID, ok := shared.ParseApplicationID(raw)
	if !ok {
		return shared.TrustedIdentity{}, "", domain.ErrInvalidApplicationID
	}
	return identity, appID, nil
}

func (s *ApplicationOperationsService) GetApplicationPlatformAvailability(ctx context.Context, req *pb.GetApplicationPlatformAvailabilityRequest) (*pb.GetApplicationPlatformAvailabilityResponse, error) {
	setNoStore(ctx)
	if s == nil || s.handlers == nil || req == nil {
		return nil, operationsError(domain.ErrInvalidApplicationID)
	}
	identity, appID, err := operationsRequest(ctx, req.GetApplicationId())
	if err != nil {
		return nil, operationsError(err)
	}
	value, err := s.handlers.Get(ctx, identity, appID)
	if err != nil {
		return nil, operationsError(err)
	}
	resource, err := availabilityResource(value)
	if err != nil {
		return nil, operationsError(err)
	}
	return &pb.GetApplicationPlatformAvailabilityResponse{Availability: resource}, nil
}

func (s *ApplicationOperationsService) SuspendApplication(ctx context.Context, req *pb.SuspendApplicationRequest) (*pb.SuspendApplicationResponse, error) {
	setNoStore(ctx)
	if s == nil || s.handlers == nil || req == nil {
		return nil, operationsError(domain.ErrInvalidApplicationID)
	}
	identity, appID, err := operationsRequest(ctx, req.GetApplicationId())
	if err != nil {
		return nil, operationsError(err)
	}
	command := req.GetCommand()
	if command == nil {
		return nil, operationsError(domain.ErrInvalidApplicationOperationReason)
	}
	value, err := s.handlers.Suspend(ctx, identity, operationCommand(appID, command))
	if err != nil {
		return nil, operationsError(err)
	}
	resource, err := availabilityResource(value)
	if err != nil {
		return nil, operationsError(err)
	}
	return &pb.SuspendApplicationResponse{Availability: resource}, nil
}

func (s *ApplicationOperationsService) RestoreApplication(ctx context.Context, req *pb.RestoreApplicationRequest) (*pb.RestoreApplicationResponse, error) {
	setNoStore(ctx)
	if s == nil || s.handlers == nil || req == nil {
		return nil, operationsError(domain.ErrInvalidApplicationID)
	}
	identity, appID, err := operationsRequest(ctx, req.GetApplicationId())
	if err != nil {
		return nil, operationsError(err)
	}
	command := req.GetCommand()
	if command == nil {
		return nil, operationsError(domain.ErrInvalidApplicationOperationReason)
	}
	value, err := s.handlers.Restore(ctx, identity, operationCommand(appID, command))
	if err != nil {
		return nil, operationsError(err)
	}
	resource, err := availabilityResource(value)
	if err != nil {
		return nil, operationsError(err)
	}
	return &pb.RestoreApplicationResponse{Availability: resource}, nil
}

func operationCommand(appID shared.ApplicationID, command *pb.ApplicationPlatformAvailabilityCommand) usecase.ApplicationOperationCommand {
	return usecase.ApplicationOperationCommand{ApplicationID: appID, ExpectedLifecycleRevision: command.GetExpectedLifecycleRevision(), ExpectedPlatformAvailabilityRevision: command.GetExpectedPlatformAvailabilityRevision(), Reason: command.GetReason()}
}

func availabilityResource(value domain.ApplicationPlatformAvailability) (*pb.ApplicationPlatformAvailability, error) {
	if !value.ApplicationID.IsValid() || value.LifecycleRevision < 1 || value.Revision < 1 {
		return nil, domain.ErrApplicationOperationStateInconsistent
	}
	resource := &pb.ApplicationPlatformAvailability{ApplicationId: value.ApplicationID.String(), LifecycleStatus: pb.ApplicationLifecycleStatus(pb.ApplicationLifecycleStatus_value["APPLICATION_LIFECYCLE_STATUS_"+string(value.LifecycleStatus)]), LifecycleRevision: value.LifecycleRevision, PlatformAvailabilityStatus: pb.PlatformAvailabilityStatus(pb.PlatformAvailabilityStatus_value["PLATFORM_AVAILABILITY_STATUS_"+string(value.Status)]), PlatformAvailabilityRevision: value.Revision}
	if resource.LifecycleStatus == pb.ApplicationLifecycleStatus_APPLICATION_LIFECYCLE_STATUS_UNSPECIFIED || resource.PlatformAvailabilityStatus == pb.PlatformAvailabilityStatus_PLATFORM_AVAILABILITY_STATUS_UNSPECIFIED {
		return nil, domain.ErrApplicationOperationStateInconsistent
	}
	if value.LastOperationEventID != nil {
		id := value.LastOperationEventID.String()
		resource.LastOperationEventId = &id
	}
	if value.SuspendedAt != nil {
		ts := timestamppb.New(*value.SuspendedAt)
		if !ts.IsValid() {
			return nil, domain.ErrApplicationOperationStateInconsistent
		}
		resource.SuspendedAt = ts
	}
	if value.RestoredAt != nil {
		ts := timestamppb.New(*value.RestoredAt)
		if !ts.IsValid() {
			return nil, domain.ErrApplicationOperationStateInconsistent
		}
		resource.RestoredAt = ts
	}
	return resource, nil
}

func operationsError(err error) error {
	switch {
	case errors.Is(err, domain.ErrUserIdentityRequired):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_USER_IDENTITY_REQUIRED", "user identity is required")
	case errors.Is(err, domain.ErrApplicationOperationForbidden):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_APPLICATION_OPERATION_FORBIDDEN", "application operation permission is required")
	case errors.Is(err, domain.ErrInvalidApplicationID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_ID", "application ID is invalid")
	case errors.Is(err, domain.ErrInvalidLifecycleRevision):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_LIFECYCLE_REVISION", "lifecycle revision is invalid")
	case errors.Is(err, domain.ErrInvalidPlatformAvailabilityRevision):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_PLATFORM_AVAILABILITY_REVISION", "platform availability revision is invalid")
	case errors.Is(err, domain.ErrInvalidApplicationOperationReason):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_OPERATION_REASON", "application operation reason is invalid")
	case errors.Is(err, domain.ErrApplicationNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_APPLICATION_NOT_FOUND", "application not found")
	case errors.Is(err, domain.ErrApplicationAvailabilityConflict):
		return transportStatus(codes.Aborted, "ERROR_REASON_APPLICATION_AVAILABILITY_CONFLICT", "application availability changed")
	case errors.Is(err, domain.ErrApplicationNotActive):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_APPLICATION_NOT_ACTIVE", "application is not active")
	case errors.Is(err, domain.ErrApplicationOperationStateInconsistent):
		return transportStatus(codes.Internal, "ERROR_REASON_APPLICATION_OPERATION_STATE_INCONSISTENT", "application operation state is inconsistent")
	case errors.Is(err, domain.ErrApplicationOperationUnavailable):
		return transportStatus(codes.Unavailable, "ERROR_REASON_APPLICATION_OPERATION_UNAVAILABLE", "application operation is unavailable")
	default:
		return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "internal failure")
	}
}
