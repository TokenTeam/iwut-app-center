package transport

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	pd "iwut-app-center/internal/profile/domain"
	pu "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

type fakeProfileManagementHandler struct {
	page   *pd.ManagedProfilePage
	detail *pd.ManagedProfileDetail
	err    error
	calls  int
}

func (f *fakeProfileManagementHandler) List(context.Context, shared.AuthenticatedUserIdentity, pu.ListProfileRevisionsQuery) (*pd.ManagedProfilePage, error) {
	f.calls++
	return f.page, f.err
}
func (f *fakeProfileManagementHandler) Get(context.Context, shared.AuthenticatedUserIdentity, pu.GetProfileRevisionQuery) (*pd.ManagedProfileDetail, error) {
	f.calls++
	return f.detail, f.err
}
func TestProfileManagementService_BRPRF047_054_056(t *testing.T) {
	at := time.Unix(123, 0)
	text := "\"\\\n"
	reason := "complete reason"
	id := "01890a5d-ac96-774b-bcce-b302099a8058"
	review := &pd.ManagedProfileReview{ProfileReviewID: id, Attempt: 1, Status: "REJECTED", SourceRevision: 1, SubmittedAt: at, Decision: &pd.ManagedProfileDecision{Outcome: "REJECTED", PolicyVersion: "historic-policy", Reason: &reason, DecidedAt: at}}
	item := pd.ManagedProfileRevision{ProfileRevisionID: id, Sequence: 1, ReviewStatus: "REJECTED", Revision: 3, DisplayName: "raw", Description: &text, CreatedBy: "creator", CreatedAt: at, UpdatedAt: at, LatestReview: review}
	f := &fakeProfileManagementHandler{page: &pd.ManagedProfilePage{Items: []pd.ManagedProfileRevision{item}, AsOf: at}, detail: &pd.ManagedProfileDetail{Profile: item, Reviews: []pd.ManagedProfileReview{*review}, AsOf: at}}
	service := NewApplicationProfileRevisionServiceWithQuery(nil, nil, f)
	if _, e := service.ListApplicationProfileRevisions(context.Background(), nil); status.Code(e) != codes.Unauthenticated || f.calls != 0 {
		t.Fatal("identity must precede input", e)
	}
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "current-admin"})
	page, e := service.ListApplicationProfileRevisions(ctx, &pb.ListApplicationProfileRevisionsRequest{})
	if e != nil {
		t.Fatal(e)
	}
	detail, e := service.GetApplicationProfileRevision(ctx, &pb.GetApplicationProfileRevisionRequest{})
	if e != nil {
		t.Fatal(e)
	}
	for _, message := range []proto.Message{page, detail} {
		raw, e := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(message)
		if e != nil {
			t.Fatal(e)
		}
		for _, key := range []string{"updatedBy", "submittedBy", "decidedBy", "snapshot", "confirmedCheckIds", "currentPolicy", "requiredCheckIds"} {
			if strings.Contains(string(raw), key) {
				t.Fatal("privacy", key)
			}
		}
		n, e := profileManagementWireSize(message)
		if e != nil || n != max(len(raw), proto.Size(message)) {
			t.Fatalf("wire size=%d %v", n, e)
		}
	}
	if page.Items[0].LatestReview == nil || detail.LatestReview.Decision.PolicyVersion != "historic-policy" || !proto.Equal(detail.LatestReview, detail.Reviews[0]) {
		t.Fatal("minimal/history projection")
	}
	unknown := &pb.ListApplicationProfileRevisionsRequest{}
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, e = service.ListApplicationProfileRevisions(ctx, unknown); status.Code(e) != codes.InvalidArgument {
		t.Fatal("unknown native fields", e)
	}
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{{pd.ErrProfileManagementNotFound, codes.NotFound}, {pd.ErrInvalidProfileManagementQuery, codes.InvalidArgument}, {pd.ErrInvalidProfileManagementPageToken, codes.InvalidArgument}, {pd.ErrProfileManagementStateInconsistent, codes.Internal}, {pd.ErrProfileManagementUnavailable, codes.Unavailable}, {pd.ErrProfileManagementResponseTooLarge, codes.ResourceExhausted}} {
		f.err = tc.err
		if _, e = service.GetApplicationProfileRevision(ctx, &pb.GetApplicationProfileRevisionRequest{}); status.Code(e) != tc.code {
			t.Fatalf("mapping %v: %v", tc.err, e)
		}
	}
	f.err = nil
	large := strings.Repeat("\"", pd.ProfileManagementResponseBudget)
	f.page.Items[0].Description = &large
	if _, e = service.ListApplicationProfileRevisions(ctx, &pb.ListApplicationProfileRevisionsRequest{}); status.Code(e) != codes.ResourceExhausted {
		t.Fatal("escape budget", e)
	}
	f.page.AsOf = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, e = service.ListApplicationProfileRevisions(ctx, &pb.ListApplicationProfileRevisionsRequest{}); status.Code(e) != codes.Internal {
		t.Fatal("unencodable data", e)
	}
	if errors.Is(e, pd.ErrProfileManagementResponseTooLarge) {
		t.Fatal("invalid data became size error")
	}
}
func TestProfileManagementHTTP_BRPRF047_055_StrictQuery(t *testing.T) {
	for _, tc := range []struct {
		query, body string
		kind        string
		valid       bool
	}{
		{"", "", "LIST", true}, {"?pageSize=100&reviewStatus=SUBMITTED", "", "LIST", true}, {"?reviewPageSize=1", "", "GET", true},
		{"?pageSize=0", "", "LIST", false}, {"?pageSize=101", "", "LIST", false}, {"?pageSize=1&pageSize=1", "", "LIST", false}, {"?page_size=1", "", "LIST", false}, {"?reviewPageSize=2", "", "LIST", false}, {"?reviewStatus=DRAFT", "", "GET", false}, {"?pageSize=%ff", "", "LIST", false}, {"?broken=%", "", "LIST", false}, {"", "{}", "LIST", false},
	} {
		r := httptest.NewRequest("GET", "http://example.test/v1/applications/a/profile-revisions"+tc.query, strings.NewReader(tc.body))
		if validProfileManagementHTTPInput(r, tc.kind) != tc.valid {
			t.Fatal(tc)
		}
	}
}
