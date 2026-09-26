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
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"iwut-app-center/internal/shared"
)

func TestE2E_UCAPP013_BR_PRF_001_007_ProfileDraft(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "profile-admin", "APPROVED")
	otherToken := e2eSignIdentity(t, key, "profile-other-admin", "APPROVED")
	pendingToken := e2eSignIdentity(t, key, "profile-admin", "PENDING")
	now := time.Now().UTC()
	userToken := e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": "profile-user", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "profile-user"})
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
	appID := createApp("Profile_HTTP_App")
	grpcAppID := createApp("Profile_GRPC_App")
	httpNullAppID := createApp("Profile_HTTP_Null_App")
	grpcNullAppID := createApp("Profile_GRPC_Null_App")
	profileHTTP := func(token, id, query, payload string) (int, string, []byte) {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+id+"/profile-revisions"+query, strings.NewReader(payload))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		response, e := (&http.Client{Timeout: 10 * time.Second}).Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		body, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, response.Header.Get("ETag"), body
	}
	const valid = `{"displayName":"Cafe\u0301","description":"Re\u0301sume\u0301","icon":"opaque:icon-e\u0301"}`
	longName := "q" + strings.Repeat("\u0301", 40)
	nulls := `{"displayName":"` + longName + `","description":null,"icon":null}`
	for _, tc := range []struct {
		token, id, query, body, reason string
		code                           int
	}{
		{"", appID, "", `{private-malformed`, transport.ReasonDeveloperIdentityRequired, 401},
		{"private-invalid-jws", appID, "", valid, transport.ReasonInvalidDeveloperIdentity, 401},
		{userToken, appID, "", valid, transport.ReasonDeveloperApprovalRequired, 403},
		{pendingToken, appID, "", valid, transport.ReasonDeveloperApprovalRequired, 403},
		{otherToken, appID, "", valid, transport.ReasonApplicationAdminRequired, 403},
		{adminToken, "invalid", "", valid, transport.ReasonInvalidApplicationID, 400},
		{adminToken, "018f0000-0000-7000-8000-000000009999", "", valid, transport.ReasonApplicationNotFound, 404},
		{adminToken, appID, "?displayName=forged", valid, transport.ReasonInvalidCreateApplicationProfileRevisionRequest, 400},
		{adminToken, appID, "", `{"displayName":"x","icon":null}`, transport.ReasonInvalidCreateApplicationProfileRevisionRequest, 400},
		{adminToken, appID, "", `{"displayName":"x","description":null}`, transport.ReasonInvalidCreateApplicationProfileRevisionRequest, 400},
		{adminToken, appID, "", `{"displayName":"x","description":false,"icon":null}`, transport.ReasonInvalidCreateApplicationProfileRevisionRequest, 400},
		{adminToken, appID, "", `{"displayName":"x","description":null,"icon":null,"revision":4}`, transport.ReasonInvalidCreateApplicationProfileRevisionRequest, 400},
		{adminToken, appID, "", `{"displayName":"x","display_name":"y","description":null,"icon":null}`, transport.ReasonInvalidCreateApplicationProfileRevisionRequest, 400},
		{adminToken, appID, "", `{"displayName":" ","description":null,"icon":null}`, transport.ReasonInvalidApplicationDisplayName, 400},
		{adminToken, appID, "", `{"displayName":"x","description":"","icon":null}`, transport.ReasonInvalidApplicationDescription, 400},
		{adminToken, appID, "", `{"displayName":"x","description":null,"icon":"\u200b"}`, transport.ReasonInvalidApplicationIcon, 400},
	} {
		code, _, body := profileHTTP(tc.token, tc.id, tc.query, tc.body)
		if code != tc.code || !bytes.Contains(body, []byte(tc.reason)) || bytes.Contains(body, []byte("private-")) {
			t.Fatalf("HTTP expected %d %s, got %d %s", tc.code, tc.reason, code, body)
		}
	}
	request := func(id string) *profilev1.CreateApplicationProfileRevisionRequest {
		return &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: id, DisplayName: "Cafe\u0301", Description: structpb.NewStringValue("Re\u0301sume\u0301"), Icon: structpb.NewStringValue("opaque:icon-e\u0301")}
	}
	for _, tc := range []struct {
		token   string
		request *profilev1.CreateApplicationProfileRevisionRequest
		code    codes.Code
	}{
		{"", request(grpcAppID), codes.Unauthenticated}, {userToken, request(grpcAppID), codes.PermissionDenied}, {pendingToken, request(grpcAppID), codes.PermissionDenied}, {otherToken, request(grpcAppID), codes.PermissionDenied}, {adminToken, request("bad-id"), codes.InvalidArgument}, {adminToken, request("018f0000-0000-7000-8000-000000009999"), codes.NotFound},
	} {
		_, e := client.CreateApplicationProfileRevision(metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, tc.token)), tc.request)
		if status.Code(e) != tc.code {
			t.Fatalf("gRPC expected %v got %v", tc.code, e)
		}
	}
	for _, field := range []string{"description", "icon"} {
		for _, value := range []*structpb.Value{nil, {}, structpb.NewBoolValue(true), structpb.NewNumberValue(1), structpb.NewStructValue(&structpb.Struct{}), structpb.NewListValue(&structpb.ListValue{}), structpb.NewStringValue("")} {
			r := request(grpcAppID)
			if field == "description" {
				r.Description = value
			} else {
				r.Icon = value
			}
			if _, e := client.CreateApplicationProfileRevision(adminCtx, r); status.Code(e) != codes.InvalidArgument {
				t.Fatalf("invalid gRPC %s Value %v got %v", field, value, e)
			}
		}
	}
	if count, e := database.Collection("application_profile_revisions").CountDocuments(ctx, bson.M{}); e != nil || count != 0 {
		t.Fatalf("failed commands wrote profiles: %d %v", count, e)
	}
	code, etag, body := profileHTTP(adminToken, appID, "", valid)
	var httpRevision profilev1.CreateApplicationProfileRevisionResponse
	if code != 201 || etag != `"1"` || protojson.Unmarshal(body, &httpRevision) != nil {
		t.Fatalf("HTTP create=%d %s %s", code, etag, body)
	}
	grpcRevision, err := client.CreateApplicationProfileRevision(adminCtx, request(grpcAppID))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*profilev1.CreateApplicationProfileRevisionResponse{&httpRevision, grpcRevision} {
		if !shared.IsUUIDv7(r.GetProfileRevisionId()) || r.GetSequence() != 1 || r.GetDisplayName() != "Café" || r.GetDescription().GetStringValue() != "Résumé" || r.GetIcon().GetStringValue() != "opaque:icon-é" || r.GetReviewStatus() != "DRAFT" || r.GetRevision() != 1 || r.GetCreatedBy() != "profile-admin" || r.GetUpdatedBy() != r.GetCreatedBy() || !r.GetCreatedAt().AsTime().Equal(r.GetUpdatedAt().AsTime()) {
			t.Fatalf("created profile=%v", r)
		}
		var stored bson.M
		if e := database.Collection("application_profile_revisions").FindOne(ctx, bson.M{"profileRevisionId": r.GetProfileRevisionId()}).Decode(&stored); e != nil {
			t.Fatal(e)
		}
		if stored["displayName"] != "Café" || stored["revision"] != int64(1) {
			t.Fatalf("stored=%v", stored)
		}
		var projection bson.M
		if e := database.Collection("application_profiles").FindOne(ctx, bson.M{"applicationId": r.GetApplicationId()}).Decode(&projection); e != nil {
			t.Fatal(e)
		}
		if projection["workingProfileRevisionId"] != r.GetProfileRevisionId() || projection["currentPublishedProfileRevisionId"] != nil {
			t.Fatalf("projection=%v", projection)
		}
	}
	code, _, body = profileHTTP(adminToken, httpNullAppID, "", nulls)
	var httpNull profilev1.CreateApplicationProfileRevisionResponse
	if code != 201 || protojson.Unmarshal(body, &httpNull) != nil || !bytes.Contains(body, []byte(`"description":null`)) || !bytes.Contains(body, []byte(`"icon":null`)) {
		t.Fatalf("HTTP null create=%d %s", code, body)
	}
	nullRequest := request(grpcNullAppID)
	nullRequest.DisplayName = longName
	nullRequest.Description = structpb.NewNullValue()
	nullRequest.Icon = structpb.NewNullValue()
	grpcNull, err := client.CreateApplicationProfileRevision(adminCtx, nullRequest)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*profilev1.CreateApplicationProfileRevisionResponse{&httpNull, grpcNull} {
		if r.GetDisplayName() != longName {
			t.Fatalf("strict NFC was changed by stream-safe insertion: %q", r.GetDisplayName())
		}
		if r.GetDescription() == nil || r.GetIcon() == nil {
			t.Fatal("explicit null Value missing")
		}
		if _, ok := r.GetDescription().GetKind().(*structpb.Value_NullValue); !ok {
			t.Fatal("description not explicit null")
		}
		if _, ok := r.GetIcon().GetKind().(*structpb.Value_NullValue); !ok {
			t.Fatal("icon not explicit null")
		}
		var stored bson.M
		if e := database.Collection("application_profile_revisions").FindOne(ctx, bson.M{"profileRevisionId": r.GetProfileRevisionId()}).Decode(&stored); e != nil {
			t.Fatal(e)
		}
		if value, ok := stored["description"]; !ok || value != nil {
			t.Fatal("stored description must be explicit null")
		}
		if value, ok := stored["icon"]; !ok || value != nil {
			t.Fatal("stored icon must be explicit null")
		}
	}
	code, _, body = profileHTTP(adminToken, appID, "", valid)
	if code != 409 || !bytes.Contains(body, []byte(transport.ReasonApplicationProfileWorkRevisionAlreadyExists)) {
		t.Fatalf("HTTP duplicate=%d %s", code, body)
	}
	if _, e := client.CreateApplicationProfileRevision(adminCtx, request(grpcAppID)); status.Code(e) != codes.Aborted {
		t.Fatalf("gRPC duplicate=%v", e)
	}
	if count, e := database.Collection("application_profile_revisions").CountDocuments(ctx, bson.M{}); e != nil || count != 4 {
		t.Fatalf("profile count=%d %v", count, e)
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("opaque icon unexpectedly triggered DNS/URL inspection")
	}
	for _, id := range []string{appID, grpcAppID, httpNullAppID, grpcNullAppID} {
		var stored bson.M
		if e := database.Collection("applications").FindOne(ctx, bson.M{"id": id}).Decode(&stored); e != nil {
			t.Fatal(e)
		}
		if stored["nextProfileRevisionSequence"] != int32(2) {
			t.Fatalf("sequence counter=%v", stored)
		}
	}
}
