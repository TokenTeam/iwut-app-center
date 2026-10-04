package transport

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
)

type fakeResolveLaunchTargetHandler struct {
	result   *catalogdomain.LaunchTargetDescriptor
	err      error
	calls    int
	identity shared.AuthenticatedUserIdentity
	query    catalogusecase.ResolveLaunchTargetQuery
}

func (handler *fakeResolveLaunchTargetHandler) Execute(_ context.Context, identity shared.AuthenticatedUserIdentity, query catalogusecase.ResolveLaunchTargetQuery) (*catalogdomain.LaunchTargetDescriptor, error) {
	handler.calls++
	handler.identity = identity
	handler.query = query
	return handler.result, handler.err
}

func runtimeDescriptor(t *testing.T, channel catalogdomain.LaunchChannel) *catalogdomain.LaunchTargetDescriptor {
	t.Helper()
	descriptor, err := catalogdomain.NewLaunchTargetDescriptor(shared.ApplicationID(testApplicationID), testApplicationReviewID, 9, channel, 4, testApplicationVersionID, "v2.0.0", "https://example.edu/runtime", 4, 5, []catalogdomain.CapabilityName{"camera.read.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func runtimeServers(t *testing.T, handler *fakeResolveLaunchTargetHandler) *Servers {
	t.Helper()
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil), NewRuntimeResolutionService(handler))
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func runtimeHTTP(servers *Servers, token, query, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/launch-target:resolve"+query, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(IdentityHeader, token)
	}
	response := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(response, request)
	return response
}

func TestRuntimeResolution_BR_RUN_011_014_017_AnonymousAndAuthenticatedHTTP(t *testing.T) {
	for _, test := range []struct {
		name, token string
		authID      shared.AuthID
		channel     catalogdomain.LaunchChannel
		proto       runtimev1.LaunchChannel
	}{
		{"anonymous stable", "", "", catalogdomain.LaunchChannelStable, runtimev1.LaunchChannel_LAUNCH_CHANNEL_STABLE},
		{"authenticated grey", ordinaryUserToken(t), "auth-123", catalogdomain.LaunchChannelGrey, runtimev1.LaunchChannel_LAUNCH_CHANNEL_GREY},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := &fakeResolveLaunchTargetHandler{result: runtimeDescriptor(t, test.channel)}
			response := runtimeHTTP(runtimeServers(t, handler), test.token, "", `{"hostRpcApiMajor":4,"hostCapabilities":["camera.read.v1","camera.read.v1"]}`)
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || handler.calls != 1 || handler.identity.AuthID != test.authID || handler.query.ApplicationID != testApplicationID || handler.query.HostRPCAPIMajor != 4 {
				t.Fatalf("response=%d %s identity=%q query=%+v", response.Code, response.Body, handler.identity.AuthID, handler.query)
			}
			var got runtimev1.LaunchTargetDescriptor
			if err := protojson.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.GetChannel() != test.proto || got.GetPublicationRevision() != 9 || got.GetVersionId() != testApplicationVersionID || len(got.GetRequiredCapabilities()) != 1 {
				t.Fatalf("descriptor=%v", &got)
			}
		})
	}
}

func TestRuntimeResolution_BR_RUN_011_017_InvalidCredentialAndSpoofingFailClosed(t *testing.T) {
	for _, test := range []struct {
		name, token, query, body, reason string
		status                           int
	}{
		{"invalid credential before body", "private-sentinel", "", `{"broken"`, ReasonInvalidAuthenticatedUser, 401},
		{"query identity", "", "?authId=private-sentinel", `{}`, ReasonInvalidResolveLaunchTargetRequest, 400},
		{"body identity", "", "", `{"authId":"private-sentinel"}`, ReasonInvalidResolveLaunchTargetRequest, 400},
		{"body channel", "", "", `{"channel":"TEST"}`, ReasonInvalidResolveLaunchTargetRequest, 400},
		{"duplicate aliases", "", "", `{"hostRpcApiMajor":4,"host_rpc_api_major":5}`, ReasonInvalidResolveLaunchTargetRequest, 400},
		{"wrong capabilities", "", "", `{"hostCapabilities":{"private-sentinel":1}}`, ReasonInvalidResolveLaunchTargetRequest, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := &fakeResolveLaunchTargetHandler{result: runtimeDescriptor(t, catalogdomain.LaunchChannelStable)}
			response := runtimeHTTP(runtimeServers(t, handler), test.token, test.query, test.body)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.reason) || strings.Contains(response.Body.String(), "private-sentinel") || response.Header().Get("Cache-Control") != "private, no-store" || handler.calls != 0 {
				t.Fatalf("response=%d %s calls=%d", response.Code, response.Body, handler.calls)
			}
		})
	}
}

func TestRuntimeResolution_BR_RUN_015_016_HTTPAndGRPCErrorMapping(t *testing.T) {
	for _, test := range []struct {
		err    error
		reason string
		http   int
		grpc   codes.Code
	}{
		{catalogdomain.ErrInvalidApplicationID, ReasonInvalidApplicationID, 400, codes.InvalidArgument},
		{catalogdomain.ErrInvalidHostRPCAPIMajor, ReasonInvalidHostRPCAPIMajor, 400, codes.InvalidArgument},
		{catalogdomain.ErrInvalidHostCapabilities, ReasonInvalidHostCapabilities, 400, codes.InvalidArgument},
		{catalogdomain.ErrApplicationNotFound, ReasonApplicationNotFound, 404, codes.NotFound},
		{catalogdomain.ErrApplicationLaunchTargetUnavailable, ReasonApplicationLaunchTargetUnavailable, 404, codes.NotFound},
		{catalogdomain.ErrApplicationRuntimeStateInconsistent, ReasonApplicationRuntimeStateInconsistent, 500, codes.Internal},
		{catalogdomain.NewInternalError(errors.New("private-database-sentinel")), ReasonInternal, 500, codes.Internal},
	} {
		t.Run(test.reason, func(t *testing.T) {
			handler := &fakeResolveLaunchTargetHandler{err: test.err}
			response := runtimeHTTP(runtimeServers(t, handler), "", "", `{"hostRpcApiMajor":4}`)
			if response.Code != test.http || !strings.Contains(response.Body.String(), test.reason) || strings.Contains(response.Body.String(), "private-database-sentinel") {
				t.Fatalf("HTTP=%d %s", response.Code, response.Body)
			}
			if got := status.Code(toTransportError(test.err)); got != test.grpc {
				t.Fatalf("gRPC=%v", got)
			}
		})
	}
}

func TestRuntimeResolution_BR_RUN_016_InvariantAlertIsSafe(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	handler := &fakeResolveLaunchTargetHandler{err: catalogdomain.ErrApplicationRuntimeStateInconsistent}
	runtimeHTTP(runtimeServers(t, handler), ordinaryUserToken(t), "", `{"hostRpcApiMajor":4}`)
	if !strings.Contains(logs.String(), ReasonApplicationRuntimeStateInconsistent) || !strings.Contains(logs.String(), `"level":"ERROR"`) || strings.Contains(logs.String(), "auth-123") {
		t.Fatalf("alert=%s", &logs)
	}
}

func TestRuntimeResolution_BR_RUN_017_RejectsGRPCUnknownFields(t *testing.T) {
	handler := &fakeResolveLaunchTargetHandler{result: runtimeDescriptor(t, catalogdomain.LaunchChannelStable)}
	service := NewRuntimeResolutionService(handler)
	for _, nested := range []bool{false, true} {
		request := &runtimev1.ResolveLaunchTargetRequest{ApplicationId: testApplicationID, Query: &runtimev1.ResolveLaunchTargetQuery{HostRpcApiMajor: 4}}
		if nested {
			request.Query.ProtoReflect().SetUnknown([]byte{0x78, 1})
		} else {
			request.ProtoReflect().SetUnknown([]byte{0x78, 1})
		}
		if _, err := service.ResolveLaunchTarget(context.Background(), request); status.Code(err) != codes.InvalidArgument || handler.calls != 0 {
			t.Fatal("unknown request field accepted")
		}
	}
}
