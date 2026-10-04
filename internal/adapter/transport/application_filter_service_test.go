package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	filterv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_filter"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	filterdomain "iwut-app-center/internal/filter/domain"
	filterusecase "iwut-app-center/internal/filter/usecase"
	"iwut-app-center/internal/shared"
)

type fakeApplicationFilterHandlers struct {
	filter       *filterdomain.ApplicationFilter
	setResult    *filterdomain.ChangeResult
	clearResult  *filterdomain.ChangeResult
	err          error
	getCalls     int
	setCalls     int
	clearCalls   int
	identity     shared.DeveloperIdentity
	application  string
	setCommand   filterusecase.SetCommand
	clearCommand filterusecase.ClearCommand
}

func (h *fakeApplicationFilterHandlers) Get(_ context.Context, identity shared.DeveloperIdentity, applicationID string) (*filterdomain.ApplicationFilter, error) {
	h.getCalls++
	h.identity, h.application = identity, applicationID
	return h.filter, h.err
}
func (h *fakeApplicationFilterHandlers) Set(_ context.Context, identity shared.DeveloperIdentity, applicationID string, command filterusecase.SetCommand) (*filterdomain.ChangeResult, error) {
	h.setCalls++
	h.identity, h.application, h.setCommand = identity, applicationID, command
	return h.setResult, h.err
}
func (h *fakeApplicationFilterHandlers) Clear(_ context.Context, identity shared.DeveloperIdentity, applicationID string, command filterusecase.ClearCommand) (*filterdomain.ChangeResult, error) {
	h.clearCalls++
	h.identity, h.application, h.clearCommand = identity, applicationID, command
	return h.clearResult, h.err
}

func publishedFilter(t *testing.T) *filterdomain.ApplicationFilter {
	t.Helper()
	filter, err := filterdomain.NewDefaultApplicationFilter(testApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := filterdomain.NewStringScalar("CN")
	rule, err := filterdomain.NewPredicate("profile.country", filterdomain.PredicateOperatorEQ, &value)
	if err != nil {
		t.Fatal(err)
	}
	result, _, err := filter.PublishRule("01890f5a-e810-7cc3-98c8-8c6d5d8b4c24", rule, "auth-123", time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func newFilterTestServers(t *testing.T, handler ApplicationFilterHandlers) *Servers {
	t.Helper()
	servers, err := NewServers(
		ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil),
		NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil),
		NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil),
		NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil), NewApplicationFilterService(handler),
	)
	if err != nil {
		t.Fatalf("NewServers() error = %v", err)
	}
	return servers
}

func TestAPIContract_UCAPP022_RoutesFieldsAndErrorReasons(t *testing.T) {
	service := filterv1.File_app_center_v1_application_filter_application_filter_proto.Services().ByName("ApplicationFilterService")
	for _, testCase := range []struct {
		name, verb, body, operation, fullMethod string
	}{
		{"GetApplicationFilter", http.MethodGet, "", GetApplicationFilterGRPCMethod, filterv1.ApplicationFilterService_GetApplicationFilter_FullMethodName},
		{"SetApplicationFilter", http.MethodPut, "command", SetApplicationFilterGRPCMethod, filterv1.ApplicationFilterService_SetApplicationFilter_FullMethodName},
		{"ClearApplicationFilter", http.MethodDelete, "", ClearApplicationFilterGRPCMethod, filterv1.ApplicationFilterService_ClearApplicationFilter_FullMethodName},
	} {
		method := service.Methods().ByName(protoreflectName(testCase.name))
		rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
		path := rule.GetGet()
		if testCase.verb == http.MethodPut {
			path = rule.GetPut()
		}
		if testCase.verb == http.MethodDelete {
			path = rule.GetDelete()
		}
		if path != ApplicationFilterInternalPath || rule.GetBody() != testCase.body || testCase.operation != testCase.fullMethod || ApplicationFilterExternalPath != ServicePrefix+ApplicationFilterInternalPath {
			t.Fatalf("%s contract drift: %v", testCase.name, rule)
		}
	}
	for _, spec := range filterDomainErrorSpecs {
		if _, ok := filterv1.ErrorReason_value[spec.reason]; !ok {
			t.Fatalf("missing error reason %s", spec.reason)
		}
	}
}

func TestApplicationFilter_BR_FLT_001_010_HTTPAndGRPCUseSameHandler(t *testing.T) {
	t.Parallel()

	published := publishedFilter(t)
	handler := &fakeApplicationFilterHandlers{filter: published, setResult: filterdomain.NewChangeResult(published, true)}
	servers := newFilterTestServers(t, handler)
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	path := strings.Replace(ApplicationFilterInternalPath, "{application_id}", testApplicationID, 1)
	body := `{"expectedRevision":"0","rule":{"predicate":{"fieldKey":"profile.country","operator":"FILTER_PREDICATE_OPERATOR_EQ","value":{"stringValue":"CN"}}}}`
	request := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(IdentityHeader, token)
	recorder := httptest.NewRecorder()
	servers.HTTP.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || recorder.Header().Get("ETag") != `"1"` {
		t.Fatalf("HTTP status=%d ETag=%q body=%s", recorder.Code, recorder.Header().Get("ETag"), recorder.Body.String())
	}
	var response filterv1.SetApplicationFilterResponse
	if err := protojson.Unmarshal(recorder.Body.Bytes(), &response); err != nil || !response.GetChanged() || response.GetFilter().GetCurrentRevision().GetRule().GetPredicate().GetValue().GetStringValue() != "CN" {
		t.Fatalf("HTTP response=%v decode=%v", &response, err)
	}
	if handler.setCalls != 1 || handler.identity.AuthID != "auth-123" || handler.application != testApplicationID || handler.setCommand.ExpectedRevision != 0 {
		t.Fatalf("HTTP handler state=%#v", handler)
	}

	listener := bufconn.Listen(1 << 20)
	go func() { _ = servers.GRPC.Server.Serve(listener) }()
	t.Cleanup(servers.GRPC.Server.Stop)
	connection, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(IdentityHeader, token))
	got, err := filterv1.NewApplicationFilterServiceClient(connection).GetApplicationFilter(ctx, &filterv1.GetApplicationFilterRequest{ApplicationId: testApplicationID})
	if err != nil || got.GetFilter().GetRevision() != 1 || got.GetFilter().GetEffectiveMode() != filterv1.FilterMode_FILTER_MODE_RULE || handler.getCalls != 1 {
		t.Fatalf("gRPC response=%v error=%v calls=%d", got, err, handler.getCalls)
	}
}

func TestApplicationFilter_BR_FLT_004_HTTPRejectsUnknownMissingAndQueryInputsBeforeHandler(t *testing.T) {
	t.Parallel()

	handler := &fakeApplicationFilterHandlers{setResult: filterdomain.NewChangeResult(publishedFilter(t), true)}
	servers := newFilterTestServers(t, handler)
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	path := strings.Replace(ApplicationFilterInternalPath, "{application_id}", testApplicationID, 1)
	for _, testCase := range []struct {
		name, suffix, body string
	}{
		{name: "query", suffix: "?unexpected=1", body: `{"expectedRevision":"0","rule":{"predicate":{"fieldKey":"profile.country","operator":"FILTER_PREDICATE_OPERATOR_EXISTS"}}}`},
		{name: "unknown", body: `{"expectedRevision":"0","rule":{"predicate":{"fieldKey":"profile.country","operator":"FILTER_PREDICATE_OPERATOR_EXISTS"}},"revision":"9"}`},
		{name: "missing revision", body: `{"rule":{"predicate":{"fieldKey":"profile.country","operator":"FILTER_PREDICATE_OPERATOR_EXISTS"}}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, path+testCase.suffix, strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(IdentityHeader, token)
			recorder := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), ReasonInvalidApplicationFilter) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
	if handler.setCalls != 0 {
		t.Fatalf("handler calls=%d, want 0", handler.setCalls)
	}
}
