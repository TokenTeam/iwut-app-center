package transport

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
)

const (
	protoPackage       = "app_center.v1.application"
	protoService       = "Application"
	protoRPC           = "CreateApplication"
	gatewayServiceName = "app-center"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate contract test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func mustFind(t *testing.T, pattern *regexp.Regexp, text string) string {
	t.Helper()
	match := pattern.FindStringSubmatch(text)
	if match == nil || len(match) < 2 {
		t.Fatalf("pattern %q not found", pattern.String())
	}
	return match[1]
}

func messageFieldNames(t *testing.T, message proto.Message) []string {
	t.Helper()
	fields := message.ProtoReflect().Descriptor().Fields()
	names := make([]string, 0, fields.Len())
	for index := 0; index < fields.Len(); index++ {
		names = append(names, string(fields.Get(index).Name()))
	}
	sort.Strings(names)
	return names
}

func containsField(message proto.Message, name string) bool {
	return message.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(name)) != nil
}

func TestAPIContract_ProtoAndGeneratedRoutesAgree(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	protoPath := filepath.Join(root, "api", "app_center", "v1", "application", "application.proto")
	protoSource, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	protoText := string(protoSource)

	postPath := mustFind(t, regexp.MustCompile(`post:\s*"([^"]+)"`), protoText)
	if postPath != "/v1/applications" {
		t.Fatalf("proto post path = %q, want /v1/applications", postPath)
	}
	if CreateApplicationInternalPath != postPath {
		t.Fatalf("internal path constant = %q, proto declares %q", CreateApplicationInternalPath, postPath)
	}

	serviceName := mustFind(t, regexp.MustCompile(`service\s+(\w+)\s*\{`), protoText)
	rpcName := mustFind(t, regexp.MustCompile(`rpc\s+(\w+)\s*\(`), protoText)
	pkgName := mustFind(t, regexp.MustCompile(`package\s+([\w.]+)\s*;`), protoText)
	if pkgName != protoPackage || serviceName != protoService || rpcName != protoRPC {
		t.Fatalf("proto identity = %s/%s/%s, want %s/%s/%s", pkgName, serviceName, rpcName, protoPackage, protoService, protoRPC)
	}
	wantGRPCMethod := "/" + pkgName + "." + serviceName + "/" + rpcName
	if CreateApplicationGRPCMethod != applicationv1.OperationApplicationCreateApplication {
		t.Fatalf("grpc method constant = %q, generated operation = %q", CreateApplicationGRPCMethod, applicationv1.OperationApplicationCreateApplication)
	}
	if CreateApplicationGRPCMethod != wantGRPCMethod {
		t.Fatalf("grpc method constant = %q, proto declares %q", CreateApplicationGRPCMethod, wantGRPCMethod)
	}

	generatedPath := filepath.Join(root, "api", "gen", "go", "app_center", "v1", "application", "application_http.pb.go")
	generatedSource, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated http code: %v", err)
	}
	generatedRoute := mustFind(t, regexp.MustCompile(`r\.POST\("([^"]+)"`), string(generatedSource))
	if generatedRoute != postPath {
		t.Fatalf("generated route = %q, proto route = %q", generatedRoute, postPath)
	}
	if !strings.Contains(string(generatedSource), applicationv1.OperationApplicationCreateApplication) {
		t.Fatalf("generated http code does not contain operation %q", applicationv1.OperationApplicationCreateApplication)
	}
}

func TestAPIContract_ExternalPrefixMapping(t *testing.T) {
	t.Parallel()

	if ServicePrefix != "/"+gatewayServiceName {
		t.Fatalf("service prefix = %q, want /%s", ServicePrefix, gatewayServiceName)
	}
	if CreateApplicationExternalPath != "/app-center/v1/applications" {
		t.Fatalf("external path = %q, want /app-center/v1/applications", CreateApplicationExternalPath)
	}
	if CreateApplicationExternalPath != ServicePrefix+CreateApplicationInternalPath {
		t.Fatalf("external path %q is not prefix + internal path %q", CreateApplicationExternalPath, CreateApplicationInternalPath)
	}
	if stripped := strings.TrimPrefix(CreateApplicationExternalPath, ServicePrefix); stripped != CreateApplicationInternalPath {
		t.Fatalf("gateway strip yields %q, want %q", stripped, CreateApplicationInternalPath)
	}
}

func TestAPIContract_MessageFieldsExcludeIdentityAndServerState(t *testing.T) {
	t.Parallel()

	request := &applicationv1.CreateApplicationRequest{}
	for _, field := range []string{"id", "admin_id", "created_at", "auth_id", "developer_status", "name_key", "next_version_sequence", "next_profile_revision_sequence"} {
		if containsField(request, field) {
			t.Fatalf("CreateApplicationRequest must not contain %q", field)
		}
	}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != "name" {
		t.Fatalf("CreateApplicationRequest fields = %v, want [name]", fields)
	}

	response := &applicationv1.CreateApplicationResponse{}
	want := []string{"admin_id", "created_at", "id", "name"}
	if fields := messageFieldNames(t, response); strings.Join(fields, ",") != strings.Join(want, ",") {
		t.Fatalf("CreateApplicationResponse fields = %v, want %v", fields, want)
	}
	for _, field := range []string{"name_key", "next_version_sequence", "next_profile_revision_sequence", "developer_status", "auth_id"} {
		if containsField(response, field) {
			t.Fatalf("CreateApplicationResponse must not contain %q", field)
		}
	}
}

func TestAPIContract_ErrorReasonsMatchGeneratedEnum(t *testing.T) {
	t.Parallel()

	reasons := []string{
		ReasonInvalidApplicationName,
		ReasonDeveloperIdentityRequired,
		ReasonInvalidDeveloperIdentity,
		ReasonDeveloperApprovalRequired,
		ReasonApplicationNameAlreadyExists,
		ReasonApplicationQuotaExceeded,
		ReasonInternal,
	}
	for _, reason := range reasons {
		if _, ok := applicationv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("generated ErrorReason enum is missing %q", reason)
		}
	}
}
