package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationusecase "iwut-app-center/internal/publication/usecase"
	"iwut-app-center/internal/shared"
)

type fakePlaceHandler struct {
	result   *publicationdomain.PlaceInTestResult
	err      error
	calls    int
	identity shared.DeveloperIdentity
	app      shared.ApplicationID
	major    int32
	command  publicationusecase.PlaceApprovedVersionInTestSlotCommand
}

func (h *fakePlaceHandler) Handle(_ context.Context, i shared.DeveloperIdentity, a shared.ApplicationID, m int32, c publicationusecase.PlaceApprovedVersionInTestSlotCommand) (*publicationdomain.PlaceInTestResult, error) {
	h.calls++
	h.identity = i
	h.app = a
	h.major = m
	h.command = c
	return h.result, h.err
}

func publicationResult(t *testing.T, existing *publicationdomain.ApplicationPublication, version publicationdomain.ApplicationVersionID) *publicationdomain.PlaceInTestResult {
	t.Helper()
	snapshot, err := publicationdomain.NewApplicationVersionReviewSnapshot("1.0", "https://example.edu", 3, 5, []string{}, []publicationdomain.ScopeName{}, []publicationdomain.ScopeName{})
	if err != nil {
		t.Fatal(err)
	}
	var revision *int64
	if existing != nil {
		v := existing.Revision()
		revision = &v
	}
	candidate, err := publicationdomain.NewTestPlacementCandidate(shared.ApplicationID(testApplicationID), 3, version, publicationdomain.ApplicationReviewID(testApplicationReviewID), 3, *snapshot, existing, revision)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.IsNoOp() {
		result, err := publicationdomain.NewNoOpResult(existing)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	var id *publicationdomain.ApplicationPublicationID
	if existing == nil {
		v := publicationdomain.ApplicationPublicationID("018f7777-7777-7777-8777-777777777779")
		id = &v
	}
	result, err := candidate.PlaceInTest(id, "018f7777-7777-7777-8777-777777777780", "admin", publicationdomain.PublicationValidation{ScopeCatalogRevision: 11, PreflightPolicyVersion: "submit-v1"}, time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPublicationService_BR_PUB_006_007_CreateReplaceNoOp(t *testing.T) {
	first := publicationResult(t, nil, publicationdomain.ApplicationVersionID(testApplicationVersionID))
	replacement := publicationResult(t, first.Publication(), "018f7777-7777-7777-8777-777777777781")
	noop := publicationResult(t, replacement.Publication(), replacement.Publication().TestVersionID())
	for _, tc := range []struct {
		name       string
		result     *publicationdomain.PlaceInTestResult
		httpStatus int
	}{{"create", first, http.StatusCreated}, {"replace", replacement, http.StatusOK}, {"noop", noop, http.StatusOK}} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakePlaceHandler{result: tc.result}
			service := NewApplicationPublicationService(h)
			var revision *int64
			if tc.name != "create" {
				value := tc.result.Publication().Revision()
				revision = &value
			}
			ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved})
			response, err := service.PlaceApprovedVersionInTestSlot(ctx, &publicationv1.PlaceApprovedVersionInTestSlotRequest{ApplicationId: testApplicationID, RpcApiMajor: 3, Command: &publicationv1.PlaceApprovedVersionInTestSlotCommand{VersionId: tc.result.Publication().TestVersionID().String(), ExpectedPublicationRevision: revision}})
			if err != nil {
				t.Fatal(err)
			}
			if h.calls != 1 || h.app.String() != testApplicationID || h.identity.AuthID != "admin" || h.major != 3 || (h.command.ExpectedPublicationRevision == nil) != (revision == nil) {
				t.Fatalf("command = %#v", h)
			}
			if response.GetChanged() != tc.result.Changed() || (response.GetHistory() != nil) != tc.result.Changed() || response.GetPublication().GetRevision() != tc.result.Publication().Revision() {
				t.Fatalf("response = %v", response)
			}
			if tc.name == "replace" && response.GetHistory().GetPreviousVersionId() != testApplicationVersionID {
				t.Fatal("previous pointer lost")
			}
			writer := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/", nil)
			if err := createdResponseEncoder(writer, request, response); err != nil {
				t.Fatal(err)
			}
			if writer.Code != tc.httpStatus {
				t.Fatalf("status = %d", writer.Code)
			}
		})
	}
}

func TestPublicationService_BR_PUB_001_AuthenticationBeforeCommand(t *testing.T) {
	h := &fakePlaceHandler{}
	service := NewApplicationPublicationService(h)
	_, err := service.PlaceApprovedVersionInTestSlot(context.Background(), &publicationv1.PlaceApprovedVersionInTestSlotRequest{})
	if status.Code(err) != codes.Unauthenticated || h.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, h.calls)
	}
}

func TestPublicationService_UCAPP007_ErrorMappings(t *testing.T) {
	tests := []struct {
		err    error
		code   codes.Code
		reason string
	}{
		{publicationdomain.ErrInvalidRpcApiMajor, codes.InvalidArgument, ReasonInvalidRpcApiMajor},
		{publicationdomain.ErrInvalidApplicationVersionId, codes.InvalidArgument, ReasonInvalidApplicationVersionId},
		{publicationdomain.ErrInvalidApplicationPublicationRevision, codes.InvalidArgument, ReasonInvalidApplicationPublicationRevision},
		{publicationdomain.ErrApplicationVersionNotFound, codes.NotFound, ReasonApplicationVersionNotFound},
		{publicationdomain.ErrApplicationAdminRequired, codes.PermissionDenied, ReasonApplicationAdminRequired},
		{publicationdomain.ErrDeveloperApprovalRequired, codes.PermissionDenied, ReasonDeveloperApprovalRequired},
		{publicationdomain.ErrApplicationVersionNotApproved, codes.FailedPrecondition, ReasonApplicationVersionNotApproved},
		{publicationdomain.ErrApplicationReviewStateInconsistent, codes.Aborted, ReasonApplicationReviewStateInconsistent},
		{publicationdomain.ErrApplicationVersionRpcApiIncompatible, codes.InvalidArgument, ReasonApplicationVersionRpcApiIncompatible},
		{publicationdomain.ErrApplicationPublicationAlreadyExists, codes.AlreadyExists, ReasonApplicationPublicationAlreadyExists},
		{publicationdomain.ErrApplicationPublicationNotFound, codes.NotFound, ReasonApplicationPublicationNotFound},
		{publicationdomain.ErrApplicationPublicationRevisionConflict, codes.Aborted, ReasonApplicationPublicationRevisionConflict},
		{publicationdomain.ErrInvalidApplicationScope, codes.InvalidArgument, ReasonInvalidApplicationScope},
		{publicationdomain.ErrApplicationLaunchURLNotReviewable, codes.InvalidArgument, ReasonApplicationLaunchURLNotReviewable},
		{publicationdomain.ErrScopeCatalogUnavailable, codes.Unavailable, ReasonScopeCatalogUnavailable},
		{publicationdomain.ErrLaunchURLInspectionUnavailable, codes.Unavailable, ReasonLaunchURLInspectionUnavailable},
		{errors.New("secret database details"), codes.Internal, ReasonInternal},
	}
	for _, tc := range tests {
		t.Run(tc.reason, func(t *testing.T) {
			err := toTransportError(tc.err)
			st := status.Convert(err)
			if st.Code() != tc.code || errorReason(st) != tc.reason {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestPublicationService_UCAPP007_HTTPErrorStatus(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, tc := range []struct {
		err  error
		code int
	}{
		{publicationdomain.ErrInvalidRpcApiMajor, 400},
		{publicationdomain.ErrInvalidApplicationVersionId, 400},
		{publicationdomain.ErrInvalidApplicationPublicationRevision, 400},
		{publicationdomain.ErrApplicationVersionNotFound, 404},
		{publicationdomain.ErrDeveloperApprovalRequired, 403},
		{publicationdomain.ErrApplicationAdminRequired, 403},
		{publicationdomain.ErrApplicationVersionNotApproved, 422},
		{publicationdomain.ErrApplicationVersionRpcApiIncompatible, 422},
		{publicationdomain.ErrInvalidApplicationScope, 422},
		{publicationdomain.ErrApplicationLaunchURLNotReviewable, 422},
		{publicationdomain.ErrApplicationPublicationAlreadyExists, 409},
		{publicationdomain.ErrApplicationPublicationNotFound, 404},
		{publicationdomain.ErrApplicationPublicationRevisionConflict, 409},
		{publicationdomain.ErrScopeCatalogUnavailable, 503},
		{publicationdomain.ErrLaunchURLInspectionUnavailable, 503},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			h := &fakePlaceHandler{err: tc.err}
			servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(h), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPut, "/v1/applications/"+testApplicationID+"/publications/3/test-slot", strings.NewReader(`{"versionId":"`+testApplicationVersionID+`"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(IdentityHeader, token)
			recorder := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(recorder, request)
			if recorder.Code != tc.code || h.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, h.calls, recorder.Body.String())
			}
		})
	}
}
