package transport

import (
	"context"
	"errors"
	"strings"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_closure"
	kratostransport "github.com/go-kratos/kratos/v2/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

type ApplicationClosureHandlers interface {
	Preview(context.Context, shared.DeveloperIdentity, shared.ApplicationID) (domain.ApplicationClosurePreview, error)
	Get(context.Context, shared.DeveloperIdentity, shared.ApplicationID) (domain.ApplicationClosure, error)
	Close(context.Context, shared.DeveloperIdentity, usecase.CloseApplicationCommand) (domain.ApplicationClosure, error)
}

type ApplicationClosureService struct {
	pb.UnimplementedApplicationClosureServiceServer
	handlers      ApplicationClosureHandlers
	proofVerifier *IdentityVerifier
}

func NewApplicationClosureService(handlers ApplicationClosureHandlers, proofVerifier *IdentityVerifier) *ApplicationClosureService {
	return &ApplicationClosureService{handlers: handlers, proofVerifier: proofVerifier}
}

func closureRequest(ctx context.Context, raw string) (shared.DeveloperIdentity, shared.ApplicationID, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok || !identity.AuthID.IsValid() {
		return shared.DeveloperIdentity{}, "", domain.ErrDeveloperIdentityRequired
	}
	appID, ok := shared.ParseApplicationID(raw)
	if !ok {
		return shared.DeveloperIdentity{}, "", domain.ErrInvalidApplicationID
	}
	return identity, appID, nil
}

func (s *ApplicationClosureService) GetApplicationClosurePreview(ctx context.Context, request *pb.GetApplicationClosurePreviewRequest) (*pb.GetApplicationClosurePreviewResponse, error) {
	setNoStore(ctx)
	if request == nil {
		return nil, closureError(domain.ErrInvalidApplicationID)
	}
	identity, appID, err := closureRequest(ctx, request.GetApplicationId())
	if err != nil {
		return nil, closureError(err)
	}
	preview, err := s.handlers.Preview(ctx, identity, appID)
	if err != nil {
		return nil, closureError(err)
	}
	return &pb.GetApplicationClosurePreviewResponse{Preview: &pb.ApplicationClosurePreview{ApplicationId: preview.ApplicationID.String(), OwnershipRevision: preview.OwnershipRevision, LifecycleRevision: preview.LifecycleRevision, LifecycleStatus: lifecycleProto(preview.LifecycleStatus), HasPendingAdminTransfer: preview.HasPendingAdminTransfer, ActivePublicationChannelCount: preview.ActivePublicationChannelCount, EnabledOauthClientCount: preview.EnabledOAuthClientCount, ActiveTesterCount: preview.ActiveTesterCount, PendingReviewCount: preview.PendingReviewCount}}, nil
}

func (s *ApplicationClosureService) CloseApplication(ctx context.Context, request *pb.CloseApplicationRequest) (*pb.CloseApplicationResponse, error) {
	setNoStore(ctx)
	if request == nil {
		return nil, closureError(domain.ErrInvalidApplicationID)
	}
	identity, appID, err := closureRequest(ctx, request.GetApplicationId())
	if err != nil {
		return nil, closureError(err)
	}
	command := request.GetCommand()
	if command == nil {
		return nil, closureError(domain.ErrInvalidCloseConfirmation)
	}
	transporter, ok := kratostransport.FromServerContext(ctx)
	if !ok {
		return nil, closureError(domain.ErrHighRiskProofRequired)
	}
	values := transporter.RequestHeader().Values(HighRiskProofHeader)
	if len(values) == 0 {
		return nil, closureError(domain.ErrHighRiskProofRequired)
	}
	if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] || s.proofVerifier == nil {
		return nil, closureError(domain.ErrHighRiskProofInvalid)
	}
	proof, err := s.proofVerifier.VerifyApplicationCloseProof(values[0], identity.AuthID, appID)
	proofStale := errors.Is(err, errApplicationCloseProofStale)
	if err != nil && !proofStale {
		return nil, closureError(err)
	}
	closure, err := s.handlers.Close(ctx, identity, usecase.CloseApplicationCommand{ApplicationID: appID, ExpectedOwnershipRevision: command.GetExpectedOwnershipRevision(), ExpectedLifecycleRevision: command.GetExpectedLifecycleRevision(), Confirmation: command.GetConfirmation(), Proof: proof, ProofStale: proofStale})
	if err != nil {
		return nil, closureError(err)
	}
	resource, err := closureResource(closure)
	if err != nil {
		return nil, closureError(err)
	}
	return &pb.CloseApplicationResponse{Closure: resource}, nil
}

func (s *ApplicationClosureService) GetApplicationClosure(ctx context.Context, request *pb.GetApplicationClosureRequest) (*pb.GetApplicationClosureResponse, error) {
	setNoStore(ctx)
	if request == nil {
		return nil, closureError(domain.ErrInvalidApplicationID)
	}
	identity, appID, err := closureRequest(ctx, request.GetApplicationId())
	if err != nil {
		return nil, closureError(err)
	}
	closure, err := s.handlers.Get(ctx, identity, appID)
	if err != nil {
		return nil, closureError(err)
	}
	resource, err := closureResource(closure)
	if err != nil {
		return nil, closureError(err)
	}
	return &pb.GetApplicationClosureResponse{Closure: resource}, nil
}

func lifecycleProto(status domain.ApplicationLifecycleStatus) pb.ApplicationLifecycleStatus {
	return pb.ApplicationLifecycleStatus(pb.ApplicationLifecycleStatus_value["APPLICATION_LIFECYCLE_STATUS_"+string(status)])
}
func closureResource(value domain.ApplicationClosure) (*pb.ApplicationClosureResource, error) {
	var lifecycle domain.ApplicationLifecycleStatus
	switch value.Status {
	case domain.ApplicationClosureClosing:
		lifecycle = domain.ApplicationLifecycleClosing
	case domain.ApplicationClosureClosed:
		lifecycle = domain.ApplicationLifecycleClosed
	default:
		return nil, domain.ErrApplicationClosureStateInconsistent
	}
	closingStartedAt := timestamppb.New(value.ClosingStartedAt)
	if !closingStartedAt.IsValid() {
		return nil, domain.ErrApplicationClosureStateInconsistent
	}
	resource := &pb.ApplicationClosureResource{ClosureId: value.ClosureID.String(), ApplicationId: value.ApplicationID.String(), LifecycleStatus: lifecycleProto(lifecycle), LifecycleRevision: value.LifecycleRevision, ClosingStartedAt: closingStartedAt}
	if value.ClosedAt != nil {
		closedAt := timestamppb.New(*value.ClosedAt)
		if !closedAt.IsValid() {
			return nil, domain.ErrApplicationClosureStateInconsistent
		}
		resource.ClosedAt = closedAt
	}
	return resource, nil
}

func closureError(err error) error {
	switch {
	case errors.Is(err, domain.ErrDeveloperIdentityRequired):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_DEVELOPER_IDENTITY_REQUIRED", "developer identity is required")
	case errors.Is(err, domain.ErrHighRiskProofRequired):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_HIGH_RISK_PROOF_REQUIRED", "application close authentication is required")
	case errors.Is(err, domain.ErrHighRiskProofInvalid):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_HIGH_RISK_PROOF_INVALID", "application close proof is invalid")
	case errors.Is(err, domain.ErrHighRiskProofReplayed):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_HIGH_RISK_PROOF_REPLAYED", "application close proof was already consumed")
	case errors.Is(err, domain.ErrDeveloperApprovalRequired):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_DEVELOPER_APPROVAL_REQUIRED", "approved developer status is required")
	case errors.Is(err, domain.ErrApplicationAdminRequired):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_APPLICATION_ADMIN_REQUIRED", "eligible application administrator is required")
	case errors.Is(err, domain.ErrInvalidApplicationID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_ID", "application ID is invalid")
	case errors.Is(err, domain.ErrInvalidOwnershipRevision):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_OWNERSHIP_REVISION", "ownership revision is invalid")
	case errors.Is(err, domain.ErrInvalidLifecycleRevision):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_LIFECYCLE_REVISION", "lifecycle revision is invalid")
	case errors.Is(err, domain.ErrInvalidCloseConfirmation):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_CLOSE_CONFIRMATION", "close confirmation is invalid")
	case errors.Is(err, domain.ErrApplicationNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_APPLICATION_NOT_FOUND", "application closure not found")
	case errors.Is(err, domain.ErrApplicationClosureNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_APPLICATION_CLOSURE_NOT_FOUND", "application closure not found")
	case errors.Is(err, domain.ErrApplicationOwnershipChanged):
		return transportStatus(codes.Aborted, "ERROR_REASON_APPLICATION_OWNERSHIP_CHANGED", "application ownership changed")
	case errors.Is(err, domain.ErrApplicationLifecycleChanged):
		return transportStatus(codes.Aborted, "ERROR_REASON_APPLICATION_LIFECYCLE_CHANGED", "application lifecycle changed")
	case errors.Is(err, domain.ErrApplicationNotActive):
		return transportStatus(codes.Aborted, "ERROR_REASON_APPLICATION_NOT_ACTIVE", "application is not active")
	case errors.Is(err, domain.ErrDeveloperStatusUnavailable):
		return transportStatus(codes.Unavailable, "ERROR_REASON_DEVELOPER_STATUS_UNAVAILABLE", "developer status is unavailable")
	case errors.Is(err, shared.ErrAccountExitBlocked):
		return transportStatus(codes.Aborted, "ERROR_REASON_ACCOUNT_OWNER_EXIT_IN_PROGRESS", "account owner exit is in progress")
	case errors.Is(err, domain.ErrApplicationClosureStateInconsistent):
		return transportStatus(codes.Internal, "ERROR_REASON_APPLICATION_CLOSURE_STATE_INCONSISTENT", "application closure state is inconsistent")
	default:
		return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "internal failure")
	}
}
