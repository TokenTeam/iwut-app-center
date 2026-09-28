package transport

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

type fakeCreateProfileHandler struct {
	calls    int
	command  profileusecase.CreateApplicationProfileRevisionCommand
	identity shared.DeveloperIdentity
	result   *profiledomain.ApplicationProfileRevision
	err      error
}

func (h *fakeCreateProfileHandler) Handle(_ context.Context, id shared.DeveloperIdentity, c profileusecase.CreateApplicationProfileRevisionCommand) (*profiledomain.ApplicationProfileRevision, error) {
	h.calls++
	h.command = c
	h.identity = id
	return h.result, h.err
}
func profileFixture(t *testing.T) *profiledomain.ApplicationProfileRevision {
	t.Helper()
	r, e := profiledomain.RestoreApplicationProfileRevision(profiledomain.ApplicationProfileRevisionState{ProfileRevisionID: "018f0000-0000-7000-8000-000000000013", ApplicationID: shared.ApplicationID(testApplicationID), Sequence: 1, DisplayName: "Café", ReviewStatus: profiledomain.ReviewStatusDraft, CreatedBy: "auth-123", CreatedAt: fixedNow(), Revision: 1, UpdatedBy: "auth-123", UpdatedAt: fixedNow()})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func profileServers(t *testing.T, h *fakeCreateProfileHandler) *Servers {
	t.Helper()
	s, e := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(h, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func profileHTTP(s *Servers, token, query, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/applications/"+testApplicationID+"/profile-revisions"+query, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	w := httptest.NewRecorder()
	s.HTTP.ServeHTTP(w, r)
	return w
}

func TestProfile_BR_PRF_001_005_007_HTTPMappingAndExplicitNull(t *testing.T) {
	h := &fakeCreateProfileHandler{result: profileFixture(t)}
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	w := profileHTTP(profileServers(t, h), token, "", `{"displayName":"Cafe\u0301","description":null,"icon":null}`)
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` {
		t.Fatalf("response=%d %s", w.Code, w.Body)
	}
	if h.calls != 1 || h.identity.AuthID != "auth-123" || h.command.ApplicationID != testApplicationID || h.command.DisplayName != "Cafe\u0301" || h.command.Description != nil || h.command.Icon != nil {
		t.Fatalf("mapping=%+v", h)
	}
	var r profilev1.CreateApplicationProfileRevisionResponse
	if err := protojson.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.GetProfileRevisionId() == "" || r.GetDisplayName() != "Café" || r.GetCreatedBy() != "auth-123" || r.GetUpdatedBy() != r.GetCreatedBy() || r.GetReviewStatus() != "DRAFT" || !bytes.Contains(w.Body.Bytes(), []byte(`"description":null`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"icon":null`)) {
		t.Fatalf("response=%s", w.Body)
	}
}
func TestProfile_BR_PRF_005_007_HTTPStrictCompleteBody(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, body := range []string{`{}`, `null`, `[]`, `{"displayName":"\ud800","description":null,"icon":null}`, `{"displayName":"x","description":"\udfff","icon":null}`, `{"displayName":"x","description":null,"icon":"\ud800"}`, `{"displayName":"x","icon":null}`, `{"displayName":"x","description":null}`, `{"description":null,"icon":null}`, `{"displayName":null,"description":null,"icon":null}`, `{"displayName":3,"description":null,"icon":null}`, `{"displayName":"x","description":true,"icon":null}`, `{"displayName":"x","description":[],"icon":null}`, `{"displayName":"x","description":null,"icon":{}}`, `{"displayName":"x","description":null,"icon":null,"createdBy":"forged"}`, `{"displayName":"x","description":null,"icon":null,"applicationId":"forged"}`, `{"displayName":"x","display_name":"y","description":null,"icon":null}`, `{"displayName":"x","description":null,"description":"y","icon":null}`, `{"displayName":"x","description":null,"icon":null} {}`} {
		t.Run(body, func(t *testing.T) {
			h := &fakeCreateProfileHandler{}
			w := profileHTTP(profileServers(t, h), token, "", body)
			if w.Code != 400 || h.calls != 0 || !strings.Contains(w.Body.String(), ReasonInvalidCreateApplicationProfileRevisionRequest) {
				t.Fatalf("response=%d %s calls=%d", w.Code, w.Body, h.calls)
			}
		})
	}
	for _, query := range []string{"?displayName=forged", "?application_id=forged", "?", "?unknown=1"} {
		h := &fakeCreateProfileHandler{}
		w := profileHTTP(profileServers(t, h), token, query, `{"displayName":"x","description":null,"icon":null}`)
		if w.Code != 400 || h.calls != 0 {
			t.Fatalf("query %s accepted", query)
		}
	}
}
func TestProfile_BR_PRF_002_AuthenticationBeforeBodyBinding(t *testing.T) {
	for _, token := range []string{"", "private-invalid-jws"} {
		h := &fakeCreateProfileHandler{}
		w := profileHTTP(profileServers(t, h), token, "", `{invalid-private-body`)
		if w.Code != 401 || h.calls != 0 || strings.Contains(w.Body.String(), "private-") {
			t.Fatalf("response=%d %s", w.Code, w.Body)
		}
	}
}
func TestProfile_BR_PRF_005_GRPCValuePresence(t *testing.T) {
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved})
	invalid := []*structpb.Value{nil, {}, structpb.NewBoolValue(true), structpb.NewNumberValue(1), structpb.NewStructValue(&structpb.Struct{}), structpb.NewListValue(&structpb.ListValue{}), {Kind: &structpb.Value_NullValue{NullValue: 1}}}
	for _, value := range invalid {
		for _, field := range []string{"description", "icon"} {
			h := &fakeCreateProfileHandler{}
			r := &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, Profile: &profilev1.ApplicationProfileContent{DisplayName: "x", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}}
			if field == "description" {
				r.Profile.Description = value
			} else {
				r.Profile.Icon = value
			}
			_, err := NewApplicationProfileRevisionService(h, nil).CreateApplicationProfileRevision(ctx, r)
			if status.Code(err) != codes.InvalidArgument || h.calls != 0 {
				t.Fatalf("%s kind %v accepted: %v", field, value, err)
			}
		}
	}
	h := &fakeCreateProfileHandler{result: profileFixture(t)}
	r := &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, Profile: &profilev1.ApplicationProfileContent{DisplayName: "x", Description: structpb.NewStringValue("description"), Icon: structpb.NewStringValue("opaque:anything")}}
	if _, e := NewApplicationProfileRevisionService(h, nil).CreateApplicationProfileRevision(ctx, r); e != nil || h.command.Description == nil || *h.command.Description != "description" || h.command.Icon == nil || *h.command.Icon != "opaque:anything" {
		t.Fatalf("string mapping failed: %v", e)
	}
}
func TestProfile_BR_PRF_002_006_007_ErrorMappingAndSafeInvariantAlert(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, tc := range []struct {
		err    error
		http   int
		grpc   codes.Code
		reason string
	}{
		{profiledomain.ErrInvalidApplicationID, 400, codes.InvalidArgument, ReasonInvalidApplicationID}, {profiledomain.ErrDeveloperIdentityRequired, 401, codes.Unauthenticated, ReasonDeveloperIdentityRequired}, {profiledomain.ErrDeveloperApprovalRequired, 403, codes.PermissionDenied, ReasonDeveloperApprovalRequired}, {profiledomain.ErrApplicationAdminRequired, 403, codes.PermissionDenied, ReasonApplicationAdminRequired}, {profiledomain.ErrApplicationNotFound, 404, codes.NotFound, ReasonApplicationNotFound}, {profiledomain.ErrApplicationProfileWorkRevisionAlreadyExists, 409, codes.Aborted, ReasonApplicationProfileWorkRevisionAlreadyExists}, {profiledomain.ErrApplicationProfileStateInconsistent, 500, codes.Internal, ReasonApplicationProfileStateInconsistent}, {profiledomain.ErrInvalidApplicationDisplayName, 400, codes.InvalidArgument, ReasonInvalidApplicationDisplayName}, {profiledomain.ErrInvalidApplicationDescription, 400, codes.InvalidArgument, ReasonInvalidApplicationDescription}, {profiledomain.ErrInvalidApplicationIcon, 400, codes.InvalidArgument, ReasonInvalidApplicationIcon}, {profiledomain.NewInternalError(nil), 500, codes.Internal, ReasonInternal},
	} {
		h := &fakeCreateProfileHandler{err: tc.err}
		w := profileHTTP(profileServers(t, h), token, "", `{"displayName":"private-content","description":null,"icon":null}`)
		if w.Code != tc.http || !strings.Contains(w.Body.String(), tc.reason) || status.Code(toTransportError(tc.err)) != tc.grpc {
			t.Fatalf("error mapping %v: %d %s", tc.err, w.Code, w.Body)
		}
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	h := &fakeCreateProfileHandler{err: profiledomain.ErrApplicationProfileStateInconsistent}
	profileHTTP(profileServers(t, h), token, "", `{"displayName":"private-content","description":null,"icon":null}`)
	if !strings.Contains(output.String(), `"level":"ERROR"`) || !strings.Contains(output.String(), ReasonApplicationProfileStateInconsistent) || strings.Contains(output.String(), "private-content") || strings.Contains(output.String(), token) {
		t.Fatalf("unsafe/missing alert: %s", output.String())
	}
}
