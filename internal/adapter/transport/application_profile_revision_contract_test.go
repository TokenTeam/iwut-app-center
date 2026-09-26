package transport

import (
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"github.com/go-kratos/kratos/v2/transport/http/binding"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func TestAPIContract_UCAPP013_BR_PRF_001_005_007_ResourceAndFields(t *testing.T) {
	method := profilev1.File_app_center_v1_application_profile_revision_application_profile_revision_proto.Services().ByName("ApplicationProfileRevision").Methods().ByName("CreateApplicationProfileRevision")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != CreateApplicationProfileRevisionInternalPath || rule.GetBody() != "profile" || CreateApplicationProfileRevisionExternalPath != "/app-center/v1/applications/{application_id}/profile-revisions" || strings.TrimPrefix(CreateApplicationProfileRevisionExternalPath, ServicePrefix) != rule.GetPost() {
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
		{&profilev1.CreateApplicationProfileRevisionRequest{}, "application_id,profile"},
		{&profilev1.CreateApplicationProfileRevisionResponse{}, "application_id,created_at,created_by,description,display_name,icon,profile_revision_id,review_status,revision,sequence,updated_at,updated_by"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("field drift %T", tc.message)
		}
	}
	for _, name := range []string{"description", "icon"} {
		field := method.Input().Fields().ByName("profile").Message().Fields().ByJSONName(name)
		if string(field.Message().FullName()) != "google.protobuf.Value" {
			t.Fatal("nullable presence contract drift")
		}
	}
	for _, spec := range profileDomainErrorSpecs {
		_, revisionOK := profilev1.ErrorReason_value[spec.reason]
		_, reviewOK := reviewv1.ErrorReason_value[spec.reason]
		if !revisionOK && !reviewOK {
			t.Fatalf("missing reason %s", spec.reason)
		}
	}
	for _, reason := range []string{ReasonInvalidCreateApplicationProfileRevisionRequest, ReasonInvalidDeveloperIdentity} {
		if _, ok := profilev1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing reason %s", reason)
		}
	}
}

func TestAPIContract_UCAPP014_BR_PRF_009_010_011_ResourceAndFields(t *testing.T) {
	method := profilev1.File_app_center_v1_application_profile_revision_application_profile_revision_proto.Services().ByName("ApplicationProfileRevision").Methods().ByName("UpdateApplicationProfileRevision")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPut() != UpdateApplicationProfileRevisionInternalPath || rule.GetBody() != "profile" || UpdateApplicationProfileRevisionExternalPath != "/app-center/v1/applications/{application_id}/profile-revisions/{profile_revision_id}" || strings.TrimPrefix(UpdateApplicationProfileRevisionExternalPath, ServicePrefix) != rule.GetPut() {
		t.Fatal("HTTP/Gateway update route drift")
	}
	if UpdateApplicationProfileRevisionGRPCMethod != "/app_center.v1.application_profile_revision.ApplicationProfileRevision/UpdateApplicationProfileRevision" {
		t.Fatal("gRPC update route drift")
	}
	if UpdateApplicationProfileRevisionGRPCMethod != profilev1.ApplicationProfileRevision_UpdateApplicationProfileRevision_FullMethodName {
		t.Fatal("generated gRPC update route drift")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&profilev1.UpdateApplicationProfileRevisionRequest{}, "application_id,expected_revision,profile,profile_revision_id"},
		{&profilev1.UpdateApplicationProfileRevisionResponse{}, "application_id,created_at,created_by,description,display_name,icon,profile_revision_id,review_status,revision,sequence,updated_at,updated_by"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("field drift %T", tc.message)
		}
	}
	for _, name := range []string{"description", "icon"} {
		if string(method.Input().Fields().ByName("profile").Message().Fields().ByJSONName(name).Message().FullName()) != "google.protobuf.Value" {
			t.Fatal("update nullable presence drift")
		}
	}
	if _, ok := profilev1.ErrorReason_value[ReasonInvalidUpdateApplicationProfileRevisionRequest]; !ok {
		t.Fatal("missing update reason")
	}
}

func TestAPIContract_ProfileGeneratedHTTPPathBindings(t *testing.T) {
	create := &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: testApplicationID}
	update := &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID}
	if got := binding.EncodeURL(CreateApplicationProfileRevisionInternalPath, create, false); got != "/v1/applications/"+testApplicationID+"/profile-revisions" {
		t.Fatalf("create path=%s", got)
	}
	if got := binding.EncodeURL(UpdateApplicationProfileRevisionInternalPath, update, false); got != "/v1/applications/"+testApplicationID+"/profile-revisions/"+testProfileRevisionID {
		t.Fatalf("update path=%s", got)
	}
}
