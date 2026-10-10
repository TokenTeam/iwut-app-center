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
	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"

	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP034_QueryApplicationVersionsOverRealHTTPAndGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "auth-e2e-version-management"
	developerToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	ordinaryUserToken := e2eSignReviewerIdentity(t, privateKey, adminID)
	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	e2eStartScopeCatalogServer(t, authAddress)
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

	statusCode, applicationBody := e2eHTTPCreate(t, httpAddress, developerToken, "Version_Management_E2E")
	if statusCode != http.StatusCreated {
		t.Fatalf("create application=%d %s", statusCode, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err = protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatal(err)
	}
	versionStatus, _, versionBody := e2eHTTPCreateVersion(t, httpAddress, developerToken, application.GetId(), "v1.0.0", []string{"camera.read.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
	if versionStatus != http.StatusCreated {
		t.Fatalf("create version=%d %s", versionStatus, versionBody)
	}
	var version applicationversionv1.CreateApplicationVersionResponse
	if err = protojson.Unmarshal(versionBody, &version); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+application.GetId()+"/versions:search", bytes.NewBufferString(`{"pageSize":20}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, ordinaryUserToken)
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
		t.Fatalf("HTTP list=%d cache=%q body=%s", response.StatusCode, response.Header.Get("Cache-Control"), body)
	}
	var page applicationversionv1.OwnedApplicationVersionPage
	if err = protojson.Unmarshal(body, &page); err != nil || len(page.GetItems()) != 1 || page.GetItems()[0].GetVersionId() != version.GetVersionId() || page.GetItems()[0].GetLatestReview() != nil {
		t.Fatalf("HTTP page=%v err=%v", &page, err)
	}

	client := applicationversionv1.NewApplicationVersionClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinaryUserToken))
	var headers metadata.MD
	detail, err := client.GetApplicationVersion(grpcCtx, &applicationversionv1.GetApplicationVersionRequest{ApplicationId: application.GetId(), VersionId: version.GetVersionId()}, grpc.Header(&headers))
	if err != nil {
		t.Fatal(err)
	}
	if detail.GetVersion().GetLaunchUrl() != version.GetLaunchUrl() || detail.GetVersion().GetRevision() != 1 || detail.GetLatestReview() != nil || len(headers.Get("cache-control")) != 1 || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatalf("gRPC detail=%v headers=%v", detail, headers)
	}
}
