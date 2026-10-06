package auth

import (
	"context"
	"errors"

	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type GRPCDeveloperLifecycleDirectory struct {
	client developerstatusv1.DeveloperStatusDirectoryClient
}

func NewGRPCDeveloperLifecycleDirectory(connection *grpc.ClientConn) (*GRPCDeveloperLifecycleDirectory, error) {
	if connection == nil {
		return nil, errors.New("Auth Developer Status gRPC connection is required")
	}
	return &GRPCDeveloperLifecycleDirectory{client: developerstatusv1.NewDeveloperStatusDirectoryClient(connection)}, nil
}

func (directory *GRPCDeveloperLifecycleDirectory) GetFresh(ctx context.Context, authIDs []shared.AuthID) ([]port.DeveloperLifecycle, error) {
	if directory == nil || directory.client == nil || len(authIDs) < 1 || len(authIDs) > 100 {
		return nil, portUnavailable()
	}
	values := make([]string, len(authIDs))
	seen := map[shared.AuthID]struct{}{}
	for index, authID := range authIDs {
		if !authID.IsValid() {
			return nil, portUnavailable()
		}
		if _, exists := seen[authID]; exists {
			return nil, portUnavailable()
		}
		seen[authID] = struct{}{}
		values[index] = authID.String()
	}
	response, err := directory.client.BatchGetDeveloperStatuses(ctx, &developerstatusv1.BatchGetDeveloperStatusesRequest{AuthIds: values})
	if status.Code(err) == codes.NotFound {
		return nil, port.ErrDeveloperLifecycleNotFound
	}
	if err != nil || response == nil || len(response.GetEntries()) != len(values) {
		return nil, portUnavailable()
	}
	result := make([]port.DeveloperLifecycle, len(values))
	for index, expected := range values {
		entry := response.GetEntries()[index]
		if entry == nil || entry.GetAuthId() != expected {
			return nil, portUnavailable()
		}
		account, developer := entry.GetAccountStatus(), entry.GetDeveloperStatus()
		if account == developerstatusv1.AccountStatus_ACCOUNT_STATUS_CLOSED {
			if developer != developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_UNSPECIFIED {
				return nil, portUnavailable()
			}
			result[index] = port.DeveloperLifecycle{AuthID: authIDs[index]}
			continue
		}
		if account != developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE && account != developerstatusv1.AccountStatus_ACCOUNT_STATUS_DISABLED || developer < developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_PENDING || developer > developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_WITHDRAWN {
			return nil, portUnavailable()
		}
		var status shared.DeveloperStatus
		switch developer {
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_PENDING:
			status = shared.DeveloperStatusPending
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED:
			status = shared.DeveloperStatusApproved
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_REJECTED:
			status = shared.DeveloperStatusRejected
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_SUSPENDED:
			status = shared.DeveloperStatusSuspended
		case developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_WITHDRAWN:
			status = shared.DeveloperStatusWithdrawn
		default:
			return nil, portUnavailable()
		}
		result[index] = port.DeveloperLifecycle{AuthID: authIDs[index], AccountActive: account == developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE, DeveloperStatus: status}
	}
	return result, nil
}

func portUnavailable() error { return errors.New("developer status unavailable") }

var _ port.DeveloperLifecycleDirectory = (*GRPCDeveloperLifecycleDirectory)(nil)
