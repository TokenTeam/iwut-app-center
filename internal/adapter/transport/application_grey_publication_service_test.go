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

type fakeGreySetHandler struct {
	result *publicationdomain.PlaceInTestResult
	calls  int
}

func (h *fakeGreySetHandler) Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.SetGreyRolloutCommand) (*publicationdomain.PlaceInTestResult, error) {
	h.calls++
	return h.result, nil
}

type fakeGreyClearHandler struct {
	result *publicationdomain.PlaceInTestResult
	calls  int
}

func (h *fakeGreyClearHandler) Handle(context.Context, shared.DeveloperIdentity, shared.ApplicationID, int32, publicationusecase.ClearGreyRolloutCommand) (*publicationdomain.PlaceInTestResult, error) {
	h.calls++
	return h.result, nil
}

func greyPublicationResults(t *testing.T) (*publicationdomain.PlaceInTestResult, *publicationdomain.PlaceInTestResult) {
	t.Helper()
	stableID := publicationdomain.ApplicationVersionID("018f7777-7777-7777-8777-777777777778")
	publicationID := publicationdomain.ApplicationPublicationID("018f7777-7777-7777-8777-777777777779")
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	publication, err := publicationdomain.RestoreApplicationPublicationSlots(publicationID, shared.ApplicationID(testApplicationID), 3, nil, &stableID, 1, "admin", at, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := publicationdomain.NewApplicationVersionReviewSnapshot("1.0", "https://example.edu", 3, 5, []string{}, []publicationdomain.ScopeName{}, []publicationdomain.ScopeName{})
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(1)
	approved, err := publicationdomain.NewTestPlacementCandidate(shared.ApplicationID(testApplicationID), 3, publicationdomain.ApplicationVersionID(testApplicationVersionID), publicationdomain.ApplicationReviewID(testApplicationReviewID), 3, *snapshot, publication, &expected)
	if err != nil {
		t.Fatal(err)
	}
	exposure, _ := publicationdomain.NewExposureBasisPoints(500)
	candidate, err := publicationdomain.NewGreyPlacementCandidate(approved, exposure)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := publicationdomain.NewCohortSeed(make([]byte, 32))
	rolloutID := publicationdomain.GreyRolloutID("018f7777-7777-7777-8777-777777777780")
	set, err := candidate.SetGrey(&rolloutID, &seed, "018f7777-7777-7777-8777-777777777781", "admin", &publicationdomain.PublicationValidation{ScopeCatalogRevision: 11, PreflightPolicyVersion: "submit-v1"}, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	clearCandidate, err := publicationdomain.NewGreyClearCandidate(set.Publication(), 2)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := clearCandidate.Clear("018f7777-7777-7777-8777-777777777782", "admin", at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return set, cleared
}

func TestPublicationService_UCAPP021_GreyResourceAndHistory(t *testing.T) {
	set, cleared := greyPublicationResults(t)
	service := NewApplicationPublicationServiceWithGrey(nil, nil, nil, &fakeGreySetHandler{result: set}, &fakeGreyClearHandler{result: cleared})
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved})
	setResponse, err := service.SetGreyRollout(ctx, &publicationv1.SetGreyRolloutRequest{ApplicationId: testApplicationID, RpcApiMajor: 3, Command: &publicationv1.SetGreyRolloutCommand{VersionId: testApplicationVersionID, ExposureBasisPoints: 500, ExpectedPublicationRevision: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if setResponse.Publication.GetGreyRollout().GetExposureBasisPoints() != 500 || setResponse.History.GreyRolloutId == nil || setResponse.History.NewExposureBasisPoints == nil || setResponse.History.PreviousExposureBasisPoints != nil {
		t.Fatalf("set response=%#v", setResponse)
	}
	clearResponse, err := service.ClearGreyRollout(ctx, &publicationv1.ClearGreyRolloutRequest{ApplicationId: testApplicationID, RpcApiMajor: 3, ExpectedPublicationRevision: 2})
	if err != nil {
		t.Fatal(err)
	}
	if clearResponse.Publication.GreyRollout != nil || clearResponse.Publication.StableVersionId == nil || clearResponse.History.PreviousExposureBasisPoints == nil || clearResponse.History.NewExposureBasisPoints != nil {
		t.Fatalf("clear response=%#v", clearResponse)
	}
}

func TestPublicationService_UCAPP021_StrictGreyHTTPInputs(t *testing.T) {
	set, cleared := greyPublicationResults(t)
	token := signToken(t, tokenOptions{claims: validClaims(fixedNow())})
	for _, test := range []struct{ name, method, url, body string }{
		{"set query override", http.MethodPut, "/v1/applications/" + testApplicationID + "/publications/3/grey-rollout?command.exposure_basis_points=100", `{"versionId":"` + testApplicationVersionID + `","exposureBasisPoints":500,"expectedPublicationRevision":"1"}`},
		{"clear missing revision", http.MethodDelete, "/v1/applications/" + testApplicationID + "/publications/3/grey-rollout", ""},
		{"clear body", http.MethodDelete, "/v1/applications/" + testApplicationID + "/publications/3/grey-rollout?expected_publication_revision=2", `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			setHandler, clearHandler := &fakeGreySetHandler{result: set}, &fakeGreyClearHandler{result: cleared}
			publicationService := NewApplicationPublicationServiceWithGrey(nil, nil, nil, setHandler, clearHandler)
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
