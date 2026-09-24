package transport

import (
	testerjoinlinkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"strings"
	"testing"
)

func TestAPIContract_UCAPP011_BR_TST_030_031_036_Revocation(t *testing.T) {
	method := testerjoinlinkv1.File_app_center_v1_tester_join_link_tester_join_link_proto.Services().ByName("TesterJoinLink").Methods().ByName("RevokeTesterJoinLink")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetDelete() != RevokeTesterJoinLinkInternalPath || rule.GetBody() != "" || RevokeTesterJoinLinkExternalPath != "/app-center/v1/applications/{application_id}/tester-join-links/{join_link_id}" || strings.TrimPrefix(RevokeTesterJoinLinkExternalPath, ServicePrefix) != rule.GetDelete() {
		t.Fatal("HTTP/Gateway route mismatch")
	}
	if RevokeTesterJoinLinkGRPCMethod != "/app_center.v1.tester_join_link.TesterJoinLink/RevokeTesterJoinLink" {
		t.Fatal("gRPC route mismatch")
	}
	if RevokeTesterJoinLinkGRPCMethod != testerjoinlinkv1.TesterJoinLink_RevokeTesterJoinLink_FullMethodName {
		t.Fatal("generated gRPC route mismatch")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&testerjoinlinkv1.RevokeTesterJoinLinkRequest{}, "application_id,join_link_id"},
		{&testerjoinlinkv1.RevokeTesterJoinLinkResponse{}, "join_link,revoked"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("unexpected fields for %T", tc.message)
		}
	}
	resource := (&testerjoinlinkv1.TesterJoinLinkRevocationResource{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"token_hash", "secret", "join_url"} {
		if resource.ByName(protoreflect.Name(name)) != nil {
			t.Fatal("credential field exposed")
		}
	}
	for _, name := range []string{"revoked_by", "revoked_at", "revocation_reason", "replaced_by_join_link_id"} {
		if !resource.ByName(protoreflect.Name(name)).HasPresence() {
			t.Fatal("audit presence required")
		}
	}
	for _, reason := range []string{ReasonDeveloperIdentityRequired, ReasonInvalidDeveloperIdentity, ReasonDeveloperApprovalRequired, ReasonApplicationAdminRequired, ReasonInvalidApplicationID, ReasonInvalidTesterJoinLinkId, ReasonApplicationTesterJoinLinkNotFound, ReasonApplicationTesterJoinLinkStateInconsistent, ReasonInvalidRevokeTesterJoinLinkRequest, ReasonInternal} {
		if _, ok := testerjoinlinkv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing reason %s", reason)
		}
	}
}
