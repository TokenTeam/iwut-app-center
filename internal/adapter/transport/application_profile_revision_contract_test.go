package transport

import (
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func TestAPIContract_UCAPP013_BR_PRF_001_005_007_ResourceAndFields(t *testing.T) {
	method := profilev1.File_app_center_v1_application_profile_revision_application_profile_revision_proto.Services().ByName("ApplicationProfileRevision").Methods().ByName("CreateApplicationProfileRevision")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != CreateApplicationProfileRevisionInternalPath || rule.GetBody() != "*" || CreateApplicationProfileRevisionExternalPath != "/app-center/v1/applications/{application_id}/profile-revisions" || strings.TrimPrefix(CreateApplicationProfileRevisionExternalPath, ServicePrefix) != rule.GetPost() {
		t.Fatal("HTTP/Gateway route drift")
	}
	if CreateApplicationProfileRevisionGRPCMethod != "/app_center.v1.application_profile_revision.ApplicationProfileRevision/CreateApplicationProfileRevision" {
		t.Fatal("gRPC route drift")
	}
	if CreateApplicationProfileRevisionGRPCMethod != profilev1.ApplicationProfileRevision_CreateApplicationProfileRevision_FullMethodName {
		t.Fatal("generated gRPC route drift")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&profilev1.CreateApplicationProfileRevisionRequest{}, "application_id,description,display_name,icon"},
		{&profilev1.CreateApplicationProfileRevisionResponse{}, "application_id,created_at,created_by,description,display_name,icon,profile_revision_id,review_status,revision,sequence,updated_at,updated_by"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("field drift %T", tc.message)
		}
	}
	for _, name := range []string{"description", "icon"} {
		field := method.Input().Fields().ByJSONName(name)
		if string(field.Message().FullName()) != "google.protobuf.Value" {
			t.Fatal("nullable presence contract drift")
		}
	}
	for _, spec := range profileDomainErrorSpecs {
		if _, ok := profilev1.ErrorReason_value[spec.reason]; !ok {
			t.Fatalf("missing reason %s", spec.reason)
		}
	}
	for _, reason := range []string{ReasonInvalidCreateApplicationProfileRevisionRequest, ReasonInvalidDeveloperIdentity} {
		if _, ok := profilev1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing reason %s", reason)
		}
	}
}
