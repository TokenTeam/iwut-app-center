package transport

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewusecase "iwut-app-center/internal/review/usecase"
	"iwut-app-center/internal/shared"
)

const testApplicationReviewID = "018f7777-7777-7777-8777-777777777778"

type fakeRestoreRejectedApplicationVersionHandler struct {
	result        *reviewdomain.RestoreRejectedVersionResult
	err           error
	calls         int
	identity      shared.DeveloperIdentity
	applicationID shared.ApplicationID
	versionID     reviewdomain.ApplicationVersionID
	reviewID      reviewdomain.ApplicationReviewID
	command       reviewusecase.RestoreRejectedApplicationVersionCommand
}

type fakeDecideApplicationVersionReviewHandler struct {
	result        *reviewdomain.ApplicationReviewDecisionResult
	err           error
	calls         int
	identity      reviewusecase.ReviewerIdentity
	applicationID shared.ApplicationID
	versionID     reviewdomain.ApplicationVersionID
	reviewID      reviewdomain.ApplicationReviewID
	command       reviewusecase.DecideApplicationVersionReviewCommand
}

func (handler *fakeDecideApplicationVersionReviewHandler) Handle(
	_ context.Context,
	identity reviewusecase.ReviewerIdentity,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
	command reviewusecase.DecideApplicationVersionReviewCommand,
) (*reviewdomain.ApplicationReviewDecisionResult, error) {
	handler.calls++
	handler.identity, handler.applicationID, handler.versionID, handler.reviewID, handler.command =
		identity, applicationID, versionID, reviewID, command
	return handler.result, handler.err
}

func TestApplicationReviewService_UCAPP005_MapsReviewerCommandAndDecisionResponse(t *testing.T) {
	t.Parallel()
	decidedAt := time.Date(2026, time.September, 22, 10, 30, 0, 0, time.UTC)
	handler := &fakeDecideApplicationVersionReviewHandler{result: transportDecisionResult(t, decidedAt)}
	service := NewApplicationReviewService(nil, nil, handler)
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{
		AuthID: "auth-reviewer", Permissions: []string{reviewusecase.PermissionApplicationVersionReview},
	})
	reason := "Policy violation"
	response, err := service.DecideApplicationVersionReview(ctx, &applicationreviewv1.DecideApplicationVersionReviewRequest{
		ApplicationId: testApplicationID,
		VersionId:     testApplicationVersionID,
		ReviewId:      testApplicationReviewID,
		Command: &applicationreviewv1.DecideApplicationVersionReviewCommand{
			Outcome:               applicationreviewv1.ReviewDecisionAction_REJECT,
			ExpectedPolicyVersion: "app-version-review-v1",
			Reason:                &reason,
		},
	})
	if err != nil {
		t.Fatalf("DecideApplicationVersionReview() error = %v", err)
	}
	if handler.calls != 1 || handler.identity.AuthID != "auth-reviewer" || len(handler.identity.Permissions) != 1 ||
		handler.command.Outcome != "REJECT" || handler.command.ExpectedPolicyVersion != "app-version-review-v1" || handler.command.Reason != reason {
		t.Fatalf("handler input = %#v", handler)
	}
	decision := response.GetReview().GetDecision()
	if response.GetReview().GetStatus() != "REJECTED" || decision.GetOutcome() != "REJECTED" ||
		decision.GetDecidedBy() != "auth-reviewer" || decision.GetReason() != reason ||
		response.GetVersion().GetReviewStatus() != "REJECTED" || response.GetVersion().GetRevision() != 3 ||
		!response.GetVersion().GetUpdatedAt().AsTime().Equal(decidedAt) {
		t.Fatalf("response = %v", response)
	}
}

func TestApplicationReviewService_UCAPP005_ErrorMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		code   codes.Code
		reason string
	}{
		{name: "permission", err: reviewdomain.ErrApplicationReviewPermissionRequired, code: codes.PermissionDenied, reason: ReasonApplicationReviewPermissionRequired},
		{name: "conflict", err: reviewdomain.ErrApplicationReviewConflictOfInterest, code: codes.PermissionDenied, reason: ReasonApplicationReviewConflictOfInterest},
		{name: "already decided", err: reviewdomain.ErrApplicationReviewAlreadyDecided, code: codes.Aborted, reason: ReasonApplicationReviewAlreadyDecided},
		{name: "policy changed", err: reviewdomain.ErrApplicationReviewPolicyChanged, code: codes.Aborted, reason: ReasonApplicationReviewPolicyChanged},
		{name: "developer status unavailable", err: reviewdomain.ErrDeveloperStatusUnavailable, code: codes.Unavailable, reason: ReasonDeveloperStatusUnavailable},
		{name: "invalid OAuth redirects", err: reviewdomain.ErrInvalidOAuthRedirectConfiguration, code: codes.InvalidArgument, reason: ReasonInvalidOAuthRedirectConfiguration},
	}
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "auth-reviewer", Permissions: []string{reviewusecase.PermissionApplicationVersionReview}})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := NewApplicationReviewService(nil, nil, &fakeDecideApplicationVersionReviewHandler{err: test.err})
			_, err := service.DecideApplicationVersionReview(ctx, decisionRequest())
			current := status.Convert(err)
			if current.Code() != test.code || errorReason(current) != test.reason {
				t.Fatalf("status = (%v, %q)", current.Code(), errorReason(current))
			}
		})
	}
}

func (handler *fakeRestoreRejectedApplicationVersionHandler) Handle(
	_ context.Context,
	identity shared.DeveloperIdentity,
	applicationID shared.ApplicationID,
	versionID reviewdomain.ApplicationVersionID,
	reviewID reviewdomain.ApplicationReviewID,
	command reviewusecase.RestoreRejectedApplicationVersionCommand,
) (*reviewdomain.RestoreRejectedVersionResult, error) {
	handler.calls++
	handler.identity, handler.applicationID, handler.versionID, handler.reviewID, handler.command = identity, applicationID, versionID, reviewID, command
	return handler.result, handler.err
}

func TestApplicationReviewService_UCAPP006_MapsCommandAndCompleteResponse(t *testing.T) {
	t.Parallel()
	restoredAt := time.Date(2026, time.September, 21, 10, 30, 0, 0, time.UTC)
	handler := &fakeRestoreRejectedApplicationVersionHandler{result: transportRestorationResult(t, restoredAt)}
	service := NewApplicationReviewService(nil, handler, nil)
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-admin", DeveloperStatus: shared.DeveloperStatusApproved})

	response, err := service.RestoreRejectedApplicationVersionToDraft(ctx, restorationRequest())
	if err != nil {
		t.Fatalf("RestoreRejectedApplicationVersionToDraft() error = %v", err)
	}
	if handler.calls != 1 || handler.identity.AuthID != "auth-admin" || handler.applicationID.String() != testApplicationID ||
		handler.versionID.String() != testApplicationVersionID || handler.reviewID.String() != testApplicationReviewID ||
		handler.command.ExpectedVersionRevision != 3 {
		t.Fatalf("handler input = calls:%d identity:%#v app:%s version:%s review:%s command:%#v", handler.calls, handler.identity, handler.applicationID, handler.versionID, handler.reviewID, handler.command)
	}
	if response.GetReview().GetStatus() != "REJECTED" || response.GetReview().GetDraftRestoration().GetRestoredBy() != "auth-admin" ||
		response.GetReview().GetDraftRestoration().GetResultVersionRevision() != 4 ||
		response.GetVersion().GetReviewStatus() != "DRAFT" || response.GetVersion().GetRevision() != 4 ||
		response.GetVersion().GetUpdatedBy() != "auth-admin" || !response.GetVersion().GetUpdatedAt().AsTime().Equal(restoredAt) {
		t.Fatalf("response = %v", response)
	}
}

func TestApplicationReviewService_UCAPP006_ErrorMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		code   codes.Code
		reason string
	}{
		{name: "revision required", err: reviewdomain.ErrApplicationVersionRevisionRequired, code: codes.InvalidArgument, reason: ReasonApplicationVersionRevisionRequired},
		{name: "review missing", err: reviewdomain.ErrApplicationReviewNotFound, code: codes.NotFound, reason: ReasonApplicationReviewNotFound},
		{name: "admin", err: reviewdomain.ErrApplicationAdminRequired, code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired},
		{name: "not latest", err: reviewdomain.ErrApplicationReviewNotLatest, code: codes.Aborted, reason: ReasonApplicationReviewNotLatest},
		{name: "already restored", err: reviewdomain.ErrApplicationReviewAlreadyRestored, code: codes.Aborted, reason: ReasonApplicationReviewAlreadyRestored},
		{name: "not rejected", err: reviewdomain.ErrApplicationVersionNotRejected, code: codes.Aborted, reason: ReasonApplicationVersionNotRejected},
		{name: "revision conflict", err: reviewdomain.ErrApplicationVersionRevisionConflict, code: codes.Aborted, reason: ReasonApplicationVersionRevisionConflict},
		{name: "inconsistent", err: reviewdomain.ErrApplicationReviewStateInconsistent, code: codes.Aborted, reason: ReasonApplicationReviewStateInconsistent},
	}
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-admin", DeveloperStatus: shared.DeveloperStatusApproved})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := NewApplicationReviewService(nil, &fakeRestoreRejectedApplicationVersionHandler{err: test.err}, nil)
			_, err := service.RestoreRejectedApplicationVersionToDraft(ctx, restorationRequest())
			current := status.Convert(err)
			if current.Code() != test.code || errorReason(current) != test.reason {
				t.Fatalf("status = (%v, %q)", current.Code(), errorReason(current))
			}
		})
	}
}

func TestApplicationReviewService_UCAPP006_RejectsMissingIdentityAndInvalidPath(t *testing.T) {
	t.Parallel()
	handler := &fakeRestoreRejectedApplicationVersionHandler{}
	service := NewApplicationReviewService(nil, handler, nil)
	if _, err := service.RestoreRejectedApplicationVersionToDraft(context.Background(), restorationRequest()); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing identity status = %v", status.Code(err))
	}
	request := restorationRequest()
	request.ReviewId = "not-a-uuid"
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-admin", DeveloperStatus: shared.DeveloperStatusApproved})
	if _, err := service.RestoreRejectedApplicationVersionToDraft(ctx, request); status.Code(err) != codes.NotFound || errorReason(status.Convert(err)) != ReasonApplicationReviewNotFound {
		t.Fatalf("invalid path error = %v", err)
	}
	if handler.calls != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.calls)
	}
}

func restorationRequest() *applicationreviewv1.RestoreRejectedApplicationVersionToDraftRequest {
	return &applicationreviewv1.RestoreRejectedApplicationVersionToDraftRequest{
		ApplicationId: testApplicationID,
		VersionId:     testApplicationVersionID,
		ReviewId:      testApplicationReviewID,
		Command:       &applicationreviewv1.RestoreRejectedApplicationVersionToDraftCommand{ExpectedVersionRevision: 3},
	}
}

func decisionRequest() *applicationreviewv1.DecideApplicationVersionReviewRequest {
	reason := "Policy violation"
	return &applicationreviewv1.DecideApplicationVersionReviewRequest{
		ApplicationId: testApplicationID,
		VersionId:     testApplicationVersionID,
		ReviewId:      testApplicationReviewID,
		Command: &applicationreviewv1.DecideApplicationVersionReviewCommand{
			Outcome:               applicationreviewv1.ReviewDecisionAction_REJECT,
			ExpectedPolicyVersion: "app-version-review-v1",
			Reason:                &reason,
		},
	}
}

func transportDecisionResult(t *testing.T, decidedAt time.Time) *reviewdomain.ApplicationReviewDecisionResult {
	t.Helper()
	applicationID, valid := shared.ParseApplicationID(testApplicationID)
	if !valid {
		t.Fatal("invalid test application ID")
	}
	versionID := reviewdomain.ApplicationVersionID(testApplicationVersionID)
	reviewID := reviewdomain.ApplicationReviewID(testApplicationReviewID)
	snapshot, err := reviewdomain.NewApplicationVersionReviewSnapshot(
		"v1.0.0", "https://example.edu/app", 1, 3,
		[]string{}, []reviewdomain.ScopeName{}, []reviewdomain.ScopeName{},
	)
	if err != nil {
		t.Fatalf("NewApplicationVersionReviewSnapshot() error = %v", err)
	}
	submission, err := reviewdomain.NewSubmissionCandidate(applicationID, versionID, 1, snapshot)
	if err != nil {
		t.Fatalf("NewSubmissionCandidate() error = %v", err)
	}
	attempt, err := reviewdomain.NewReviewAttempt(1)
	if err != nil {
		t.Fatalf("NewReviewAttempt() error = %v", err)
	}
	catalogRevision, err := reviewdomain.NewScopeCatalogRevision(1)
	if err != nil {
		t.Fatalf("NewScopeCatalogRevision() error = %v", err)
	}
	preflightPolicy, err := reviewdomain.NewPreflightPolicyVersion("public-https.v1")
	if err != nil {
		t.Fatalf("NewPreflightPolicyVersion() error = %v", err)
	}
	review, err := reviewdomain.NewPendingApplicationReview(submission, reviewID, attempt, catalogRevision, preflightPolicy, "auth-submitter", decidedAt.Add(-time.Hour))
	if err != nil {
		t.Fatalf("NewPendingApplicationReview() error = %v", err)
	}
	decisionSnapshot := review.Snapshot()
	candidate, err := reviewdomain.NewApplicationReviewDecisionCandidate(review, reviewdomain.SubmittedVersionReviewStatus, 2, "auth-creator", &decisionSnapshot, "auth-admin")
	if err != nil {
		t.Fatalf("NewApplicationReviewDecisionCandidate() error = %v", err)
	}
	policyVersion, _ := reviewdomain.NewReviewPolicyVersion("app-version-review-v1")
	decision, err := review.Reject(policyVersion, "Policy violation", "auth-reviewer", decidedAt)
	if err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	version, err := reviewdomain.NewDecidedApplicationVersion(candidate, decision)
	if err != nil {
		t.Fatalf("NewDecidedApplicationVersion() error = %v", err)
	}
	result, err := reviewdomain.NewApplicationReviewDecisionResult(review, version)
	if err != nil {
		t.Fatalf("NewApplicationReviewDecisionResult() error = %v", err)
	}
	return result
}

func transportRestorationResult(t *testing.T, restoredAt time.Time) *reviewdomain.RestoreRejectedVersionResult {
	t.Helper()
	applicationID, _ := shared.ParseApplicationID(testApplicationID)
	versionID := reviewdomain.ApplicationVersionID(testApplicationVersionID)
	reviewID := reviewdomain.ApplicationReviewID(testApplicationReviewID)
	snapshot, _ := reviewdomain.NewApplicationVersionReviewSnapshot("v1", "https://example.edu/app", 1, 3, []string{}, []reviewdomain.ScopeName{}, []reviewdomain.ScopeName{})
	candidate, _ := reviewdomain.NewSubmissionCandidate(applicationID, versionID, 1, snapshot)
	attempt, _ := reviewdomain.NewReviewAttempt(1)
	catalogRevision, _ := reviewdomain.NewScopeCatalogRevision(1)
	preflightPolicy, _ := reviewdomain.NewPreflightPolicyVersion("public-https.v1")
	review, _ := reviewdomain.NewPendingApplicationReview(candidate, reviewID, attempt, catalogRevision, preflightPolicy, "auth-admin", restoredAt.Add(-2*time.Hour))
	reviewPolicy, _ := reviewdomain.NewReviewPolicyVersion("review.v1")
	if _, err := review.Reject(reviewPolicy, "reason", "auth-reviewer", restoredAt.Add(-time.Hour)); err != nil {
		t.Fatalf("Reject() error = %v", err)
	}
	if _, err := review.RecordDraftRestoration("auth-admin", restoredAt, 4); err != nil {
		t.Fatalf("RecordDraftRestoration() error = %v", err)
	}
	version, _ := reviewdomain.NewRestoredApplicationVersion(applicationID, versionID, 4, "auth-admin", restoredAt)
	result, err := reviewdomain.NewRestoreRejectedVersionResult(review, version)
	if err != nil {
		t.Fatalf("NewRestoreRejectedVersionResult() error = %v", err)
	}
	return result
}

var _ RestoreRejectedApplicationVersionHandler = (*fakeRestoreRejectedApplicationVersionHandler)(nil)
var _ DecideApplicationVersionReviewHandler = (*fakeDecideApplicationVersionReviewHandler)(nil)
