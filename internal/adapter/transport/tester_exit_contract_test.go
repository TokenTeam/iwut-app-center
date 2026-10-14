package transport

import (
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func TestAPIContractUCAPP040BRTST044045050MinimalExit(t *testing.T) {
	method := pb.File_app_center_v1_tester_membership_tester_membership_proto.Services().ByName("TesterMembership").Methods().ByName("LeaveApplicationAsTester")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != LeaveApplicationAsTesterInternalPath || rule.GetBody() != "" || LeaveApplicationAsTesterExternalPath != "/app-center/v1/applications/{application_id}/tester-memberships/{membership_id}:leave" || strings.TrimPrefix(LeaveApplicationAsTesterExternalPath, ServicePrefix) != rule.GetPost() || LeaveApplicationAsTesterGRPCMethod != pb.TesterMembership_LeaveApplicationAsTester_FullMethodName {
		t.Fatal("route contract")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&pb.LeaveApplicationAsTesterRequest{}, "application_id,membership_id"},
		{&pb.LeaveApplicationAsTesterResponse{}, "left,membership"},
		{&pb.TesterExitMembership{}, "application_id,joined_at,membership_id,removed_at,status"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("fields for %T", tc.message)
		}
	}
	for _, reason := range []string{ReasonUserIdentityRequired, ReasonInvalidUserIdentity, ReasonInvalidApplicationID, ReasonInvalidTesterMembershipId, ReasonApplicationTesterMembershipNotFound, ReasonApplicationTesterStateInconsistent, "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", "ERROR_REASON_APPLICATION_TESTER_EXIT_UNAVAILABLE"} {
		if _, ok := pb.ErrorReason_value[reason]; !ok {
			t.Fatal(reason)
		}
	}
}
