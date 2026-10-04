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

	applicationcatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_catalog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
)

type fakePublicCatalogHandler struct {
	page      *catalogdomain.PublicApplicationCatalogPage
	item      *catalogdomain.PublicApplicationCatalogItem
	err       error
	listCalls int
	getCalls  int
	identity  shared.AuthenticatedUserIdentity
	listQuery catalogusecase.ListPublicApplicationsQuery
	getQuery  catalogusecase.GetPublicApplicationQuery
}

func (handler *fakePublicCatalogHandler) List(_ context.Context, identity shared.AuthenticatedUserIdentity, query catalogusecase.ListPublicApplicationsQuery) (*catalogdomain.PublicApplicationCatalogPage, error) {
	handler.listCalls++
	handler.identity = identity
	handler.listQuery = query
	return handler.page, handler.err
}

func (handler *fakePublicCatalogHandler) Get(_ context.Context, identity shared.AuthenticatedUserIdentity, query catalogusecase.GetPublicApplicationQuery) (*catalogdomain.PublicApplicationCatalogItem, error) {
	handler.getCalls++
	handler.identity = identity
	handler.getQuery = query
	return handler.item, handler.err
}

func publicCatalogItem(t *testing.T, channel catalogdomain.LaunchChannel) *catalogdomain.PublicApplicationCatalogItem {
	t.Helper()
	description := "A reviewed public application"
	profile, err := catalogdomain.NewPublicApplicationProfile(testApplicationReviewID, "Study Tools", &description, nil)
	if err != nil {
		t.Fatal(err)
	}
	scalar := catalogdomain.NewPublicFilterScalar("STRING", "undergraduate", 0, false)
	rule := catalogdomain.NewPublicFilterPredicate("education.stage", "EQ", &scalar)
	filter, err := catalogdomain.NewPublicApplicationFilter(2, testApplicationVersionID, "RULE", &rule)
	if err != nil {
		t.Fatal(err)
	}
	item, err := catalogdomain.NewPublicApplicationCatalogItem(shared.ApplicationID(testApplicationID), profile, runtimeDescriptor(t, channel), filter)
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func publicCatalogServers(t *testing.T, handler *fakePublicCatalogHandler) *Servers {
	t.Helper()
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil), NewApplicationCatalogService(handler))
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func publicCatalogHTTP(servers *Servers, path, token, query, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path+query, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(IdentityHeader, token)
	}
	response := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(response, request)
	return response
}

func TestApplicationCatalog_AnonymousListAndAuthenticatedDetail(t *testing.T) {
	item := publicCatalogItem(t, catalogdomain.LaunchChannelGrey)
	page, err := catalogdomain.NewPublicApplicationCatalogPage([]*catalogdomain.PublicApplicationCatalogItem{item}, "next-page")
	if err != nil {
		t.Fatal(err)
	}
	listHandler := &fakePublicCatalogHandler{page: page}
	response := publicCatalogHTTP(publicCatalogServers(t, listHandler), ListPublicApplicationsInternalPath, "", "", `{"runtime":{"hostRpcApiMajor":4,"hostCapabilities":["camera.read.v1"]},"pageSize":12}`)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || listHandler.listCalls != 1 || listHandler.identity.AuthID != "" || listHandler.listQuery.PageSize != 12 {
		t.Fatalf("list=%d %s identity=%q query=%+v", response.Code, response.Body, listHandler.identity.AuthID, listHandler.listQuery)
	}
	if !strings.Contains(response.Body.String(), `"nextPageToken":"next-page"`) || !strings.Contains(response.Body.String(), `"mode":"FILTER_MODE_RULE"`) || !strings.Contains(response.Body.String(), `"channel":"LAUNCH_CHANNEL_GREY"`) {
		t.Fatalf("list body=%s", response.Body)
	}

	getHandler := &fakePublicCatalogHandler{item: item}
	path := strings.Replace(GetPublicApplicationInternalPath, "{application_id}", testApplicationID, 1)
	response = publicCatalogHTTP(publicCatalogServers(t, getHandler), path, ordinaryUserToken(t), "", `{"hostRpcApiMajor":4}`)
	if response.Code != http.StatusOK || getHandler.getCalls != 1 || getHandler.identity.AuthID != "auth-123" || getHandler.getQuery.ApplicationID != testApplicationID {
		t.Fatalf("get=%d %s identity=%q query=%+v", response.Code, response.Body, getHandler.identity.AuthID, getHandler.getQuery)
	}
}

func TestApplicationCatalog_InvalidCredentialAndHTTPShapeFailClosed(t *testing.T) {
	for _, test := range []struct {
		name, token, query, body, reason string
		status                           int
	}{
		{"invalid credential", "private-sentinel", "", `{"runtime":{"hostRpcApiMajor":4}}`, ReasonInvalidAuthenticatedUser, 401},
		{"query parameters", "", "?authId=private-sentinel", `{"runtime":{"hostRpcApiMajor":4}}`, ReasonInvalidApplicationCatalogRequest, 400},
		{"missing runtime", "", "", `{}`, ReasonInvalidApplicationCatalogRequest, 400},
		{"unknown field", "", "", `{"runtime":{"hostRpcApiMajor":4},"authId":"private-sentinel"}`, ReasonInvalidApplicationCatalogRequest, 400},
		{"nested unknown field", "", "", `{"runtime":{"hostRpcApiMajor":4,"channel":"TEST"}}`, ReasonInvalidApplicationCatalogRequest, 400},
		{"malformed body", "", "", `{"runtime":`, ReasonInvalidApplicationCatalogRequest, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := &fakePublicCatalogHandler{}
			response := publicCatalogHTTP(publicCatalogServers(t, handler), ListPublicApplicationsInternalPath, test.token, test.query, test.body)
			if response.Code != test.status || !strings.Contains(response.Body.String(), test.reason) || strings.Contains(response.Body.String(), "private-sentinel") || response.Header().Get("Cache-Control") != "private, no-store" || handler.listCalls != 0 {
				t.Fatalf("response=%d %s calls=%d", response.Code, response.Body, handler.listCalls)
			}
		})
	}
}

func TestApplicationCatalog_ErrorMappingAndSafeInvariantAlert(t *testing.T) {
	for _, test := range []struct {
		err    error
		reason string
		http   int
		grpc   codes.Code
	}{
		{catalogdomain.ErrInvalidPageSize, ReasonInvalidPageSize, 400, codes.InvalidArgument},
		{catalogdomain.ErrInvalidPageToken, ReasonInvalidPageToken, 400, codes.InvalidArgument},
		{catalogdomain.ErrPublicApplicationNotFound, ReasonPublicApplicationNotFound, 404, codes.NotFound},
		{catalogdomain.ErrApplicationCatalogStateInconsistent, ReasonApplicationCatalogStateInconsistent, 500, codes.Internal},
		{catalogdomain.NewInternalError(errors.New("private-database-sentinel")), ReasonInternal, 500, codes.Internal},
	} {
		t.Run(test.reason, func(t *testing.T) {
			handler := &fakePublicCatalogHandler{err: test.err}
			response := publicCatalogHTTP(publicCatalogServers(t, handler), ListPublicApplicationsInternalPath, "", "", `{"runtime":{"hostRpcApiMajor":4}}`)
			if response.Code != test.http || !strings.Contains(response.Body.String(), test.reason) || strings.Contains(response.Body.String(), "private-database-sentinel") || status.Code(toTransportError(test.err)) != test.grpc {
				t.Fatalf("response=%d %s grpc=%v", response.Code, response.Body, status.Code(toTransportError(test.err)))
			}
		})
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	service := NewApplicationCatalogService(&fakePublicCatalogHandler{err: catalogdomain.ErrApplicationCatalogStateInconsistent})
	_, _ = service.ListPublicApplications(context.Background(), &applicationcatalogv1.ListPublicApplicationsRequest{Query: &applicationcatalogv1.ListPublicApplicationsQuery{Runtime: &applicationcatalogv1.CatalogRuntimeQuery{HostRpcApiMajor: 4}}})
	if !strings.Contains(logs.String(), ReasonApplicationCatalogStateInconsistent) || strings.Contains(logs.String(), testApplicationID) {
		t.Fatalf("alert=%s", &logs)
	}
}

func TestApplicationCatalog_RejectsGRPCUnknownFields(t *testing.T) {
	handler := &fakePublicCatalogHandler{}
	service := NewApplicationCatalogService(handler)
	request := &applicationcatalogv1.ListPublicApplicationsRequest{Query: &applicationcatalogv1.ListPublicApplicationsQuery{Runtime: &applicationcatalogv1.CatalogRuntimeQuery{HostRpcApiMajor: 4}}}
	request.Query.Runtime.ProtoReflect().SetUnknown([]byte{0x78, 1})
	if _, err := service.ListPublicApplications(context.Background(), request); status.Code(err) != codes.InvalidArgument || handler.listCalls != 0 {
		t.Fatal("unknown request field accepted")
	}
}
