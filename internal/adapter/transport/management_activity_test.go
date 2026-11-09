package transport

import (
	"context"
	"errors"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management_activity"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type activityTransportFake struct {
	page   ad.ManagementActivityPage
	detail ad.ManagementActivityDetail
	err    error
}

func (f *activityTransportFake) List(context.Context, shared.TrustedIdentity, string, ad.ManagementActivityQuery) (ad.ManagementActivityPage, error) {
	return f.page, f.err
}
func (f *activityTransportFake) Get(context.Context, shared.TrustedIdentity, string, string) (ad.ManagementActivityDetail, error) {
	return f.detail, f.err
}
func TestManagementActivityTransport_BRAPP067069070(t *testing.T) {
	s := NewApplicationManagementActivityService(nil)
	if _, err := s.ListApplicationManagementActivity(t.Context(), nil); status.Code(err) != codes.Unauthenticated {
		t.Fatal("identity first", err)
	}
	ctx := withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "owner", DeveloperStatus: shared.DeveloperStatusSuspended})
	app := "019a0000-0000-7000-8000-000000000001"
	id := (ad.ManagementActivityLocator{Source: 0, Phase: 1, ReviewID: app}).String()
	at := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	label := "historical label"
	a := ad.ManagementActivity{ActivityID: id, ApplicationID: shared.ApplicationID(app), Kind: ad.VersionReviewSubmitted, OccurredAt: at, VersionReview: &ad.VersionReviewActivityTarget{VersionID: app, ReviewID: app, Attempt: 1}, VersionLabelAtSubmission: &label}
	f := &activityTransportFake{detail: ad.ManagementActivityDetail{Activity: a, AsOf: at}, page: ad.ManagementActivityPage{Items: []ad.ManagementActivity{a}, AsOf: at}}
	s = NewApplicationManagementActivityService(f)
	req := &pb.GetApplicationManagementActivityRequest{ApplicationId: app, ActivityId: id}
	out, err := s.GetApplicationManagementActivity(ctx, req)
	if err != nil || out.RejectionReason == nil {
		t.Fatal(err)
	}
	raw, err := protojson.Marshal(out)
	if err != nil || !strings.Contains(string(raw), `"rejectionReason":null`) {
		t.Fatal("explicit null", string(raw), err)
	}
	list := &pb.ListApplicationManagementActivityRequest{ApplicationId: app, Query: &pb.ListApplicationManagementActivityQuery{}}
	if _, err := s.ListApplicationManagementActivity(ctx, list); err != nil {
		t.Fatal(err)
	}
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1)
	list.Query.ProtoReflect().SetUnknown(unknown)
	if _, err := s.ListApplicationManagementActivity(ctx, list); status.Code(err) != codes.InvalidArgument {
		t.Fatal("unknown nested field", err)
	}
	list.Query.ProtoReflect().SetUnknown(nil)
	for _, kind := range []pb.ApplicationManagementActivityKind{0, 99} {
		list.Query.Kinds = []pb.ApplicationManagementActivityKind{kind}
		if _, err := s.ListApplicationManagementActivity(ctx, list); status.Code(err) != codes.InvalidArgument {
			t.Fatal("unknown enum", err)
		}
	}
	list.Query.Kinds = nil
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{{ad.ErrManagementActivityNotFound, codes.NotFound}, {ad.ErrInvalidManagementActivityPageToken, codes.InvalidArgument}, {ad.ErrManagementActivityStateInconsistent, codes.Internal}, {ad.ErrManagementActivityUnavailable, codes.Unavailable}, {ad.ErrManagementActivityResponseTooLarge, codes.ResourceExhausted}, {errors.New("private cause"), codes.Internal}} {
		f.err = tc.err
		if _, err := s.GetApplicationManagementActivity(ctx, req); status.Code(err) != tc.code || strings.Contains(err.Error(), "private cause") {
			t.Fatal("error map", err)
		}
	}
	f.err = nil
	reason := strings.Repeat("\x01", 100000)
	f.detail.Activity.Kind = ad.VersionReviewRejected
	f.detail.Activity.ActivityID = (ad.ManagementActivityLocator{Source: 0, Phase: 0, ReviewID: app}).String()
	f.detail.RejectionReason = &reason
	req.ActivityId = f.detail.Activity.ActivityID
	if _, err := s.GetApplicationManagementActivity(ctx, req); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("JSON escaped response budget", err)
	}
}
func TestManagementActivityHTTP_BRAPP070_StrictInput(t *testing.T) {
	for _, tc := range []struct {
		method, body, query, media string
		want                       bool
	}{
		{"POST", "{}", "", "application/json", true}, {"POST", `{"kinds":["PROFILE_REVIEW_REJECTED"],"pageSize":1}`, "", "application/json", true},
		{"POST", `{"query":{}}`, "", "application/json", false}, {"POST", `{"actor":"other"}`, "", "application/json", false}, {"POST", "{} {}", "", "application/json", false}, {"POST", "null", "", "application/json", false}, {"POST", "{}", "pageSize=1", "application/json", false}, {"POST", strings.Repeat(" ", 16384) + "{}", "", "application/json", false},
		{"GET", "", "", "", true}, {"GET", "{}", "", "", false}, {"GET", "", "x=1", "", false},
	} {
		r := httptest.NewRequest(tc.method, "/v1/applications/id/management-activity:search", strings.NewReader(tc.body))
		r.URL.RawQuery = tc.query
		r.Header.Set("Content-Type", tc.media)
		if validManagementActivityHTTPBody(httptest.NewRecorder(), r) != tc.want {
			t.Fatal(tc.method, tc.body[:min(30, len(tc.body))])
		}
	}
	for _, err := range []error{ad.ErrInvalidManagementActivityQuery, ad.ErrManagementActivityNotFound, ad.ErrManagementActivityStateInconsistent, ad.ErrManagementActivityUnavailable} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", nil)
		managementActivityHTTPError(w, r, activityTransportError(r.Context(), err))
		if w.Code < 400 || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("private error", w.Code)
		}
	}
}
