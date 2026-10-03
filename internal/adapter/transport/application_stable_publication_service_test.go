package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationusecase "iwut-app-center/internal/publication/usecase"
	"iwut-app-center/internal/shared"
)

type fakeStableSetHandler struct {
	result *publicationdomain.PlaceInTestResult
	calls  int
}

func (h *fakeStableSetHandler) Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.SetApprovedVersionInStableSlotCommand) (*publicationdomain.PlaceInTestResult, error) {
	h.calls++
	return h.result, nil
}

type fakeStableClearHandler struct {
	result *publicationdomain.PlaceInTestResult
	calls  int
}

func (h *fakeStableClearHandler) Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.ClearStableSlotCommand) (*publicationdomain.PlaceInTestResult, error) {
	h.calls++
	return h.result, nil
}

func stablePublicationResults(t *testing.T) (*publicationdomain.PlaceInTestResult, *publicationdomain.PlaceInTestResult) {
	t.Helper()
	snapshot, err := publicationdomain.NewApplicationVersionReviewSnapshot("1.0", "https://example.edu", 3, 5, []string{}, []publicationdomain.ScopeName{}, []publicationdomain.ScopeName{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	candidate, err := publicationdomain.NewStablePlacementCandidate(shared.ApplicationID(testApplicationID), 3, publicationdomain.ApplicationVersionID(testApplicationVersionID), publicationdomain.ApplicationReviewID(testApplicationReviewID), 3, *snapshot, nil, nil)
	if err != nil {
		t.Fatalf("candidate: %v", err)
	}
	publicationID := publicationdomain.ApplicationPublicationID("018f7777-7777-7777-8777-777777777779")
	set, err := candidate.SetStable(&publicationID, "018f7777-7777-7777-8777-777777777780", "admin", publicationdomain.PublicationValidation{ScopeCatalogRevision: 11, PreflightPolicyVersion: "submit-v1"}, time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	clearCandidate, err := publicationdomain.NewStableClearCandidate(set.Publication(), 1)
	if err != nil {
		t.Fatalf("clear candidate: %v", err)
	}
	cleared, err := clearCandidate.Clear("018f7777-7777-7777-8777-777777777781", "admin", time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	return set, cleared
}

func TestPublicationService_UCAPP020_StablePresenceAndClearHistory(t *testing.T) {
	setResult, clearResult := stablePublicationResults(t)
	setHandler := &fakeStableSetHandler{result: setResult}
	clearHandler := &fakeStableClearHandler{result: clearResult}
	service := NewApplicationPublicationServiceWithStable(nil, setHandler, clearHandler)
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved})
	setResponse, err := service.SetApprovedVersionInStableSlot(ctx, &publicationv1.SetApprovedVersionInStableSlotRequest{ApplicationId: testApplicationID, RpcApiMajor: 3, Command: &publicationv1.SetApprovedVersionInStableSlotCommand{VersionId: testApplicationVersionID}})
	if err != nil {
		t.Fatal(err)
	}
	if setResponse.Publication.TestVersionId != nil || setResponse.Publication.StableVersionId == nil || *setResponse.Publication.StableVersionId != testApplicationVersionID || setResponse.History.NewVersionId == nil {
		t.Fatalf("set response=%#v", setResponse)
	}
	writer := httptest.NewRecorder()
	if err := createdResponseEncoder(writer, httptest.NewRequest(http.MethodPut, "/", nil), setResponse); err != nil || writer.Code != http.StatusCreated {
		t.Fatalf("create status=%d error=%v", writer.Code, err)
	}
	clearResponse, err := service.ClearStableSlot(ctx, &publicationv1.ClearStableSlotRequest{ApplicationId: testApplicationID, RpcApiMajor: 3, ExpectedPublicationRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	if clearResponse.Publication.TestVersionId != nil || clearResponse.Publication.StableVersionId != nil || clearResponse.History.PreviousVersionId == nil || clearResponse.History.NewVersionId != nil || clearResponse.History.ApprovedReviewId != nil || clearResponse.History.ScopeCatalogRevision != nil || clearResponse.History.PreflightPolicyVersion != nil {
		t.Fatalf("clear response=%#v", clearResponse)
	}
}

func TestPublicationService_UCAPP020_StrictStableHTTPInputs(t *testing.T) {
	setResult, clearResult := stablePublicationResults(t)
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, test := range []struct{ name, method, url, body string }{
		{"set query override", http.MethodPut, "/v1/applications/" + testApplicationID + "/publications/3/stable-slot?command.version_id=" + testApplicationVersionID, `{"versionId":"` + testApplicationVersionID + `"}`},
		{"clear missing revision", http.MethodDelete, "/v1/applications/" + testApplicationID + "/publications/3/stable-slot", ""},
		{"clear duplicate revision", http.MethodDelete, "/v1/applications/" + testApplicationID + "/publications/3/stable-slot?expected_publication_revision=1&expected_publication_revision=2", ""},
		{"clear unknown query", http.MethodDelete, "/v1/applications/" + testApplicationID + "/publications/3/stable-slot?expected_publication_revision=1&other=2", ""},
		{"clear body", http.MethodDelete, "/v1/applications/" + testApplicationID + "/publications/3/stable-slot?expected_publication_revision=1", `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			setHandler := &fakeStableSetHandler{result: setResult}
			clearHandler := &fakeStableClearHandler{result: clearResult}
			publicationService := NewApplicationPublicationServiceWithStable(nil, setHandler, clearHandler)
			servers, err := NewServers(ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil), NewApplicationReviewService(nil, nil, nil), publicationService, NewTesterJoinLinkService(nil, nil), NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil), NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(test.method, test.url, strings.NewReader(test.body))
			request.Header.Set(IdentityHeader, token)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			servers.HTTP.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || setHandler.calls != 0 || clearHandler.calls != 0 {
				t.Fatalf("status=%d setCalls=%d clearCalls=%d body=%s", recorder.Code, setHandler.calls, clearHandler.calls, recorder.Body.String())
			}
		})
	}
}
