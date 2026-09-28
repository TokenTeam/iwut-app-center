package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	testerjoinlinkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerusecase "iwut-app-center/internal/tester/usecase"
)

type fakeTesterJoinLinkHandler struct {
	result   *testerusecase.CreateOrRotateTesterJoinLinkResult
	err      error
	calls    int
	identity shared.DeveloperIdentity
	app      shared.ApplicationID
	command  testerusecase.CreateOrRotateTesterJoinLinkCommand
}

func (h *fakeTesterJoinLinkHandler) Handle(_ context.Context, identity shared.DeveloperIdentity, app shared.ApplicationID, command testerusecase.CreateOrRotateTesterJoinLinkCommand) (*testerusecase.CreateOrRotateTesterJoinLinkResult, error) {
	h.calls++
	h.identity = identity
	h.app = app
	h.command = command
	return h.result, h.err
}

func testerLinkResponse(t *testing.T, replaced *testerdomain.ApplicationTesterJoinLinkID) *testerusecase.CreateOrRotateTesterJoinLinkResult {
	t.Helper()
	link, err := testerdomain.NewActiveTesterJoinLink(testerdomain.ApplicationTesterJoinLinkID(testApplicationReviewID), shared.ApplicationID(testApplicationID), testerdomain.NewTesterJoinTokenHash([32]byte{1}), "auth-123", time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	result, err := testerdomain.NewCreateOrRotateTesterJoinLinkResult(link, replaced)
	if err != nil {
		t.Fatal(err)
	}
	response, err := testerusecase.NewCreateOrRotateTesterJoinLinkResult(result, "https://app.example/tester/join#joinLinkId="+link.JoinLinkID().String()+"&secret=test-only-secret")
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestTesterJoinLinkService_BR_TST_004_005_HTTPCreateRotateSensitiveBoundary(t *testing.T) {
	for _, rotate := range []bool{false, true} {
		var replaced *testerdomain.ApplicationTesterJoinLinkID
		body := `{"expectedActiveJoinLinkId":null,"tokenHash":"ignored","secret":"ignored","joinLinkId":"ignored","status":"REVOKED","createdBy":"attacker"}`
		if rotate {
			id := testerdomain.ApplicationTesterJoinLinkID(testApplicationVersionID)
			replaced = &id
			body = `{"expectedActiveJoinLinkId":"` + id.String() + `"}`
		}
		handler := &fakeTesterJoinLinkHandler{result: testerLinkResponse(t, replaced)}
		servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(handler, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/tester-join-links", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(IdentityHeader, signToken(t, tokenOptions{claims: validClaims(fixedNow())}))
		recorder := httptest.NewRecorder()
		servers.HTTP.ServeHTTP(recorder, request)
		if recorder.Code != 201 || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("response status/cache=%d/%s", recorder.Code, recorder.Header().Get("Cache-Control"))
		}
		if handler.calls != 1 || handler.identity.AuthID != "auth-123" || handler.app.String() != testApplicationID || (handler.command.ExpectedActiveJoinLinkID != nil) != rotate {
			t.Fatal("command mapping mismatch")
		}
		if strings.Contains(recorder.Body.String(), "tokenHash") || strings.Contains(recorder.Body.String(), "attacker") || !strings.Contains(recorder.Body.String(), "joinUrl") {
			t.Fatal("response public metadata boundary mismatch")
		}
	}
}

func TestTesterJoinLinkService_UCAPP008_HTTPAndGRPCErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		http   int
		grpc   codes.Code
		reason string
	}{
		{testerdomain.ErrDeveloperIdentityRequired, 401, codes.Unauthenticated, ReasonDeveloperIdentityRequired},
		{testerdomain.ErrDeveloperApprovalRequired, 403, codes.PermissionDenied, ReasonDeveloperApprovalRequired},
		{testerdomain.ErrInvalidApplicationId, 400, codes.InvalidArgument, ReasonInvalidApplicationID},
		{testerdomain.ErrInvalidTesterJoinLinkId, 400, codes.InvalidArgument, ReasonInvalidTesterJoinLinkId},
		{testerdomain.ErrApplicationNotFound, 404, codes.NotFound, ReasonApplicationNotFound},
		{testerdomain.ErrApplicationAdminRequired, 403, codes.PermissionDenied, ReasonApplicationAdminRequired},
		{testerdomain.ErrApplicationTesterJoinLinkAlreadyExists, 409, codes.AlreadyExists, ReasonApplicationTesterJoinLinkAlreadyExists},
		{testerdomain.ErrApplicationTesterJoinLinkNotFound, 404, codes.NotFound, ReasonApplicationTesterJoinLinkNotFound},
		{testerdomain.ErrApplicationTesterJoinLinkChanged, 409, codes.Aborted, ReasonApplicationTesterJoinLinkChanged},
		{errors.New("secret-from-infrastructure"), 500, codes.Internal, ReasonInternal},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			handler := &fakeTesterJoinLinkHandler{err: tc.err, result: testerLinkResponse(t, nil)}
			servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(handler, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/tester-join-links", strings.NewReader(`{}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(IdentityHeader, signToken(t, tokenOptions{claims: validClaims(fixedNow())}))
			recorder := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(recorder, request)
			if recorder.Code != tc.http || !strings.Contains(recorder.Body.String(), tc.reason) || strings.Contains(recorder.Body.String(), "secret") || strings.Contains(recorder.Body.String(), "joinUrl") {
				t.Fatalf("wrong or sensitive error response: status=%d", recorder.Code)
			}
			current := status.Convert(toTransportError(tc.err))
			if current.Code() != tc.grpc || errorReason(current) != tc.reason {
				t.Fatal("gRPC error mismatch")
			}
		})
	}
}

func TestTesterJoinLinkService_BR_TST_001_IdentityRequiredBeforeCommand(t *testing.T) {
	handler := &fakeTesterJoinLinkHandler{}
	_, err := NewTesterJoinLinkService(handler, nil).CreateOrRotateTesterJoinLink(context.Background(), &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{})
	if status.Code(err) != codes.Unauthenticated || handler.calls != 0 {
		t.Fatal("missing identity reached usecase")
	}
}

func TestTesterJoinLinkService_UCAPP008_RejectsQueryCommandOverride(t *testing.T) {
	for _, query := range []string{
		"command.expected_active_join_link_id=" + testApplicationVersionID,
		"command.expectedActiveJoinLinkId=" + testApplicationVersionID,
	} {
		t.Run(query, func(t *testing.T) {
			handler := &fakeTesterJoinLinkHandler{result: testerLinkResponse(t, nil)}
			servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(handler, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/tester-join-links?"+query, strings.NewReader(`{"expectedActiveJoinLinkId":null}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(IdentityHeader, signToken(t, tokenOptions{claims: validClaims(fixedNow())}))
			recorder := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || handler.calls != 0 {
				t.Fatalf("query command accepted: status=%d handlerCalls=%d expectedIdInjected=%t", recorder.Code, handler.calls, handler.command.ExpectedActiveJoinLinkID != nil)
			}
			if strings.Contains(recorder.Body.String(), "joinUrl") || strings.Contains(recorder.Body.String(), "tokenHash") {
				t.Fatal("rejected query exposed credential")
			}
		})
	}
}
