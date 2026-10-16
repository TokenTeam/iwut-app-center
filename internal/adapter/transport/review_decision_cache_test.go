package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	profilepb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	versionpb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	profiledomain "iwut-app-center/internal/profile/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
)

// UC-CONSOLE-006/007 retain private review facts even in decision errors.
// Exercise actual generated handlers and native gRPC, including binding and
// identity failures that occur before the business handler can set headers.
func TestReviewDecisions_PrivateNoStoreAcrossProtocols(t *testing.T) {
	claims := validClaims(fixedNow())
	delete(claims, "developer_status")
	claims["permissions"] = []string{"app.version.review", "app.profile.review"}
	token := signToken(t, tokenOptions{claims: claims})
	for _, profile := range []bool{false, true} {
		name := "version"
		if profile {
			name = "profile"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name                   string
				versionErr, profileErr error
				httpCode               int
				grpcCode               codes.Code
			}{
				{"success", nil, nil, 200, codes.OK},
				{"permission", reviewdomain.ErrApplicationReviewPermissionRequired, profiledomain.ErrApplicationProfileReviewPermissionRequired, 403, codes.PermissionDenied},
				{"conflict", reviewdomain.ErrApplicationReviewAlreadyDecided, profiledomain.ErrApplicationProfileReviewAlreadyDecided, 409, codes.Aborted},
				{"internal", reviewdomain.NewInternalError(nil), profiledomain.NewInternalError(nil), 500, codes.Internal},
				{"unavailable", reviewdomain.ErrDeveloperStatusUnavailable, reviewdomain.ErrDeveloperStatusUnavailable, 503, codes.Unavailable},
				{"anonymous", nil, nil, 401, codes.Unauthenticated},
				{"binding", nil, nil, 400, codes.InvalidArgument},
			} {
				if profile && tc.name == "unavailable" {
					continue
				} // Profile decisions have no external-status dependency.
				t.Run(tc.name, func(t *testing.T) {
					version := &fakeDecideApplicationVersionReviewHandler{result: transportDecisionResult(t, fixedNow()), err: tc.versionErr}
					prof := &fakeDecideProfileHandler{result: profileDecisionFixture(t, "APPROVE"), err: tc.profileErr}
					servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil),
						NewApplicationReviewService(nil, nil, version), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil),
						NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, prof), NewOAuthClientService(nil))
					if err != nil {
						t.Fatal(err)
					}
					path := "/v1/applications/" + testApplicationID + "/versions/" + testApplicationVersionID + "/reviews/" + testApplicationReviewID + "/decision"
					body := `{"outcome":"REJECT","expectedPolicyVersion":"app-version-review-v1","confirmedCheckIds":[],"reason":"Rejected"}`
					if profile {
						path = "/v1/applications/" + testApplicationID + "/profile-revisions/" + testProfileRevisionID + "/reviews/" + testApplicationReviewID + "/decision"
						body = profileApprovalJSON
					}
					if tc.name == "binding" {
						body = `{invalid`
					}
					req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					if tc.name != "anonymous" {
						req.Header.Set(IdentityHeader, token)
					}
					response := httptest.NewRecorder()
					servers.HTTP.ServeHTTP(response, req)
					if response.Code != tc.httpCode || response.Header().Get("Cache-Control") != "private, no-store" {
						t.Errorf("HTTP status/cache=%d/%q, want %d/private, no-store", response.Code, response.Header().Get("Cache-Control"), tc.httpCode)
					}
					if tc.name == "binding" {
						return
					} // JSON binding has no native equivalent.
					listener := bufconn.Listen(1 << 20)
					go func() { _ = servers.GRPC.Server.Serve(listener) }()
					t.Cleanup(servers.GRPC.Server.Stop)
					conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = conn.Close() })
					ctx := t.Context()
					if tc.name != "anonymous" {
						ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(IdentityHeader, token))
					}
					var headers metadata.MD
					if profile {
						_, err = profilepb.NewApplicationProfileReviewClient(conn).DecideApplicationProfileRevisionReview(ctx, &profilepb.DecideApplicationProfileRevisionReviewRequest{
							ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, ProfileReviewId: testApplicationReviewID,
							Command: &profilepb.DecideApplicationProfileRevisionReviewCommand{Outcome: profilepb.ProfileReviewDecisionAction_REJECT}}, grpc.Header(&headers))
					} else {
						_, err = versionpb.NewApplicationReviewClient(conn).DecideApplicationVersionReview(ctx, decisionRequest(), grpc.Header(&headers))
					}
					if status.Code(err) != tc.grpcCode {
						t.Fatalf("gRPC status=%v, want %v", status.Code(err), tc.grpcCode)
					}
					assertPrivateNoStoreMetadata(t, headers)
				})
			}
		})
	}
}
