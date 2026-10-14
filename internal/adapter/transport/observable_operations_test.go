package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	"github.com/go-kratos/kratos/v2/middleware"
	ktransport "github.com/go-kratos/kratos/v2/transport"
	kgrpc "github.com/go-kratos/kratos/v2/transport/grpc"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func TestServers_ObservabilityRecognizesGeneratedHTTPOperation(t *testing.T) {
	t.Parallel()
	known := map[string][]string{}
	observed := ""
	observer := func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, request any) (any, error) {
			info, ok := ktransport.FromServerContext(ctx)
			if !ok {
				t.Fatal("HTTP transport context is missing")
			}
			observed = info.Kind().String() + ".unmatched"
			if slices.Contains(known[info.Kind().String()], info.Operation()) {
				observed = info.Operation()
			}
			return next(ctx, request)
		}
	}
	handler := &fakeCreateApplicationHandler{result: newApplication(t, tokenSubject, fixedNow())}
	servers, err := NewServers(
		ServerConfig{
			HTTPAddr: "127.0.0.1:0", GRPCAddr: "127.0.0.1:0",
			ObservabilityMiddleware: observer,
			RegisterObservableOperations: func(kind string, operations []string) {
				known[kind] = operations
			},
		}, newTestVerifier(t), NewApplicationService(handler),
		NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil),
		NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil),
		NewTesterMembershipService(nil, nil), NewCatalogService(nil),
		NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil),
		NewOAuthClientService(nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/applications", strings.NewReader(`{"name":"Course_Table"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(IdentityHeader, signToken(t, tokenOptions{claims: validClaims(fixedNow())}))
	response := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || handler.calls != 1 {
		t.Fatalf("create status=%d handler calls=%d body=%s", response.Code, handler.calls, response.Body.String())
	}
	if observed != applicationv1.OperationApplicationCreateApplication {
		t.Fatalf("HTTP operation=%q, want %q", observed, applicationv1.OperationApplicationCreateApplication)
	}
}

func TestRegisterObservableOperations_OnlyRegisteredHTTPBindings(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"", http.MethodGet, http.MethodPost} {
		t.Run("method="+method, func(t *testing.T) {
			httpServer := khttp.NewServer()
			if method != "" {
				httpServer.Route("/").Handle(method, "/v1/applications", func(khttp.Context) error { return nil })
			}
			grpcServer := kgrpc.NewServer()
			applicationv1.RegisterApplicationServer(grpcServer, NewApplicationService(nil))
			known := map[string][]string{}
			registerObservableOperations(func(kind string, operations []string) { known[kind] = operations }, httpServer, grpcServer)
			operation := applicationv1.OperationApplicationCreateApplication
			if got := slices.Contains(known["http"], operation); got != (method == http.MethodPost) {
				t.Fatalf("HTTP method=%q registered operation=%t operations=%v", method, got, known["http"])
			}
			if !slices.Contains(known["grpc"], operation) || !slices.Contains(known["grpc"], "/grpc.health.v1.Health/Check") {
				t.Fatalf("gRPC operation registry=%v", known["grpc"])
			}
			if slices.Contains(known["http"], "/grpc.health.v1.Health/Check") || slices.Contains(known["http"], "/attacker/controlled/value") {
				t.Fatalf("HTTP registry includes an unregistered operation: %v", known["http"])
			}
		})
	}
}
