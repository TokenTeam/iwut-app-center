package main

import (
	"bytes"
	"context"
	"errors"
	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/test_application_catalog"
	linkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	memberv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestE2E_UCAPP038_BR_CAT_011_017_RealProtocolsGeneratedClientAndNoExternalCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	const adminID = "my-test-admin"
	developer := e2eSignIdentity(t, key, adminID, "APPROVED")
	reviewer := e2eSignReviewerIdentity(t, key, "my-test-reviewer", "app.version.review", "app.profile.review")
	ordinary := e2eSignReviewerIdentity(t, key, "my-test-user")
	addresses := e2eReserveAddresses(t, 3)
	httpAddress, grpcAddress, authAddress := addresses[0], addresses[1], addresses[2]
	scopes, developers := e2eStartAuthServer(t, authAddress)
	developers.systemAuthID = "system"
	developers.SetStatus(adminID, developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED)
	resolver := &catalogE2EDNSResolver{base: e2eDNSResolver{addresses: map[string][]netip.Addr{"example.edu": {netip.MustParseAddr("8.8.8.8")}}}}
	configuration, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: authAddress, config.ScopeCatalogCacheTTLEnv: "1ns"}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(done) }()
	t.Cleanup(func() {
		_ = app.Stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("app stop timeout")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, done, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, done, &runErr)
	defer connection.Close()
	code, body := e2eHTTPCreate(t, httpAddress, developer, "My_Test_E2E")
	if code != 201 {
		t.Fatal("application", code)
	}
	var application applicationv1.CreateApplicationResponse
	if err = protojson.Unmarshal(body, &application); err != nil {
		t.Fatal(err)
	}
	appID := application.Id
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, developer))
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinary))
	links := linkv1.NewTesterJoinLinkClient(connection)
	members := memberv1.NewTesterMembershipClient(connection)
	catalog := catalogv1.NewTestApplicationCatalogServiceClient(connection)
	link, err := links.CreateOrRotateTesterJoinLink(adminCtx, &linkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: appID, Command: &linkv1.CreateOrRotateTesterJoinLinkCommand{}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(link.JoinUrl)
	fragment, _ := url.ParseQuery(parsed.Fragment)
	joined, err := members.JoinApplicationAsTester(userCtx, &memberv1.JoinApplicationAsTesterRequest{JoinLinkId: link.JoinLink.JoinLinkId, Command: &memberv1.JoinApplicationAsTesterCommand{Secret: fragment.Get("secret")}})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &catalogv1.RuntimeQuery{HostRpcApiMajor: 3, HostCapabilities: []string{"user.profile.v1", "user.profile.v1"}}
	get := &catalogv1.GetMyTestApplicationRequest{ApplicationId: appID, Runtime: runtime}
	var headers metadata.MD
	detail, err := catalog.GetMyTestApplication(userCtx, get, grpc.Header(&headers))
	if err != nil || detail.Item.Availability != catalogv1.TestAvailability_TEST_AVAILABILITY_PROFILE_REQUIRED || detail.Item.Profile != nil || detail.Item.LaunchTarget != nil || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatal("profile required", detail, err)
	}
	e2eApproveApplicationProfile(t, ctx, connection, appID, developer, reviewer)
	detail, err = catalog.GetMyTestApplication(userCtx, get)
	if err != nil || detail.Item.Availability != catalogv1.TestAvailability_TEST_AVAILABILITY_TEST_TARGET_UNAVAILABLE || detail.Item.Profile == nil {
		t.Fatal("target absent", detail, err)
	}
	code, _, body = e2eHTTPCreateVersion(t, httpAddress, developer, appID, "v1", []string{"user.profile.v1"}, []string{"profile.basic"}, nil)
	if code != 201 {
		t.Fatal("version", code, string(body))
	}
	var version versionv1.CreateApplicationVersionResponse
	_ = protojson.Unmarshal(body, &version)
	code, _, body = e2eHTTPSubmitReview(t, httpAddress, developer, appID, version.VersionId, 1)
	if code != 201 {
		t.Fatal("submit", code)
	}
	var review reviewv1.SubmitApplicationVersionReviewResponse
	_ = protojson.Unmarshal(body, &review)
	code, body = e2eHTTPDecideReview(t, httpAddress, reviewer, appID, version.VersionId, review.Review.ReviewId, "APPROVE", "app-version-review-v2", []string{"requested-access-reviewed", "content-policy-reviewed", "launch-url-content-reviewed", "oauth-redirects-reviewed"}, "")
	if code != 200 {
		t.Fatal("approve", code, string(body))
	}
	publications := publicationv1.NewApplicationPublicationClient(connection)
	published, err := publications.PlaceApprovedVersionInTestSlot(adminCtx, &publicationv1.PlaceApprovedVersionInTestSlotRequest{ApplicationId: appID, RpcApiMajor: 3, Command: &publicationv1.PlaceApprovedVersionInTestSlotCommand{VersionId: version.VersionId}})
	if err != nil {
		t.Fatal(err)
	}
	// All external publishing dependencies now reject. Queries still succeed
	// and neither add calls nor mutate the complete business fact collections.
	scopeCalls, developerCalls, dnsCalls := scopes.calls.Load(), developers.calls.Load(), resolver.calls.Load()
	scopes.unavailable.Store(true)
	developers.unavailable.Store(true)
	collections := []string{"applications", "application_profiles", "application_profile_revisions", "application_profile_reviews", "application_versions", "application_version_oauth_configs", "application_reviews", "application_publications", "application_publication_history", "application_tester_memberships", "application_tester_join_links", "application_filters", "application_filter_revisions"}
	readAll := func() map[string][]bson.M {
		t.Helper()
		all := map[string][]bson.M{}
		for _, collection := range collections {
			cursor, err := db.Collection(collection).Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
			if err != nil {
				t.Fatal(err)
			}
			var docs []bson.M
			if err = cursor.All(ctx, &docs); err != nil {
				t.Fatal(err)
			}
			all[collection] = docs
		}
		return all
	}
	before := readAll()
	detail, err = catalog.GetMyTestApplication(userCtx, get, grpc.Header(&headers))
	if err != nil || detail.Item.Availability != catalogv1.TestAvailability_TEST_AVAILABILITY_READY || detail.Item.LaunchTarget.VersionId != version.VersionId || detail.Item.Membership.MembershipId != joined.Membership.MembershipId || detail.Item.LaunchTarget.PublicationRevision != published.Publication.Revision {
		t.Fatal("ready", detail, err)
	}
	page, err := catalog.ListMyTestApplications(userCtx, &catalogv1.ListMyTestApplicationsRequest{Query: &catalogv1.ListQuery{Runtime: runtime, PageSize: 1}}, grpc.Header(&headers))
	if err != nil || len(page.Items) != 1 || page.AsOf == nil || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatal("list", page, err)
	}
	insufficient, err := catalog.GetMyTestApplication(userCtx, &catalogv1.GetMyTestApplicationRequest{ApplicationId: appID, Runtime: &catalogv1.RuntimeQuery{HostRpcApiMajor: 3}})
	if err != nil || insufficient.Item.Availability != catalogv1.TestAvailability_TEST_AVAILABILITY_HOST_CAPABILITIES_INSUFFICIENT || insufficient.Item.LaunchTarget != nil || !reflect.DeepEqual(insufficient.Item.MissingCapabilities, []string{"user.profile.v1"}) {
		t.Fatal("capability", insufficient, err)
	}
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	generated := catalogv1.NewTestApplicationCatalogServiceHTTPClient(httpClient)
	h := http.Header{transport.IdentityHeader: []string{ordinary}}
	generatedPage, err := generated.ListMyTestApplications(ctx, &catalogv1.ListMyTestApplicationsRequest{Query: &catalogv1.ListQuery{Runtime: runtime}}, khttp.Header(&h))
	if err != nil || len(generatedPage.Items) != 1 || h.Get("Cache-Control") != "private, no-store" {
		t.Fatal("generated list", err)
	}
	h = http.Header{transport.IdentityHeader: []string{ordinary}}
	generatedDetail, err := generated.GetMyTestApplication(ctx, get, khttp.Header(&h))
	if err != nil || generatedDetail.Item.LaunchTarget.VersionId != version.VersionId || h.Get("Cache-Control") != "private, no-store" {
		t.Fatal("generated detail", err)
	}
	rawCall := func(path, token, payload string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+path, bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set(transport.IdentityHeader, token)
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("response cache/privacy", err)
		}
		return response.StatusCode, body
	}
	for _, tc := range []struct {
		path, token, body string
		code              int
	}{{"/v1/test-applications:search", "", "invalid", 401}, {"/v1/test-applications:search", "bad", `{}`, 401}, {"/v1/test-applications:search?hostRpcApiMajor=4", ordinary, `{"runtime":{"hostRpcApiMajor":3}}`, 400}, {"/v1/test-applications:search", ordinary, `{"runtime":{"hostRpcApiMajor":3},"authId":"other"}`, 400}, {"/v1/test-applications/" + appID + ":resolve", developer, `{"hostRpcApiMajor":3}`, 404}} {
		code, body := rawCall(tc.path, tc.token, tc.body)
		if code != tc.code {
			t.Fatal("HTTP boundary", code, string(body))
		}
	}
	for _, tc := range []struct {
		ctx     context.Context
		request *catalogv1.GetMyTestApplicationRequest
		code    codes.Code
	}{{ctx, get, codes.Unauthenticated}, {adminCtx, get, codes.NotFound}, {userCtx, &catalogv1.GetMyTestApplicationRequest{ApplicationId: appID, Runtime: &catalogv1.RuntimeQuery{}}, codes.InvalidArgument}} {
		_, err := catalog.GetMyTestApplication(tc.ctx, tc.request, grpc.Header(&headers))
		if status.Code(err) != tc.code || headers.Get("cache-control")[0] != "private, no-store" {
			t.Fatal("gRPC boundary", err)
		}
	}
	if !reflect.DeepEqual(before, readAll()) || scopes.calls.Load() != scopeCalls || developers.calls.Load() != developerCalls || resolver.calls.Load() != dnsCalls {
		t.Fatal("query wrote or called external dependency")
	}
	// Clear is a real command; the unavailable episode survives unchanged.
	if _, err = publications.ClearTestSlot(adminCtx, &publicationv1.ClearTestSlotRequest{ApplicationId: appID, RpcApiMajor: 3, ExpectedPublicationRevision: 1}); err != nil {
		t.Fatal(err)
	}
	h = http.Header{transport.IdentityHeader: []string{ordinary}}
	cleared, err := generated.GetMyTestApplication(ctx, get, khttp.Header(&h))
	if err != nil || cleared.Item.Availability != catalogv1.TestAvailability_TEST_AVAILABILITY_TEST_TARGET_UNAVAILABLE || cleared.Item.Membership.MembershipId != joined.Membership.MembershipId {
		t.Fatal("clear card", err)
	}
	// Stored corruption cannot become a normal unavailable card.
	if _, err = db.Collection("application_profiles").UpdateOne(ctx, bson.M{"applicationId": appID}, bson.M{"$set": bson.M{"currentPublishedProfileRevisionId": "01890f47-0000-7000-8000-000000009999"}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	code, body = rawCall("/v1/test-applications:search", ordinary, `{"runtime":{"hostRpcApiMajor":3}}`)
	if code != 500 || !strings.Contains(string(body), "ERROR_REASON_MY_TEST_APPLICATION_STATE_INCONSISTENT") {
		t.Fatal("HTTP corrupt", code, string(body))
	}
	_, err = catalog.GetMyTestApplication(userCtx, get)
	if status.Code(err) != codes.Internal {
		t.Fatal("gRPC corrupt", err)
	}
}
