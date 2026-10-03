package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"testing"
	"time"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP007_BR_PUB_001_010_TestPlacement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const (
		adminID    = "auth-e2e-review-admin"
		reviewerID = "auth-e2e-reviewer"
		systemID   = "auth-e2e-system"
	)
	adminToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	reviewerToken := e2eSignReviewerIdentity(t, privateKey, reviewerID, "app.version.review", "app.profile.review")

	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	_, developerStatusServer := e2eStartAuthServer(t, authAddress)
	developerStatusServer.systemAuthID = systemID
	developerStatusServer.SetStatus(adminID, developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED)
	resolver := &e2eDNSResolver{addresses: map[string][]netip.Addr{
		"example.edu": {netip.MustParseAddr("8.8.8.8")},
	}}
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress,
		config.ScopeCatalogCacheTTLEnv:   "1ns",
	}))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	app, appCleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatalf("wireAppWithResolver() error = %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop() error = %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop within 15s")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run() error = %v", runErr)
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	createStatus, applicationBody := e2eHTTPCreate(t, httpAddress, adminToken, "Review_Decision_E2E_App")
	if createStatus != http.StatusCreated {
		t.Fatalf("create predecessor application status = %d; body = %s", createStatus, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatalf("decode predecessor application: %v", err)
	}

	createAndSubmit := func(label string) (*applicationversionv1.CreateApplicationVersionResponse, *applicationreviewv1.SubmitApplicationVersionReviewResponse) {
		t.Helper()
		versionStatus, _, versionBody := e2eHTTPCreateVersion(
			t, httpAddress, adminToken, application.GetId(), label,
			[]string{"user.profile.v1"}, []string{"profile.basic"}, nil,
		)
		if versionStatus != http.StatusCreated {
			t.Fatalf("create predecessor version status = %d; body = %s", versionStatus, versionBody)
		}
		var version applicationversionv1.CreateApplicationVersionResponse
		if err := protojson.Unmarshal(versionBody, &version); err != nil {
			t.Fatalf("decode predecessor version: %v", err)
		}
		submitStatus, _, submitBody := e2eHTTPSubmitReview(t, httpAddress, adminToken, application.GetId(), version.GetVersionId(), 1)
		if submitStatus != http.StatusCreated {
			t.Fatalf("submit predecessor review status = %d; body = %s", submitStatus, submitBody)
		}
		var submitted applicationreviewv1.SubmitApplicationVersionReviewResponse
		if err := protojson.Unmarshal(submitBody, &submitted); err != nil {
			t.Fatalf("decode predecessor review: %v", err)
		}
		return &version, &submitted
	}

	approve := func(label string) string {
		version, review := createAndSubmit(label)
		code, body := e2eHTTPDecideReview(t, httpAddress, reviewerToken, application.GetId(), version.GetVersionId(), review.GetReview().GetReviewId(), "APPROVE", "app-version-review-v2", []string{"requested-access-reviewed", "content-policy-reviewed", "launch-url-content-reviewed", "oauth-redirects-reviewed"}, "")
		if code != http.StatusOK {
			t.Fatalf("approve status=%d body=%s", code, body)
		}
		return version.GetVersionId()
	}
	firstVersion := approve("v7.0.0")
	secondVersion := approve("v7.0.1")
	put := func(token string, major int32, version string, expected *int64) (int, []byte) {
		payload, err := json.Marshal(map[string]any{"versionId": version, "expectedPublicationRevision": expected})
		if err != nil {
			t.Fatal(err)
		}
		path := fmt.Sprintf("http://%s/v1/applications/%s/publications/%d/test-slot", httpAddress, application.GetId(), major)
		request, err := http.NewRequestWithContext(ctx, http.MethodPut, path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(transport.IdentityHeader, token)
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	code, body := put(adminToken, 3, firstVersion, nil)
	if code != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte(transport.ReasonApplicationProfileRequired)) {
		t.Fatalf("profile gate status=%d body=%s", code, body)
	}
	e2eApproveApplicationProfile(t, ctx, connection, application.GetId(), adminToken, reviewerToken)
	code, body = put("", 3, firstVersion, nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("identity status=%d body=%s", code, body)
	}
	otherToken := e2eSignIdentity(t, privateKey, "other-admin", "APPROVED")
	code, body = put(otherToken, 3, firstVersion, nil)
	if code != http.StatusForbidden {
		t.Fatalf("admin status=%d body=%s", code, body)
	}
	code, body = put(adminToken, 2, firstVersion, nil)
	if code != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte(transport.ReasonApplicationVersionRpcApiIncompatible)) {
		t.Fatalf("range status=%d body=%s", code, body)
	}
	code, body = put(adminToken, 3, firstVersion, nil)
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", code, body)
	}
	var created publicationv1.PlaceApprovedVersionInTestSlotResponse
	if err := protojson.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if !created.GetChanged() || created.GetPublication().GetRevision() != 1 || created.GetHistory() == nil || created.GetHistory().PreviousVersionId != nil || created.GetHistory().GetScopeCatalogRevision() != 11 {
		t.Fatalf("created=%v", &created)
	}
	code, body = put(adminToken, 3, firstVersion, nil)
	if code != http.StatusConflict || !bytes.Contains(body, []byte(transport.ReasonApplicationPublicationAlreadyExists)) {
		t.Fatalf("duplicate status=%d body=%s", code, body)
	}
	revision := int64(1)
	code, body = put(adminToken, 3, secondVersion, &revision)
	if code != http.StatusOK {
		t.Fatalf("replace status=%d body=%s", code, body)
	}
	var replaced publicationv1.PlaceApprovedVersionInTestSlotResponse
	if err := protojson.Unmarshal(body, &replaced); err != nil {
		t.Fatal(err)
	}
	if !replaced.GetChanged() || replaced.GetPublication().GetRevision() != 2 || replaced.GetHistory().GetPreviousVersionId() != firstVersion {
		t.Fatalf("replaced=%v", &replaced)
	}
	client := publicationv1.NewApplicationPublicationClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	_, err = client.PlaceApprovedVersionInTestSlot(grpcCtx, &publicationv1.PlaceApprovedVersionInTestSlotRequest{ApplicationId: application.GetId(), RpcApiMajor: 3, Command: &publicationv1.PlaceApprovedVersionInTestSlotCommand{VersionId: firstVersion, ExpectedPublicationRevision: &revision}})
	if status.Code(err) != codes.Aborted || e2eErrorReason(status.Convert(err)) != transport.ReasonApplicationPublicationRevisionConflict {
		t.Fatalf("stale gRPC error=%v", err)
	}
	revision = 2
	noop, err := client.PlaceApprovedVersionInTestSlot(grpcCtx, &publicationv1.PlaceApprovedVersionInTestSlotRequest{ApplicationId: application.GetId(), RpcApiMajor: 3, Command: &publicationv1.PlaceApprovedVersionInTestSlotCommand{VersionId: secondVersion, ExpectedPublicationRevision: &revision}})
	if err != nil || noop.GetChanged() || noop.GetHistory() != nil || noop.GetPublication().GetRevision() != 2 {
		t.Fatalf("noop=%v err=%v", noop, err)
	}
	code, body = put(adminToken, 3, secondVersion, &revision)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"changed":false`)) {
		t.Fatalf("HTTP noop status=%d body=%s", code, body)
	}
	stablePayload, _ := json.Marshal(map[string]any{"versionId": secondVersion, "expectedPublicationRevision": int64(2)})
	stableURL := fmt.Sprintf("http://%s/v1/applications/%s/publications/3/stable-slot", httpAddress, application.GetId())
	stableRequest, _ := http.NewRequestWithContext(ctx, http.MethodPut, stableURL, bytes.NewReader(stablePayload))
	stableRequest.Header.Set("Content-Type", "application/json")
	stableRequest.Header.Set(transport.IdentityHeader, adminToken)
	stableHTTPResponse, err := (&http.Client{Timeout: 10 * time.Second}).Do(stableRequest)
	if err != nil {
		t.Fatal(err)
	}
	stableBody, _ := io.ReadAll(stableHTTPResponse.Body)
	stableHTTPResponse.Body.Close()
	if stableHTTPResponse.StatusCode != http.StatusOK {
		t.Fatalf("stable set status=%d body=%s", stableHTTPResponse.StatusCode, stableBody)
	}
	var stableSet publicationv1.SetApprovedVersionInStableSlotResponse
	if err := protojson.Unmarshal(stableBody, &stableSet); err != nil {
		t.Fatal(err)
	}
	if !stableSet.GetChanged() || stableSet.GetPublication().GetRevision() != 3 || stableSet.GetPublication().GetStableVersionId() != secondVersion || stableSet.GetPublication().GetTestVersionId() != secondVersion {
		t.Fatalf("stable set=%v", &stableSet)
	}
	cleared, err := client.ClearStableSlot(grpcCtx, &publicationv1.ClearStableSlotRequest{ApplicationId: application.GetId(), RpcApiMajor: 3, ExpectedPublicationRevision: 3})
	if err != nil || !cleared.GetChanged() || cleared.GetPublication().GetRevision() != 4 || cleared.GetPublication().StableVersionId != nil || cleared.GetHistory().NewVersionId != nil {
		t.Fatalf("gRPC clear=%v err=%v", cleared, err)
	}
	revision = 4
	stableGRPC, err := client.SetApprovedVersionInStableSlot(grpcCtx, &publicationv1.SetApprovedVersionInStableSlotRequest{ApplicationId: application.GetId(), RpcApiMajor: 3, Command: &publicationv1.SetApprovedVersionInStableSlotCommand{VersionId: firstVersion, ExpectedPublicationRevision: &revision}})
	if err != nil || !stableGRPC.GetChanged() || stableGRPC.GetPublication().GetRevision() != 5 || stableGRPC.GetPublication().GetStableVersionId() != firstVersion {
		t.Fatalf("gRPC stable set=%v err=%v", stableGRPC, err)
	}
	clearRequest, _ := http.NewRequestWithContext(ctx, http.MethodDelete, stableURL+"?expected_publication_revision=5", nil)
	clearRequest.Header.Set(transport.IdentityHeader, adminToken)
	clearHTTPResponse, err := (&http.Client{Timeout: 10 * time.Second}).Do(clearRequest)
	if err != nil {
		t.Fatal(err)
	}
	clearBody, _ := io.ReadAll(clearHTTPResponse.Body)
	clearHTTPResponse.Body.Close()
	if clearHTTPResponse.StatusCode != http.StatusOK {
		t.Fatalf("HTTP clear status=%d body=%s", clearHTTPResponse.StatusCode, clearBody)
	}
	var clearHTTP publicationv1.ClearStableSlotResponse
	if err := protojson.Unmarshal(clearBody, &clearHTTP); err != nil {
		t.Fatal(err)
	}
	if !clearHTTP.GetChanged() || clearHTTP.GetPublication().GetRevision() != 6 || clearHTTP.GetPublication().StableVersionId != nil || clearHTTP.GetPublication().GetTestVersionId() != secondVersion {
		t.Fatalf("HTTP clear=%v", &clearHTTP)
	}
	for collection, want := range map[string]int64{"application_publications": 1, "application_publication_history": 6} {
		count, err := database.Collection(collection).CountDocuments(ctx, bson.M{"applicationId": application.GetId()})
		if err != nil || count != want {
			t.Fatalf("%s count=%d err=%v", collection, count, err)
		}
	}
	for _, id := range []string{firstVersion, secondVersion} {
		var version e2eApplicationVersionDocument
		if err := database.Collection("application_versions").FindOne(ctx, bson.M{"versionId": id}).Decode(&version); err != nil {
			t.Fatal(err)
		}
		if version.ReviewStatus != "APPROVED" || version.Revision != 3 {
			t.Fatalf("publication changed version=%#v", version)
		}
	}
}
