package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	testerjoinlinkv1 "iwut-app-center/api/gen/go/app_center/v1/tester_join_link"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeTesterRevocationHandler struct {
	result   *testerdomain.RevokeTesterJoinLinkResult
	err      error
	calls    int
	identity shared.DeveloperIdentity
	app      shared.ApplicationID
	link     testerdomain.ApplicationTesterJoinLinkID
}

func (h *fakeTesterRevocationHandler) Handle(_ context.Context, identity shared.DeveloperIdentity, app shared.ApplicationID, link testerdomain.ApplicationTesterJoinLinkID) (*testerdomain.RevokeTesterJoinLinkResult, error) {
	h.calls++
	h.identity = identity
	h.app = app
	h.link = link
	return h.result, h.err
}
func revocationResult(t *testing.T, revoked, rotated bool) *testerdomain.RevokeTesterJoinLinkResult {
	t.Helper()
	link := testerLinkResponse(t, nil).JoinLink()
	var err error
	if rotated {
		link, err = link.Rotate(testerdomain.ApplicationTesterJoinLinkID(testApplicationVersionID), "auth-123", fixedNow().Add(time.Hour))
	} else {
		link, err = link.Revoke("auth-123", fixedNow().Add(time.Hour))
	}
	if err != nil {
		t.Fatal(err)
	}
	result, err := testerdomain.NewRevokeTesterJoinLinkResult(link, revoked)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func revocationServers(t *testing.T, h *fakeTesterRevocationHandler) *Servers {
	t.Helper()
	s, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, h), NewTesterMembershipService(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func revocationHTTP(servers *Servers, token, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodDelete, "/v1/applications/"+testApplicationID+"/tester-join-links/"+testApplicationVersionID+query, strings.NewReader(body))
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(w, r)
	return w
}
func TestTesterRevocation_BR_TST_029_031_032_036_HTTPResult(t *testing.T) {
	for _, tc := range []struct{ revoked, rotated bool }{{true, false}, {false, false}, {false, true}} {
		h := &fakeTesterRevocationHandler{result: revocationResult(t, tc.revoked, tc.rotated)}
		w := revocationHTTP(revocationServers(t, h), signToken(t, tokenOptions{claims: validClaims(fixedNow())}), "", "")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status/cache %d", w.Code)
		}
		var r testerjoinlinkv1.RevokeTesterJoinLinkResponse
		if err := protojson.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		l := r.GetJoinLink()
		if r.GetRevoked() != tc.revoked || l.GetStatus() != "REVOKED" || l.GetRevokedBy() != "auth-123" || !l.GetRevokedAt().AsTime().Equal(fixedNow().Add(time.Hour)) {
			t.Fatal("public audit mapping mismatch")
		}
		if tc.rotated {
			if l.GetRevocationReason() != "ROTATED" || l.GetReplacedByJoinLinkId() != testApplicationVersionID {
				t.Fatal("lost rotation history")
			}
		} else if l.GetRevocationReason() != "MANUAL" || l.ReplacedByJoinLinkId != nil {
			t.Fatal("manual result mismatch")
		}
		if h.calls != 1 || h.identity.AuthID != "auth-123" || h.app.String() != testApplicationID || h.link.String() != testApplicationVersionID {
			t.Fatal("identity/path mapping mismatch")
		}
		for _, private := range []string{"tokenHash", "token_hash", "secret", "joinUrl"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("credential exposed")
			}
		}
	}
}
func TestTesterRevocation_BR_TST_029_030_036_HTTPAuthAndErrors(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, tc := range []struct {
		name, token, reason string
		err                 error
		code                int
	}{
		{"missing", "", ReasonDeveloperIdentityRequired, nil, 401},
		{"invalid", "private-JWS", ReasonInvalidDeveloperIdentity, nil, 401},
		{"approval", token, ReasonDeveloperApprovalRequired, testerdomain.ErrDeveloperApprovalRequired, 403},
		{"admin", token, ReasonApplicationAdminRequired, testerdomain.ErrApplicationAdminRequired, 403},
		{"application", token, ReasonInvalidApplicationID, testerdomain.ErrInvalidApplicationId, 400},
		{"join link", token, ReasonInvalidTesterJoinLinkId, testerdomain.ErrInvalidTesterJoinLinkId, 400},
		{"notfound", token, ReasonApplicationTesterJoinLinkNotFound, testerdomain.ErrApplicationTesterJoinLinkNotFound, 404},
		{"inconsistent", token, ReasonApplicationTesterJoinLinkStateInconsistent, fmt.Errorf("private-storage: %w", testerdomain.ErrApplicationTesterJoinLinkStateInconsistent), 500},
		{"internal", token, ReasonInternal, errors.New("private-secret-storage"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeTesterRevocationHandler{err: tc.err, result: revocationResult(t, true, false)}
			w := revocationHTTP(revocationServers(t, h), tc.token, "", "")
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("status/reason %d %s", w.Code, w.Body.String())
			}
			if tc.err == nil && h.calls != 0 {
				t.Fatal("unauthenticated request reached handler")
			}
		})
	}
}
func TestTesterRevocation_BR_TST_030_036_RejectHTTPBodyAndQuery(t *testing.T) {
	for _, input := range []struct{ query, body string }{
		{"", `{"authId":"private-value"}`}, {"", `{"createReplacement":true}`}, {"", `{"revokedBy":"private-value","revokedAt":"private-value"}`}, {"", `{"status":"ACTIVE","activeTesterCount":100}`}, {"", `{"removeTesters":true}`}, {"", `{"revocationReason":"ROTATED","replacedByJoinLinkId":"private-value"}`}, {"", `{}`}, {"", `private-malformed`},
		{"?authId=private-value", ""}, {"?join_link_id=private-value", ""}, {"?application_id=private-value", ""}, {"?removeTesters=true", ""}, {"?revoked_by=private-value", ""}, {"?activeTesterCount=1", ""}, {"?bad=%zz", ""},
	} {
		h := &fakeTesterRevocationHandler{result: revocationResult(t, true, false)}
		w := revocationHTTP(revocationServers(t, h), signToken(t, tokenOptions{claims: validClaims(fixedNow())}), input.query, input.body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), ReasonInvalidRevokeTesterJoinLinkRequest) || h.calls != 0 || strings.Contains(w.Body.String(), "private-") {
			t.Fatalf("input was accepted or leaked: %d %s", w.Code, w.Body.String())
		}
	}
	h := &fakeTesterRevocationHandler{result: revocationResult(t, true, false)}
	w := revocationHTTP(revocationServers(t, h), "", "?authId=private-value", `private-malformed`)
	if w.Code != 401 || !strings.Contains(w.Body.String(), ReasonDeveloperIdentityRequired) || h.calls != 0 {
		t.Fatal("authentication must precede body validation")
	}
}
func TestTesterRevocation_BR_TST_036_StateInconsistencyAlertIsSafe(t *testing.T) {
	var log bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	defer slog.SetDefault(previous)
	h := &fakeTesterRevocationHandler{err: fmt.Errorf("private-token-hash-user-storage: %w", testerdomain.ErrApplicationTesterJoinLinkStateInconsistent)}
	_, err := NewTesterJoinLinkService(nil, h).RevokeTesterJoinLink(withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "private-admin", DeveloperStatus: shared.DeveloperStatusApproved}), &testerjoinlinkv1.RevokeTesterJoinLinkRequest{ApplicationId: testApplicationID, JoinLinkId: testApplicationVersionID})
	if status.Code(err) != codes.Internal || errorReason(status.Convert(err)) != ReasonApplicationTesterJoinLinkStateInconsistent || !strings.Contains(log.String(), ReasonApplicationTesterJoinLinkStateInconsistent) || !strings.Contains(log.String(), `"level":"ERROR"`) || strings.Contains(log.String(), "private-") || strings.Contains(log.String(), testApplicationID) {
		t.Fatalf("unsafe or missing alert: %s", log.String())
	}
}
func TestTesterRevocation_BR_TST_029_032_036_GRPC(t *testing.T) {
	h := &fakeTesterRevocationHandler{result: revocationResult(t, true, false)}
	servers := revocationServers(t, h)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = servers.GRPC.Server.Serve(listener) }()
	t.Cleanup(servers.GRPC.Server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := testerjoinlinkv1.NewTesterJoinLinkClient(connection)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, signToken(t, tokenOptions{claims: validClaims(fixedNow())})))
	request := &testerjoinlinkv1.RevokeTesterJoinLinkRequest{ApplicationId: testApplicationID, JoinLinkId: testApplicationVersionID}
	var headers metadata.MD
	response, err := client.RevokeTesterJoinLink(ctx, request, grpc.Header(&headers))
	if err != nil || !response.GetRevoked() || response.GetJoinLink().GetRevokedBy() != "auth-123" {
		t.Fatalf("gRPC revocation failed: %v", err)
	}
	if v := headers.Get("cache-control"); len(v) != 1 || v[0] != "no-store" {
		t.Fatal("no-store missing")
	}
	h.result = revocationResult(t, false, false)
	response, err = client.RevokeTesterJoinLink(ctx, request)
	if err != nil || response.GetRevoked() {
		t.Fatal("idempotent revocation mapping failed")
	}
	h.err = testerdomain.ErrApplicationTesterJoinLinkStateInconsistent
	_, err = client.RevokeTesterJoinLink(ctx, request)
	if status.Code(err) != codes.Internal || errorReason(status.Convert(err)) != ReasonApplicationTesterJoinLinkStateInconsistent {
		t.Fatal("state inconsistency must be INTERNAL")
	}
	h.err = nil
	calls := h.calls
	for _, tc := range []struct{ token, reason string }{{"", ReasonDeveloperIdentityRequired}, {"private-JWS", ReasonInvalidDeveloperIdentity}} {
		_, err := client.RevokeTesterJoinLink(metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, tc.token)), request)
		if status.Code(err) != codes.Unauthenticated || errorReason(status.Convert(err)) != tc.reason || strings.Contains(err.Error(), "private-") {
			t.Fatal("gRPC identity boundary failed")
		}
	}
	request.ProtoReflect().SetUnknown([]byte{0x18, 0x01})
	_, err = client.RevokeTesterJoinLink(ctx, request)
	if status.Code(err) != codes.InvalidArgument || errorReason(status.Convert(err)) != ReasonInvalidRevokeTesterJoinLinkRequest || h.calls != calls {
		t.Fatal("unknown fields reached handler")
	}
}
