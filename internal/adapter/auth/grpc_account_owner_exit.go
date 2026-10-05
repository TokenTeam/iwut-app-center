package auth

import (
	"context"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/account_owner_exit"
	"google.golang.org/grpc"
	d "iwut-app-center/internal/ownerexit/domain"
)

type AccountOwnerExitDecisions struct {
	client pb.AccountOwnerExitDecisionServiceClient
}

func NewAccountOwnerExitDecisions(c *grpc.ClientConn) *AccountOwnerExitDecisions {
	return &AccountOwnerExitDecisions{pb.NewAccountOwnerExitDecisionServiceClient(c)}
}
func (a *AccountOwnerExitDecisions) Get(ctx context.Context, k d.Key) (d.Status, error) {
	r, e := a.client.GetAccountOwnerExitDecision(ctx, &pb.GetAccountOwnerExitDecisionRequest{AuthId: k.AuthID, OperationId: k.OperationID})
	if e != nil {
		return d.Status{}, e
	}
	if r == nil || r.Decision < 1 || r.Decision > 3 || r.Purpose < 1 || r.Purpose > 2 {
		return d.Status{}, d.ErrUnavailable
	}
	return d.Status{Prepare: d.Prepare{Key: k, Purpose: d.Purpose(r.Purpose)}, ReceiptID: r.ReceiptId, Decision: d.Decision(r.Decision)}, nil
}
