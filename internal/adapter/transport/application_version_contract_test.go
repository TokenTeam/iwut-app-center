package transport

import (
	"sort"
	"strings"
	"testing"

	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	"google.golang.org/protobuf/proto"
)

func TestAPIContract_UCAPP002_RequestExcludesServerOwnedFieldsAndResponseIsComplete(t *testing.T) {
	t.Parallel()
	request := &applicationversionv1.CreateApplicationVersionRequest{}
	wantRequest := []string{
		"application_id", "launch_url", "oauth_redirects", "optional_scopes", "required_capabilities", "required_scopes",
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
		"application_id", "created_at", "created_by", "launch_url", "oauth_redirects", "optional_scopes", "required_capabilities",
		"required_scopes", "review_status", "revision", "rpc_api_max_version_exclusive", "rpc_api_min_version",
		"sequence", "updated_at", "updated_by", "version_id", "version_label",
	}
	sort.Strings(wantResponse)
	if fields := messageFieldNames(t, response); strings.Join(fields, ",") != strings.Join(wantResponse, ",") {
		t.Fatalf("response fields = %v, want %v", fields, wantResponse)
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
	wantReplacement := []string{"launch_url", "oauth_redirects", "optional_scopes", "required_capabilities", "required_scopes", "rpc_api_max_version_exclusive", "rpc_api_min_version", "version_label"}
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

func TestAPIContract_UCAPP034_QueryFieldsAndReasons(t *testing.T) {
	t.Parallel()
	request := &applicationversionv1.ListApplicationVersionsRequest{}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != "application_id,query" {
		t.Fatalf("list request fields=%v", fields)
	}
	query := &applicationversionv1.ListApplicationVersionsQuery{}
	if fields := messageFieldNames(t, query); strings.Join(fields, ",") != "page_size,page_token,review_status" {
		t.Fatalf("list query fields=%v", fields)
	}
	for _, field := range []string{"auth_id", "admin_id", "created_by", "updated_by"} {
		if containsField(request, field) || containsField(query, field) {
			t.Fatalf("query contains caller-controlled identity field %q", field)
		}
	}
	for _, reason := range []string{ReasonUserIdentityRequired, ReasonInvalidUserIdentity, ReasonInvalidApplicationVersionQuery, ReasonInvalidApplicationVersionPageToken, ReasonApplicationVersionManagementNotFound, ReasonApplicationVersionManagementStateInconsistent, ReasonApplicationVersionManagementUnavailable} {
		if _, ok := applicationversionv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("generated UC-APP-034 ErrorReason enum is missing %q", reason)
		}
	}
}

func TestAPIContract_UCAPP034_ReadModelsCannotExposeReviewerOrSYSTEMAuthIDs(t *testing.T) {
	t.Parallel()
	for _, message := range []struct {
		name  string
		value proto.Message
	}{
		{"list summary", &applicationversionv1.OwnedApplicationVersionSummary{}},
		{"detail resource", &applicationversionv1.OwnedApplicationVersionResource{}},
		{"latest review", &applicationversionv1.OwnedApplicationVersionLatestReview{}},
		{"review decision", &applicationversionv1.OwnedApplicationVersionReviewDecision{}},
		{"draft restoration", &applicationversionv1.OwnedApplicationVersionDraftRestoration{}},
	} {
		for _, identity := range []string{"updated_by", "decided_by", "restored_by", "reviewer_id", "system_auth_id"} {
			if containsField(message.value, identity) {
				t.Fatalf("%s exposes Reviewer/SYSTEM identity field %q", message.name, identity)
			}
		}
	}
}

func TestAPIContract_UCAPP035_MinimalFieldsAndReasons(t *testing.T) {
	t.Parallel()
	request := &applicationversionv1.AbandonApplicationVersionDraftRequest{}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != "application_id,expected_revision,version_id" {
		t.Fatalf("request fields=%v", fields)
	}
	fact := &applicationversionv1.ApplicationVersionAbandonment{}
	if fields := messageFieldNames(t, fact); strings.Join(fields, ",") != "abandoned_at,application_id,sequence,source_revision,version_id,version_label" {
		t.Fatalf("abandonment fields=%v", fields)
	}
	for _, forbidden := range []string{"abandoned_by", "launch_url", "required_capabilities", "required_scopes", "optional_scopes", "oauth_redirects", "created_by", "updated_by"} {
		if containsField(fact, forbidden) {
			t.Fatalf("response exposes %q", forbidden)
		}
	}
	for _, reason := range []string{
		ReasonUserIdentityRequired, ReasonInvalidUserIdentity, ReasonApplicationVersionRevisionRequired,
		ReasonApplicationVersionNotFound, ReasonApplicationVersionRevisionConflict, ReasonInvalidApplicationVersionAbandonment,
		ReasonApplicationVersionNotAbandonable, ReasonApplicationVersionAbandonmentNotAllowed,
		ReasonApplicationVersionAbandonmentStateInconsistent, ReasonApplicationVersionAbandonmentUnavailable,
	} {
		if _, ok := applicationversionv1.ErrorReason_value[reason]; !ok {
			t.Fatalf("missing reason %s", reason)
		}
	}
}
