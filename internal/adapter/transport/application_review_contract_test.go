package transport

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	applicationreviewv1 "iwut-app-center/api/gen/go/app_center/v1/application_review"
)

func TestAPIContract_UCAPP004_ResourceRouteAndGeneratedMethodAgree(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	protoPath := filepath.Join(root, "api", "app_center", "v1", "application_review", "application_review.proto")
	protoSource, err := os.ReadFile(protoPath)
	if err != nil {
		t.Fatalf("read proto: %v", err)
	}
	postPath := mustFind(t, regexp.MustCompile(`post:\s*"([^"]+)"`), string(protoSource))
	if postPath != SubmitApplicationVersionReviewInternalPath {
		t.Fatalf("Proto path = %q, constant = %q", postPath, SubmitApplicationVersionReviewInternalPath)
	}
	if SubmitApplicationVersionReviewExternalPath != "/app-center/v1/applications/{application_id}/versions/{version_id}/reviews" ||
		strings.TrimPrefix(SubmitApplicationVersionReviewExternalPath, ServicePrefix) != SubmitApplicationVersionReviewInternalPath {
		t.Fatalf("external/internal mapping = %q -> %q", SubmitApplicationVersionReviewExternalPath, SubmitApplicationVersionReviewInternalPath)
	}
	if SubmitApplicationVersionReviewGRPCMethod != "/app_center.v1.application_review.ApplicationReview/SubmitApplicationVersionReview" {
		t.Fatalf("gRPC method = %q", SubmitApplicationVersionReviewGRPCMethod)
	}
	if SubmitApplicationVersionReviewGRPCMethod != applicationreviewv1.OperationApplicationReviewSubmitApplicationVersionReview {
		t.Fatalf("gRPC method = %q", SubmitApplicationVersionReviewGRPCMethod)
	}
	generated, err := os.ReadFile(filepath.Join(root, "api", "gen", "go", "app_center", "v1", "application_review", "application_review_http.pb.go"))
	if err != nil {
		t.Fatalf("read generated HTTP code: %v", err)
	}
	if generatedRoute := mustFind(t, regexp.MustCompile(`r\.POST\("([^"]+)"`), string(generated)); generatedRoute != postPath {
		t.Fatalf("generated route = %q, Proto = %q", generatedRoute, postPath)
	}
}

func TestAPIContract_UCAPP004_HTTPBodyOnlyAcceptsExpectedRevision(t *testing.T) {
	t.Parallel()
	request := &applicationreviewv1.SubmitApplicationVersionReviewRequest{}
	wantOuter := []string{"application_id", "command", "version_id"}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != strings.Join(wantOuter, ",") {
		t.Fatalf("outer request fields = %v, want %v", fields, wantOuter)
	}
	command := &applicationreviewv1.SubmitApplicationVersionReviewCommand{}
	if fields := messageFieldNames(t, command); strings.Join(fields, ",") != "expected_revision" {
		t.Fatalf("HTTP command fields = %v, want [expected_revision]", fields)
	}
	for _, field := range []string{"review_id", "attempt", "status", "snapshot", "decision", "draft_restoration", "submitted_by", "submitted_at"} {
		if containsField(command, field) {
			t.Fatalf("HTTP body contains server-owned field %q", field)
		}
	}
}

func TestAPIContract_UCAPP004_ErrorReasonsMatchGeneratedEnum(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{
		ReasonDeveloperIdentityRequired,
		ReasonInvalidDeveloperIdentity,
		ReasonDeveloperApprovalRequired,
		ReasonApplicationVersionRevisionRequired,
		ReasonApplicationVersionNotFound,
		ReasonApplicationAdminRequired,
		ReasonApplicationVersionNotDraft,
		ReasonApplicationVersionRevisionConflict,
		ReasonApplicationLaunchURLNotReviewable,
		ReasonLaunchURLInspectionUnavailable,
		ReasonInvalidApplicationScope,
		ReasonScopeCatalogUnavailable,
		ReasonInternal,
	} {
		if _, ok := applicationreviewv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("generated UC-APP-004 ErrorReason enum is missing %q", reason)
		}
	}
}
