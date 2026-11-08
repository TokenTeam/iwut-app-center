package main

import (
	"context"
	"errors"
	appv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	ops "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_operations"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_platform_query"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestE2E_UCAPP051_PlatformApplicationQueries(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, identityKeyPath := e2eIdentity(t)
	addresses := e2eReserveAddresses(t, 3)
	_, auth := e2eStartAuthServer(t, addresses[2])

	cfg, err := config.Load(e2eEnvironment(map[string]string{config.ApplicationOperationsEnabledEnv: "true", config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: addresses[0], config.GRPCAddrEnv: addresses[1], config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + identityKeyPath, config.AuthScopeCatalogTargetEnv: addresses[2]}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(done) }()
	t.Cleanup(func() {
		defer cleanup()
		_ = app.Stop()
		select {
		case <-done:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Error(runErr)
			}
		case <-time.After(15 * time.Second):
			t.Error("App did not stop")
		}
	})
	e2eWaitForHTTPListener(t, ctx, addresses[0], done, &runErr)
	connection := e2eDialGRPC(t, ctx, addresses[1], done, &runErr)
	defer connection.Close()

	source := e2eSignIdentity(t, key, "directory-owner", "APPROVED")
	sourceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, source))
	application, err := appv1.NewApplicationClient(connection).CreateApplication(sourceCtx, &appv1.CreateApplicationRequest{Name: "Directory_App"})
	if err != nil {
		t.Fatal(err)
	}
	reader := e2eSignReviewerIdentity(t, key, "directory-reader", "app.application.search")
	readerCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reader))
	service := pb.NewApplicationPlatformQueryServiceClient(connection)
	const path = "/v1/platform/applications:search"
	auth.unavailable.Store(true)
	authReads := auth.calls.Load()
	for _, permissions := range [][]string{nil, {"app.application.suspend"}, {"app.application.restore"}, {"app.version.review"}, {"platform.admin"}} {
		token := e2eSignReviewerIdentity(t, key, "directory-other", permissions...)
		code, headers, _ := e2eHTTPAdminTransfer(t, addresses[0], token, http.MethodPost, path, []byte("bad json"))
		if code != 403 || headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal("permission must precede JSON", permissions, code)
		}
		denied := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, token))
		if _, err := service.GetPlatformApplicationSummary(denied, &pb.GetPlatformApplicationSummaryRequest{ApplicationId: application.Id}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("native permission", err)
		}
	}
	code, headers, raw := e2eHTTPAdminTransfer(t, addresses[0], reader, http.MethodPost, path, []byte(`{"namePrefix":"DIRECTORY_"}`))
	var page pb.PlatformApplicationPage
	if code != 200 || headers.Get("Cache-Control") != "private, no-store" || protojson.Unmarshal(raw, &page) != nil || len(page.Items) != 1 || page.Items[0].AdminAuthId != "directory-owner" {
		t.Fatal("minimum HTTP core", code)
	}
	for _, private := range []string{"nameKey", "versions", "profile", "oauth", "credentials", "counts", "Filter"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private projection", private)
		}
	}
	got, err := service.GetPlatformApplicationSummary(readerCtx, &pb.GetPlatformApplicationSummaryRequest{ApplicationId: application.Id})
	if err != nil || got.Name != application.Name || got.LifecycleStatus != "ACTIVE" {
		t.Fatal("native summary", err)
	}
	unknown := &pb.SearchPlatformApplicationsRequest{Query: &pb.SearchPlatformApplicationsQuery{}}
	unknown.Query.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	if _, err := service.SearchPlatformApplications(readerCtx, unknown); status.Code(err) != codes.InvalidArgument {
		t.Fatal("native unknown", err)
	}
	for _, tc := range []struct {
		method, path, body, token string
		want                      int
	}{
		{"POST", path, "bad", reader, 400}, {"POST", path, `{"lifecycleStatuses":["UNKNOWN"]}`, reader, 400}, {"POST", path, `{"pageToken":"!"}`, reader, 400}, {"POST", path, `{"adminAuthId":"someone-else"}`, reader, 200},
		{"POST", path, "bad", "", 401}, {"POST", path, `{"unknown":1}`, reader, 400},
		{"GET", "/v1/platform/applications/019a0000-0000-7000-8000-000000000001", "", reader, 404},
		{"GET", "/v1/platform/applications/" + application.Id + "?other=1", "", reader, 400},
		{"GET", "/v1/platform/applications/" + application.Id, "{}", reader, 400},
	} {
		code, headers, raw := e2eHTTPAdminTransfer(t, addresses[0], tc.token, tc.method, tc.path, []byte(tc.body))
		if code != tc.want || headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal(tc.method, tc.path, code, tc.want)
		}
		if tc.want == 200 && !strings.Contains(string(raw), `"items":[]`) {
			t.Fatal("explicit empty", string(raw))
		}
	}
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+addresses[0]), khttp.WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	clientHeaders := http.Header{transport.IdentityHeader: []string{reader}}
	typed := pb.NewApplicationPlatformQueryServiceHTTPClient(httpClient)
	typedPage, err := typed.SearchPlatformApplications(ctx, &pb.SearchPlatformApplicationsRequest{Query: &pb.SearchPlatformApplicationsQuery{}}, khttp.Header(&clientHeaders))
	if err != nil || len(typedPage.GetItems()) != 1 {
		t.Fatal("generated HTTP search", err)
	}
	clientHeaders.Set(transport.IdentityHeader, reader)
	detail, err := typed.GetPlatformApplicationSummary(ctx, &pb.GetPlatformApplicationSummaryRequest{ApplicationId: application.Id}, khttp.Header(&clientHeaders))
	if err != nil || detail.GetApplicationId() != application.Id {
		t.Fatal("generated HTTP summary", err)
	}
	if _, err := ops.NewApplicationOperationsServiceClient(connection).GetApplicationPlatformAvailability(readerCtx, &ops.GetApplicationPlatformAvailabilityRequest{ApplicationId: application.Id}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("search became operation grant", err)
	}
	if auth.calls.Load() != authReads {
		t.Fatal("query called Auth")
	}
}
