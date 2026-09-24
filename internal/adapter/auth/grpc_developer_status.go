package auth

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"

	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

// GRPCDeveloperSuspensionChecker adapts Auth Developer Status v1 to the
// review capability's narrow suspension gate.
type GRPCDeveloperSuspensionChecker struct {
	client developerstatusv1.DeveloperStatusDirectoryClient
}

func NewGRPCDeveloperSuspensionChecker(connection *grpc.ClientConn) (*GRPCDeveloperSuspensionChecker, error) {
	if connection == nil {
		return nil, errors.New("Auth Developer Status gRPC connection is required")
	}
	return &GRPCDeveloperSuspensionChecker{
		client: developerstatusv1.NewDeveloperStatusDirectoryClient(connection),
	}, nil
}

func (checker *GRPCDeveloperSuspensionChecker) AnySuspended(
	ctx context.Context,
	authIDs []shared.AuthID,
) (bool, error) {
	if checker == nil || checker.client == nil {
		return false, reviewport.ErrDeveloperStatusUnavailable
	}
	values := make([]string, 0, len(authIDs))
	seen := make(map[shared.AuthID]struct{}, len(authIDs))
	for _, authID := range authIDs {
		if !authID.IsValid() {
			return false, fmt.Errorf("%w: invalid Auth ID", reviewport.ErrDeveloperStatusUnavailable)
		}
		if _, duplicate := seen[authID]; duplicate {
			continue
		}
		seen[authID] = struct{}{}
		values = append(values, authID.String())
	}
	if len(values) == 0 || len(values) > 100 {
		return false, fmt.Errorf("%w: invalid batch size", reviewport.ErrDeveloperStatusUnavailable)
	}

	response, err := checker.client.BatchGetDeveloperStatuses(
		ctx,
		&developerstatusv1.BatchGetDeveloperStatusesRequest{AuthIds: values},
	)
	if err != nil {
		return false, fmt.Errorf("%w: query Auth Developer Status: %w", reviewport.ErrDeveloperStatusUnavailable, err)
	}
	if response == nil || len(response.GetEntries()) != len(values) {
		return false, fmt.Errorf("%w: incomplete Auth Developer Status response", reviewport.ErrDeveloperStatusUnavailable)
	}

	suspended := false
	for index, expectedAuthID := range values {
		entry := response.GetEntries()[index]
		if entry == nil || entry.GetAuthId() != expectedAuthID {
			return false, fmt.Errorf("%w: misordered Auth Developer Status response", reviewport.ErrDeveloperStatusUnavailable)
		}
		switch entry.GetDeveloperStatus() {
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_PENDING,
			developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED,
			developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_REJECTED:
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_SUSPENDED:
			suspended = true
		default:
			return false, fmt.Errorf("%w: invalid Auth Developer Status value", reviewport.ErrDeveloperStatusUnavailable)
		}
	}
	return suspended, nil
}

var _ reviewport.DeveloperSuspensionChecker = (*GRPCDeveloperSuspensionChecker)(nil)
