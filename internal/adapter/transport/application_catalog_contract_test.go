package transport

import (
	"strings"
	"testing"

	applicationcatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_catalog"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestAPIContract_UCAPP024_PublicCatalogBoundary(t *testing.T) {
	service := applicationcatalogv1.File_app_center_v1_application_catalog_application_catalog_proto.Services().ByName("ApplicationCatalogService")
	for _, test := range []struct {
		method, internalPath, externalPath, grpcMethod string
	}{
		{"ListPublicApplications", ListPublicApplicationsInternalPath, ListPublicApplicationsExternalPath, ListPublicApplicationsGRPCMethod},
		{"GetPublicApplication", GetPublicApplicationInternalPath, GetPublicApplicationExternalPath, GetPublicApplicationGRPCMethod},
	} {
		method := service.Methods().ByName(protoreflect.Name(test.method))
		rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
		if rule.GetPost() != test.internalPath || rule.GetBody() != "query" || test.externalPath != ServicePrefix+test.internalPath {
			t.Fatalf("HTTP/Gateway resource drift for %s", test.method)
		}
		if !strings.HasSuffix(test.grpcMethod, "/"+test.method) {
			t.Fatalf("gRPC method drift for %s: %s", test.method, test.grpcMethod)
		}
	}
	for _, test := range []struct {
		message proto.Message
		fields  string
	}{
		{&applicationcatalogv1.CatalogRuntimeQuery{}, "host_capabilities,host_rpc_api_major"},
		{&applicationcatalogv1.ListPublicApplicationsRequest{}, "query"},
		{&applicationcatalogv1.ListPublicApplicationsQuery{}, "page_size,page_token,runtime"},
		{&applicationcatalogv1.GetPublicApplicationRequest{}, "application_id,query"},
		{&applicationcatalogv1.PublicApplicationProfile{}, "description,display_name,icon,profile_revision_id"},
		{&applicationcatalogv1.PublicApplicationFilter{}, "filter_revision_id,mode,revision,rule,schema_version"},
		{&applicationcatalogv1.PublicApplicationCatalogItem{}, "application_id,filter,launch_target,profile"},
		{&applicationcatalogv1.PublicApplicationCatalogPage{}, "applications,next_page_token"},
	} {
		if strings.Join(messageFieldNames(t, test.message), ",") != test.fields {
			t.Fatalf("field boundary drift for %T", test.message)
		}
	}
	for _, reason := range []string{ReasonInvalidAuthenticatedUser, ReasonInvalidApplicationID, ReasonInvalidHostRPCAPIMajor, ReasonInvalidHostCapabilities, ReasonInvalidPageSize, ReasonInvalidPageToken, ReasonPublicApplicationNotFound, ReasonApplicationCatalogStateInconsistent, ReasonInternal, ReasonInvalidApplicationCatalogRequest} {
		if _, ok := applicationcatalogv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing enum reason %s", reason)
		}
	}
}
