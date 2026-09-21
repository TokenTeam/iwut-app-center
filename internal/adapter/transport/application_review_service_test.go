package transport

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	applicationreviewv1 "iwut-app-center/api/gen/go/app_center/v1/application_review"
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
	service := NewApplicationReviewService(nil, handler)
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
			service := NewApplicationReviewService(nil, &fakeRestoreRejectedApplicationVersionHandler{err: test.err})
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
	service := NewApplicationReviewService(nil, handler)
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
