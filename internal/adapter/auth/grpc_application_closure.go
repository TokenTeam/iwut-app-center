package auth

import (
	"context"
	"errors"
	"time"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/application_closure"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
)

type GRPCApplicationClosure struct {
	client pb.ApplicationClosureServiceClient
}

func NewGRPCApplicationClosure(connection *grpc.ClientConn) (*GRPCApplicationClosure, error) {
	if connection == nil {
		return nil, errors.New("Auth Application Closure gRPC connection is required")
	}
	return &GRPCApplicationClosure{client: pb.NewApplicationClosureServiceClient(connection)}, nil
}

func (a *GRPCApplicationClosure) Apply(ctx context.Context, closure domain.ApplicationClosure) (port.AuthApplicationClosureReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := a.client.ApplyApplicationClosure(ctx, &pb.ApplyApplicationClosureRequest{ApplicationId: closure.ApplicationID.String(), ClosureId: closure.ClosureID.String(), ClosingStartedAt: timestamppb.New(closure.ClosingStartedAt)})
	return closureReceipt(response, err, closure)
}

func (a *GRPCApplicationClosure) Get(ctx context.Context, closure domain.ApplicationClosure) (port.AuthApplicationClosureReceipt, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := a.client.GetApplicationClosureStatus(ctx, &pb.GetApplicationClosureStatusRequest{ApplicationId: closure.ApplicationID.String(), ClosureId: closure.ClosureID.String()})
	return closureReceipt(response, err, closure)
}

func closureReceipt(response *pb.ApplicationClosureStatus, err error, closure domain.ApplicationClosure) (port.AuthApplicationClosureReceipt, error) {
	if err != nil {
		switch status.Code(err) {
		case codes.NotFound:
			return port.AuthApplicationClosureReceipt{}, port.ErrAuthApplicationClosureNotFound
		case codes.AlreadyExists, codes.FailedPrecondition, codes.Aborted:
			return port.AuthApplicationClosureReceipt{}, port.ErrAuthApplicationClosureConflict
		}
		return port.AuthApplicationClosureReceipt{}, err
	}
	if response == nil || response.GetApplicationId() != closure.ApplicationID.String() || response.GetClosureId() != closure.ClosureID.String() || response.GetState() != pb.ApplicationClosureState_APPLICATION_CLOSURE_STATE_APPLIED || response.GetReceiptId() == "" || response.GetAppliedAt() == nil || !response.GetAppliedAt().IsValid() {
		return port.AuthApplicationClosureReceipt{}, domain.ErrApplicationClosureStateInconsistent
	}
	return port.AuthApplicationClosureReceipt{ReceiptID: response.GetReceiptId(), AppliedAt: response.GetAppliedAt().AsTime().UTC()}, nil
}
