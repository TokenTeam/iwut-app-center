package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
)

type fakeResolveTestLaunchHandler struct {
	result   *catalogdomain.TestLaunchDescriptor
	err      error
	calls    int
	identity shared.AuthenticatedUserIdentity
	query    catalogusecase.ResolveTestLaunchTargetQuery
}

func (h *fakeResolveTestLaunchHandler) Execute(_ context.Context, identity shared.AuthenticatedUserIdentity, query catalogusecase.ResolveTestLaunchTargetQuery) (*catalogdomain.TestLaunchDescriptor, error) {
	h.calls++
	h.identity = identity
	h.query = query
	return h.result, h.err
}
func catalogDescriptor(t *testing.T) *catalogdomain.TestLaunchDescriptor {
	t.Helper()
	d, err := catalogdomain.NewTestLaunchDescriptor(shared.ApplicationID(testApplicationID), testApplicationReviewID, 3, 4, testApplicationVersionID, "v1.2.0-beta", "https://example.edu/test", 4, 5, []catalogdomain.CapabilityName{"camera.read.v1", "user.profile.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func catalogServers(t *testing.T, h *fakeResolveTestLaunchHandler) *Servers {
	t.Helper()
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(h), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	return servers
}
func catalogHTTP(servers *Servers, token, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/test-launch:resolve"+query, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	w := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(w, r)
	return w
}
func TestCatalog_BR_RUN_001_006_009_HTTPOrdinaryIdentityDescriptor(t *testing.T) {
	h := &fakeResolveTestLaunchHandler{result: catalogDescriptor(t)}
	w := catalogHTTP(catalogServers(t, h), ordinaryUserToken(t), "", `{"hostRpcApiMajor":4,"hostCapabilities":["user.profile.v1","camera.read.v1","camera.read.v1"]}`)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("response=%d %s", w.Code, w.Body)
	}
	if h.calls != 1 || h.identity.AuthID != "auth-123" || h.query.ApplicationID != testApplicationID || h.query.HostRPCAPIMajor != 4 || len(h.query.HostCapabilities) != 3 {
		t.Fatalf("mapping=%+v %+v", h.identity, h.query)
	}
	var got catalogv1.TestLaunchDescriptor
	if err := protojson.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GetPublicationRevision() != 3 || got.GetRpcApiMajor() != 4 || got.GetVersionId() != testApplicationVersionID || got.GetLaunchUrl() != "https://example.edu/test" || got.GetRpcApiMinVersion() != 4 || got.GetRpcApiMaxVersionExclusive() != 5 || len(got.GetRequiredCapabilities()) != 2 || len(got.GetRequiredScopes()) != 1 || len(got.GetOptionalScopes()) != 1 {
		t.Fatalf("descriptor=%v", &got)
	}
	for _, secret := range []string{"auth-123", "membership", "review", "secret"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("unexpected private field %s", secret)
		}
	}
}
func TestCatalog_BR_RUN_001_009_HTTPRejectsSpoofingAndSanitizesBinding(t *testing.T) {
	for _, tc := range []struct {
		name, token, query, body, reason string
		code                             int
	}{
		{"auth before malformed", "", "", `{"hostCapabilities":["private-sentinel"`, ReasonAuthenticatedUserRequired, 401},
		{"invalid token", "private-sentinel", "", `{}`, ReasonInvalidAuthenticatedUser, 401},
		{"query major", ordinaryUserToken(t), "?query.hostRpcApiMajor=9", `{"hostRpcApiMajor":4}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"query identity", ordinaryUserToken(t), "?authId=private-sentinel", `{}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"body identity", ordinaryUserToken(t), "", `{"authId":"private-sentinel"}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"body target", ordinaryUserToken(t), "", `{"versionId":"private-sentinel"}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"nested identity", ordinaryUserToken(t), "", `{"query":{"authId":"private-sentinel"}}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"wrong major type", ordinaryUserToken(t), "", `{"hostRpcApiMajor":"private-sentinel"}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"wrong caps type", ordinaryUserToken(t), "", `{"hostCapabilities":{"private-sentinel":1}}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"ambiguous aliases", ordinaryUserToken(t), "", `{"hostRpcApiMajor":4,"host_rpc_api_major":9}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"duplicate key", ordinaryUserToken(t), "", `{"hostRpcApiMajor":4,"hostRpcApiMajor":9}`, ReasonInvalidResolveTestLaunchRequest, 400},
		{"trailing data", ordinaryUserToken(t), "", `{} {"private-sentinel":1}`, ReasonInvalidResolveTestLaunchRequest, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeResolveTestLaunchHandler{result: catalogDescriptor(t)}
			w := catalogHTTP(catalogServers(t, h), tc.token, tc.query, tc.body)
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.reason) || w.Header().Get("Cache-Control") != "private, no-store" || h.calls != 0 || strings.Contains(w.Body.String(), "private-sentinel") {
				t.Fatalf("response=%d %s calls=%d", w.Code, w.Body, h.calls)
			}
		})
	}
}
func TestCatalog_BR_RUN_004_005_009_HTTPAndGRPCErrorMapping(t *testing.T) {
	missing := catalogdomain.NewHostCapabilitiesInsufficientError([]catalogdomain.CapabilityName{"user.profile.v1", "camera.read.v1", "camera.read.v1"})
	for _, tc := range []struct {
		err    error
		reason string
		http   int
		grpc   codes.Code
	}{
		{catalogdomain.ErrAuthenticatedUserRequired, ReasonAuthenticatedUserRequired, 401, codes.Unauthenticated},
		{catalogdomain.ErrInvalidApplicationID, ReasonInvalidApplicationID, 400, codes.InvalidArgument},
		{catalogdomain.ErrInvalidHostRPCAPIMajor, ReasonInvalidHostRPCAPIMajor, 400, codes.InvalidArgument},
		{catalogdomain.ErrInvalidHostCapabilities, ReasonInvalidHostCapabilities, 400, codes.InvalidArgument},
		{catalogdomain.ErrApplicationNotFound, ReasonApplicationNotFound, 404, codes.NotFound},
		{catalogdomain.ErrApplicationTesterRequired, ReasonApplicationTesterRequired, 403, codes.PermissionDenied},
		{catalogdomain.ErrApplicationTestTargetUnavailable, ReasonApplicationTestTargetUnavailable, 404, codes.NotFound},
		{missing, ReasonHostCapabilitiesInsufficient, 422, codes.FailedPrecondition},
		{catalogdomain.ErrApplicationTestPublicationInconsistent, ReasonApplicationTestPublicationInconsistent, 503, codes.Unavailable},
		{catalogdomain.NewInternalError(errors.New("private-database-sentinel")), ReasonInternal, 500, codes.Internal},
		{errors.New("private-database-sentinel"), ReasonInternal, 500, codes.Internal},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			h := &fakeResolveTestLaunchHandler{err: tc.err}
			w := catalogHTTP(catalogServers(t, h), ordinaryUserToken(t), "", `{"hostRpcApiMajor":4}`)
			if w.Code != tc.http || !strings.Contains(w.Body.String(), tc.reason) || w.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(w.Body.String(), "private-database-sentinel") {
				t.Fatalf("HTTP=%d %s", w.Code, w.Body)
			}
			st := status.Convert(toTransportError(tc.err))
			if st.Code() != tc.grpc {
				t.Fatalf("gRPC=%v", st)
			}
			info := st.Details()[0].(*errdetails.ErrorInfo)
			if info.Reason != tc.reason {
				t.Fatalf("reason=%s", info.Reason)
			}
			if tc.err == missing {
				if len(info.Metadata) != 1 || info.Metadata["missingCapabilities"] != `["camera.read.v1","user.profile.v1"]` {
					t.Fatalf("details=%v", info.Metadata)
				}
				var body struct {
					Metadata map[string]string `json:"metadata"`
				}
				if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Metadata["missingCapabilities"] != info.Metadata["missingCapabilities"] {
					t.Fatalf("HTTP details=%s", w.Body)
				}
			} else if len(info.Metadata) != 0 {
				t.Fatal("unexpected error details")
			}
		})
	}
}
func TestCatalog_BR_RUN_004_InvariantAlertIsSafe(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	h := &fakeResolveTestLaunchHandler{err: catalogdomain.ErrApplicationTestPublicationInconsistent}
	catalogHTTP(catalogServers(t, h), ordinaryUserToken(t), "", `{"hostRpcApiMajor":4}`)
	if !strings.Contains(logs.String(), ReasonApplicationTestPublicationInconsistent) || !strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), "auth-123") {
		t.Fatalf("alert=%s", &logs)
	}
}
func TestCatalog_BR_RUN_001_009_RejectsGRPCUnknownFields(t *testing.T) {
	h := &fakeResolveTestLaunchHandler{result: catalogDescriptor(t)}
	s := NewCatalogService(h)
	for _, nested := range []bool{false, true} {
		r := &catalogv1.ResolveTestLaunchTargetRequest{ApplicationId: testApplicationID, Query: &catalogv1.ResolveTestLaunchTargetQuery{HostRpcApiMajor: 4}}
		if nested {
			r.Query.ProtoReflect().SetUnknown([]byte{0x78, 1})
		} else {
			r.ProtoReflect().SetUnknown([]byte{0x78, 1})
		}
		_, err := s.ResolveTestLaunchTarget(withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "auth-123"}), r)
		if status.Code(err) != codes.InvalidArgument || h.calls != 0 {
			t.Fatal("unknown request field accepted")
		}
	}
}
