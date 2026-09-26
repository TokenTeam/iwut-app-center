package transport

import (
	"strings"
	"testing"

	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
)

func TestAPIContract_UCAPP012_BR_RUN_001_006_009_ResourceAndPrivateFields(t *testing.T) {
	method := catalogv1.File_app_center_v1_catalog_catalog_proto.Services().ByName("Catalog").Methods().ByName("ResolveTestLaunchTarget")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != ResolveTestLaunchTargetInternalPath || rule.GetBody() != "query" || ResolveTestLaunchTargetExternalPath != "/app-center/v1/applications/{application_id}/test-launch:resolve" || strings.TrimPrefix(ResolveTestLaunchTargetExternalPath, ServicePrefix) != rule.GetPost() {
		t.Fatal("HTTP/Gateway resource drift")
	}
	if ResolveTestLaunchTargetGRPCMethod != "/app_center.v1.catalog.Catalog/ResolveTestLaunchTarget" {
		t.Fatal("gRPC method literal drift")
	}
	if ResolveTestLaunchTargetGRPCMethod != catalogv1.Catalog_ResolveTestLaunchTarget_FullMethodName {
		t.Fatal("gRPC method drift")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{
		{&catalogv1.ResolveTestLaunchTargetRequest{}, "application_id,query"},
		{&catalogv1.ResolveTestLaunchTargetQuery{}, "host_capabilities,host_rpc_api_major"},
		{&catalogv1.TestLaunchDescriptor{}, "application_id,launch_url,optional_scopes,publication_id,publication_revision,required_capabilities,required_scopes,rpc_api_major,rpc_api_max_version_exclusive,rpc_api_min_version,version_id,version_label"},
	} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("field boundary drift for %T", tc.message)
		}
	}
	for _, reason := range []string{ReasonAuthenticatedUserRequired, ReasonInvalidAuthenticatedUser, ReasonInvalidApplicationID, ReasonInvalidHostRPCAPIMajor, ReasonInvalidHostCapabilities, ReasonApplicationNotFound, ReasonApplicationTesterRequired, ReasonApplicationTestTargetUnavailable, ReasonHostCapabilitiesInsufficient, ReasonApplicationTestPublicationInconsistent, ReasonInternal, ReasonInvalidResolveTestLaunchRequest} {
		if _, ok := catalogv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing enum reason %s", reason)
		}
	}
}
