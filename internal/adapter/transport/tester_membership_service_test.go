package transport

import (
	"context"
	"errors"
	testermembershipv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerusecase "iwut-app-center/internal/tester/usecase"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeTesterMembershipHandler struct {
	result   *testerdomain.JoinApplicationAsTesterResult
	err      error
	calls    int
	identity shared.AuthenticatedUserIdentity
	link     testerdomain.ApplicationTesterJoinLinkID
	command  testerusecase.JoinApplicationAsTesterCommand
}

func (h *fakeTesterMembershipHandler) Handle(_ context.Context, identity shared.AuthenticatedUserIdentity, link testerdomain.ApplicationTesterJoinLinkID, command testerusecase.JoinApplicationAsTesterCommand) (*testerdomain.JoinApplicationAsTesterResult, error) {
	h.calls++
	h.identity = identity
	h.link = link
	h.command = command
	return h.result, h.err
}
func membershipResponse(t *testing.T, joined bool) *testerdomain.JoinApplicationAsTesterResult {
	t.Helper()
	membership, err := testerdomain.NewActiveTesterMembership(testerdomain.ApplicationTesterMembershipID(testApplicationVersionID), shared.ApplicationID(testApplicationID), "auth-123", testerdomain.ApplicationTesterJoinLinkID(testApplicationReviewID), fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	result, err := testerdomain.NewJoinApplicationAsTesterResult(membership, joined, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func membershipServers(t *testing.T, h *fakeTesterMembershipHandler) *Servers {
	t.Helper()
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(h, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	return servers
}
func ordinaryUserToken(t *testing.T) string {
	t.Helper()
	claims := validClaims(fixedNow())
	delete(claims, "developer_status")
	return signToken(t, tokenOptions{claims: claims})
}
func membershipHTTP(servers *Servers, token, query, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/tester-join-links/"+testApplicationReviewID+"/memberships"+query, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(IdentityHeader, token)
	}
	recorder := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(recorder, request)
	return recorder
}
func TestTesterMembership_BR_TST_010_013_019_HTTPOrdinaryUserAndIdempotency(t *testing.T) {
	for _, joined := range []bool{true, false} {
		h := &fakeTesterMembershipHandler{result: membershipResponse(t, joined)}
		servers := membershipServers(t, h)
		recorder := membershipHTTP(servers, ordinaryUserToken(t), "", `{"secret":"test-sensitive-credential","authId":"attacker","testerAuthId":"attacker","applicationId":"attacker","membershipId":"attacker","status":"REMOVED","joinedAt":"2000-01-01T00:00:00Z"}`)
		want := 200
		if joined {
			want = 201
		}
		if recorder.Code != want || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status/cache=%d/%s", recorder.Code, recorder.Header().Get("Cache-Control"))
		}
		if h.calls != 1 || h.identity.AuthID != "auth-123" || h.link.String() != testApplicationReviewID || h.command.Secret != "test-sensitive-credential" {
			t.Fatal("trusted identity or body mapping mismatch")
		}
		var response testermembershipv1.JoinApplicationAsTesterResponse
		if err := protojson.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal("decode response")
		}
		if response.Joined != joined || response.GetCapacity().GetActiveTesterCount() != 100 || response.GetCapacity().GetTesterLimit() != 100 {
			t.Fatal("result mapping mismatch")
		}
		for _, forbidden := range []string{"test-sensitive-credential", "tokenHash", "attacker"} {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatal("sensitive or caller-owned field leaked")
			}
		}
	}
}
func TestTesterMembership_BR_TST_010_019_HTTPAuthenticationAndCredentialErrors(t *testing.T) {
	for _, tc := range []struct {
		name, token, query, body, reason string
		handlerErr                       error
		code                             int
	}{
		{name: "malformed without identity", body: `{"secret":{"private-sensitive-credential":1}}`, reason: ReasonAuthenticatedUserRequired, code: 401},
		{name: "malformed with invalid identity", token: "private-invalid-JWS", body: `{"secret":{"private-sensitive-credential":1}}`, reason: ReasonInvalidAuthenticatedUser, code: 401},
		{name: "missing", body: `{}`, reason: ReasonAuthenticatedUserRequired, code: 401},
		{name: "invalid", token: "private-invalid-JWS", body: `{}`, reason: ReasonInvalidAuthenticatedUser, code: 401},
		{name: "domain identity", token: ordinaryUserToken(t), body: `{}`, handlerErr: testerdomain.ErrAuthenticatedUserRequired, reason: ReasonAuthenticatedUserRequired, code: 401},
		{name: "id", token: ordinaryUserToken(t), body: `{}`, handlerErr: testerdomain.ErrInvalidTesterJoinLinkId, reason: ReasonInvalidTesterJoinLinkId, code: 400},
		{name: "secret", token: ordinaryUserToken(t), body: `{}`, handlerErr: testerdomain.ErrInvalidTesterJoinSecret, reason: ReasonInvalidTesterJoinSecret, code: 400},
		{name: "invalid link", token: ordinaryUserToken(t), body: `{}`, handlerErr: testerdomain.ErrTesterJoinLinkInvalid, reason: ReasonTesterJoinLinkInvalid, code: 404},
		{name: "capacity", token: ordinaryUserToken(t), body: `{}`, handlerErr: testerdomain.ErrApplicationTesterLimitReached, reason: ReasonApplicationTesterLimitReached, code: 409},
		{name: "internal", token: ordinaryUserToken(t), body: `{}`, handlerErr: errors.New("private-sensitive-credential"), reason: ReasonInternal, code: 500},
		{name: "query secret", token: ordinaryUserToken(t), query: "?secret=private-sensitive-credential", body: `{}`, reason: ReasonInvalidTesterJoinSecret, code: 400},
		{name: "nested query secret", token: ordinaryUserToken(t), query: "?command.secret=private-sensitive-credential", body: `{"secret":"body-secret"}`, reason: ReasonInvalidTesterJoinSecret, code: 400},
		{name: "query command", token: ordinaryUserToken(t), query: "?command=private-sensitive-credential", body: `{}`, reason: ReasonInvalidTesterJoinSecret, code: 400},
		{name: "malformed JSON", token: ordinaryUserToken(t), body: `{"secret":{"private-sensitive-credential":1}}`, reason: ReasonInvalidTesterJoinSecret, code: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeTesterMembershipHandler{err: tc.handlerErr, result: membershipResponse(t, true)}
			recorder := membershipHTTP(membershipServers(t, h), tc.token, tc.query, tc.body)
			if recorder.Code != tc.code || !strings.Contains(recorder.Body.String(), tc.reason) || recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d expected=%d response=%s", recorder.Code, tc.code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "private-") || strings.Contains(recorder.Body.String(), "body-secret") {
				t.Fatal("credential leaked")
			}
			if tc.handlerErr == nil && h.calls != 0 {
				t.Fatal("invalid transport reached handler")
			}
		})
	}
}
func TestTesterMembership_BR_TST_010_019_GRPCOrdinaryUserAndErrors(t *testing.T) {
	h := &fakeTesterMembershipHandler{result: membershipResponse(t, true)}
	servers := membershipServers(t, h)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = servers.GRPC.Server.Serve(listener) }()
	t.Cleanup(servers.GRPC.Server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := testermembershipv1.NewTesterMembershipClient(connection)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, ordinaryUserToken(t)))
	var headers metadata.MD
	response, err := client.JoinApplicationAsTester(ctx, &testermembershipv1.JoinApplicationAsTesterRequest{JoinLinkId: testApplicationReviewID, Command: &testermembershipv1.JoinApplicationAsTesterCommand{Secret: "private-sensitive-credential"}}, grpc.Header(&headers))
	if err != nil || !response.GetJoined() || h.identity.AuthID != "auth-123" {
		t.Fatal("ordinary user gRPC failed")
	}
	if values := headers.Get("cache-control"); len(values) != 1 || values[0] != "no-store" {
		t.Fatal("no-store missing")
	}
	for _, tc := range []struct {
		token, reason string
		code          codes.Code
	}{{"", ReasonAuthenticatedUserRequired, codes.Unauthenticated}, {"private-invalid-JWS", ReasonInvalidAuthenticatedUser, codes.Unauthenticated}} {
		_, err := client.JoinApplicationAsTester(metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, tc.token)), &testermembershipv1.JoinApplicationAsTesterRequest{})
		st := status.Convert(err)
		if st.Code() != tc.code || errorReason(st) != tc.reason || strings.Contains(st.Message(), "private-") {
			t.Fatal("gRPC auth mapping or redaction failed")
		}
	}
	if h.calls != 1 {
		t.Fatal("invalid identity reached handler")
	}
}
