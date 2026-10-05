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

// GRPCDeveloperApprovalChecker adapts Auth Developer Status v1 to the
// review capability's narrow suspension gate.
type GRPCDeveloperApprovalChecker struct {
	client developerstatusv1.DeveloperStatusDirectoryClient
}

func NewGRPCDeveloperApprovalChecker(connection *grpc.ClientConn) (*GRPCDeveloperApprovalChecker, error) {
	if connection == nil {
		return nil, errors.New("Auth Developer Status gRPC connection is required")
	}
	return &GRPCDeveloperApprovalChecker{
		client: developerstatusv1.NewDeveloperStatusDirectoryClient(connection),
	}, nil
}

func (checker *GRPCDeveloperApprovalChecker) BlocksApproval(
	ctx context.Context,
	currentAdminID, submittedBy shared.AuthID,
) (bool, error) {
	if checker == nil || checker.client == nil {
		return false, reviewport.ErrDeveloperStatusUnavailable
	}
	authIDs := []shared.AuthID{currentAdminID, submittedBy}
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
		account := entry.GetAccountStatus()
		developer := entry.GetDeveloperStatus()
		if account == developerstatusv1.AccountStatus_ACCOUNT_STATUS_CLOSED {
			if developer != developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_UNSPECIFIED {
				return false, reviewport.ErrDeveloperStatusUnavailable
			}
		} else {
			if account != developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE && account != developerstatusv1.AccountStatus_ACCOUNT_STATUS_DISABLED {
				return false, reviewport.ErrDeveloperStatusUnavailable
			}
			if developer < developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_PENDING || developer > developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_WITHDRAWN {
				return false, reviewport.ErrDeveloperStatusUnavailable
			}
		}
		if expectedAuthID == currentAdminID.String() {
			suspended = suspended || account != developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE || developer != developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED
		} else {
			suspended = suspended || account == developerstatusv1.AccountStatus_ACCOUNT_STATUS_DISABLED || developer == developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_SUSPENDED
		}
	}
	return suspended, nil
}

var _ reviewport.DeveloperApprovalChecker = (*GRPCDeveloperApprovalChecker)(nil)
