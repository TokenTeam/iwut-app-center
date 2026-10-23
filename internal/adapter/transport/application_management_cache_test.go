package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	managementv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	managementdomain "iwut-app-center/internal/management/domain"
	managementusecase "iwut-app-center/internal/management/usecase"
	"iwut-app-center/internal/shared"
)

type managementCacheHandler struct {
	fakeManagementHandler
	err error
}

func (h *managementCacheHandler) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, q managementusecase.ListQuery) (*managementdomain.Page, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.fakeManagementHandler.List(ctx, identity, q)
}

func (h *managementCacheHandler) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, id string) (*managementdomain.Detail, error) {
	if h.err != nil {
		return nil, h.err
	}
	return h.fakeManagementHandler.Get(ctx, identity, id)
}

func managementCacheServers(t *testing.T, handler ApplicationManagementQueryHandler) *Servers {
	t.Helper()
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil),
		NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil),
		NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil),
		NewApplicationManagementQueryService(handler))
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

// Use generated bindings so domain, identity and JSON binding failures all
// exercise the real error encoder, rather than a synthetic handler response.
func TestApplicationManagement_BRAPP038_BRAPP040_BRAPP042_PrivateNoStore(t *testing.T) {
	for _, detail := range []bool{false, true} {
		name, method, path := "list", http.MethodPost, "/v1/applications:search"
		if detail {
			name, method, path = "detail", http.MethodGet, "/v1/applications/"+testApplicationID
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name, reason string
				err          error
				httpCode     int
				grpcCode     codes.Code
			}{
				{"success", "", nil, 200, codes.OK},
				{"invalid-query", ReasonInvalidApplicationManagementRequest, managementdomain.ErrInvalidRequest, 400, codes.InvalidArgument},
				{"invalid-cursor", ReasonInvalidApplicationManagementPageToken, managementdomain.ErrInvalidPageToken, 400, codes.InvalidArgument},
				{"hidden-resource", ReasonApplicationManagementNotFound, managementdomain.ErrNotFound, 404, codes.NotFound},
				{"inconsistent", ReasonApplicationManagementStateInconsistent, managementdomain.ErrStateInconsistent, 500, codes.Internal},
				{"internal", ReasonInternal, managementdomain.NewInternalError(nil), 500, codes.Internal},
				{"anonymous", "", nil, 401, codes.Unauthenticated},
				{"invalid-identity", "", nil, 401, codes.Unauthenticated},
				{"binding", "", nil, 400, codes.InvalidArgument},
			} {
				if detail && tc.name == "binding" {
					continue // The detail binding has no JSON body.
				}
				t.Run(tc.name, func(t *testing.T) {
					servers := managementCacheServers(t, &managementCacheHandler{err: tc.err})
					body := `{}`
					if tc.name == "binding" {
						body = `{invalid`
					}
					token := ordinaryUserToken(t)
					if tc.name == "anonymous" {
						token = ""
					} else if tc.name == "invalid-identity" {
						token = "invalid-identity-sentinel"
					}
					request := httptest.NewRequest(method, path, strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					if token != "" {
						request.Header.Set(IdentityHeader, token)
					}
					response := httptest.NewRecorder()
					servers.HTTP.ServeHTTP(response, request)
					if response.Code != tc.httpCode || response.Header().Get("Cache-Control") != "private, no-store" {
						t.Errorf("HTTP status/cache=%d/%q, want %d/private, no-store", response.Code, response.Header().Get("Cache-Control"), tc.httpCode)
					}
					if tc.reason != "" && !strings.Contains(response.Body.String(), tc.reason) {
						t.Errorf("HTTP lost stable reason %s", tc.reason)
					}
					if tc.name == "binding" {
						return
					}
					listener := bufconn.Listen(1 << 20)
					go func() { _ = servers.GRPC.Server.Serve(listener) }()
					t.Cleanup(servers.GRPC.Server.Stop)
					conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = conn.Close() })
					ctx := t.Context()
					if token != "" {
						ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(IdentityHeader, token))
					}
					var headers metadata.MD
					client := managementv1.NewApplicationManagementQueryServiceClient(conn)
					if detail {
						_, err = client.GetMyApplication(ctx, &managementv1.GetMyApplicationRequest{ApplicationId: testApplicationID}, grpc.Header(&headers))
					} else {
						_, err = client.ListMyApplications(ctx, &managementv1.ListMyApplicationsRequest{}, grpc.Header(&headers))
					}
					if status.Code(err) != tc.grpcCode {
						t.Errorf("gRPC status=%v, want %v", status.Code(err), tc.grpcCode)
					}
					if tc.reason != "" && errorReason(status.Convert(err)) != tc.reason {
						t.Errorf("gRPC lost stable reason %s", tc.reason)
					}
					assertPrivateNoStoreMetadata(t, headers)
				})
			}
		})
	}
}
