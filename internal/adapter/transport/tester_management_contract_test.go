package transport

import (
	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_management"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBR_TST_040_043_TesterManagementAPIContract(t *testing.T) {
	service := v1.File_app_center_v1_tester_management_tester_management_proto.Services().ByName("ApplicationTesterManagementService")
	for _, tc := range []struct{ method, path, body string }{{"GetApplicationTesterManagement", "/v1/applications/{application_id}/tester-management", ""}, {"ListApplicationTesterMemberships", "/v1/applications/{application_id}/tester-memberships:search", "query"}, {"ListApplicationTesterJoinLinks", "/v1/applications/{application_id}/tester-join-links:search", "query"}} {
		m := service.Methods().ByName(protoreflect.Name(tc.method))
		rule := proto.GetExtension(m.Options(), annotations.E_Http).(*annotations.HttpRule)
		path := rule.GetGet()
		if path == "" {
			path = rule.GetPost()
		}
		if path != tc.path || rule.Body != tc.body {
			t.Fatal("HTTP contract drift")
		}
		external := ServicePrefix + path
		if strings.TrimPrefix(external, ServicePrefix) != tc.path {
			t.Fatal("prefix mapping")
		}
		if m.Input().Fields().ByName("application_id").JSONName() != "application_id" {
			t.Fatal("generated client path binding")
		}
	}
	messages := []protoreflect.MessageDescriptor{(&v1.ManagedTesterMembership{}).ProtoReflect().Descriptor(), (&v1.ManagedTesterJoinLink{}).ProtoReflect().Descriptor(), (&v1.MembershipPage{}).ProtoReflect().Descriptor(), (&v1.JoinLinkPage{}).ProtoReflect().Descriptor()}
	for _, m := range messages {
		for _, forbidden := range []string{"secret", "token_hash", "join_url", "created_by", "revoked_by", "removed_by", "total_count", "profile", "email", "name"} {
			if m.Fields().ByName(protoreflect.Name(forbidden)) != nil {
				t.Fatal("private field", forbidden)
			}
		}
	}
}
func TestBR_TST_043_TesterManagementStrictHTTPInput(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		want               bool
	}{{"GET", "/v1/applications/a/tester-management", "", true}, {"GET", "/v1/applications/a/tester-management?application_id=other", "", false}, {"GET", "/v1/applications/a/tester-management", "{}", false}, {"POST", "/v1/applications/a/tester-memberships:search", "{}", true}, {"POST", "/v1/applications/a/tester-memberships:search?status=ACTIVE", "{}", false}, {"POST", "/v1/applications/a/tester-memberships:search", `{"application_id":"other"}`, false}, {"POST", "/v1/applications/a/tester-join-links:search", `{"query":{}}`, false}, {"POST", "/v1/applications/a/tester-join-links:search", `{"testerAuthId":"user"}`, false}, {"POST", "/v1/applications/a/tester-join-links:search", `{"pageSize":1,"page_size":2}`, false}, {"POST", "/v1/applications/a/tester-join-links:search", `null`, false}, {"POST", "/v1/applications/a/tester-join-links:search", `{"pageSize":100,"status":"JOIN_LINK_STATUS_ALL"}`, true}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		if tc.body == "" {
			r.Body = http.NoBody
			r.ContentLength = 0
		}
		if !isTesterManagementRequest(r) || validTesterManagementHTTPInput(r) != tc.want {
			t.Fatal("HTTP input", tc)
		}
	}
}
