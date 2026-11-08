package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_platform_query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

func TestPlatformApplicationTransport_UCAPP051_BRAPP063066(t *testing.T) {
	s := NewApplicationPlatformQueryService(nil)
	if _, err := s.SearchPlatformApplications(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	without := withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "owner", DeveloperStatus: shared.DeveloperStatusApproved, Permissions: []string{usecase.PermissionApplicationSuspend}})
	if _, err := s.GetPlatformApplicationSummary(without, nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal("permission before malformed input", err)
	}
	authorized := withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "reader", Permissions: []string{usecase.PermissionApplicationSearch}})
	unknown := &pb.SearchPlatformApplicationsRequest{Query: &pb.SearchPlatformApplicationsQuery{}}
	unknown.Query.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	if _, err := s.SearchPlatformApplications(authorized, unknown); status.Code(err) != codes.InvalidArgument {
		t.Fatal("unknown native input", err)
	}
	for _, err := range []error{domain.ErrApplicationPlatformSearchRequired, domain.ErrInvalidPlatformApplicationQuery, domain.ErrInvalidPlatformApplicationPageToken, domain.ErrPlatformApplicationNotFound, domain.ErrPlatformApplicationStateInconsistent, domain.ErrPlatformApplicationUnavailable} {
		w := httptest.NewRecorder()
		platformApplicationHTTPError(w, httptest.NewRequest(http.MethodPost, platformApplicationSearchPath, nil), platformApplicationError(err))
		if w.Code < 400 || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("private error", w.Code)
		}
	}
}
func TestPlatformApplicationHTTP_UCAPP051_BRAPP066_StrictInput(t *testing.T) {
	for _, tc := range []struct {
		method, body, query, media string
		want                       bool
	}{
		{"POST", "{}", "", "application/json", true}, {"POST", `{"namePrefix":"App","lifecycleStatuses":["CLOSED"]}`, "", "application/json", true},
		{"POST", `{"query":{}}`, "", "application/json", false}, {"POST", `{"permissions":["app.application.search"]}`, "", "application/json", false},
		{"POST", "{} {}", "", "application/json", false}, {"POST", "null", "", "application/json", false}, {"POST", "{}", "pageSize=1", "application/json", false}, {"POST", strings.Repeat(" ", 16*1024) + "{}", "", "application/json", false},
		{"GET", "", "", "", true}, {"GET", "{}", "", "", false}, {"GET", "", "applicationId=other", "", false},
	} {
		r := httptest.NewRequest(tc.method, platformApplicationSearchPath, strings.NewReader(tc.body))
		r.URL.RawQuery = tc.query
		r.Header.Set("Content-Type", tc.media)
		if validPlatformApplicationHTTPBody(httptest.NewRecorder(), r) != tc.want {
			t.Fatal(tc.method, tc.body[:min(len(tc.body), 40)], tc.query)
		}
	}
}
