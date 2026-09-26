package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeTesterRemovalHandler struct {
	result     *testerdomain.RemoveApplicationTesterResult
	err        error
	calls      int
	identity   shared.DeveloperIdentity
	app        shared.ApplicationID
	membership testerdomain.ApplicationTesterMembershipID
}

func (h *fakeTesterRemovalHandler) Handle(_ context.Context, identity shared.DeveloperIdentity, app shared.ApplicationID, membership testerdomain.ApplicationTesterMembershipID) (*testerdomain.RemoveApplicationTesterResult, error) {
	h.calls++
	h.identity = identity
	h.app = app
	h.membership = membership
	return h.result, h.err
}
func removalResult(t *testing.T, removed, activeLink bool) *testerdomain.RemoveApplicationTesterResult {
	t.Helper()
	m := membershipResponse(t, true).Membership()
	m, err := m.Remove("auth-123", fixedNow().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	r, err := testerdomain.NewRemoveApplicationTesterResult(m, removed, 0, 100, activeLink)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func removalServers(t *testing.T, h *fakeTesterRemovalHandler) *Servers {
	t.Helper()
	s, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, h), NewCatalogService(nil), NewApplicationProfileRevisionService(nil))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func removalHTTP(servers *Servers, token, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodDelete, "/v1/applications/"+testApplicationID+"/tester-memberships/"+testApplicationVersionID+query, strings.NewReader(body))
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(w, r)
	return w
}
func TestTesterRemoval_BR_TST_020_022_023_026_HTTPResult(t *testing.T) {
	for _, removed := range []bool{false, true} {
		for _, activeLink := range []bool{false, true} {
			h := &fakeTesterRemovalHandler{result: removalResult(t, removed, activeLink)}
			w := removalHTTP(removalServers(t, h), signToken(t, tokenOptions{claims: validClaims(fixedNow())}), "", "")
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status/cache %d/%s", w.Code, w.Header().Get("Cache-Control"))
			}
			var response testermembershipv1.RemoveApplicationTesterResponse
			if err := protojson.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			m := response.GetMembership()
			if response.GetRemoved() != removed || response.GetActiveJoinLinkExists() != activeLink || response.GetCapacity().GetActiveTesterCount() != 0 || response.GetCapacity().GetTesterLimit() != 100 || m.GetStatus() != "REMOVED" || m.GetRemovedBy() != "auth-123" || !m.GetRemovedAt().AsTime().Equal(fixedNow().Add(time.Hour)) || !m.GetJoinedAt().AsTime().Equal(fixedNow()) {
				t.Fatal("removal mapping mismatch")
			}
			if h.calls != 1 || h.identity.AuthID != "auth-123" || h.identity.DeveloperStatus != shared.DeveloperStatusApproved || h.app.String() != testApplicationID || h.membership.String() != testApplicationVersionID {
				t.Fatal("trusted identity/path mapping mismatch")
			}
		}
	}
}
func TestTesterRemoval_BR_TST_020_021_024_028_HTTPAuthAndErrors(t *testing.T) {
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
		{"membership", token, ReasonInvalidTesterMembershipId, testerdomain.ErrInvalidTesterMembershipId, 400},
		{"notfound", token, ReasonApplicationTesterMembershipNotFound, testerdomain.ErrApplicationTesterMembershipNotFound, 404},
		{"inconsistent", token, ReasonApplicationTesterStateInconsistent, fmt.Errorf("private-storage: %w", testerdomain.ErrApplicationTesterStateInconsistent), 500},
		{"internal", token, ReasonInternal, errors.New("private-secret-storage"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeTesterRemovalHandler{err: tc.err, result: removalResult(t, true, true)}
			w := removalHTTP(removalServers(t, h), tc.token, "", "")
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "private-") {
				t.Fatalf("status/reason %d %s", w.Code, w.Body.String())
			}
			if tc.err == nil && h.calls != 0 {
				t.Fatal("unauthenticated request reached handler")
			}
		})
	}
}
func TestTesterRemoval_BR_TST_021_026_028_RejectHTTPBodyAndQuery(t *testing.T) {
	for _, input := range []struct{ query, body string }{
		{"", `{"authId":"private-value"}`}, {"", `{"testerAuthId":"private-value"}`}, {"", `{"removedBy":"private-value","removedAt":"private-value"}`}, {"", `{"status":"ACTIVE","activeTesterCount":100}`}, {"", `{"revokeJoinLink":true}`}, {"", `{"confirm":true}`}, {"", `{}`}, {"", `private-malformed`},
		{"?authId=private-value", ""}, {"?membership_id=private-value", ""}, {"?application_id=private-value", ""}, {"?revokeJoinLink=true", ""}, {"?removed_by=private-value", ""}, {"?activeTesterCount=1", ""}, {"?bad=%zz", ""},
	} {
		h := &fakeTesterRemovalHandler{result: removalResult(t, true, true)}
		w := removalHTTP(removalServers(t, h), signToken(t, tokenOptions{claims: validClaims(fixedNow())}), input.query, input.body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), ReasonInvalidRemoveTesterRequest) || h.calls != 0 || strings.Contains(w.Body.String(), "private-") {
			t.Fatalf("input was accepted or leaked: %d %s", w.Code, w.Body.String())
		}
	}
	h := &fakeTesterRemovalHandler{result: removalResult(t, true, true)}
	w := removalHTTP(removalServers(t, h), "", "?authId=private-value", `private-malformed`)
	if w.Code != 401 || !strings.Contains(w.Body.String(), ReasonDeveloperIdentityRequired) || h.calls != 0 {
		t.Fatal("authentication must precede body validation")
	}
}
func TestTesterRemoval_BR_TST_024_StateInconsistencyAlertIsSafe(t *testing.T) {
	var log bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&log, nil)))
	defer slog.SetDefault(previous)
	h := &fakeTesterRemovalHandler{err: fmt.Errorf("private-token-hash-user-storage: %w", testerdomain.ErrApplicationTesterStateInconsistent)}
	_, err := NewTesterMembershipService(nil, h).RemoveApplicationTester(withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "private-admin", DeveloperStatus: shared.DeveloperStatusApproved}), &testermembershipv1.RemoveApplicationTesterRequest{ApplicationId: testApplicationID, MembershipId: testApplicationVersionID})
	if status.Code(err) != codes.Internal || errorReason(status.Convert(err)) != ReasonApplicationTesterStateInconsistent || !strings.Contains(log.String(), ReasonApplicationTesterStateInconsistent) || !strings.Contains(log.String(), `"level":"ERROR"`) || strings.Contains(log.String(), "private-") || strings.Contains(log.String(), testApplicationID) {
		t.Fatalf("unsafe or missing alert: %s", log.String())
	}
}
func TestTesterRemoval_BR_TST_020_022_024_028_GRPC(t *testing.T) {
	h := &fakeTesterRemovalHandler{result: removalResult(t, true, true)}
	servers := removalServers(t, h)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = servers.GRPC.Server.Serve(listener) }()
	t.Cleanup(servers.GRPC.Server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := testermembershipv1.NewTesterMembershipClient(connection)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, signToken(t, tokenOptions{claims: validClaims(fixedNow())})))
	request := &testermembershipv1.RemoveApplicationTesterRequest{ApplicationId: testApplicationID, MembershipId: testApplicationVersionID}
	var headers metadata.MD
	response, err := client.RemoveApplicationTester(ctx, request, grpc.Header(&headers))
	if err != nil || !response.GetRemoved() || response.GetMembership().GetRemovedBy() != "auth-123" {
		t.Fatalf("gRPC removal failed: %v", err)
	}
	if v := headers.Get("cache-control"); len(v) != 1 || v[0] != "no-store" {
		t.Fatal("no-store missing")
	}
	h.result = removalResult(t, false, true)
	response, err = client.RemoveApplicationTester(ctx, request)
	if err != nil || response.GetRemoved() {
		t.Fatal("idempotent removal mapping failed")
	}
	h.err = testerdomain.ErrApplicationTesterStateInconsistent
	_, err = client.RemoveApplicationTester(ctx, request)
	if status.Code(err) != codes.Internal || errorReason(status.Convert(err)) != ReasonApplicationTesterStateInconsistent {
		t.Fatal("state inconsistency must be INTERNAL")
	}
	h.err = nil
	calls := h.calls
	for _, tc := range []struct{ token, reason string }{{"", ReasonDeveloperIdentityRequired}, {"private-JWS", ReasonInvalidDeveloperIdentity}} {
		_, err := client.RemoveApplicationTester(metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, tc.token)), request)
		if status.Code(err) != codes.Unauthenticated || errorReason(status.Convert(err)) != tc.reason || strings.Contains(err.Error(), "private-") {
			t.Fatal("gRPC identity boundary failed")
		}
	}
	request.ProtoReflect().SetUnknown([]byte{0x18, 0x01})
	_, err = client.RemoveApplicationTester(ctx, request)
	if status.Code(err) != codes.InvalidArgument || errorReason(status.Convert(err)) != ReasonInvalidRemoveTesterRequest || h.calls != calls {
		t.Fatal("unknown fields reached handler")
	}
}
