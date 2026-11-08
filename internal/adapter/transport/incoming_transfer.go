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

const incomingTransferOperation = pb.OperationApplicationAdminTransferServiceListIncomingApplicationAdminTransfers
const incomingTransferPath = "/v1/application-admin-transfers/incoming:search"

type IncomingApplicationAdminTransfersHandler interface {
	List(context.Context, shared.AuthenticatedUserIdentity, int32, string) (domain.IncomingApplicationAdminTransferPage, error)
}

func (s *ApplicationAdminTransferService) ListIncomingApplicationAdminTransfers(ctx context.Context, req *pb.ListIncomingApplicationAdminTransfersRequest) (*pb.IncomingApplicationAdminTransferPage, error) {
	setPrivateNoStore(ctx)
	trusted, _ := trustedIdentityFromContext(ctx)
	identity := shared.AuthenticatedUserIdentity{AuthID: trusted.AuthID}
	if !identity.AuthID.IsValid() {
		return nil, incomingTransferError(domain.ErrIncomingTransferIdentityRequired)
	}
	if req == nil || len(req.ProtoReflect().GetUnknown()) != 0 || (req.Query != nil && len(req.Query.ProtoReflect().GetUnknown()) != 0) {
		return nil, incomingTransferError(domain.ErrInvalidIncomingTransferQuery)
	}
	page, err := s.incoming.List(ctx, identity, req.GetQuery().GetPageSize(), req.GetQuery().GetPageToken())
	if err != nil {
		return nil, incomingTransferError(err)
	}
	result := &pb.IncomingApplicationAdminTransferPage{Items: make([]*pb.IncomingApplicationAdminTransferItem, 0, len(page.Items)), NextPageToken: page.NextPageToken, AsOf: timestamppb.New(page.AsOf)}
	for _, item := range page.Items {
		result.Items = append(result.Items, &pb.IncomingApplicationAdminTransferItem{TransferId: item.TransferID.String(), ApplicationId: item.ApplicationID.String(), ApplicationName: item.ApplicationName, FromAuthId: item.FromAuthID.String(), ToAuthId: item.ToAuthID.String(), Status: pb.ApplicationAdminTransferStatus_APPLICATION_ADMIN_TRANSFER_STATUS_PENDING, RequestedAt: timestamppb.New(item.RequestedAt), ExpiresAt: timestamppb.New(item.ExpiresAt)})
	}
	return result, nil
}
func incomingTransferError(err error) error {
	switch {
	case errors.Is(err, domain.ErrIncomingTransferIdentityRequired):
		return transportStatus(codes.Unauthenticated, ReasonUserIdentityRequired, "user identity is required")
	case errors.Is(err, domain.ErrInvalidIncomingTransferQuery):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_INCOMING_APPLICATION_TRANSFER_QUERY", "incoming application transfer query is invalid")
	case errors.Is(err, domain.ErrInvalidIncomingTransferPageToken):
		return transportStatus(codes.InvalidArgument, "ERROR_REASON_INVALID_INCOMING_APPLICATION_TRANSFER_PAGE_TOKEN", "incoming application transfer page token is invalid")
	case errors.Is(err, domain.ErrApplicationAdminTransferStateInconsistent):
		return transportStatus(codes.Internal, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_STATE_INCONSISTENT", "application administrator transfer state is inconsistent")
	default:
		return transportStatus(codes.Unavailable, "ERROR_REASON_INCOMING_APPLICATION_TRANSFER_UNAVAILABLE", "incoming application transfer query is unavailable")
	}
}
