package transport

import (
	"strings"
	"testing"

	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
)

func TestAPIContract_UCAPP023_BR_RUN_011_012_017_ResourceBoundary(t *testing.T) {
	method := runtimev1.File_app_center_v1_runtime_resolution_runtime_resolution_proto.Services().ByName("RuntimeResolutionService").Methods().ByName("ResolveLaunchTarget")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != ResolveLaunchTargetInternalPath || rule.GetBody() != "query" || ResolveLaunchTargetExternalPath != "/app-center/v1/applications/{application_id}/launch-target:resolve" || strings.TrimPrefix(ResolveLaunchTargetExternalPath, ServicePrefix) != rule.GetPost() {
		t.Fatal("HTTP/Gateway resource drift")
	}
	if ResolveLaunchTargetGRPCMethod != "/app_center.v1.runtime_resolution.RuntimeResolutionService/ResolveLaunchTarget" {
		t.Fatal("gRPC method literal drift")
	}
	if ResolveLaunchTargetGRPCMethod != runtimev1.RuntimeResolutionService_ResolveLaunchTarget_FullMethodName {
		t.Fatal("gRPC method drift")
	}
	for _, test := range []struct {
		message proto.Message
		fields  string
	}{
		{&runtimev1.ResolveLaunchTargetRequest{}, "application_id,query"},
		{&runtimev1.ResolveLaunchTargetQuery{}, "host_capabilities,host_rpc_api_major"},
		{&runtimev1.LaunchTargetDescriptor{}, "application_id,channel,launch_url,optional_scopes,publication_id,publication_revision,required_capabilities,required_scopes,rpc_api_major,rpc_api_max_version_exclusive,rpc_api_min_version,version_id,version_label"},
	} {
		if strings.Join(messageFieldNames(t, test.message), ",") != test.fields {
			t.Fatalf("field boundary drift for %T", test.message)
		}
	}
	for _, reason := range []string{ReasonInvalidAuthenticatedUser, ReasonInvalidApplicationID, ReasonInvalidHostRPCAPIMajor, ReasonInvalidHostCapabilities, ReasonApplicationNotFound, ReasonApplicationLaunchTargetUnavailable, ReasonApplicationRuntimeStateInconsistent, ReasonInternal, ReasonInvalidResolveLaunchTargetRequest} {
		if _, ok := runtimev1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing enum reason %s", reason)
		}
	}
}
