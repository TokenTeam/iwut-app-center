package transport

import (
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	testermembershipv1 "iwut-app-center/api/gen/go/app_center/v1/tester_membership"
	"strings"
	"testing"
)

func TestAPIContract_UCAPP010_BR_TST_021_023_026_028_Removal(t *testing.T) {
	method := testermembershipv1.File_app_center_v1_tester_membership_tester_membership_proto.Services().ByName("TesterMembership").Methods().ByName("RemoveApplicationTester")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetDelete() != RemoveApplicationTesterInternalPath || rule.GetBody() != "" || RemoveApplicationTesterExternalPath != "/app-center/v1/applications/{application_id}/tester-memberships/{membership_id}" || strings.TrimPrefix(RemoveApplicationTesterExternalPath, ServicePrefix) != rule.GetDelete() {
		t.Fatal("HTTP/Gateway route mismatch")
	}
	if RemoveApplicationTesterGRPCMethod != "/app_center.v1.tester_membership.TesterMembership/RemoveApplicationTester" {
		t.Fatal("gRPC route mismatch")
	}
	if RemoveApplicationTesterGRPCMethod != testermembershipv1.TesterMembership_RemoveApplicationTester_FullMethodName {
		t.Fatal("generated gRPC route mismatch")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&testermembershipv1.RemoveApplicationTesterRequest{}, "application_id,membership_id"},
		{&testermembershipv1.RemoveApplicationTesterResponse{}, "active_join_link_exists,capacity,membership,removed"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("unexpected fields for %T", tc.message)
		}
	}
	resource := (&testermembershipv1.TesterMembershipResource{}).ProtoReflect().Descriptor().Fields()
	if resource.ByName("removed_by").Number() != 7 || !resource.ByName("removed_by").HasPresence() || resource.ByName("removed_at").Number() != 8 || !resource.ByName("removed_at").HasPresence() {
		t.Fatal("audit field numbering/presence mismatch")
	}
	for _, reason := range []string{ReasonDeveloperIdentityRequired, ReasonInvalidDeveloperIdentity, ReasonDeveloperApprovalRequired, ReasonApplicationAdminRequired, ReasonInvalidApplicationID, ReasonInvalidTesterMembershipId, ReasonApplicationTesterMembershipNotFound, ReasonApplicationTesterStateInconsistent, ReasonInvalidRemoveTesterRequest, ReasonInternal} {
		if _, ok := testermembershipv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing reason %s", reason)
		}
	}
}
