package auth

import (
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	"testing"
)

func TestBR_APP_011_RoleAwareLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name             string
		ownerAccount     pb.AccountStatus
		ownerStatus      pb.DeveloperStatus
		submitAccount    pb.AccountStatus
		submitStatus     pb.DeveloperStatus
		blocked, invalid bool
	}{
		{"ordinary approval", 1, 2, 1, 2, false, false}, {"withdrawn historic", 1, 2, 1, 5, false, false}, {"closed historic", 1, 2, 3, 0, false, false}, {"disabled historic", 1, 2, 2, 2, true, false}, {"suspended historic", 1, 2, 1, 4, true, false}, {"withdrawn owner", 1, 5, 1, 2, true, false}, {"closed owner", 3, 0, 1, 2, true, false}, {"disabled owner", 2, 2, 1, 2, true, false}, {"missing account", 0, 2, 1, 2, false, true}, {"closed carrying developer", 1, 2, 3, 2, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeDeveloperStatusClient{response: &pb.BatchGetDeveloperStatusesResponse{Entries: []*pb.DeveloperStatusEntry{{AuthId: "owner", AccountStatus: tc.ownerAccount, DeveloperStatus: tc.ownerStatus}, {AuthId: "submitter", AccountStatus: tc.submitAccount, DeveloperStatus: tc.submitStatus}}}}
			c := &GRPCDeveloperApprovalChecker{client: client}
			blocked, e := c.BlocksApproval(t.Context(), "owner", "submitter")
			if (e != nil) != tc.invalid || blocked != tc.blocked {
				t.Fatalf("blocked=%v err=%v", blocked, e)
			}
		})
	}
	client := &fakeDeveloperStatusClient{response: &pb.BatchGetDeveloperStatusesResponse{Entries: []*pb.DeveloperStatusEntry{{AuthId: "same", AccountStatus: 3}}}}
	blocked, e := (&GRPCDeveloperApprovalChecker{client: client}).BlocksApproval(t.Context(), "same", "same")
	if e != nil || !blocked || len(client.authIDs) != 1 {
		t.Fatalf("same actor %v %v", blocked, e)
	}
}
