package transport

import (
	"context"
	v1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/test_application_catalog"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	cd "iwut-app-center/internal/catalog/domain"
	cu "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/shared"
	"strings"
	"testing"
	"time"
)

type myTestHandlerFake struct {
	page   *cd.MyTestApplicationPage
	detail *cd.MyTestApplicationDetail
	err    error
	caller shared.AuthID
	calls  int
}

func (f *myTestHandlerFake) List(_ context.Context, id shared.AuthenticatedUserIdentity, _ cu.ListMyTestApplicationsQuery) (*cd.MyTestApplicationPage, error) {
	f.calls++
	f.caller = id.AuthID
	return f.page, f.err
}
func (f *myTestHandlerFake) Get(_ context.Context, id shared.AuthenticatedUserIdentity, _ cu.GetMyTestApplicationQuery) (*cd.MyTestApplicationDetail, error) {
	f.calls++
	f.caller = id.AuthID
	return f.detail, f.err
}
func myTestServers(t *testing.T, h *myTestHandlerFake) *Servers {
	t.Helper()
	s, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil), NewTestApplicationCatalogService(h))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestAPIContract_UCAPP038_RoutesAndPrivacy(t *testing.T) {
	s := v1.File_app_center_v1_test_application_catalog_test_application_catalog_proto.Services().ByName("TestApplicationCatalogService")
	for _, tc := range []struct{ name, path, body, operation string }{{"ListMyTestApplications", "/v1/test-applications:search", "query", ListMyTestApplicationsGRPCMethod}, {"GetMyTestApplication", "/v1/test-applications/{application_id}:resolve", "runtime", GetMyTestApplicationGRPCMethod}} {
		method := s.Methods().ByName(protoreflect.Name(tc.name))
		rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
		if rule.GetPost() != tc.path || rule.GetBody() != tc.body || tc.operation != "/app_center.v1.test_application_catalog.TestApplicationCatalogService/"+tc.name {
			t.Fatal("route drift")
		}
		if !strings.HasPrefix(ServicePrefix+rule.GetPost(), "/app-center/v1/test-applications") {
			t.Fatal("gateway mapping")
		}
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{{&v1.ListMyTestApplicationsRequest{}, "query"}, {&v1.ListQuery{}, "page_size,page_token,runtime"}, {&v1.GetMyTestApplicationRequest{}, "application_id,runtime"}, {&v1.RuntimeQuery{}, "host_capabilities,host_rpc_api_major"}, {&v1.OwnMembership{}, "joined_at,membership_id"}, {&v1.PublicProfile{}, "description,display_name,icon,profile_revision_id"}, {&v1.MyTestApplicationItem{}, "application_id,availability,launch_target,membership,missing_capabilities,profile"}, {&v1.MyTestApplicationPage{}, "as_of,items,next_page_token"}, {&v1.MyTestApplicationDetail{}, "as_of,item"}} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatal("private field drift", tc.message)
		}
	}
}
func TestUCAPP038_BR_CAT_014_017_HTTPStatesErrorsPrivacyNoStore(t *testing.T) {
	item, err := cd.NewMyTestApplicationItem(shared.ApplicationID(testApplicationID), cd.OwnTestMembership{MembershipID: cd.MyTestMembershipID(testApplicationVersionID), JoinedAt: time.Now()}, nil, cd.MyTestProfileRequired, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &myTestHandlerFake{page: &cd.MyTestApplicationPage{Items: []*cd.MyTestApplicationItem{item}, AsOf: time.Now()}, detail: &cd.MyTestApplicationDetail{Item: item, AsOf: time.Now()}}
	s := myTestServers(t, h)
	response := publicCatalogHTTP(s, "/v1/test-applications:search", ordinaryUserToken(t), "", `{"runtime":{"hostRpcApiMajor":1}}`)
	if response.Code != 200 || h.caller != "auth-123" || response.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(response.Body.String(), "TEST_AVAILABILITY_PROFILE_REQUIRED") {
		t.Fatal(response.Code, response.Body)
	}
	for _, forbidden := range []string{"testerAuthId", "joinedViaJoinLinkId", "adminId", "technicalName", "launchUrl", "requiredScopes", "filter"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Fatal("private projection", forbidden)
		}
	}
	for _, tc := range []struct {
		token, query, body string
		code               int
		reason             string
	}{{"", "", `bad`, 401, ReasonUserIdentityRequired}, {"bad", "", `{}`, 401, ReasonInvalidUserIdentity}, {ordinaryUserToken(t), "?pageSize=1", `{"runtime":{"hostRpcApiMajor":1}}`, 400, "ERROR_REASON_INVALID_MY_TEST_APPLICATION_QUERY"}, {ordinaryUserToken(t), "", `{"runtime":{"hostRpcApiMajor":1},"authId":"other"}`, 400, "ERROR_REASON_INVALID_MY_TEST_APPLICATION_QUERY"}, {ordinaryUserToken(t), "", `{"runtime":{"hostRpcApiMajor":1,"channel":"STABLE"}}`, 400, "ERROR_REASON_INVALID_MY_TEST_APPLICATION_QUERY"}, {ordinaryUserToken(t), "", `{"runtime":null}`, 400, "ERROR_REASON_INVALID_MY_TEST_APPLICATION_QUERY"}} {
		r := publicCatalogHTTP(s, "/v1/test-applications:search", tc.token, tc.query, tc.body)
		if r.Code != tc.code || r.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(r.Body.String(), tc.reason) {
			t.Fatal(r.Code, r.Body)
		}
	}
	for _, tc := range []struct {
		err    error
		http   int
		grpc   codes.Code
		reason string
	}{{cd.ErrMyTestApplicationNotFound, 404, codes.NotFound, "ERROR_REASON_MY_TEST_APPLICATION_NOT_FOUND"}, {cd.ErrMyTestApplicationStateInconsistent, 500, codes.Internal, "ERROR_REASON_MY_TEST_APPLICATION_STATE_INCONSISTENT"}, {cd.ErrMyTestApplicationUnavailable, 503, codes.Unavailable, "ERROR_REASON_MY_TEST_APPLICATION_UNAVAILABLE"}, {cd.ErrInvalidMyTestApplicationPageToken, 400, codes.InvalidArgument, "ERROR_REASON_INVALID_MY_TEST_APPLICATION_PAGE_TOKEN"}} {
		h.err = tc.err
		r := publicCatalogHTTP(s, "/v1/test-applications/"+testApplicationID+":resolve", ordinaryUserToken(t), "", `{"hostRpcApiMajor":1}`)
		if r.Code != tc.http || r.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(r.Body.String(), tc.reason) {
			t.Fatal(r.Code, r.Body)
		}
		ctx := withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "auth-123"})
		_, err := NewTestApplicationCatalogService(h).GetMyTestApplication(ctx, &v1.GetMyTestApplicationRequest{ApplicationId: testApplicationID, Runtime: &v1.RuntimeQuery{HostRpcApiMajor: 1}})
		if status.Code(err) != tc.grpc {
			t.Fatal(err)
		}
	}
	h.err = nil
	req := &v1.ListMyTestApplicationsRequest{Query: &v1.ListQuery{Runtime: &v1.RuntimeQuery{HostRpcApiMajor: 1}}}
	req.ProtoReflect().SetUnknown([]byte{0x78, 1})
	before := h.calls
	_, err = NewTestApplicationCatalogService(h).ListMyTestApplications(withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "auth-123"}), req)
	if status.Code(err) != codes.InvalidArgument || h.calls != before {
		t.Fatal("unknown request reached query", err)
	}
}
