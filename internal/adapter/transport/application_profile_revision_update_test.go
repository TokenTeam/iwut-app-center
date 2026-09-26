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
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileusecase "iwut-app-center/internal/profile/usecase"
	"iwut-app-center/internal/shared"
)

type fakeUpdateProfileHandler struct {
	calls    int
	identity shared.DeveloperIdentity
	command  profileusecase.UpdateDraftApplicationProfileRevisionCommand
	result   *profiledomain.ApplicationProfileRevision
	err      error
}

func (h *fakeUpdateProfileHandler) Handle(_ context.Context, identity shared.DeveloperIdentity, command profileusecase.UpdateDraftApplicationProfileRevisionCommand) (*profiledomain.ApplicationProfileRevision, error) {
	h.calls++
	h.identity = identity
	h.command = command
	return h.result, h.err
}
func updateProfileServers(t *testing.T, h *fakeUpdateProfileHandler) *Servers {
	t.Helper()
	s, e := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, h), NewApplicationProfileReviewService(nil))
	if e != nil {
		t.Fatal(e)
	}
	return s
}

const testProfileRevisionID = "018f0000-0000-7000-8000-000000000013"
const updateProfileBody = `{"displayName":"Cafe\u0301","description":null,"icon":null}`

func updateProfileHTTP(s *Servers, token, query, body string, ifMatch []string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPut, "/v1/applications/"+testApplicationID+"/profile-revisions/"+testProfileRevisionID+query, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set(IdentityHeader, token)
	}
	for _, v := range ifMatch {
		r.Header.Add("If-Match", v)
	}
	w := httptest.NewRecorder()
	s.HTTP.ServeHTTP(w, r)
	return w
}
func validUpdateProfileGRPCRequest() *profilev1.UpdateApplicationProfileRevisionRequest {
	return &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, ExpectedRevision: 1, Profile: &profilev1.ApplicationProfileContent{DisplayName: "Café", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}}
}
func TestProfileUpdate_BR_PRF_009_011_014_HTTPCompleteMappingAndNoopETag(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, revision := range []int64{1, 2} {
		state := profiledomain.ApplicationProfileRevisionState{ProfileRevisionID: testProfileRevisionID, ApplicationID: shared.ApplicationID(testApplicationID), Sequence: 1, DisplayName: "Café", ReviewStatus: profiledomain.ReviewStatusDraft, CreatedBy: "auth-123", CreatedAt: fixedNow(), Revision: revision, UpdatedBy: "auth-123", UpdatedAt: fixedNow()}
		result, err := profiledomain.RestoreApplicationProfileRevision(state)
		if err != nil {
			t.Fatal(err)
		}
		h := &fakeUpdateProfileHandler{result: result}
		w := updateProfileHTTP(updateProfileServers(t, h), token, "", updateProfileBody, []string{`"1"`})
		if w.Code != 200 || (revision == 1 && w.Header().Get("ETag") != `"1"`) || (revision == 2 && w.Header().Get("ETag") != `"2"`) {
			t.Fatalf("response=%d %s ETag=%s", w.Code, w.Body, w.Header().Get("ETag"))
		}
		if h.calls != 1 || h.identity.AuthID != "auth-123" || h.command.ApplicationID != testApplicationID || h.command.ProfileRevisionID != testProfileRevisionID || h.command.ExpectedRevision != 1 || h.command.DisplayName != "Cafe\u0301" || h.command.Description != nil || h.command.Icon != nil {
			t.Fatalf("mapping=%+v", h)
		}
		var response profilev1.UpdateApplicationProfileRevisionResponse
		if err := protojson.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Revision != revision || response.ProfileRevisionId != testProfileRevisionID || response.CreatedBy != "auth-123" || response.ReviewStatus != "DRAFT" || !bytes.Contains(w.Body.Bytes(), []byte(`"description":null`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"icon":null`)) {
			t.Fatalf("response=%s", w.Body)
		}
	}
}
func TestProfileUpdate_BR_PRF_011_HTTPIfMatchStrict(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, values := range [][]string{nil, {""}, {"1"}, {`W/"1"`}, {"*"}, {`"1", "2"`}, {`"1"`, `"1"`}, {`"0"`}, {`"-1"`}, {`"+1"`}, {`" 1"`}, {`"1 "`}, {`"1.0"`}, {`"9223372036854775808"`}, {`"١"`}} {
		h := &fakeUpdateProfileHandler{}
		w := updateProfileHTTP(updateProfileServers(t, h), token, "", updateProfileBody, values)
		want := 400
		if len(values) == 0 {
			want = 428
		}
		if w.Code != want || h.calls != 0 || !strings.Contains(w.Body.String(), ReasonApplicationProfileExpectedRevisionRequired) {
			t.Fatalf("headers=%q response=%d %s calls=%d", values, w.Code, w.Body, h.calls)
		}
	}
	h := &fakeUpdateProfileHandler{result: profileFixture(t)}
	w := updateProfileHTTP(updateProfileServers(t, h), token, "", updateProfileBody, []string{`"9223372036854775807"`})
	if w.Code != 200 || h.command.ExpectedRevision != 9223372036854775807 {
		t.Fatalf("int64 maximum rejected: %d %s", w.Code, w.Body)
	}
}
func TestProfileUpdate_BR_PRF_009_010_HTTPRejectPartialUnknownAndOverrides(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, body := range []string{`{}`, `null`, `[]`, `{"displayName":"x","description":null}`, `{"displayName":"x","icon":null}`, `{"description":null,"icon":null}`, `{"displayName":null,"description":null,"icon":null}`, `{"displayName":"x","description":false,"icon":null}`, `{"displayName":"x","description":null,"icon":[]}`, `{"displayName":"x","description":null,"icon":null,"expectedRevision":"1"}`, `{"displayName":"x","description":null,"icon":null,"profileRevisionId":"forged"}`, `{"displayName":"x","description":null,"icon":null,"sequence":2}`, `{"displayName":"x","description":null,"icon":null,"createdBy":"forged"}`, `{"displayName":"x","description":null,"icon":null,"reviewStatus":"APPROVED"}`, `{"displayName":"x","description":null,"icon":null,"launchUrl":"forged"}`, `{"displayName":"x","display_name":"y","description":null,"icon":null}`, `{"displayName":"x","description":null,"icon":null,"icon":"y"}`, `{"displayName":"\ud800","description":null,"icon":null}`, updateProfileBody + ` {}`} {
		h := &fakeUpdateProfileHandler{}
		w := updateProfileHTTP(updateProfileServers(t, h), token, "", body, []string{`"1"`})
		if w.Code != 400 || h.calls != 0 || !strings.Contains(w.Body.String(), ReasonInvalidUpdateApplicationProfileRevisionRequest) {
			t.Fatalf("body=%s response=%d %s calls=%d", body, w.Code, w.Body, h.calls)
		}
	}
	for _, query := range []string{"?", "?expected_revision=1", "?displayName=forged", "?application_id=forged", "?profile_revision_id=forged"} {
		h := &fakeUpdateProfileHandler{}
		w := updateProfileHTTP(updateProfileServers(t, h), token, query, updateProfileBody, []string{`"1"`})
		if w.Code != 400 || h.calls != 0 {
			t.Fatalf("query accepted %s: %d %s", query, w.Code, w.Body)
		}
	}
}
func TestProfileUpdate_BR_PRF_013_AuthenticationBeforeBinding(t *testing.T) {
	for _, token := range []string{"", "private-invalid-token"} {
		h := &fakeUpdateProfileHandler{}
		w := updateProfileHTTP(updateProfileServers(t, h), token, "?forged=private", `{private-invalid-body`, nil)
		if w.Code != 401 || h.calls != 0 || strings.Contains(w.Body.String(), "private") {
			t.Fatalf("response=%d %s", w.Code, w.Body)
		}
	}
}
func TestProfileUpdate_BR_PRF_009_011_GRPCPresenceAndRevision(t *testing.T) {
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved})
	for _, invalid := range []*structpb.Value{nil, {}, structpb.NewBoolValue(true), structpb.NewNumberValue(1), structpb.NewListValue(&structpb.ListValue{}), {Kind: &structpb.Value_NullValue{NullValue: 1}}} {
		for _, field := range []string{"description", "icon"} {
			r := validUpdateProfileGRPCRequest()
			if field == "description" {
				r.Profile.Description = invalid
			} else {
				r.Profile.Icon = invalid
			}
			h := &fakeUpdateProfileHandler{}
			_, err := NewApplicationProfileRevisionService(nil, h).UpdateApplicationProfileRevision(ctx, r)
			if status.Code(err) != codes.InvalidArgument || h.calls != 0 {
				t.Fatalf("%s invalid=%v err=%v", field, invalid, err)
			}
		}
	}
	for _, revision := range []int64{0, -1} {
		h := &fakeUpdateProfileHandler{}
		r := validUpdateProfileGRPCRequest()
		r.ExpectedRevision = revision
		_, err := NewApplicationProfileRevisionService(nil, h).UpdateApplicationProfileRevision(ctx, r)
		if status.Code(err) != codes.InvalidArgument || h.calls != 0 {
			t.Fatalf("revision accepted=%d: %v", revision, err)
		}
	}
	h := &fakeUpdateProfileHandler{result: profileFixture(t)}
	r := validUpdateProfileGRPCRequest()
	r.Profile.Description = structpb.NewStringValue("updated description")
	r.Profile.Icon = structpb.NewStringValue("opaque:updated")
	if _, err := NewApplicationProfileRevisionService(nil, h).UpdateApplicationProfileRevision(ctx, r); err != nil || h.calls != 1 || h.command.Description == nil || *h.command.Description != "updated description" || h.command.Icon == nil || *h.command.Icon != "opaque:updated" {
		t.Fatalf("string mapping failed %v", err)
	}
	r.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
	if _, err := NewApplicationProfileRevisionService(nil, h).UpdateApplicationProfileRevision(ctx, r); status.Code(err) != codes.InvalidArgument || h.calls != 1 {
		t.Fatalf("unknown field accepted: %v", err)
	}
}
func TestProfileUpdate_BR_PRF_008_011_013_ErrorMappingsAndSafeAlert(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved})
	for _, tc := range []struct {
		err    error
		http   int
		grpc   codes.Code
		reason string
	}{
		{profiledomain.ErrInvalidApplicationProfileRevisionID, 400, codes.InvalidArgument, ReasonInvalidApplicationProfileRevisionID},
		{profiledomain.ErrApplicationProfileExpectedRevisionRequired, 400, codes.InvalidArgument, ReasonApplicationProfileExpectedRevisionRequired},
		{profiledomain.ErrApplicationProfileRevisionNotFound, 404, codes.NotFound, ReasonApplicationProfileRevisionNotFound},
		{profiledomain.ErrApplicationProfileRevisionNotDraft, 409, codes.Aborted, ReasonApplicationProfileRevisionNotDraft},
		{profiledomain.ErrApplicationProfileRevisionConflict, 412, codes.Aborted, ReasonApplicationProfileRevisionConflict},
		{profiledomain.ErrApplicationAdminRequired, 403, codes.PermissionDenied, ReasonApplicationAdminRequired},
		{profiledomain.ErrApplicationProfileStateInconsistent, 500, codes.Internal, ReasonApplicationProfileStateInconsistent},
	} {
		h := &fakeUpdateProfileHandler{err: tc.err}
		w := updateProfileHTTP(updateProfileServers(t, h), token, "", updateProfileBody, []string{`"1"`})
		_, err := NewApplicationProfileRevisionService(nil, h).UpdateApplicationProfileRevision(ctx, validUpdateProfileGRPCRequest())
		if w.Code != tc.http || !strings.Contains(w.Body.String(), tc.reason) || status.Code(err) != tc.grpc {
			t.Fatalf("mapping %v: HTTP %d %s gRPC %v", tc.err, w.Code, w.Body, err)
		}
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)
	h := &fakeUpdateProfileHandler{err: profiledomain.ErrApplicationProfileStateInconsistent}
	updateProfileHTTP(updateProfileServers(t, h), token, "", `{"displayName":"private-content","description":null,"icon":null}`, []string{`"1"`})
	if !strings.Contains(output.String(), `"level":"ERROR"`) || !strings.Contains(output.String(), ReasonApplicationProfileStateInconsistent) || strings.Contains(output.String(), "private-content") || strings.Contains(output.String(), token) {
		t.Fatalf("unsafe/missing alert: %s", output.String())
	}
}

type profileHTTPRoundTripper func(*http.Request) (*http.Response, error)

func (f profileHTTPRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestProfile_BR_PRF_007_009_011_GeneratedHTTPClientsUseOnlyProfileBody(t *testing.T) {
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	create := &fakeCreateProfileHandler{result: profileFixture(t)}
	update := &fakeUpdateProfileHandler{result: profileFixture(t)}
	servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(create, update), NewApplicationProfileReviewService(nil))
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(servers.HTTP)
	defer host.Close()
	cc, err := khttp.NewClient(context.Background(), khttp.WithEndpoint(host.URL), khttp.WithTransport(profileHTTPRoundTripper(func(r *http.Request) (*http.Response, error) {
		r.Header.Set(IdentityHeader, token)
		if r.Method == http.MethodPut {
			r.Header.Set("If-Match", `"1"`)
		}
		return http.DefaultTransport.RoundTrip(r)
	})))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	client := profilev1.NewApplicationProfileRevisionHTTPClient(cc)
	content := &profilev1.ApplicationProfileContent{DisplayName: "Café", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}
	responseHeader := make(http.Header)
	created, err := client.CreateApplicationProfileRevision(context.Background(), &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, Profile: content}, khttp.Header(&responseHeader))
	if err != nil || create.calls != 1 || created.GetProfileRevisionId() != testProfileRevisionID || responseHeader.Get("ETag") != `"1"` {
		t.Fatalf("generated create client failed: %v response=%v", err, created)
	}
	// expected_revision is native-gRPC input; HTTP's sole source is If-Match.
	updated, err := client.UpdateApplicationProfileRevision(context.Background(), &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, ExpectedRevision: 99, Profile: content}, khttp.Header(&responseHeader))
	if err != nil || update.calls != 1 || updated.GetProfileRevisionId() != testProfileRevisionID || update.command.ExpectedRevision != 1 || responseHeader.Get("ETag") != `"1"` {
		t.Fatalf("generated update client failed: %v response=%v", err, updated)
	}
}

func TestProfile_BR_PRF_007_009_GRPCRejectMissingOrUnknownProfile(t *testing.T) {
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved})
	for _, content := range []*profilev1.ApplicationProfileContent{nil, {DisplayName: "Café", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}} {
		if content != nil {
			content.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
		}
		create := &fakeCreateProfileHandler{}
		update := &fakeUpdateProfileHandler{}
		service := NewApplicationProfileRevisionService(create, update)
		_, err := service.CreateApplicationProfileRevision(ctx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, Profile: content})
		if status.Code(err) != codes.InvalidArgument || create.calls != 0 {
			t.Fatalf("invalid create content accepted: %v", err)
		}
		_, err = service.UpdateApplicationProfileRevision(ctx, &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: testApplicationID, ProfileRevisionId: testProfileRevisionID, ExpectedRevision: 1, Profile: content})
		if status.Code(err) != codes.InvalidArgument || update.calls != 0 {
			t.Fatalf("invalid update content accepted: %v", err)
		}
	}
}
