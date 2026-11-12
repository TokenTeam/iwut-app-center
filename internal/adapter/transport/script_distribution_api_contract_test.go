package transport

import (
	"regexp"
	"strings"
	"testing"

	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_distribution"
	"github.com/go-kratos/kratos/v2/transport/http/binding"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
)

// Test the actual framework encoder against URI templates. Proto default JSON
// names otherwise leave snake_case path parameters empty and leak them to query.
func TestScriptHTTPClientURIParameters_UCAPP041042043044045046047(t *testing.T) {
	parameter := regexp.MustCompile(`\{([a-z_]+)\}`)
	for _, name := range []string{"application_publication.ApplicationPublication", "script_authoring.ScriptAuthoringService", "script_review.ScriptReviewService", "script_query.ScriptQueryService", "script_publication.ScriptPublicationService", "script_distribution.ScriptDistributionService"} {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName("app_center.v1." + name))
		if err != nil {
			t.Fatal(err)
		}
		service := descriptor.(protoreflect.ServiceDescriptor)
		for i := 0; i < service.Methods().Len(); i++ {
			method := service.Methods().Get(i)
			rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
			path := rule.GetGet()
			if path == "" {
				path = rule.GetPost()
			}
			if path == "" {
				path = rule.GetPut()
			}
			if path == "" {
				path = rule.GetDelete()
			}
			if path == "" {
				t.Fatal("missing HTTP pattern", method.FullName())
			}
			request := dynamicpb.NewMessage(method.Input())
			expected := path
			for _, match := range parameter.FindAllStringSubmatch(path, -1) {
				field := method.Input().Fields().ByName(protoreflect.Name(match[1]))
				if field == nil {
					t.Fatal("missing path field", match[1])
				}
				value := "01940000-0000-7000-8000-000000000001"
				if field.Kind() == protoreflect.Int32Kind {
					value = "1"
					request.Set(field, protoreflect.ValueOfInt32(1))
				} else {
					request.Set(field, protoreflect.ValueOfString(value))
				}
				expected = strings.ReplaceAll(expected, match[0], value)
			}
			if got := binding.EncodeURL(path, request, false); got != expected {
				t.Fatalf("%s encoded %q, want %q", method.FullName(), got, expected)
			}
		}
	}
	request := &v1.GetPublishedScriptContentRequest{ResourceId: "resource", VersionId: "version", ExpectedDigest: "digest"}
	if got := binding.EncodeURL("/v1/scripts/{resource_id}/versions/{version_id}/content", request, true); got != "/v1/scripts/resource/versions/version/content?expectedDigest=digest" {
		t.Fatal("unexpected query encoding", got)
	}
}
