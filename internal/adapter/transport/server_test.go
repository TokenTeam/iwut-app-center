package transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"

	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func newTestServers(t *testing.T, handler *fakeCreateApplicationHandler) *Servers {
	t.Helper()
	service := NewApplicationService(handler)
	servers, err := NewServers(
		ServerConfig{HTTPAddr: "127.0.0.1:0", GRPCAddr: "127.0.0.1:0"},
		newTestVerifier(t),
		service,
	)
	if err != nil {
		t.Fatalf("NewServers() error = %v", err)
	}
	return servers
}

func doHTTPCreateApplication(t *testing.T, servers *Servers, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, CreateApplicationInternalPath, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(IdentityHeader, token)
	}
	recorder := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(recorder, request)
	return recorder
}

func TestServers_HTTPAndGRPCReachTheSameUseCase(t *testing.T) {
	t.Parallel()

	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	createdAt := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	handler := &fakeCreateApplicationHandler{result: newApplication(t, "auth-123", createdAt)}
	servers := newTestServers(t, handler)

	// Real HTTP request through the generated route and Kratos middleware.
	recorder := doHTTPCreateApplication(t, servers, token, `{"name":"Course_Table"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("HTTP status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
	var httpResponse applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(recorder.Body.Bytes(), &httpResponse); err != nil {
		t.Fatalf("decode HTTP response: %v; body = %s", err, recorder.Body.String())
	}
	if httpResponse.GetAdminId() != "auth-123" || httpResponse.GetName() != "Course_Table" {
		t.Fatalf("HTTP response = %v", &httpResponse)
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls after HTTP = %d, want 1", handler.calls)
	}

	// Real gRPC call through the same service and middleware over bufconn.
	listener := bufconn.Listen(1 << 20)
	go func() {
		_ = servers.GRPC.Server.Serve(listener)
	}()
	t.Cleanup(servers.GRPC.Server.Stop)

	connection, err := grpc.DialContext(
		context.Background(),
		"bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	client := applicationv1.NewApplicationClient(connection)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, token))
	grpcResponse, err := client.CreateApplication(ctx, &applicationv1.CreateApplicationRequest{Name: "Course_Table"})
	if err != nil {
		t.Fatalf("gRPC CreateApplication() error = %v", err)
	}
	if grpcResponse.GetAdminId() != "auth-123" || grpcResponse.GetId() != testApplicationID {
		t.Fatalf("gRPC response = %v", grpcResponse)
	}
	if handler.calls != 2 {
		t.Fatalf("handler calls after gRPC = %d, want 2", handler.calls)
	}
}

func TestServers_HTTPStatusMapping(t *testing.T) {
	t.Parallel()

	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	testCases := []struct {
		name       string
		token      string
		handlerErr error
		wantStatus int
		wantReason string
	}{
		{name: "missing identity", token: "", wantStatus: http.StatusUnauthorized, wantReason: ReasonDeveloperIdentityRequired},
		{name: "invalid name", token: token, handlerErr: domain.ErrInvalidApplicationName, wantStatus: http.StatusBadRequest, wantReason: ReasonInvalidApplicationName},
		{name: "not approved", token: token, handlerErr: domain.ErrDeveloperApprovalRequired, wantStatus: http.StatusForbidden, wantReason: ReasonDeveloperApprovalRequired},
		{name: "name conflict", token: token, handlerErr: domain.ErrApplicationNameAlreadyExists, wantStatus: http.StatusConflict, wantReason: ReasonApplicationNameAlreadyExists},
		{name: "quota", token: token, handlerErr: domain.ErrApplicationQuotaExceeded, wantStatus: http.StatusTooManyRequests, wantReason: ReasonApplicationQuotaExceeded},
		{name: "internal", token: token, handlerErr: domain.NewInternalError(errors.New("database credentials leaked")), wantStatus: http.StatusInternalServerError, wantReason: ReasonInternal},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			handler := &fakeCreateApplicationHandler{result: newApplication(t, "auth-123", time.Now()), err: testCase.handlerErr}
			servers := newTestServers(t, handler)
			recorder := doHTTPCreateApplication(t, servers, testCase.token, `{"name":"Course_Table"}`)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
			if !strings.Contains(recorder.Body.String(), testCase.wantReason) {
				t.Fatalf("body = %s, want reason %s", recorder.Body.String(), testCase.wantReason)
			}
			if strings.Contains(recorder.Body.String(), "database credentials leaked") {
				t.Fatalf("body leaked internal cause: %s", recorder.Body.String())
			}
		})
	}
}

func TestServers_HTTPBodyCannotOverrideIdentityOrServerFields(t *testing.T) {
	t.Parallel()

	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	handler := &fakeCreateApplicationHandler{result: newApplication(t, "auth-123", time.Now())}
	servers := newTestServers(t, handler)

	recorder := doHTTPCreateApplication(
		t,
		servers,
		token,
		`{"name":"Course_Table","adminId":"evil","id":"evil","createdAt":"2000-01-01T00:00:00Z"}`,
	)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", recorder.Code, recorder.Body.String())
	}
	var response applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.GetAdminId() != "auth-123" || response.GetId() != testApplicationID {
		t.Fatalf("response = %v", &response)
	}
	if handler.identity.AuthID != shared.AuthID("auth-123") {
		t.Fatalf("handler identity = %#v", handler.identity)
	}
}
