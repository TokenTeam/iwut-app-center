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
	"time"

	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	"github.com/go-kratos/kratos/v2/transport/http/binding"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

type fakeDecideProfileHandler struct {
	calls    int
	identity profileusecase.ReviewerIdentity
	command  profileusecase.DecideApplicationProfileRevisionReviewCommand
	result   *profiledomain.ApplicationProfileDecisionResult
	err      error
}

func (h *fakeDecideProfileHandler) Handle(_ context.Context, i profileusecase.ReviewerIdentity, c profileusecase.DecideApplicationProfileRevisionReviewCommand) (*profiledomain.ApplicationProfileDecisionResult, error) {
	h.calls++
	h.identity = i
	h.command = c
	return h.result, h.err
}
func decideProfileServers(t *testing.T, h *fakeDecideProfileHandler) *Servers {
	t.Helper()
	s, e := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, h), NewOAuthClientService(nil))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func decideProfileHTTP(s *Servers, token, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/profile-revisions/"+testProfileRevisionID+"/reviews/"+testApplicationReviewID+"/decision"+query, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", `"999"`)
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	w := httptest.NewRecorder()
	s.HTTP.ServeHTTP(w, r)
	return w
}

const profileApprovalJSON = `{"expectedProfileRevisionRevision":"2","expectedCurrentPublishedProfileRevisionId":null,"expectedPolicyVersion":"app-profile-review-v1","outcome":"APPROVE","confirmedCheckIds":["content-policy-reviewed","icon-content-reviewed"]}`

func profileDecisionFixture(t *testing.T, outcome string) *profiledomain.ApplicationProfileDecisionResult {
	t.Helper()
	name, _ := profiledomain.NewApplicationDisplayName("name")
	draft, _ := profiledomain.NewDraftApplicationProfileRevision(testProfileRevisionID, testApplicationID, name, nil, nil, "creator", time.Unix(10, 0))
	revision, _ := draft.AssignSequence(1)
	submission, e := revision.SubmitDraft(1, profiledomain.ApplicationProfileReviewID(testApplicationReviewID), 1, "submitter", time.Unix(20, 0))
	if e != nil {
		t.Fatal(e)
	}
	checks := profiledomain.InitialProfileReviewChecks()
	var reason *string
	if outcome == "REJECT" {
		checks = nil
		v := "Cafe\u0301 rejected"
		reason = &v
	}
	result, e := submission.ProfileRevision.DecideReview(submission.Review, "reviewer", "admin", []string{"app.profile.review"}, 2, outcome, profiledomain.ProfileReviewPolicy{Version: "app-profile-review-v1", RequiredChecks: profiledomain.InitialProfileReviewChecks(), Status: "ACTIVE"}, checks, reason, time.Unix(30, 123000000))
	if e != nil {
		t.Fatal(e)
	}
	if outcome == "APPROVE" {
		id := result.ProfileRevision.ProfileRevisionID()
		result.CurrentPublishedProfileRevisionID = &id
	}
	return result
}
func TestProfileDecision_BR_PRF_023_026_030_StrictBindingAndResponse(t *testing.T) {
	claims := validClaims(fixedNow())
	delete(claims, "developer_status")
	claims["permissions"] = []string{"app.profile.review"}
	token := signToken(t, tokenOptions{claims: claims})
	h := &fakeDecideProfileHandler{result: profileDecisionFixture(t, "APPROVE")}
	w := decideProfileHTTP(decideProfileServers(t, h), token, "", profileApprovalJSON)
	if w.Code != 200 || w.Header().Get("ETag") != `"3"` || h.calls != 1 || h.identity.AuthID != "auth-123" || len(h.identity.Permissions) != 1 || h.identity.Permissions[0] != "app.profile.review" || h.command.ApplicationID != testApplicationID || h.command.ProfileReviewID != testApplicationReviewID || h.command.ExpectedProfileRevisionRevision != 2 || !h.command.PublicationPreconditionPresent || h.command.ExpectedCurrentPublishedProfileRevisionID != nil || !strings.Contains(w.Body.String(), `"outcome":"APPROVED"`) || !strings.Contains(w.Body.String(), `"reason":null`) {
		t.Fatalf("%d %s %+v %+v", w.Code, w.Body, h.identity, h.command)
	}
	for _, body := range []string{`{}`, `null`, strings.Replace(profileApprovalJSON, `"outcome":"APPROVE"`, `"outcome":null`, 1), strings.Replace(profileApprovalJSON, `"outcome":"APPROVE"`, `"outcome":"private"`, 1), strings.Replace(profileApprovalJSON, `"expectedCurrentPublishedProfileRevisionId":null`, `"expectedCurrentPublishedProfileRevisionId":true`, 1), strings.Replace(profileApprovalJSON, `"expectedCurrentPublishedProfileRevisionId":null`, `"expectedCurrentPublishedProfileRevisionId":{}`, 1), strings.Replace(profileApprovalJSON, `"expectedProfileRevisionRevision":"2"`, `"expectedProfileRevisionRevision":"private"`, 1), strings.Replace(profileApprovalJSON, `"expectedProfileRevisionRevision":"2"`, `"expectedProfileRevisionRevision":"2","expected_profile_revision_revision":"2"`, 1), strings.Replace(profileApprovalJSON, `"outcome":"APPROVE"`, `"outcome":"APPROVE","reason":null`, 1), strings.Replace(profileApprovalJSON, `"outcome":"APPROVE"`, `"outcome":"APPROVE","decidedBy":"private"`, 1), `{"command":` + profileApprovalJSON + `}`, profileApprovalJSON + ` {}`, strings.Repeat(" ", 32769)} {
		h := &fakeDecideProfileHandler{}
		w := decideProfileHTTP(decideProfileServers(t, h), token, "", body)
		if w.Code != 400 || h.calls != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("body=%s: %d %s", body, w.Code, w.Body)
		}
	}
	for _, query := range []string{"?", "?expectedPolicyVersion=private", "?application_id=private"} {
		h := &fakeDecideProfileHandler{}
		w := decideProfileHTTP(decideProfileServers(t, h), token, query, profileApprovalJSON)
		if w.Code != 400 || h.calls != 0 {
			t.Fatal(w.Code, w.Body)
		}
	}
	for _, tc := range []struct{ token, reason string }{{"", ReasonReviewerIdentityRequired}, {"private", ReasonInvalidReviewerIdentity}} {
		h := &fakeDecideProfileHandler{}
		w := decideProfileHTTP(decideProfileServers(t, h), tc.token, "?private", `{private`)
		if w.Code != 401 || h.calls != 0 || !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "private") {
			t.Fatal(w.Code, w.Body)
		}
	}
}
func TestProfileDecision_BR_PRF_023_032_ErrorMapping(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, tc := range []struct {
		err    error
		code   int
		grpc   codes.Code
		reason string
	}{
		{profiledomain.ErrReviewerIdentityRequired, 401, codes.Unauthenticated, ReasonReviewerIdentityRequired},
		{profiledomain.ErrApplicationProfileReviewPermissionRequired, 403, codes.PermissionDenied, ReasonApplicationProfileReviewPermissionRequired},
		{profiledomain.ErrApplicationProfileReviewConflictOfInterest, 403, codes.PermissionDenied, ReasonApplicationProfileReviewConflictOfInterest},
		{profiledomain.ErrInvalidApplicationProfileReviewDecision, 400, codes.InvalidArgument, ReasonInvalidApplicationProfileReviewDecision},
		{profiledomain.ErrApplicationProfileReviewNotFound, 404, codes.NotFound, ReasonApplicationProfileReviewNotFound},
		{profiledomain.ErrApplicationProfileReviewAlreadyDecided, 409, codes.Aborted, ReasonApplicationProfileReviewAlreadyDecided},
		{profiledomain.ErrApplicationProfileReviewStateConflict, 409, codes.Aborted, ReasonApplicationProfileReviewStateConflict},
		{profiledomain.ErrApplicationProfileRevisionConflict, 409, codes.Aborted, ReasonApplicationProfileRevisionConflict},
		{profiledomain.ErrApplicationProfilePublicationConflict, 409, codes.Aborted, ReasonApplicationProfilePublicationConflict},
		{profiledomain.ErrApplicationProfileReviewStateInconsistent, 500, codes.Internal, ReasonApplicationProfileReviewStateInconsistent},
		{profiledomain.ErrProfileReviewPolicyUnavailable, 409, codes.Aborted, ReasonProfileReviewPolicyUnavailable},
		{profiledomain.ErrProfileReviewChecksIncomplete, 400, codes.InvalidArgument, ReasonProfileReviewChecksIncomplete},
		{profiledomain.ErrInvalidProfileReviewReason, 400, codes.InvalidArgument, ReasonInvalidProfileReviewReason},
		{profiledomain.ErrInvalidApplicationProfileContent, 400, codes.InvalidArgument, ReasonInvalidApplicationProfileContent},
		{profiledomain.NewInternalError(errors.New("private mongodb details")), 500, codes.Internal, ReasonInternal},
	} {
		h := &fakeDecideProfileHandler{err: tc.err}
		w := decideProfileHTTP(decideProfileServers(t, h), token, "", profileApprovalJSON)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "private") || status.Code(toTransportError(tc.err)) != tc.grpc {
			t.Fatalf("%v: %d %s", tc.err, w.Code, w.Body)
		}
	}
}
func TestProfileDecision_BR_PRF_026_027_031_RejectedResponseAndGRPCPresence(t *testing.T) {
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "reviewer", Permissions: []string{"app.profile.review"}})
	reason := "Cafe\u0301 rejected"
	req := &reviewv1.DecideApplicationProfileRevisionReviewRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, ProfileReviewId: testApplicationReviewID, Command: &reviewv1.DecideApplicationProfileRevisionReviewCommand{ExpectedProfileRevisionRevision: 2, ExpectedPolicyVersion: "app-profile-review-v1", Outcome: reviewv1.ProfileReviewDecisionAction_REJECT, Reason: &reason}}
	h := &fakeDecideProfileHandler{result: profileDecisionFixture(t, "REJECT")}
	res, err := NewApplicationProfileReviewService(nil, h).DecideApplicationProfileRevisionReview(ctx, req)
	if err != nil || h.command.PublicationPreconditionPresent || h.command.Reason == nil || *h.command.Reason != reason || res.Review.Decision.GetStructValue().Fields["reason"].GetStringValue() != reason || res.CurrentPublishedProfileRevisionId.GetKind() == nil || res.Review.Status != "REJECTED" {
		t.Fatalf("%v %v", res, err)
	}
	for _, req := range []*reviewv1.DecideApplicationProfileRevisionReviewRequest{nil, {}, {Command: &reviewv1.DecideApplicationProfileRevisionReviewCommand{Outcome: 99}}, {Command: &reviewv1.DecideApplicationProfileRevisionReviewCommand{Outcome: reviewv1.ProfileReviewDecisionAction_APPROVE, ExpectedCurrentPublishedProfileRevisionId: structpb.NewNumberValue(1)}}} {
		if _, err := NewApplicationProfileReviewService(nil, h).DecideApplicationProfileRevisionReview(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	req.ProtoReflect().SetUnknown([]byte{0xf8, 0x07, 0x01})
	if _, err := NewApplicationProfileReviewService(nil, h).DecideApplicationProfileRevisionReview(ctx, req); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
func TestProfileDecision_BR_PRF_025_SafeInvariantAlert(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(old)
	h := &fakeDecideProfileHandler{err: profiledomain.ErrApplicationProfileReviewStateInconsistent}
	ctx := withTrustedIdentity(context.Background(), shared.TrustedIdentity{AuthID: "reviewer"})
	_, _ = NewApplicationProfileReviewService(nil, h).DecideApplicationProfileRevisionReview(ctx, &reviewv1.DecideApplicationProfileRevisionReviewRequest{Command: &reviewv1.DecideApplicationProfileRevisionReviewCommand{Outcome: reviewv1.ProfileReviewDecisionAction_REJECT}})
	if !strings.Contains(buf.String(), `"level":"ERROR"`) || !strings.Contains(buf.String(), ReasonApplicationProfileReviewStateInconsistent) {
		t.Fatal(buf.String())
	}
}
func TestProfileDecision_APIContract(t *testing.T) {
	method := reviewv1.File_app_center_v1_application_profile_review_application_profile_review_proto.Services().ByName("ApplicationProfileReview").Methods().ByName("DecideApplicationProfileRevisionReview")
	rule := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if rule.GetPost() != DecideApplicationProfileReviewInternalPath || rule.Body != "command" || DecideApplicationProfileReviewExternalPath != "/app-center"+rule.GetPost() || DecideApplicationProfileReviewGRPCMethod != "/app_center.v1.application_profile_review.ApplicationProfileReview/DecideApplicationProfileRevisionReview" {
		t.Fatal(rule)
	}
	req := &reviewv1.DecideApplicationProfileRevisionReviewRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, ProfileReviewId: testApplicationReviewID}
	path := binding.EncodeURL(rule.GetPost(), req, false)
	if path != "/v1/applications/"+testApplicationID+"/profile-revisions/"+testProfileRevisionID+"/reviews/"+testApplicationReviewID+"/decision" {
		t.Fatal(path)
	}
	field := (&reviewv1.ApplicationProfileReviewRecord{}).ProtoReflect().Descriptor().Fields().ByName("decision")
	if field.Number() != 10 || field.Message().FullName() != "google.protobuf.Value" {
		t.Fatal(field)
	}
}
