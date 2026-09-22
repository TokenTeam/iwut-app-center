package transport

import (
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	testerjoinlinkv1 "iwut-app-center/api/gen/go/app_center/v1/tester_join_link"
)

func TestAPIContract_UCAPP008_BR_TST_002_004_ResourceAndSensitiveFields(t *testing.T) {
	descriptor := testerjoinlinkv1.File_app_center_v1_tester_join_link_tester_join_link_proto.Services().ByName("TesterJoinLink").Methods().ByName("CreateOrRotateTesterJoinLink")
	rule := proto.GetExtension(descriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != CreateOrRotateTesterJoinLinkInternalPath || rule.GetBody() != "command" {
		t.Fatalf("HTTP rule = %v", rule)
	}
	if CreateOrRotateTesterJoinLinkExternalPath != "/app-center/v1/applications/{application_id}/tester-join-links" || strings.TrimPrefix(CreateOrRotateTesterJoinLinkExternalPath, ServicePrefix) != rule.GetPost() {
		t.Fatal("gateway route mismatch")
	}
	if CreateOrRotateTesterJoinLinkGRPCMethod != "/app_center.v1.tester_join_link.TesterJoinLink/CreateOrRotateTesterJoinLink" {
		t.Fatal("gRPC route mismatch")
	}
	if CreateOrRotateTesterJoinLinkGRPCMethod != testerjoinlinkv1.TesterJoinLink_CreateOrRotateTesterJoinLink_FullMethodName {
		t.Fatal("generated gRPC route mismatch")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{}, "application_id,command"},
		{&testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{}, "expected_active_join_link_id"},
		{&testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse{}, "join_link,join_url,replaced_join_link_id"},
		{&testerjoinlinkv1.TesterJoinLinkResource{}, "application_id,created_at,created_by,join_link_id,status"},
	} {
		if fields := strings.Join(messageFieldNames(t, tc.message), ","); fields != tc.fields {
			t.Fatalf("fields=%s want=%s", fields, tc.fields)
		}
	}
}

func TestAPIContract_UCAPP008_BR_TST_005_ExpectedLinkPresence(t *testing.T) {
	for _, tc := range []struct {
		body    string
		present bool
	}{
		{`{}`, false}, {`{"expectedActiveJoinLinkId":null}`, false}, {`{"expectedActiveJoinLinkId":""}`, true}, {`{"expectedActiveJoinLinkId":"018f7777-7777-7777-8777-777777777777"}`, true},
	} {
		var command testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand
		if err := protojson.Unmarshal([]byte(tc.body), &command); err != nil {
			t.Fatal(err)
		}
		if (command.ExpectedActiveJoinLinkId != nil) != tc.present {
			t.Fatalf("presence wrong for %s", tc.body)
		}
	}
	for _, spec := range testerDomainErrorSpecs {
		if _, ok := testerjoinlinkv1.ErrorReason_value[spec.reason]; !ok {
			t.Fatalf("missing error reason %s", spec.reason)
		}
	}
}
