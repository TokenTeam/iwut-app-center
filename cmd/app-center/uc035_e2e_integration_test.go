package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	applicationmanagementv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management"
	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP035_AbandonDraftOverRealHTTPAndGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "auth-e2e-version-abandonment"
	developerToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	ordinaryAdminToken := e2eSignReviewerIdentity(t, privateKey, adminID)
	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	e2eStartScopeCatalogServer(t, authAddress)
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: database.Name(),
		config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress,
		config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: authAddress, config.ScopeCatalogCacheTTLEnv: "1ns",
	}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireApp(configuration)
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(runDone) }()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("app.Stop(): %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app.Run() did not stop")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("app.Run(): %v", runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	statusCode, applicationBody := e2eHTTPCreate(t, httpAddress, developerToken, "Version_Abandonment_E2E")
	if statusCode != http.StatusCreated {
		t.Fatalf("create application=%d %s", statusCode, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatal(err)
	}
	versionStatus, _, versionBody := e2eHTTPCreateVersion(t, httpAddress, developerToken, application.GetId(), "reusable-label", nil, nil, nil)
	if versionStatus != http.StatusCreated {
		t.Fatalf("create version=%d %s", versionStatus, versionBody)
	}
	var version applicationversionv1.CreateApplicationVersionResponse
	if err := protojson.Unmarshal(versionBody, &version); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://"+httpAddress+"/v1/applications/"+application.GetId()+"/versions/"+version.GetVersionId(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(transport.IdentityHeader, ordinaryAdminToken)
	request.Header.Set("If-Match", `"1"`)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("HTTP abandon=%d cache=%q body=%s", response.StatusCode, response.Header.Get("Cache-Control"), body)
	}
	var first applicationversionv1.AbandonApplicationVersionDraftResponse
	if err := protojson.Unmarshal(body, &first); err != nil || !first.GetChanged() || first.GetAbandonment().GetApplicationId() != application.GetId() || first.GetAbandonment().GetVersionId() != version.GetVersionId() || first.GetAbandonment().GetSourceRevision() != 1 {
		t.Fatalf("HTTP abandonment=%v err=%v", &first, err)
	}
	for _, forbidden := range [][]byte{[]byte("launchUrl"), []byte("oauthRedirects"), []byte("abandonedBy"), []byte(adminID)} {
		if bytes.Contains(body, forbidden) {
			t.Fatalf("response leaks %q: %s", forbidden, body)
		}
	}

	client := applicationversionv1.NewApplicationVersionClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinaryAdminToken))
	var headers metadata.MD
	retry, err := client.AbandonApplicationVersionDraft(grpcCtx, &applicationversionv1.AbandonApplicationVersionDraftRequest{ApplicationId: application.GetId(), VersionId: version.GetVersionId(), ExpectedRevision: 1}, grpc.Header(&headers))
	if err != nil || retry.GetChanged() || !proto.Equal(first.GetAbandonment(), retry.GetAbandonment()) || len(headers.Get("cache-control")) != 1 || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatalf("gRPC retry=%v headers=%v err=%v", retry, headers, err)
	}
	_, err = client.AbandonApplicationVersionDraft(grpcCtx, &applicationversionv1.AbandonApplicationVersionDraftRequest{ApplicationId: application.GetId(), VersionId: version.GetVersionId(), ExpectedRevision: 2})
	if status.Code(err) != codes.Aborted || e2eErrorReason(status.Convert(err)) != transport.ReasonApplicationVersionRevisionConflict {
		t.Fatalf("stale retry=%v", err)
	}
	_, err = client.GetApplicationVersion(grpcCtx, &applicationversionv1.GetApplicationVersionRequest{ApplicationId: application.GetId(), VersionId: version.GetVersionId()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("abandoned detail=%v", err)
	}
	managementClient := applicationmanagementv1.NewApplicationManagementQueryServiceClient(connection)
	managed, err := managementClient.GetMyApplication(grpcCtx, &applicationmanagementv1.GetMyApplicationRequest{ApplicationId: application.GetId()})
	if err != nil || managed.GetCounts().GetVersionCount() != 0 || managed.GetCounts().GetDraftVersionCount() != 0 || len(managed.GetOauthRegistrations()) != 0 {
		t.Fatalf("management projection after abandonment=%v err=%v", managed, err)
	}

	recreatedStatus, _, recreatedBody := e2eHTTPCreateVersion(t, httpAddress, developerToken, application.GetId(), "reusable-label", nil, nil, nil)
	var recreated applicationversionv1.CreateApplicationVersionResponse
	if err := protojson.Unmarshal(recreatedBody, &recreated); err != nil || recreatedStatus != http.StatusCreated || recreated.GetVersionId() == version.GetVersionId() || recreated.GetSequence() != 2 {
		t.Fatalf("recreated=%d %v err=%v", recreatedStatus, &recreated, err)
	}
	if count, err := database.Collection("application_version_abandonments").CountDocuments(ctx, bson.M{"versionId": version.GetVersionId()}); err != nil || count != 1 {
		t.Fatalf("tombstones=%d err=%v", count, err)
	}
}
