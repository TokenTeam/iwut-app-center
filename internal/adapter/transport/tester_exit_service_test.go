package transport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
)

type exitErrorHandler struct{ err error }

func (h exitErrorHandler) Handle(context.Context, shared.AuthenticatedUserIdentity, shared.ApplicationID, td.ApplicationTesterMembershipID) (*td.LeaveApplicationAsTesterResult, error) {
	return nil, h.err
}
func TestUCAPP040BRTST050PrivateFailuresAcrossProtocols(t *testing.T) {
	for _, tc := range []struct {
		err    error
		reason string
		http   int
		grpc   codes.Code
	}{
		{td.ErrApplicationTesterExitUnavailable, "ERROR_REASON_APPLICATION_TESTER_EXIT_UNAVAILABLE", 503, codes.Unavailable},
		{td.ErrApplicationTesterStateInconsistent, ReasonApplicationTesterStateInconsistent, 500, codes.Internal},
		{errors.New("private-driver-identity-secret"), ReasonInternal, 500, codes.Internal},
		{nil, ReasonApplicationTesterStateInconsistent, 500, codes.Internal},
	} {
		t.Run(tc.reason+tc.grpc.String(), func(t *testing.T) {
			servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipServiceWithExit(nil, nil, exitErrorHandler{tc.err}), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
			if err != nil {
				t.Fatal(err)
			}
			token := ordinaryUserToken(t)
			r := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/tester-memberships/"+testApplicationVersionID+":leave", nil)
			r.Header.Set(IdentityHeader, token)
			w := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(w, r)
			if w.Code != tc.http || !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "private-driver") || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
			}
			listener := bufconn.Listen(1 << 20)
			go func() { _ = servers.GRPC.Server.Serve(listener) }()
			t.Cleanup(servers.GRPC.Server.Stop)
			conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(IdentityHeader, token))
			var headers metadata.MD
			_, err = pb.NewTesterMembershipClient(conn).LeaveApplicationAsTester(ctx, &pb.LeaveApplicationAsTesterRequest{ApplicationId: testApplicationID, MembershipId: testApplicationVersionID}, grpc.Header(&headers))
			if status.Code(err) != tc.grpc || errorReason(status.Convert(err)) != tc.reason || len(headers.Get("cache-control")) != 1 || headers.Get("cache-control")[0] != "private, no-store" {
				t.Fatal("gRPC private failure", err, headers)
			}
		})
	}
}
