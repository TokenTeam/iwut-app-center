package transport

import (
	"strings"
	"testing"

	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
)

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

func TestAPIContract_UCAPP006_HTTPBodyOnlyAcceptsExpectedVersionRevision(t *testing.T) {
	t.Parallel()
	request := &applicationreviewv1.RestoreRejectedApplicationVersionToDraftRequest{}
	wantOuter := []string{"application_id", "command", "review_id", "version_id"}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != strings.Join(wantOuter, ",") {
		t.Fatalf("outer request fields = %v, want %v", fields, wantOuter)
	}
	command := &applicationreviewv1.RestoreRejectedApplicationVersionToDraftCommand{}
	if fields := messageFieldNames(t, command); strings.Join(fields, ",") != "expected_version_revision" {
		t.Fatalf("HTTP command fields = %v, want [expected_version_revision]", fields)
	}
	for _, field := range []string{"status", "draft_restoration", "restored_by", "restored_at", "result_version_revision", "developer_status"} {
		if containsField(command, field) {
			t.Fatalf("HTTP body contains server-owned field %q", field)
		}
	}
}

func TestAPIContract_UCAPP005_DecisionBodyExcludesTrustedAndServerFields(t *testing.T) {
	t.Parallel()
	request := &applicationreviewv1.DecideApplicationVersionReviewRequest{}
	wantOuter := []string{"application_id", "command", "review_id", "version_id"}
	if fields := messageFieldNames(t, request); strings.Join(fields, ",") != strings.Join(wantOuter, ",") {
		t.Fatalf("outer request fields = %v, want %v", fields, wantOuter)
	}
	command := &applicationreviewv1.DecideApplicationVersionReviewCommand{}
	wantCommand := []string{"confirmed_check_ids", "expected_policy_version", "outcome", "reason"}
	if fields := messageFieldNames(t, command); strings.Join(fields, ",") != strings.Join(wantCommand, ",") {
		t.Fatalf("command fields = %v, want %v", fields, wantCommand)
	}
	for _, field := range []string{"auth_id", "permissions", "developer_status", "decided_by", "decided_at", "approval_validation", "status"} {
		if containsField(command, field) {
			t.Fatalf("decision command contains trusted/server-owned field %q", field)
		}
	}
}
