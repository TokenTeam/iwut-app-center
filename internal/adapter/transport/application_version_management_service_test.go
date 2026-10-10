package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionusecase "iwut-app-center/internal/version/usecase"
)

type fakeApplicationVersionManagementQuery struct {
	page      *versiondomain.OwnedVersionPage
	detail    *versiondomain.OwnedVersionDetail
	err       error
	identity  shared.AuthenticatedUserIdentity
	listQuery versionusecase.ListApplicationVersionsQuery
}

func (f *fakeApplicationVersionManagementQuery) List(_ context.Context, identity shared.AuthenticatedUserIdentity, query versionusecase.ListApplicationVersionsQuery) (*versiondomain.OwnedVersionPage, error) {
	f.identity, f.listQuery = identity, query
	return f.page, f.err
}
func (f *fakeApplicationVersionManagementQuery) Get(_ context.Context, identity shared.AuthenticatedUserIdentity, _, _ string) (*versiondomain.OwnedVersionDetail, error) {
	f.identity = identity
	return f.detail, f.err
}

func applicationVersionManagementServers(t *testing.T, handler ApplicationVersionManagementQueryHandler) *Servers {
	t.Helper()
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionServiceWithQuery(nil, nil, handler), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func applicationVersionManagementPage() *versiondomain.OwnedVersionPage {
	now := time.Date(2026, 10, 10, 1, 2, 3, 0, time.UTC)
	reason := "fix redirects"
	return &versiondomain.OwnedVersionPage{Items: []versiondomain.OwnedVersion{{VersionID: testApplicationVersionID, ApplicationID: testApplicationID, Sequence: 2, VersionLabel: "v2", RPCApiMinVersion: 1, RPCApiMaxVersionExclusive: 3, ReviewStatus: "REJECTED", Revision: 3, CreatedBy: "auth-123", CreatedAt: now, UpdatedAt: now, LatestReview: &versiondomain.OwnedLatestReview{ReviewID: testApplicationReviewID, Attempt: 1, Status: "REJECTED", SubmittedBy: "auth-123", SubmittedAt: now, Decision: &versiondomain.OwnedReviewDecision{Outcome: "REJECTED", Reason: &reason, DecidedAt: now}}}}, NextPageToken: "next", AsOf: now}
}

func TestApplicationVersionManagement_BRVER019_BRVER021_BRVER025HTTPProjectionAndPrivacy(t *testing.T) {
	handler := &fakeApplicationVersionManagementQuery{page: applicationVersionManagementPage()}
	servers := applicationVersionManagementServers(t, handler)
	request := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/versions:search", strings.NewReader(`{"reviewStatus":"REJECTED","pageSize":7}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(IdentityHeader, ordinaryUserToken(t))
	response := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || handler.identity.AuthID != "auth-123" || handler.listQuery.ReviewStatus == nil || *handler.listQuery.ReviewStatus != "REJECTED" || handler.listQuery.PageSize != 7 {
		t.Fatalf("response=%d %s cache=%q handler=%#v", response.Code, response.Body, response.Header().Get("Cache-Control"), handler)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"nextPageToken":"next"`) || !strings.Contains(body, `"latestReview"`) || strings.Contains(body, "fix redirects") || strings.Contains(body, "submittedBy") || strings.Contains(body, "launchUrl") || strings.Contains(body, "updatedBy") || strings.Contains(body, "reviewer-auth-sentinel") || strings.Contains(body, "system-auth-sentinel") {
		t.Fatalf("list disclosure is wrong: %s", body)
	}
	handler.detail = &versiondomain.OwnedVersionDetail{Version: handler.page.Items[0], AsOf: handler.page.AsOf}
	request = httptest.NewRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/versions/"+testApplicationVersionID, nil)
	request.Header.Set(IdentityHeader, ordinaryUserToken(t))
	response = httptest.NewRecorder()
	servers.HTTP.ServeHTTP(response, request)
	body = response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"launchUrl"`) || !strings.Contains(body, `"createdBy":"auth-123"`) || strings.Contains(body, "updatedBy") || strings.Contains(body, "decidedBy") || strings.Contains(body, "reviewer-auth-sentinel") || strings.Contains(body, "system-auth-sentinel") {
		t.Fatalf("detail leaks Reviewer/SYSTEM identity: %d %s", response.Code, body)
	}

	for _, test := range []struct {
		token  string
		status int
		reason string
	}{
		{"", http.StatusUnauthorized, ReasonUserIdentityRequired},
		{"private-token-sentinel", http.StatusUnauthorized, ReasonInvalidUserIdentity},
	} {
		request = httptest.NewRequest(http.MethodGet, "/v1/applications/"+testApplicationID+"/versions/"+testApplicationVersionID, nil)
		if test.token != "" {
			request.Header.Set(IdentityHeader, test.token)
		}
		response = httptest.NewRecorder()
		servers.HTTP.ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Body.String(), test.reason) || strings.Contains(response.Body.String(), "private-token-sentinel") {
			t.Fatalf("identity response=%d %s cache=%q", response.Code, response.Body, response.Header().Get("Cache-Control"))
		}
	}
}

func TestApplicationVersionManagement_BRVER019_BRVER024_BRVER025GRPCHeadersAndErrors(t *testing.T) {
	handler := &fakeApplicationVersionManagementQuery{page: applicationVersionManagementPage()}
	servers := applicationVersionManagementServers(t, handler)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = servers.GRPC.Server.Serve(listener) }()
	t.Cleanup(servers.GRPC.Server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := applicationversionv1.NewApplicationVersionClient(connection)
	ctx := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(IdentityHeader, ordinaryUserToken(t)))
	var headers metadata.MD
	if _, err = client.ListApplicationVersions(ctx, &applicationversionv1.ListApplicationVersionsRequest{ApplicationId: testApplicationID}, grpc.Header(&headers)); err != nil {
		t.Fatal(err)
	}
	assertPrivateNoStoreMetadata(t, headers)

	handler.err = versiondomain.ErrApplicationVersionManagementNotFound
	headers = nil
	_, err = client.GetApplicationVersion(ctx, &applicationversionv1.GetApplicationVersionRequest{ApplicationId: testApplicationID, VersionId: testApplicationVersionID}, grpc.Header(&headers))
	if status.Code(err) != codes.NotFound || errorReason(status.Convert(err)) != ReasonApplicationVersionManagementNotFound {
		t.Fatalf("domain error=%v", err)
	}
	assertPrivateNoStoreMetadata(t, headers)

	headers = nil
	_, err = client.ListApplicationVersions(t.Context(), &applicationversionv1.ListApplicationVersionsRequest{ApplicationId: testApplicationID}, grpc.Header(&headers))
	if status.Code(err) != codes.Unauthenticated || errorReason(status.Convert(err)) != ReasonUserIdentityRequired {
		t.Fatalf("identity error=%v", err)
	}
	assertPrivateNoStoreMetadata(t, headers)
}
