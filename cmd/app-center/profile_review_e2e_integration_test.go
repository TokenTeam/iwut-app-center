package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP015_BR_PRF_015_022_ProfileSubmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "profile-admin", "APPROVED")
	otherToken := e2eSignIdentity(t, key, "profile-other-admin", "APPROVED")
	pendingToken := e2eSignIdentity(t, key, "profile-admin", "PENDING")
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: database.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &catalogE2EDNSResolver{}
	app, cleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(runDone) }()
	t.Cleanup(func() {
		if e := app.Stop(); e != nil {
			t.Error(e)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app did not stop")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })
	client := profilev1.NewApplicationProfileRevisionClient(connection)
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	createApp := func(name string) string {
		t.Helper()
		code, body := e2eHTTPCreate(t, httpAddress, adminToken, name)
		var app applicationv1.CreateApplicationResponse
		if code != 201 || protojson.Unmarshal(body, &app) != nil {
			t.Fatalf("create app: %d %s", code, body)
		}
		return app.GetId()
	}
	reviewClient := reviewv1.NewApplicationProfileReviewClient(connection)
	appID := createApp("Profile_Submission_App")
	draft, err := client.CreateApplicationProfileRevision(adminCtx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: appID, Profile: &profilev1.ApplicationProfileContent{DisplayName: "Cafe\u0301", Description: structpb.NewNullValue(), Icon: structpb.NewStringValue("opaque://127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	submitHTTP := func(token, appID, id, query, payload string) (int, string, []byte) {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+appID+"/profile-revisions/"+id+"/reviews"+query, strings.NewReader(payload))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		r.Header.Set("If-Match", `"999"`)
		res, e := (&http.Client{Timeout: 10 * time.Second}).Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		body, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		return res.StatusCode, res.Header.Get("ETag"), body
	}
	for _, tc := range []struct {
		token, app, id, query, body string
		code                        int
	}{
		{"", appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1"}`, 401},
		{otherToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1"}`, 403},
		{pendingToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1"}`, 403},
		{adminToken, appID, draft.ProfileRevisionId, "?expectedRevision=1", `{"expectedRevision":"1"}`, 400},
		{adminToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1","snapshot":"secret"}`, 400},
		{adminToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1","expected_revision":"1"}`, 400},
		{adminToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":null}`, 400},
		{adminToken, appID, draft.ProfileRevisionId, "", `{}`, 400},
		{adminToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"2"}`, 409},
		{adminToken, appID, "01995000-0000-7000-8000-000000000999", "", `{"expectedRevision":"1"}`, 404},
	} {
		code, _, body := submitHTTP(tc.token, tc.app, tc.id, tc.query, tc.body)
		if code != tc.code || bytes.Contains(body, []byte("secret")) {
			t.Fatalf("want%d got%d %s", tc.code, code, body)
		}
	}
	code, etag, body := submitHTTP(adminToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1"}`)
	var response reviewv1.SubmitApplicationProfileRevisionReviewResponse
	if code != 201 || etag != `"2"` || protojson.Unmarshal(body, &response) != nil {
		t.Fatalf("submit=%d %s %s", code, etag, body)
	}
	if response.ProfileRevision.ReviewStatus != "SUBMITTED" || response.ProfileRevision.Revision != 2 || response.Review.SourceRevision != 1 || response.Review.Attempt != 1 || response.Review.Status != "PENDING" || response.Review.Snapshot.DisplayName != "Café" || response.Review.Snapshot.Icon.GetStringValue() != "opaque://127.0.0.1" || response.Review.SubmittedBy != "profile-admin" || !bytes.Contains(body, []byte(`"decision":null`)) || !bytes.Contains(body, []byte(`"description":null`)) {
		t.Fatal("snapshot/submission mismatch")
	}
	code, _, body = submitHTTP(adminToken, appID, draft.ProfileRevisionId, "", `{"expectedRevision":"1"}`)
	if code != 409 || !bytes.Contains(body, []byte("ERROR_REASON_APPLICATION_PROFILE_REVISION_NOT_DRAFT")) {
		t.Fatalf("duplicate %d %s", code, body)
	}
	_, err = client.UpdateApplicationProfileRevision(adminCtx, &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: appID, ProfileRevisionId: draft.ProfileRevisionId, ExpectedRevision: 2, Profile: &profilev1.ApplicationProfileContent{DisplayName: "overwrite", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}})
	if status.Code(err) != codes.Aborted {
		t.Fatal("submitted editable", err)
	}
	otherApp := createApp("Profile_Submission_Grpc")
	otherDraft, err := client.CreateApplicationProfileRevision(adminCtx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: otherApp, Profile: &profilev1.ApplicationProfileContent{DisplayName: "gRPC", Description: structpb.NewStringValue("description"), Icon: structpb.NewNullValue()}})
	if err != nil {
		t.Fatal(err)
	}
	request := &reviewv1.SubmitApplicationProfileRevisionReviewRequest{ApplicationId: otherApp, ProfileRevisionId: otherDraft.ProfileRevisionId, Command: &reviewv1.SubmitApplicationProfileRevisionReviewCommand{ExpectedRevision: 1}}
	if _, err = reviewClient.SubmitApplicationProfileRevisionReview(ctx, request); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	grpcResult, err := reviewClient.SubmitApplicationProfileRevisionReview(adminCtx, request)
	if err != nil || grpcResult.ProfileRevision.Revision != 2 || grpcResult.Review.Snapshot.Description.GetStringValue() != "description" {
		t.Fatalf("grpc=%v %v", grpcResult, err)
	}
	if _, err = reviewClient.SubmitApplicationProfileRevisionReview(adminCtx, request); status.Code(err) != codes.Aborted {
		t.Fatal(err)
	}
	// Generated HTTP client must encode only command and bind both snake-case path fields.
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	generated := reviewv1.NewApplicationProfileReviewHTTPClient(httpClient)
	generatedApp := createApp("Profile_Submission_HTTP_Client")
	generatedDraft, err := client.CreateApplicationProfileRevision(adminCtx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: generatedApp, Profile: &profilev1.ApplicationProfileContent{DisplayName: "generated", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}})
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{transport.IdentityHeader: []string{adminToken}}
	generatedResult, err := generated.SubmitApplicationProfileRevisionReview(ctx, &reviewv1.SubmitApplicationProfileRevisionReviewRequest{ApplicationId: generatedApp, ProfileRevisionId: generatedDraft.ProfileRevisionId, Command: &reviewv1.SubmitApplicationProfileRevisionReviewCommand{ExpectedRevision: 1}}, khttp.Header(&headers))
	if err != nil || generatedResult.ProfileRevision.Revision != 2 || headers.Get("ETag") != `"2"` {
		t.Fatalf("generated=%v %v etag=%q", generatedResult, err, headers.Get("ETag"))
	}
	// Submission revalidation must fail without repairing damaged persisted content.
	brokenApp := createApp("Profile_Submission_Broken")
	broken, err := client.CreateApplicationProfileRevision(adminCtx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: brokenApp, Profile: &profilev1.ApplicationProfileContent{DisplayName: "valid", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = database.Collection("application_profile_revisions").UpdateOne(ctx, bson.M{"profileRevisionId": broken.ProfileRevisionId}, bson.M{"$set": bson.M{"displayName": "Cafe\u0301"}}); err != nil {
		t.Fatal(err)
	}
	code, _, body = submitHTTP(adminToken, brokenApp, broken.ProfileRevisionId, "", `{"expectedRevision":"1"}`)
	if code != 400 || !bytes.Contains(body, []byte("ERROR_REASON_INVALID_APPLICATION_PROFILE_CONTENT")) {
		t.Fatalf("content=%d %s", code, body)
	}
	if _, err = database.Collection("application_profile_revisions").UpdateOne(ctx, bson.M{"profileRevisionId": broken.ProfileRevisionId}, bson.M{"$set": bson.M{"displayName": "valid"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = database.Collection("application_profiles").UpdateOne(ctx, bson.M{"applicationId": brokenApp}, bson.M{"$set": bson.M{"workingProfileRevisionId": nil}}); err != nil {
		t.Fatal(err)
	}
	code, _, body = submitHTTP(adminToken, brokenApp, broken.ProfileRevisionId, "", `{"expectedRevision":"1"}`)
	if code != 500 || !bytes.Contains(body, []byte("ERROR_REASON_APPLICATION_PROFILE_STATE_INCONSISTENT")) {
		t.Fatalf("pointer=%d %s", code, body)
	}
	if count, err := database.Collection("application_profile_reviews").CountDocuments(ctx, bson.M{}); err != nil || count != 3 {
		t.Fatalf("reviews=%d %v", count, err)
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("unexpected external inspection")
	}
}
