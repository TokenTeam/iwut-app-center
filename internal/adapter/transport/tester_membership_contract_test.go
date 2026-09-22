package transport

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	testermembershipv1 "iwut-app-center/api/gen/go/app_center/v1/tester_membership"
	"strings"
	"testing"
)

func TestAPIContract_UCAPP009_BR_TST_010_012_019_ResourceAndSensitiveBoundary(t *testing.T) {
	method := testermembershipv1.File_app_center_v1_tester_membership_tester_membership_proto.Services().ByName("TesterMembership").Methods().ByName("JoinApplicationAsTester")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != JoinApplicationAsTesterInternalPath || rule.GetBody() != "command" || JoinApplicationAsTesterExternalPath != "/app-center/v1/tester-join-links/{join_link_id}/memberships" || strings.TrimPrefix(JoinApplicationAsTesterExternalPath, ServicePrefix) != rule.GetPost() {
		t.Fatal("HTTP/Gateway route mismatch")
	}
	if JoinApplicationAsTesterGRPCMethod != "/app_center.v1.tester_membership.TesterMembership/JoinApplicationAsTester" {
		t.Fatal("gRPC route mismatch")
	}
	if JoinApplicationAsTesterGRPCMethod != testermembershipv1.TesterMembership_JoinApplicationAsTester_FullMethodName {
		t.Fatal("generated gRPC route mismatch")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&testermembershipv1.JoinApplicationAsTesterRequest{}, "command,join_link_id"},
		{&testermembershipv1.JoinApplicationAsTesterCommand{}, "secret"},
		{&testermembershipv1.JoinApplicationAsTesterResponse{}, "capacity,joined,membership"},
		{&testermembershipv1.TesterMembershipResource{}, "application_id,joined_at,joined_via_join_link_id,membership_id,removed_at,removed_by,status,tester_auth_id"},
		{&testermembershipv1.TesterCapacity{}, "active_tester_count,tester_limit"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("unexpected fields for %T", tc.message)
		}
	}
	for _, reason := range []string{ReasonAuthenticatedUserRequired, ReasonInvalidAuthenticatedUser, ReasonInvalidTesterJoinLinkId, ReasonInvalidTesterJoinSecret, ReasonTesterJoinLinkInvalid, ReasonApplicationTesterLimitReached, ReasonInternal} {
		if _, ok := testermembershipv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing reason %s", reason)
		}
	}
}
