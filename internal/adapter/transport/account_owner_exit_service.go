package transport

import (
	"context"
	"errors"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/account_owner_exit"
	"google.golang.org/grpc/codes"
	d "iwut-app-center/internal/ownerexit/domain"
	u "iwut-app-center/internal/ownerexit/usecase"
)

type AccountOwnerExitService struct {
	pb.UnimplementedAccountOwnerExitServiceServer
	handlers *u.Handlers
}

func NewAccountOwnerExitService(h *u.Handlers) *AccountOwnerExitService {
	return &AccountOwnerExitService{handlers: h}
}
func (s *AccountOwnerExitService) PrepareAccountOwnerExit(ctx context.Context, r *pb.PrepareAccountOwnerExitRequest) (*pb.PrepareAccountOwnerExitResponse, error) {
	if r == nil {
		return nil, ownerExitError(d.ErrInvalid)
	}
	out, e := s.handlers.Prepare(ctx, d.Prepare{Key: d.Key{AuthID: r.AuthId, OperationID: r.OperationId}, Purpose: d.Purpose(r.Purpose)})
	if e != nil {
		return nil, ownerExitError(e)
	}
	if out.Blocked {
		return &pb.PrepareAccountOwnerExitResponse{Outcome: pb.Outcome_OUTCOME_BLOCKED, Blocker: pb.Blocker_BLOCKER_OWNED_APPLICATIONS}, nil
	}
	return &pb.PrepareAccountOwnerExitResponse{Outcome: pb.Outcome_OUTCOME_PREPARED, ReceiptId: out.ReceiptID}, nil
}
func (s *AccountOwnerExitService) FinishAccountOwnerExit(ctx context.Context, r *pb.FinishAccountOwnerExitRequest) (*pb.AccountOwnerExitStatus, error) {
	if r == nil {
		return nil, ownerExitError(d.ErrInvalid)
	}
	out, e := s.handlers.Finish(ctx, d.Finish{Prepare: d.Prepare{Key: d.Key{AuthID: r.AuthId, OperationID: r.OperationId}, Purpose: d.Purpose(r.Purpose)}, ReceiptID: r.ReceiptId, Decision: d.Decision(r.Decision)})
	if e != nil {
		return nil, ownerExitError(e)
	}
	return ownerExitStatus(out), nil
}
func (s *AccountOwnerExitService) GetAccountOwnerExitStatus(ctx context.Context, r *pb.GetAccountOwnerExitStatusRequest) (*pb.AccountOwnerExitStatus, error) {
	if r == nil {
		return nil, ownerExitError(d.ErrInvalid)
	}
	out, e := s.handlers.Get(ctx, d.Key{AuthID: r.AuthId, OperationID: r.OperationId})
	if e != nil {
		return nil, ownerExitError(e)
	}
	return ownerExitStatus(out), nil
}
func ownerExitStatus(o d.Status) *pb.AccountOwnerExitStatus {
	return &pb.AccountOwnerExitStatus{AuthId: o.AuthID, OperationId: o.OperationID, Purpose: pb.Purpose(o.Purpose), ReceiptId: o.ReceiptID, Decision: pb.Decision(o.Decision), CleanupState: pb.CleanupState(o.Cleanup)}
}
func ownerExitError(e error) error {
	switch {
	case errors.Is(e, d.ErrInvalid):
		return transportStatus(codes.InvalidArgument, "ACCOUNT_OWNER_EXIT_INVALID", "invalid account owner exit request")
	case errors.Is(e, d.ErrConflict):
		return transportStatus(codes.FailedPrecondition, "ACCOUNT_OWNER_EXIT_CONFLICT", "account owner exit conflict")
	case errors.Is(e, d.ErrNotFound):
		return transportStatus(codes.NotFound, "ACCOUNT_OWNER_EXIT_NOT_FOUND", "account owner exit not found")
	default:
		return transportStatus(codes.Unavailable, "ACCOUNT_OWNER_EXIT_UNAVAILABLE", "account owner exit unavailable")
	}
}
