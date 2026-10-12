package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	"github.com/go-kratos/kratos/v2/transport/http/binding"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationusecase "iwut-app-center/internal/publication/usecase"
	"iwut-app-center/internal/shared"
)

type fakeTestClearHandler struct {
	result *publicationdomain.PlaceInTestResult
	err    error
	calls  int
}

func (f *fakeTestClearHandler) Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.ClearTestSlotCommand) (*publicationdomain.PlaceInTestResult, error) {
	f.calls++
	return f.result, f.err
}

func TestAPIContract_UCAPP036_ClearTestRouteAndGeneratedBinding(t *testing.T) {
	descriptor := publicationv1.File_app_center_v1_application_publication_application_publication_proto.Services().ByName("ApplicationPublication").Methods().ByName("ClearTestSlot")
	rule := proto.GetExtension(descriptor.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetDelete() != ClearTestSlotInternalPath || rule.GetBody() != "" || ClearTestSlotGRPCMethod != publicationv1.ApplicationPublication_ClearTestSlot_FullMethodName || strings.TrimPrefix(ClearTestSlotExternalPath, ServicePrefix) != rule.GetDelete() {
		t.Fatal(rule)
	}
	if fields := messageFieldNames(t, &publicationv1.ClearTestSlotRequest{}); strings.Join(fields, ",") != "application_id,expected_publication_revision,rpc_api_major" {
		t.Fatal(fields)
	}
	url := binding.EncodeURL(rule.GetDelete(), &publicationv1.ClearTestSlotRequest{ApplicationId: testApplicationID, RpcApiMajor: 3, ExpectedPublicationRevision: 4}, true)
	if url != "/v1/applications/"+testApplicationID+"/publications/3/test-slot?expected_publication_revision=4" {
		t.Fatal("generated binding", url)
	}
}

func TestPublicationService_UCAPP036_StrictHTTPAndPrivateNoStore(t *testing.T) {
	set := publicationResult(t, nil, publicationdomain.ApplicationVersionID(testApplicationVersionID))
	candidate, _ := publicationdomain.NewTestClearCandidate(set.Publication(), 1)
	cleared, _ := candidate.Clear("018f7777-7777-7777-8777-777777777781", "admin", time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC))
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, tc := range []struct {
		name, query, body, token string
		err                      error
		want                     int
	}{
		{"success", "expected_publication_revision=1", "", token, nil, 200},
		{"missing", "", "", token, nil, 400},
		{"duplicate", "expected_publication_revision=1&expected_publication_revision=1", "", token, nil, 400},
		{"leading zero", "expected_publication_revision=01", "", token, nil, 400},
		{"plus", "expected_publication_revision=%2B1", "", token, nil, 400},
		{"zero", "expected_publication_revision=0", "", token, nil, 400},
		{"overflow", "expected_publication_revision=9223372036854775808", "", token, nil, 400},
		{"unknown", "expected_publication_revision=1&other=2", "", token, nil, 400},
		{"path override", "expected_publication_revision=1&application_id=other", "", token, nil, 400},
		{"body", "expected_publication_revision=1", "{}", token, nil, 400},
		{"whitespace body", "expected_publication_revision=1", " ", token, nil, 400},
		{"unauthenticated", "", "{}", "", nil, 401},
		{"closing", "expected_publication_revision=1", "", token, publicationdomain.ErrApplicationPublicationNotAllowed, 422},
		{"conflict", "expected_publication_revision=1", "", token, publicationdomain.ErrApplicationPublicationRevisionConflict, 409},
		{"inconsistent", "expected_publication_revision=1", "", token, publicationdomain.ErrApplicationPublicationStateInconsistent, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTestClearHandler{result: cleared, err: tc.err}
			s := NewApplicationPublicationServiceWithTestClear(nil, nil, nil, nil, nil, f)
			servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), s, NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodDelete, "/v1/applications/"+testApplicationID+"/publications/3/test-slot?"+tc.query, strings.NewReader(tc.body))
			if tc.token != "" {
				r.Header.Set(IdentityHeader, tc.token)
			}
			w := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(w, r)
			if w.Code != tc.want || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("status=%d cache=%q body=%s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
			}
			if tc.want == 400 || tc.want == 401 {
				if f.calls != 0 {
					t.Fatal("invalid request reached handler")
				}
			}
		})
	}
}
