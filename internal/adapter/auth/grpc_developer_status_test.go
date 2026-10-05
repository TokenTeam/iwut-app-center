package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"google.golang.org/grpc"

	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	reviewport "iwut-app-center/internal/review/port"
)

func TestAuthDeveloperStatusV1_ConsumerContract(t *testing.T) {
	t.Parallel()
	if developerstatusv1.DeveloperStatusDirectory_BatchGetDeveloperStatuses_FullMethodName !=
		"/auth_center.v1.developer_status.DeveloperStatusDirectory/BatchGetDeveloperStatuses" {
		t.Fatalf("full method = %q", developerstatusv1.DeveloperStatusDirectory_BatchGetDeveloperStatuses_FullMethodName)
	}
	requestFields := (&developerstatusv1.BatchGetDeveloperStatusesRequest{}).ProtoReflect().Descriptor().Fields()
	if requestFields.Len() != 1 || requestFields.Get(0).Name() != "auth_ids" || requestFields.Get(0).Number() != 1 || !requestFields.Get(0).IsList() {
		t.Fatalf("request fields = %v", requestFields)
	}
	entryFields := (&developerstatusv1.DeveloperStatusEntry{}).ProtoReflect().Descriptor().Fields()
	if entryFields.Len() != 3 || entryFields.ByName("auth_id").Number() != 1 || entryFields.ByName("developer_status").Number() != 2 {
		t.Fatalf("entry fields = %v", entryFields)
	}
}

type fakeDeveloperStatusClient struct {
	response *developerstatusv1.BatchGetDeveloperStatusesResponse
	err      error
	authIDs  []string
}

func (client *fakeDeveloperStatusClient) BatchGetDeveloperStatuses(
	_ context.Context,
	request *developerstatusv1.BatchGetDeveloperStatusesRequest,
	_ ...grpc.CallOption,
) (*developerstatusv1.BatchGetDeveloperStatusesResponse, error) {
	client.authIDs = append([]string{}, request.GetAuthIds()...)
	return client.response, client.err
}

func TestGRPCDeveloperApprovalChecker_ContractProjection(t *testing.T) {
	t.Parallel()

	client := &fakeDeveloperStatusClient{response: &developerstatusv1.BatchGetDeveloperStatusesResponse{
		Entries: []*developerstatusv1.DeveloperStatusEntry{
			{AccountStatus: developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE, AuthId: "auth-admin", DeveloperStatus: developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED},
			{AccountStatus: developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE, AuthId: "auth-submitter", DeveloperStatus: developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_SUSPENDED},
		},
	}}
	checker := &GRPCDeveloperApprovalChecker{client: client}
	suspended, err := checker.BlocksApproval(t.Context(), "auth-admin", "auth-submitter")
	if err != nil || !suspended {
		t.Fatalf("AnySuspended() = (%t, %v), want true", suspended, err)
	}
	if !reflect.DeepEqual(client.authIDs, []string{"auth-admin", "auth-submitter"}) {
		t.Fatalf("request auth IDs = %v", client.authIDs)
	}
}

func TestGRPCDeveloperApprovalChecker_FailsClosedOnContractViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response *developerstatusv1.BatchGetDeveloperStatusesResponse
		err      error
	}{
		{name: "nil response"},
		{name: "missing entry", response: &developerstatusv1.BatchGetDeveloperStatusesResponse{}},
		{name: "nil entry", response: &developerstatusv1.BatchGetDeveloperStatusesResponse{Entries: []*developerstatusv1.DeveloperStatusEntry{nil}}},
		{name: "wrong auth ID", response: &developerstatusv1.BatchGetDeveloperStatusesResponse{Entries: []*developerstatusv1.DeveloperStatusEntry{{AccountStatus: developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE, AuthId: "other", DeveloperStatus: developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED}}}},
		{name: "unspecified", response: &developerstatusv1.BatchGetDeveloperStatusesResponse{Entries: []*developerstatusv1.DeveloperStatusEntry{{AccountStatus: developerstatusv1.AccountStatus_ACCOUNT_STATUS_ACTIVE, AuthId: "auth-admin"}}}},
		{name: "rpc unavailable", err: errors.New("unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checker := &GRPCDeveloperApprovalChecker{client: &fakeDeveloperStatusClient{response: test.response, err: test.err}}
			if suspended, err := checker.BlocksApproval(t.Context(), "auth-admin", "auth-admin"); suspended || !errors.Is(err, reviewport.ErrDeveloperStatusUnavailable) {
				t.Fatalf("AnySuspended() = (%t, %v), want unavailable", suspended, err)
			}
		})
	}
}
