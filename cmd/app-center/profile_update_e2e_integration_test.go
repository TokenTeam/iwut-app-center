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

func TestE2E_UCAPP014_BR_PRF_008_014_ProfileReplacement(t *testing.T) {
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
	appID := createApp("Profile_Update_App")
	original, err := client.CreateApplicationProfileRevision(adminCtx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: appID, Profile: &profilev1.ApplicationProfileContent{DisplayName: "original", Description: structpb.NewStringValue("desc"), Icon: structpb.NewStringValue("icon")}})
	if err != nil {
		t.Fatal(err)
	}
	updateHTTP := func(token, id, etag, query, payload string) (int, string, []byte) {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+httpAddress+"/v1/applications/"+appID+"/profile-revisions/"+id+query, strings.NewReader(payload))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		if etag != "" {
			r.Header.Set("If-Match", etag)
		}
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
	const payload = `{"displayName":"Cafe\u0301","description":null,"icon":null}`
	id := original.ProfileRevisionId
	for _, tc := range []struct {
		token, id, etag, query, body string
		code                         int
	}{
		{"", id, `"1"`, "", payload, 401}, {otherToken, id, `"1"`, "", payload, 403}, {pendingToken, id, `"1"`, "", payload, 403}, {adminToken, id, "", "", payload, 428}, {adminToken, id, `W/"1"`, "", payload, 400}, {adminToken, id, `"1"`, "?expectedRevision=4", payload, 400}, {adminToken, id, `"1"`, "", `{"displayName":"new","description":null}`, 400}, {adminToken, id, `"1"`, "", `{"displayName":"new","description":null,"icon":null,"createdBy":"forged"}`, 400}, {adminToken, "01995000-0000-7000-8000-000000000999", `"1"`, "", payload, 404},
	} {
		code, _, body := updateHTTP(tc.token, tc.id, tc.etag, tc.query, tc.body)
		if code != tc.code {
			t.Fatalf("want %d got %d %s", tc.code, code, body)
		}
	}
	code, etag, body := updateHTTP(adminToken, id, `"1"`, "", payload)
	var updated profilev1.UpdateApplicationProfileRevisionResponse
	if code != 200 || etag != `"2"` || protojson.Unmarshal(body, &updated) != nil {
		t.Fatalf("update=%d %s %s", code, etag, body)
	}
	if updated.DisplayName != "Café" || updated.CreatedBy != original.CreatedBy || !updated.CreatedAt.AsTime().Equal(original.CreatedAt.AsTime()) || updated.Sequence != original.Sequence || updated.ReviewStatus != "DRAFT" || !bytes.Contains(body, []byte(`"description":null`)) || !bytes.Contains(body, []byte(`"icon":null`)) {
		t.Fatal("replacement/audit response")
	}
	code, etag, body = updateHTTP(adminToken, id, `"2"`, "", payload)
	var noop profilev1.UpdateApplicationProfileRevisionResponse
	if code != 200 || etag != `"2"` || protojson.Unmarshal(body, &noop) != nil || !noop.UpdatedAt.AsTime().Equal(updated.UpdatedAt.AsTime()) {
		t.Fatalf("noop=%d %s", code, body)
	}
	code, _, body = updateHTTP(adminToken, id, `"1"`, "", payload)
	if code != 412 {
		t.Fatalf("stale normalized noop=%d %s", code, body)
	}
	request := &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: appID, ProfileRevisionId: id, ExpectedRevision: 2, Profile: &profilev1.ApplicationProfileContent{DisplayName: "gRPC", Description: structpb.NewStringValue("new description"), Icon: structpb.NewStringValue("opaque://127.0.0.1")}}
	grpcResult, err := client.UpdateApplicationProfileRevision(adminCtx, request)
	if err != nil || grpcResult.Revision != 3 || grpcResult.Description.GetStringValue() != "new description" {
		t.Fatalf("grpc=%v %v", grpcResult, err)
	}
	if _, err = client.UpdateApplicationProfileRevision(adminCtx, request); status.Code(err) != codes.Aborted {
		t.Fatal("stale grpc", err)
	}
	request.ExpectedRevision = 3
	request.Profile.Description = nil
	if _, err = client.UpdateApplicationProfileRevision(adminCtx, request); status.Code(err) != codes.InvalidArgument {
		t.Fatal("missing grpc field", err)
	}
	request.Profile.Description = structpb.NewNullValue()
	request.ExpectedRevision = 0
	if _, err = client.UpdateApplicationProfileRevision(adminCtx, request); status.Code(err) != codes.InvalidArgument {
		t.Fatal("missing grpc revision", err)
	}
	if _, err = database.Collection("application_profile_revisions").UpdateOne(ctx, bson.M{"profileRevisionId": id}, bson.M{"$set": bson.M{"reviewStatus": "SUBMITTED", "revision": int64(4)}}); err != nil {
		t.Fatal(err)
	}
	code, _, body = updateHTTP(adminToken, id, `"4"`, "", payload)
	if code != 409 {
		t.Fatalf("frozen HTTP=%d %s", code, body)
	}
	request.ExpectedRevision = 4
	if _, err = client.UpdateApplicationProfileRevision(adminCtx, request); status.Code(err) != codes.Aborted {
		t.Fatal("frozen grpc", err)
	}
	// Exercise the generated HTTP client itself: only the nested profile body
	// may be encoded, never path IDs or the native-gRPC expected revision.
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	generated := profilev1.NewApplicationProfileRevisionHTTPClient(httpClient)
	generatedAppID := createApp("Profile_Generated_HTTP_App")
	headers := http.Header{transport.IdentityHeader: []string{adminToken}}
	generatedDraft, err := generated.CreateApplicationProfileRevision(ctx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: generatedAppID, Profile: &profilev1.ApplicationProfileContent{DisplayName: "generated", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}}, khttp.Header(&headers))
	if err != nil || generatedDraft.Revision != 1 || headers.Get("ETag") != `"1"` {
		t.Fatalf("generated create=%v %v etag=%q", generatedDraft, err, headers.Get("ETag"))
	}
	headers = http.Header{transport.IdentityHeader: []string{adminToken}, "If-Match": []string{`"1"`}}
	generatedUpdate, err := generated.UpdateApplicationProfileRevision(ctx, &profilev1.UpdateApplicationProfileRevisionRequest{ApplicationId: generatedAppID, ProfileRevisionId: generatedDraft.ProfileRevisionId, ExpectedRevision: 999, Profile: &profilev1.ApplicationProfileContent{DisplayName: "generated updated", Description: structpb.NewNullValue(), Icon: structpb.NewStringValue("opaque")}}, khttp.Header(&headers))
	if err != nil || generatedUpdate.Revision != 2 || headers.Get("ETag") != `"2"` {
		t.Fatalf("generated update=%v %v etag=%q", generatedUpdate, err, headers.Get("ETag"))
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("unexpected external URL inspection")
	}
}
