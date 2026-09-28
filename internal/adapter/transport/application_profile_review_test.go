package transport

import (
	"bytes"
	"context"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	"github.com/go-kratos/kratos/v2/transport/http/binding"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeSubmitProfileHandler struct {
	calls    int
	command  profileusecase.SubmitApplicationProfileRevisionReviewCommand
	identity shared.DeveloperIdentity
	result   *profiledomain.ApplicationProfileSubmission
	err      error
}

func (h *fakeSubmitProfileHandler) Handle(_ context.Context, i shared.DeveloperIdentity, c profileusecase.SubmitApplicationProfileRevisionReviewCommand) (*profiledomain.ApplicationProfileSubmission, error) {
	h.calls++
	h.command = c
	h.identity = i
	return h.result, h.err
}
func submitProfileServers(t *testing.T, h *fakeSubmitProfileHandler) *Servers {
	t.Helper()
	s, e := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(h, nil), NewOAuthClientService(nil))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func submitProfileHTTP(s *Servers, token, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/profile-revisions/"+testProfileRevisionID+"/reviews"+query, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", `"999"`)
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	w := httptest.NewRecorder()
	s.HTTP.ServeHTTP(w, r)
	return w
}
func TestProfileReview_BR_PRF_015_021_StrictCommandBinding(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	name, _ := profiledomain.NewApplicationDisplayName("name")
	d, _ := profiledomain.NewDraftApplicationProfileRevision(testProfileRevisionID, testApplicationID, name, nil, nil, "creator", time.Unix(10, 0))
	v, _ := d.AssignSequence(1)
	result, _ := v.SubmitDraft(1, profiledomain.ApplicationProfileReviewID(testApplicationReviewID), 1, "auth-123", time.Unix(20, 0))
	h := &fakeSubmitProfileHandler{result: result}
	w := submitProfileHTTP(submitProfileServers(t, h), token, "", `{"expectedRevision":"1"}`)
	if w.Code != 201 || w.Header().Get("ETag") != `"2"` || h.calls != 1 || h.command.ApplicationID != testApplicationID || h.command.ProfileRevisionID != testProfileRevisionID || h.command.ExpectedRevision != 1 || h.identity.AuthID != "auth-123" || !strings.Contains(w.Body.String(), `"decision":null`) {
		t.Fatalf("%d %s %+v", w.Code, w.Body, h.command)
	}
	for _, body := range []string{`{}`, `null`, `{"expectedRevision":null}`, `{"expectedRevision":"private"}`, `{"expectedRevision":"1","expected_revision":"1"}`, `{"expectedRevision":1,"snapshot":"private"}`, `{"expectedRevision":1,"application_id":"private"}`, `{"command":{"expectedRevision":"1"}}`, `{"expectedRevision":1} {}`, strings.Repeat(" ", 1025)} {
		h := &fakeSubmitProfileHandler{}
		w := submitProfileHTTP(submitProfileServers(t, h), token, "", body)
		if w.Code != 400 || h.calls != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("body=%q %d %s", body, w.Code, w.Body)
		}
	}
	for _, query := range []string{"?", "?expectedRevision=1", "?application_id=private"} {
		h := &fakeSubmitProfileHandler{}
		w := submitProfileHTTP(submitProfileServers(t, h), token, query, `{"expectedRevision":"1"}`)
		if w.Code != 400 || h.calls != 0 {
			t.Fatal(w.Code)
		}
	}
	for _, token := range []string{"", "private-token"} {
		h := &fakeSubmitProfileHandler{}
		w := submitProfileHTTP(submitProfileServers(t, h), token, "?private", `{private`)
		if w.Code != 401 || h.calls != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
}
func TestProfileReview_BR_PRF_015_017_021_ErrorMapping(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, tc := range []struct {
		err    error
		code   int
		grpc   codes.Code
		reason string
	}{
		{profiledomain.ErrDeveloperApprovalRequired, 403, codes.PermissionDenied, ReasonDeveloperApprovalRequired}, {profiledomain.ErrApplicationAdminRequired, 403, codes.PermissionDenied, ReasonApplicationAdminRequired}, {profiledomain.ErrApplicationProfileRevisionNotFound, 404, codes.NotFound, ReasonApplicationProfileRevisionNotFound}, {profiledomain.ErrApplicationProfileRevisionNotDraft, 409, codes.Aborted, ReasonApplicationProfileRevisionNotDraft}, {profiledomain.ErrApplicationProfileRevisionConflict, 409, codes.Aborted, ReasonApplicationProfileRevisionConflict}, {profiledomain.ErrInvalidApplicationProfileContent, 400, codes.InvalidArgument, ReasonInvalidApplicationProfileContent}, {profiledomain.ErrInvalidApplicationProfileReviewSubmission, 400, codes.InvalidArgument, ReasonInvalidApplicationProfileReviewSubmission}, {profiledomain.ErrApplicationProfileStateInconsistent, 500, codes.Internal, ReasonApplicationProfileStateInconsistent},
	} {
		h := &fakeSubmitProfileHandler{err: tc.err}
		w := submitProfileHTTP(submitProfileServers(t, h), token, "", `{"expectedRevision":"1"}`)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.reason) || status.Code(toTransportError(tc.err)) != tc.grpc {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	h := &fakeSubmitProfileHandler{err: profiledomain.NewApplicationProfileStateInconsistentError(context.DeadlineExceeded)}
	submitProfileHTTP(submitProfileServers(t, h), token, "", `{"expectedRevision":"1"}`)
	if !strings.Contains(logs.String(), `"level":"ERROR"`) || !strings.Contains(logs.String(), ReasonApplicationProfileStateInconsistent) || strings.Contains(logs.String(), "deadline") {
		t.Fatal(logs.String())
	}
}
func TestAPIContract_UCAPP015_BR_PRF_018_019_ResourceAndFields(t *testing.T) {
	m := reviewv1.File_app_center_v1_application_profile_review_application_profile_review_proto.Services().ByName("ApplicationProfileReview").Methods().ByName("SubmitApplicationProfileRevisionReview")
	rule := proto.GetExtension(m.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != SubmitApplicationProfileReviewInternalPath || rule.GetBody() != "command" || SubmitApplicationProfileReviewExternalPath != "/app-center/v1/applications/{application_id}/profile-revisions/{profile_revision_id}/reviews" || strings.TrimPrefix(SubmitApplicationProfileReviewExternalPath, ServicePrefix) != rule.GetPost() || SubmitApplicationProfileReviewGRPCMethod != "/app_center.v1.application_profile_review.ApplicationProfileReview/SubmitApplicationProfileRevisionReview" {
		t.Fatal("route contract")
	}
	if SubmitApplicationProfileReviewGRPCMethod != reviewv1.ApplicationProfileReview_SubmitApplicationProfileRevisionReview_FullMethodName {
		t.Fatal("generated gRPC route")
	}
	for _, tc := range []struct {
		message proto.Message
		fields  string
	}{{&reviewv1.SubmitApplicationProfileRevisionReviewRequest{}, "application_id,command,profile_revision_id"}, {&reviewv1.SubmitApplicationProfileRevisionReviewCommand{}, "expected_revision"}, {&reviewv1.SubmitApplicationProfileRevisionReviewResponse{}, "profile_revision,review"}, {&reviewv1.ApplicationProfileReviewRecord{}, "application_id,attempt,decision,profile_review_id,profile_revision_id,snapshot,source_revision,status,submitted_at,submitted_by"}} {
		if strings.Join(messageFieldNames(t, tc.message), ",") != tc.fields {
			t.Fatalf("field drift %T", tc.message)
		}
	}
	r := &reviewv1.SubmitApplicationProfileRevisionReviewRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID}
	if path := binding.EncodeURL(SubmitApplicationProfileReviewInternalPath, r, false); path != "/v1/applications/"+testApplicationID+"/profile-revisions/"+testProfileRevisionID+"/reviews" {
		t.Fatal(path)
	}
	for _, reason := range []string{ReasonDeveloperIdentityRequired, ReasonInvalidDeveloperIdentity, ReasonDeveloperApprovalRequired, ReasonInvalidApplicationProfileReviewSubmission, ReasonApplicationProfileRevisionNotFound, ReasonApplicationAdminRequired, ReasonApplicationProfileRevisionNotDraft, ReasonApplicationProfileRevisionConflict, ReasonInvalidApplicationProfileContent, ReasonApplicationProfileStateInconsistent, ReasonInternal} {
		if _, ok := reviewv1.ErrorReason_value[reason]; !ok {
			t.Fatal(reason)
		}
	}
}

func TestProfileReview_GRPCRejectsAbsentOrUnknownCommand(t *testing.T) {
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved})
	for _, request := range []*reviewv1.SubmitApplicationProfileRevisionReviewRequest{nil, {}, {Command: &reviewv1.SubmitApplicationProfileRevisionReviewCommand{ExpectedRevision: 1}}} {
		if request != nil && request.Command != nil {
			request.Command.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
		}
		h := &fakeSubmitProfileHandler{}
		_, err := NewApplicationProfileReviewService(h, nil).SubmitApplicationProfileRevisionReview(ctx, request)
		if status.Code(err) != codes.InvalidArgument || h.calls != 0 {
			t.Fatal(err)
		}
	}
	request := &reviewv1.SubmitApplicationProfileRevisionReviewRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, Command: &reviewv1.SubmitApplicationProfileRevisionReviewCommand{ExpectedRevision: 1}}
	request.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
	_, err := NewApplicationProfileReviewService(&fakeSubmitProfileHandler{}, nil).SubmitApplicationProfileRevisionReview(ctx, request)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	request.ProtoReflect().SetUnknown(nil)
	_, err = NewApplicationProfileReviewService(nil, nil).SubmitApplicationProfileRevisionReview(ctx, request)
	if status.Code(err) != codes.Internal {
		t.Fatal(err)
	}
	_, err = NewApplicationProfileReviewService(&fakeSubmitProfileHandler{}, nil).SubmitApplicationProfileRevisionReview(ctx, request)
	if status.Code(err) != codes.Internal {
		t.Fatal(err)
	}
}
