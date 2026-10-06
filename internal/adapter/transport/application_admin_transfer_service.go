package transport

import (
	"context"
	"errors"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type ApplicationAdminTransferHandlers interface {
	GetOwnership(context.Context, shared.DeveloperIdentity, shared.ApplicationID) (domain.ApplicationOwnership, error)
	Initiate(context.Context, shared.DeveloperIdentity, shared.ApplicationID, shared.AuthID, int64) (domain.ApplicationAdminTransfer, error)
	Get(context.Context, shared.DeveloperIdentity, domain.ApplicationAdminTransferID) (domain.ApplicationAdminTransfer, error)
	Accept(context.Context, shared.DeveloperIdentity, domain.ApplicationAdminTransferID, domain.ConfidentialCredentialHandling) (domain.AcceptApplicationAdminTransferResult, error)
	Reject(context.Context, shared.DeveloperIdentity, domain.ApplicationAdminTransferID) (domain.ApplicationAdminTransfer, error)
	Cancel(context.Context, shared.DeveloperIdentity, domain.ApplicationAdminTransferID) (domain.ApplicationAdminTransfer, error)
}

type ApplicationAdminTransferService struct {
	pb.UnimplementedApplicationAdminTransferServiceServer
	handlers ApplicationAdminTransferHandlers
}

func NewApplicationAdminTransferService(handlers ApplicationAdminTransferHandlers) *ApplicationAdminTransferService {
	return &ApplicationAdminTransferService{handlers: handlers}
}

func transferIdentityFromContext(ctx context.Context) (shared.DeveloperIdentity, error) {
	identity, ok := developerIdentityFromContext(ctx)
	if !ok || !identity.AuthID.IsValid() {
		return shared.DeveloperIdentity{}, domain.ErrDeveloperIdentityRequired
	}
	return identity, nil
}

func (s *ApplicationAdminTransferService) GetApplicationOwnership(ctx context.Context, request *pb.GetApplicationOwnershipRequest) (*pb.GetApplicationOwnershipResponse, error) {
	setNoStore(ctx)
	identity, err := transferIdentityFromContext(ctx)
	if err != nil {
		return nil, transferError(err)
	}
	if request == nil {
		return nil, transferError(domain.ErrInvalidApplicationID)
	}
	appID, ok := shared.ParseApplicationID(request.GetApplicationId())
	if !ok {
		return nil, transferError(domain.ErrInvalidApplicationID)
	}
	ownership, err := s.handlers.GetOwnership(ctx, identity, appID)
	if err != nil {
		return nil, transferError(err)
	}
	resource := &pb.ApplicationOwnershipResource{ApplicationId: ownership.ApplicationID.String(), OwnershipRevision: ownership.OwnershipRevision}
	if ownership.PendingTransferID != nil {
		value := ownership.PendingTransferID.String()
		resource.PendingTransferId = &value
	}
	if ownership.PendingExpiresAt != nil {
		resource.PendingExpiresAt = timestamppb.New(*ownership.PendingExpiresAt)
	}
	return &pb.GetApplicationOwnershipResponse{Ownership: resource}, nil
}

func (s *ApplicationAdminTransferService) InitiateApplicationAdminTransfer(ctx context.Context, request *pb.InitiateApplicationAdminTransferRequest) (*pb.InitiateApplicationAdminTransferResponse, error) {
	setNoStore(ctx)
	identity, err := transferIdentityFromContext(ctx)
	if err != nil {
		return nil, transferError(err)
	}
	if request == nil {
		return nil, transferError(domain.ErrInvalidApplicationID)
	}
	appID, ok := shared.ParseApplicationID(request.GetApplicationId())
	if !ok {
		return nil, transferError(domain.ErrInvalidApplicationID)
	}
	command := request.GetCommand()
	if command == nil {
		return nil, transferError(domain.ErrInvalidTargetAuthID)
	}
	transfer, err := s.handlers.Initiate(ctx, identity, appID, shared.AuthID(command.GetToAuthId()), command.GetExpectedOwnershipRevision())
	if err != nil {
		return nil, transferError(err)
	}
	return &pb.InitiateApplicationAdminTransferResponse{Transfer: transferResource(transfer)}, nil
}

func (s *ApplicationAdminTransferService) GetApplicationAdminTransfer(ctx context.Context, request *pb.GetApplicationAdminTransferRequest) (*pb.GetApplicationAdminTransferResponse, error) {
	setNoStore(ctx)
	identity, err := transferIdentityFromContext(ctx)
	if err != nil {
		return nil, transferError(err)
	}
	if request == nil {
		return nil, transferError(domain.ErrInvalidApplicationAdminTransferID)
	}
	id, err := domain.ParseApplicationAdminTransferID(request.GetTransferId())
	if err != nil {
		return nil, transferError(err)
	}
	transfer, err := s.handlers.Get(ctx, identity, id)
	if err != nil {
		return nil, transferError(err)
	}
	return &pb.GetApplicationAdminTransferResponse{Transfer: transferResource(transfer)}, nil
}

func (s *ApplicationAdminTransferService) AcceptApplicationAdminTransfer(ctx context.Context, request *pb.AcceptApplicationAdminTransferRequest) (*pb.AcceptApplicationAdminTransferResponse, error) {
	setNoStore(ctx)
	identity, err := transferIdentityFromContext(ctx)
	if err != nil {
		return nil, transferError(err)
	}
	if request == nil {
		return nil, transferError(domain.ErrInvalidApplicationAdminTransferID)
	}
	id, err := domain.ParseApplicationAdminTransferID(request.GetTransferId())
	if err != nil {
		return nil, transferError(err)
	}
	command := request.GetCommand()
	if command == nil {
		return nil, transferError(domain.ErrInvalidConfidentialCredentialHandling)
	}
	handling, err := transferHandling(command.GetConfidentialCredentialHandling())
	if err != nil {
		return nil, transferError(err)
	}
	result, err := s.handlers.Accept(ctx, identity, id, handling)
	if err != nil {
		return nil, transferError(err)
	}
	response := &pb.AcceptApplicationAdminTransferResponse{Transfer: transferResource(result.Transfer), SecretsDisclosed: result.SecretsDisclosed}
	for _, credential := range result.RotatedCredentials {
		response.RotatedCredentials = append(response.RotatedCredentials, &pb.RotatedCredentialResource{Channel: credential.Channel, ClientId: credential.ClientID, CredentialRevision: credential.CredentialRevision, ClientSecret: credential.ClientSecret})
	}
	return response, nil
}

func (s *ApplicationAdminTransferService) RejectApplicationAdminTransfer(ctx context.Context, request *pb.RejectApplicationAdminTransferRequest) (*pb.RejectApplicationAdminTransferResponse, error) {
	setNoStore(ctx)
	identity, err := transferIdentityFromContext(ctx)
	if err != nil {
		return nil, transferError(err)
	}
	if request == nil {
		return nil, transferError(domain.ErrInvalidApplicationAdminTransferID)
	}
	id, err := domain.ParseApplicationAdminTransferID(request.GetTransferId())
	if err != nil {
		return nil, transferError(err)
	}
	transfer, err := s.handlers.Reject(ctx, identity, id)
	if err != nil {
		return nil, transferError(err)
	}
	return &pb.RejectApplicationAdminTransferResponse{Transfer: transferResource(transfer)}, nil
}

func (s *ApplicationAdminTransferService) CancelApplicationAdminTransfer(ctx context.Context, request *pb.CancelApplicationAdminTransferRequest) (*pb.CancelApplicationAdminTransferResponse, error) {
	setNoStore(ctx)
	identity, err := transferIdentityFromContext(ctx)
	if err != nil {
		return nil, transferError(err)
	}
	if request == nil {
		return nil, transferError(domain.ErrInvalidApplicationAdminTransferID)
	}
	id, err := domain.ParseApplicationAdminTransferID(request.GetTransferId())
	if err != nil {
		return nil, transferError(err)
	}
	transfer, err := s.handlers.Cancel(ctx, identity, id)
	if err != nil {
		return nil, transferError(err)
	}
	return &pb.CancelApplicationAdminTransferResponse{Transfer: transferResource(transfer)}, nil
}

func transferHandling(value pb.ConfidentialCredentialHandling) (domain.ConfidentialCredentialHandling, error) {
	if value == pb.ConfidentialCredentialHandling_CONFIDENTIAL_CREDENTIAL_HANDLING_KEEP {
		return domain.ConfidentialCredentialKeep, nil
	}
	if value == pb.ConfidentialCredentialHandling_CONFIDENTIAL_CREDENTIAL_HANDLING_ROTATE {
		return domain.ConfidentialCredentialRotate, nil
	}
	return "", domain.ErrInvalidConfidentialCredentialHandling
}

func transferResource(value domain.ApplicationAdminTransfer) *pb.ApplicationAdminTransferResource {
	resource := &pb.ApplicationAdminTransferResource{TransferId: value.TransferID.String(), ApplicationId: value.ApplicationID.String(), FromAdminId: value.FromAdminID.String(), ToAdminId: value.ToAdminID.String(), SourceOwnershipRevision: value.SourceOwnershipRevision, Status: pb.ApplicationAdminTransferStatus(pb.ApplicationAdminTransferStatus_value["APPLICATION_ADMIN_TRANSFER_STATUS_"+string(value.Status)]), RequestedAt: timestamppb.New(value.RequestedAt), ExpiresAt: timestamppb.New(value.ExpiresAt)}
	if value.ResolvedAt != nil {
		resource.ResolvedAt = timestamppb.New(*value.ResolvedAt)
	}
	if value.ResolvedBy != nil {
		resolvedBy := value.ResolvedBy.String()
		resource.ResolvedBy = &resolvedBy
	}
	if value.ConfidentialCredentialHandling != nil {
		handling := pb.ConfidentialCredentialHandling(pb.ConfidentialCredentialHandling_value["CONFIDENTIAL_CREDENTIAL_HANDLING_"+string(*value.ConfidentialCredentialHandling)])
		resource.ConfidentialCredentialHandling = &handling
	}
	return resource
}

func transferError(err error) error {
	switch {
	case errors.Is(err, domain.ErrDeveloperIdentityRequired):
		return transportStatus(codes.Unauthenticated, "ERROR_REASON_DEVELOPER_IDENTITY_REQUIRED", "developer identity is required")
	case errors.Is(err, domain.ErrDeveloperApprovalRequired):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_DEVELOPER_APPROVAL_REQUIRED", "approved developer status is required")
	case errors.Is(err, domain.ErrInvalidApplicationID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_APPLICATION_ID", "application ID is invalid")
	case errors.Is(err, domain.ErrInvalidApplicationAdminTransferID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_TRANSFER_ID", "transfer ID is invalid")
	case errors.Is(err, domain.ErrInvalidTargetAuthID):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_TARGET_AUTH_ID", "target Auth ID is invalid")
	case errors.Is(err, domain.ErrInvalidOwnershipRevision):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_OWNERSHIP_REVISION", "ownership revision is invalid")
	case errors.Is(err, domain.ErrInvalidConfidentialCredentialHandling):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_CONFIDENTIAL_CREDENTIAL_HANDLING", "confidential credential handling is invalid")
	case errors.Is(err, domain.ErrApplicationNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_APPLICATION_NOT_FOUND", "application not found")
	case errors.Is(err, domain.ErrApplicationAdminTransferNotFound):
		return transportStatus(codes.NotFound, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_NOT_FOUND", "application administrator transfer not found")
	case errors.Is(err, domain.ErrApplicationAdminRequired):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_APPLICATION_ADMIN_REQUIRED", "application administrator is required")
	case errors.Is(err, domain.ErrApplicationAdminTransferParticipantRequired):
		return transportStatus(codes.PermissionDenied, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_PARTICIPANT_REQUIRED", "transfer participant is required")
	case errors.Is(err, domain.ErrApplicationAdminTransferAlreadyPending):
		return transportStatus(codes.AlreadyExists, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_ALREADY_PENDING", "another transfer is already pending")
	case errors.Is(err, domain.ErrApplicationNameConflict):
		return transportStatus(codes.AlreadyExists, "ERROR_REASON_APPLICATION_NAME_CONFLICT", "application name conflicts at target")
	case errors.Is(err, domain.ErrApplicationQuotaExceeded):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_APPLICATION_QUOTA_EXCEEDED", "target application quota is exhausted")
	case errors.Is(err, domain.ErrApplicationAdminTransferExpired):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_EXPIRED", "transfer has expired")
	case errors.Is(err, domain.ErrApplicationAdminTransferNotPending):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_NOT_PENDING", "transfer is not pending")
	case errors.Is(err, domain.ErrApplicationOwnershipChanged):
		return transportStatus(codes.Aborted, "ERROR_REASON_APPLICATION_OWNERSHIP_CHANGED", "application ownership changed")
	case errors.Is(err, shared.ErrAccountExitBlocked):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_ACCOUNT_OWNER_EXIT_IN_PROGRESS", "account owner exit is in progress")
	case errors.Is(err, domain.ErrSourceDeveloperIneligible):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_SOURCE_DEVELOPER_INELIGIBLE", "source developer is ineligible")
	case errors.Is(err, domain.ErrTargetDeveloperIneligible):
		return transportStatus(codes.FailedPrecondition, "ERROR_REASON_TARGET_DEVELOPER_INELIGIBLE", "target developer is ineligible")
	case errors.Is(err, domain.ErrDeveloperStatusUnavailable):
		return transportStatus(codes.Unavailable, "ERROR_REASON_DEVELOPER_STATUS_UNAVAILABLE", "developer status is unavailable")
	case errors.Is(err, domain.ErrApplicationAdminTransferStateInconsistent):
		return transportStatus(codes.Internal, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_STATE_INCONSISTENT", "application administrator transfer state is inconsistent")
	default:
		return transportStatus(codes.Internal, "ERROR_REASON_INTERNAL", "internal failure")
	}
}
