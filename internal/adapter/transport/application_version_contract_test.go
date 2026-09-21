package transport

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	applicationversionv1 "iwut-app-center/api/gen/go/app_center/v1/application_version"
)

func TestAPIContract_UCAPP002_ResourceRouteAndGeneratedMethodAgree(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	protoPath := filepath.Join(root, "api", "app_center", "v1", "application_version", "application_version.proto")
	protoSource, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	protoText := string(protoSource)
	postPath := mustFind(t, regexp.MustCompile(`post:\s*"([^"]+)"`), protoText)
	if postPath != CreateApplicationVersionInternalPath {
		t.Fatalf("Proto path = %q, constant = %q", postPath, CreateApplicationVersionInternalPath)
	}
	if CreateApplicationVersionExternalPath != "/app-center/v1/applications/{application_id}/versions" ||
		strings.TrimPrefix(CreateApplicationVersionExternalPath, ServicePrefix) != CreateApplicationVersionInternalPath {
		t.Fatalf("external/internal mapping = %q -> %q", CreateApplicationVersionExternalPath, CreateApplicationVersionInternalPath)
	}
	if CreateApplicationVersionGRPCMethod != "/app_center.v1.application_version.ApplicationVersion/CreateApplicationVersion" {
		t.Fatalf("gRPC method = %q", CreateApplicationVersionGRPCMethod)
	}
	if CreateApplicationVersionGRPCMethod != applicationversionv1.OperationApplicationVersionCreateApplicationVersion {
		t.Fatalf("gRPC method = %q, generated = %q", CreateApplicationVersionGRPCMethod, applicationversionv1.OperationApplicationVersionCreateApplicationVersion)
	}
	generatedSource, err := os.ReadFile(filepath.Join(root, "api", "gen", "go", "app_center", "v1", "application_version", "application_version_http.pb.go"))
	if err != nil {
		t.Fatalf("read generated HTTP code: %v", err)
	}
	if generatedRoute := mustFind(t, regexp.MustCompile(`r\.POST\("([^"]+)"`), string(generatedSource)); generatedRoute != postPath {
		t.Fatalf("generated route = %q, Proto = %q", generatedRoute, postPath)
	}
}

func TestAPIContract_UCAPP002_RequestExcludesServerOwnedFieldsAndResponseIsComplete(t *testing.T) {
	t.Parallel()
	request := &applicationversionv1.CreateApplicationVersionRequest{}
	wantRequest := []string{
		"application_id", "launch_url", "optional_scopes", "required_capabilities", "required_scopes",
		"rpc_api_max_version_exclusive", "rpc_api_min_version", "version_label",
	}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != strings.Join(wantRequest, ",") {
		t.Fatalf("request fields = %v, want %v", fields, wantRequest)
	}
	for _, field := range []string{"sequence", "review_status", "version_id", "created_by", "created_at", "revision", "updated_by", "updated_at"} {
		if containsField(request, field) {
			t.Fatalf("request contains server-owned field %q", field)
		}
	}

	response := &applicationversionv1.CreateApplicationVersionResponse{}
	wantResponse := []string{
		"application_id", "created_at", "created_by", "launch_url", "optional_scopes", "required_capabilities",
		"required_scopes", "review_status", "revision", "rpc_api_max_version_exclusive", "rpc_api_min_version",
		"sequence", "updated_at", "updated_by", "version_id", "version_label",
	}
	sort.Strings(wantResponse)
	if fields := messageFieldNames(t, response); strings.Join(fields, ",") != strings.Join(wantResponse, ",") {
		t.Fatalf("response fields = %v, want %v", fields, wantResponse)
	}
}

func TestAPIContract_UCAPP002_ErrorReasonsMatchGeneratedEnum(t *testing.T) {
	t.Parallel()
	reasons := []string{
		ReasonInvalidApplicationID,
		ReasonInvalidVersionLabel,
		ReasonInvalidApplicationLaunchURL,
		ReasonInvalidRPCApiRange,
		ReasonInvalidRequiredCapability,
		ReasonInvalidApplicationScope,
		ReasonDeveloperIdentityRequired,
		ReasonInvalidDeveloperIdentity,
		ReasonDeveloperApprovalRequired,
		ReasonApplicationNotFound,
		ReasonApplicationAdminRequired,
		ReasonApplicationVersionLabelExists,
		ReasonScopeCatalogUnavailable,
		ReasonInternal,
	}
	for _, reason := range reasons {
		if _, ok := applicationversionv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("generated UC-APP-002 ErrorReason enum is missing %q", reason)
		}
	}
}

func TestAPIContract_UCAPP003_ResourceRouteAndGeneratedMethodAgree(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	protoPath := filepath.Join(root, "api", "app_center", "v1", "application_version", "application_version.proto")
	protoSource, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	putPath := mustFind(t, regexp.MustCompile(`put:\s*"([^"]+)"`), string(protoSource))
	if putPath != UpdateApplicationVersionInternalPath {
		t.Fatalf("Proto path = %q, constant = %q", putPath, UpdateApplicationVersionInternalPath)
	}
	if UpdateApplicationVersionExternalPath != "/app-center/v1/applications/{application_id}/versions/{version_id}" ||
		strings.TrimPrefix(UpdateApplicationVersionExternalPath, ServicePrefix) != UpdateApplicationVersionInternalPath {
		t.Fatalf("external/internal mapping = %q -> %q", UpdateApplicationVersionExternalPath, UpdateApplicationVersionInternalPath)
	}
	if UpdateApplicationVersionGRPCMethod != "/app_center.v1.application_version.ApplicationVersion/UpdateApplicationVersion" {
		t.Fatalf("gRPC method = %q", UpdateApplicationVersionGRPCMethod)
	}
	if UpdateApplicationVersionGRPCMethod != applicationversionv1.OperationApplicationVersionUpdateApplicationVersion {
		t.Fatalf("gRPC method = %q", UpdateApplicationVersionGRPCMethod)
	}
	generated, err := os.ReadFile(filepath.Join(root, "api", "gen", "go", "app_center", "v1", "application_version", "application_version_http.pb.go"))
	if err != nil {
		t.Fatalf("read generated HTTP code: %v", err)
	}
	if generatedRoute := mustFind(t, regexp.MustCompile(`r\.PUT\("([^"]+)"`), string(generated)); generatedRoute != putPath {
		t.Fatalf("generated route = %q, Proto = %q", generatedRoute, putPath)
	}
}

func TestAPIContract_UCAPP003_HTTPBodyIsCompleteReplacementWithoutServerState(t *testing.T) {
	t.Parallel()
	request := &applicationversionv1.UpdateApplicationVersionRequest{}
	wantOuter := []string{"application_id", "expected_revision", "replacement", "version_id"}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != strings.Join(wantOuter, ",") {
		t.Fatalf("outer request fields = %v, want %v", fields, wantOuter)
	}
	replacement := &applicationversionv1.DraftApplicationVersionReplacement{}
	wantReplacement := []string{"launch_url", "optional_scopes", "required_capabilities", "required_scopes", "rpc_api_max_version_exclusive", "rpc_api_min_version", "version_label"}
	if fields := messageFieldNames(t, replacement); strings.Join(fields, ",") != strings.Join(wantReplacement, ",") {
		t.Fatalf("replacement fields = %v, want %v", fields, wantReplacement)
	}
	for _, field := range []string{"application_id", "version_id", "sequence", "review_status", "created_by", "created_at", "revision", "updated_by", "updated_at", "expected_revision"} {
		if containsField(replacement, field) {
			t.Fatalf("HTTP replacement contains non-editable field %q", field)
		}
	}

	reasons := []string{
		ReasonApplicationVersionRevisionRequired,
		ReasonApplicationVersionNotFound,
		ReasonApplicationVersionNotDraft,
		ReasonApplicationVersionRevisionConflict,
	}
	for _, reason := range reasons {
		if _, ok := applicationversionv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("generated UC-APP-003 ErrorReason enum is missing %q", reason)
		}
	}
}
