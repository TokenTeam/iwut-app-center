package transport

import (
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
)

func TestAPIContract_UCAPP007_BR_PUB_002_ResourceRouteAndBody(t *testing.T) {
	descriptor := publicationv1.File_app_center_v1_application_publication_application_publication_proto.Services().ByName("ApplicationPublication").Methods().ByName("PlaceApprovedVersionInTestSlot")
	rule := proto.GetExtension(descriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPut() != PlaceApprovedVersionInTestSlotInternalPath || rule.GetBody() != "command" {
		t.Fatalf("HTTP rule = %v", rule)
	}
	if PlaceApprovedVersionInTestSlotExternalPath != "/app-center/v1/applications/{application_id}/publications/{rpc_api_major}/test-slot" || strings.TrimPrefix(PlaceApprovedVersionInTestSlotExternalPath, ServicePrefix) != rule.GetPut() {
		t.Fatal("gateway route mismatch")
	}
	if PlaceApprovedVersionInTestSlotGRPCMethod != "/app_center.v1.application_publication.ApplicationPublication/PlaceApprovedVersionInTestSlot" {
		t.Fatal("gRPC method mismatch")
	}
	if PlaceApprovedVersionInTestSlotGRPCMethod != publicationv1.ApplicationPublication_PlaceApprovedVersionInTestSlot_FullMethodName {
		t.Fatal("generated gRPC mismatch")
	}
	if fields := messageFieldNames(t, &publicationv1.PlaceApprovedVersionInTestSlotRequest{}); strings.Join(fields, ",") != "application_id,command,rpc_api_major" {
		t.Fatalf("request fields = %v", fields)
	}
	if fields := messageFieldNames(t, &publicationv1.PlaceApprovedVersionInTestSlotCommand{}); strings.Join(fields, ",") != "expected_publication_revision,version_id" {
		t.Fatalf("command fields = %v", fields)
	}
}

func TestAPIContract_UCAPP007_BR_PUB_006_ExpectedRevisionPresence(t *testing.T) {
	for _, tc := range []struct {
		body    string
		present bool
		value   int64
	}{
		{`{"versionId":"v"}`, false, 0},
		{`{"versionId":"v","expectedPublicationRevision":null}`, false, 0},
		{`{"versionId":"v","expectedPublicationRevision":"0"}`, true, 0},
		{`{"versionId":"v","expectedPublicationRevision":"1"}`, true, 1},
	} {
		var command publicationv1.PlaceApprovedVersionInTestSlotCommand
		if err := protojson.Unmarshal([]byte(tc.body), &command); err != nil {
			t.Fatal(err)
		}
		if (command.ExpectedPublicationRevision != nil) != tc.present || command.GetExpectedPublicationRevision() != tc.value {
			t.Fatalf("presence/value for %s = %v", tc.body, &command)
		}
	}
}

func TestAPIContract_UCAPP007_ErrorReasonsMatchGeneratedEnum(t *testing.T) {
	for _, spec := range publicationDomainErrorSpecs {
		if _, ok := publicationv1.ErrorReason_value[spec.reason]; !ok {
			t.Fatalf("missing error reason %s", spec.reason)
		}
	}
	if _, ok := publicationv1.ErrorReason_value[ReasonInvalidDeveloperIdentity]; !ok {
		t.Fatal("missing invalid identity reason")
	}
}

func TestAPIContract_UCAPP020_StableRoutesPresenceAndClearQuery(t *testing.T) {
	service := publicationv1.File_app_center_v1_application_publication_application_publication_proto.Services().ByName("ApplicationPublication")
	setDescriptor := service.Methods().ByName("SetApprovedVersionInStableSlot")
	setRule := proto.GetExtension(setDescriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if setRule.GetPut() != SetApprovedVersionInStableSlotInternalPath || setRule.GetBody() != "command" || SetApprovedVersionInStableSlotGRPCMethod != publicationv1.ApplicationPublication_SetApprovedVersionInStableSlot_FullMethodName || strings.TrimPrefix(SetApprovedVersionInStableSlotExternalPath, ServicePrefix) != setRule.GetPut() {
		t.Fatalf("stable set contract=%v", setRule)
	}
	clearDescriptor := service.Methods().ByName("ClearStableSlot")
	clearRule := proto.GetExtension(clearDescriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if clearRule.GetDelete() != ClearStableSlotInternalPath || clearRule.GetBody() != "" || ClearStableSlotGRPCMethod != publicationv1.ApplicationPublication_ClearStableSlot_FullMethodName || strings.TrimPrefix(ClearStableSlotExternalPath, ServicePrefix) != clearRule.GetDelete() {
		t.Fatalf("stable clear contract=%v", clearRule)
	}
	if fields := messageFieldNames(t, &publicationv1.ClearStableSlotRequest{}); strings.Join(fields, ",") != "application_id,expected_publication_revision,rpc_api_major" {
		t.Fatalf("clear fields=%v", fields)
	}
	resource := &publicationv1.ApplicationPublicationResource{}
	if resource.TestVersionId != nil || resource.StableVersionId != nil {
		t.Fatal("empty slot presence was lost")
	}
	value := "01890f47-0000-7000-8000-000000000020"
	resource.StableVersionId = &value
	if resource.GetStableVersionId() != value || resource.TestVersionId != nil {
		t.Fatalf("stable-only resource=%#v", resource)
	}
}

func TestAPIContract_UCAPP021_GreyRoutesAndResources(t *testing.T) {
	service := publicationv1.File_app_center_v1_application_publication_application_publication_proto.Services().ByName("ApplicationPublication")
	setDescriptor := service.Methods().ByName("SetGreyRollout")
	setRule := proto.GetExtension(setDescriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if setRule.GetPut() != SetGreyRolloutInternalPath || setRule.GetBody() != "command" || SetGreyRolloutGRPCMethod != publicationv1.ApplicationPublication_SetGreyRollout_FullMethodName || strings.TrimPrefix(SetGreyRolloutExternalPath, ServicePrefix) != setRule.GetPut() {
		t.Fatalf("grey set contract=%v", setRule)
	}
	clearDescriptor := service.Methods().ByName("ClearGreyRollout")
	clearRule := proto.GetExtension(clearDescriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if clearRule.GetDelete() != ClearGreyRolloutInternalPath || clearRule.GetBody() != "" || ClearGreyRolloutGRPCMethod != publicationv1.ApplicationPublication_ClearGreyRollout_FullMethodName || strings.TrimPrefix(ClearGreyRolloutExternalPath, ServicePrefix) != clearRule.GetDelete() {
		t.Fatalf("grey clear contract=%v", clearRule)
	}
	if fields := messageFieldNames(t, &publicationv1.SetGreyRolloutCommand{}); strings.Join(fields, ",") != "expected_publication_revision,exposure_basis_points,version_id" {
		t.Fatalf("set fields=%v", fields)
	}
	if fields := messageFieldNames(t, &publicationv1.ClearGreyRolloutRequest{}); strings.Join(fields, ",") != "application_id,expected_publication_revision,rpc_api_major" {
		t.Fatalf("clear fields=%v", fields)
	}
	resource := &publicationv1.ApplicationPublicationResource{GreyRollout: &publicationv1.GreyRolloutResource{RolloutId: "r", VersionId: "v", ExposureBasisPoints: 500}}
	if resource.GetGreyRollout().GetExposureBasisPoints() != 500 {
		t.Fatalf("resource=%#v", resource)
	}
}
