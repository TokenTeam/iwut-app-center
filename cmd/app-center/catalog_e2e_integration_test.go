package main

import (
	"bytes"
	"context"
	"errors"
	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	applicationcatalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_catalog"
	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_publication"
	applicationreviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	applicationversionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	catalogv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/catalog"
	runtimev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/runtime_resolution"
	testerjoinlinkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	testermembershipv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

type catalogE2EDNSResolver struct {
	base  e2eDNSResolver
	calls atomic.Int32
}

func (r *catalogE2EDNSResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	r.calls.Add(1)
	return r.base.LookupNetIP(ctx, network, host)
}
func TestE2E_UCAPP012_BR_RUN_001_010_TestLaunch(t *testing.T) {
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
	scopeServer, developerStatusServer := e2eStartAuthServer(t, authAddress)
	developerStatusServer.systemAuthID = systemID
	developerStatusServer.SetStatus(adminID, developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED)
	resolver := &catalogE2EDNSResolver{base: e2eDNSResolver{addresses: map[string][]netip.Addr{
		"example.edu": {netip.MustParseAddr("8.8.8.8")},
	}}}
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
	versionID := approve("v12.0.0")
	e2eApproveApplicationProfile(t, ctx, connection, application.GetId(), adminToken, reviewerToken)
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	publication, err := publicationv1.NewApplicationPublicationClient(connection).PlaceApprovedVersionInTestSlot(adminCtx, &publicationv1.PlaceApprovedVersionInTestSlotRequest{ApplicationId: application.GetId(), RpcApiMajor: 3, Command: &publicationv1.PlaceApprovedVersionInTestSlotCommand{VersionId: versionID}})
	if err != nil {
		t.Fatal(err)
	}
	expectedPublicationRevision := int64(1)
	stablePublication, err := publicationv1.NewApplicationPublicationClient(connection).SetApprovedVersionInStableSlot(adminCtx, &publicationv1.SetApprovedVersionInStableSlotRequest{ApplicationId: application.GetId(), RpcApiMajor: 3, Command: &publicationv1.SetApprovedVersionInStableSlotCommand{VersionId: versionID, ExpectedPublicationRevision: &expectedPublicationRevision}})
	if err != nil || stablePublication.GetPublication().GetRevision() != 2 {
		t.Fatalf("set stable publication=%v error=%v", stablePublication, err)
	}
	link, err := testerjoinlinkv1.NewTesterJoinLinkClient(connection).CreateOrRotateTesterJoinLink(adminCtx, &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: application.GetId(), Command: &testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(link.GetJoinUrl())
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	userToken := e2eSignIdentityClaims(t, privateKey, map[string]any{"iss": e2eIssuer, "sub": "catalog-ordinary-user", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "catalog-user"})
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, userToken))
	memberships := testermembershipv1.NewTesterMembershipClient(connection)
	membership, err := memberships.JoinApplicationAsTester(userCtx, &testermembershipv1.JoinApplicationAsTesterRequest{JoinLinkId: link.GetJoinLink().GetJoinLinkId(), Command: &testermembershipv1.JoinApplicationAsTesterCommand{Secret: fragment.Get("secret")}})
	if err != nil {
		t.Fatal(err)
	}
	// External preflight services are unavailable after publishing. Resolve must
	// remain independent of both them and all domain writes.
	scopeCalls, developerCalls, dnsCalls := scopeServer.calls.Load(), developerStatusServer.calls.Load(), resolver.calls.Load()
	scopeServer.unavailable.Store(true)
	developerStatusServer.unavailable.Store(true)
	before := map[string][]bson.M{}
	collections := []string{"applications", "application_versions", "application_reviews", "application_publications", "application_publication_history", "application_tester_memberships", "application_tester_join_links"}
	readAll := func(collection string) []bson.M {
		t.Helper()
		cursor, err := database.Collection(collection).Find(ctx, bson.M{}, options.Find().SetSort(bson.M{"_id": 1}))
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close(ctx)
		var docs []bson.M
		if err := cursor.All(ctx, &docs); err != nil {
			t.Fatal(err)
		}
		return docs
	}
	for _, collection := range collections {
		before[collection] = readAll(collection)
	}
	resolveHTTP := func(token, appID, query, payload string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+appID+"/test-launch:resolve"+query, bytes.NewBufferString(payload))
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
		if response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cache policy %q", response.Header.Get("Cache-Control"))
		}
		for _, secret := range []string{fragment.Get("secret"), "private-forged", "tokenHash", "testerAuthId", "decidedBy"} {
			if bytes.Contains(body, []byte(secret)) {
				t.Fatal("private data exposed")
			}
		}
		return response.StatusCode, body
	}
	const validBody = `{"hostRpcApiMajor":3,"hostCapabilities":["user.profile.v1","camera.read.v1","user.profile.v1"]}`
	code, body := resolveHTTP(userToken, application.GetId(), "", validBody)
	var httpDescriptor catalogv1.TestLaunchDescriptor
	if code != 200 || protojson.Unmarshal(body, &httpDescriptor) != nil {
		t.Fatalf("resolve status%d body%s", code, body)
	}
	if httpDescriptor.GetVersionId() != versionID || httpDescriptor.GetApplicationId() != application.GetId() || httpDescriptor.GetPublicationId() != publication.GetPublication().GetPublicationId() || httpDescriptor.GetPublicationRevision() != 2 || httpDescriptor.GetRpcApiMajor() != 3 || httpDescriptor.GetRpcApiMinVersion() != 3 || httpDescriptor.GetRpcApiMaxVersionExclusive() != 5 || httpDescriptor.GetVersionLabel() != "v12.0.0" || httpDescriptor.GetLaunchUrl() == "" || !reflect.DeepEqual(httpDescriptor.GetRequiredCapabilities(), []string{"user.profile.v1"}) || !reflect.DeepEqual(httpDescriptor.GetRequiredScopes(), []string{"profile.basic"}) || len(httpDescriptor.GetOptionalScopes()) != 0 {
		t.Fatalf("descriptor %v", &httpDescriptor)
	}
	client := catalogv1.NewCatalogClient(connection)
	request := &catalogv1.ResolveTestLaunchTargetRequest{ApplicationId: application.GetId(), Query: &catalogv1.ResolveTestLaunchTargetQuery{HostRpcApiMajor: 3, HostCapabilities: []string{"user.profile.v1"}}}
	var headers metadata.MD
	descriptor, err := client.ResolveTestLaunchTarget(userCtx, request, grpc.Header(&headers))
	if err != nil || !proto.Equal(descriptor, &httpDescriptor) {
		t.Fatalf("gRPC descriptor %v err%v", descriptor, err)
	}
	if values := headers.Get("cache-control"); len(values) != 1 || values[0] != "private, no-store" {
		t.Fatal("gRPC cache policy missing")
	}
	resolveRuntimeHTTP := func(token, payload string) (int, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+application.GetId()+"/launch-target:resolve", bytes.NewBufferString(payload))
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
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("runtime cache policy %q", response.Header.Get("Cache-Control"))
		}
		return response.StatusCode, body
	}
	for _, runtimeCase := range []struct {
		name, token string
		channel     runtimev1.LaunchChannel
	}{
		{"anonymous stable", "", runtimev1.LaunchChannel_LAUNCH_CHANNEL_STABLE},
		{"tester test", userToken, runtimev1.LaunchChannel_LAUNCH_CHANNEL_TEST},
	} {
		t.Run("UCAPP023 "+runtimeCase.name, func(t *testing.T) {
			code, body := resolveRuntimeHTTP(runtimeCase.token, validBody)
			var descriptor runtimev1.LaunchTargetDescriptor
			if code != http.StatusOK || protojson.Unmarshal(body, &descriptor) != nil || descriptor.GetChannel() != runtimeCase.channel || descriptor.GetVersionId() != versionID || descriptor.GetPublicationRevision() != 2 {
				t.Fatalf("runtime status=%d descriptor=%v body=%s", code, &descriptor, body)
			}
		})
	}
	runtimeClient := runtimev1.NewRuntimeResolutionServiceClient(connection)
	runtimeRequest := &runtimev1.ResolveLaunchTargetRequest{ApplicationId: application.GetId(), Query: &runtimev1.ResolveLaunchTargetQuery{HostRpcApiMajor: 3, HostCapabilities: []string{"user.profile.v1"}}}
	var runtimeHeaders metadata.MD
	anonymousRuntime, err := runtimeClient.ResolveLaunchTarget(ctx, runtimeRequest, grpc.Header(&runtimeHeaders))
	if err != nil || anonymousRuntime.GetChannel() != runtimev1.LaunchChannel_LAUNCH_CHANNEL_STABLE {
		t.Fatalf("anonymous runtime=%v error=%v", anonymousRuntime, err)
	}
	if values := runtimeHeaders.Get("cache-control"); len(values) != 1 || values[0] != "private, no-store" {
		t.Fatal("runtime gRPC cache policy missing")
	}
	testerRuntime, err := runtimeClient.ResolveLaunchTarget(userCtx, runtimeRequest)
	if err != nil || testerRuntime.GetChannel() != runtimev1.LaunchChannel_LAUNCH_CHANNEL_TEST {
		t.Fatalf("tester runtime=%v error=%v", testerRuntime, err)
	}
	queryCatalogHTTP := func(token, path, payload string) (int, []byte) {
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
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("catalog cache policy %q", response.Header.Get("Cache-Control"))
		}
		return response.StatusCode, body
	}
	code, body = queryCatalogHTTP("", "/v1/catalog/applications:search", `{"runtime":{"hostRpcApiMajor":3,"hostCapabilities":["user.profile.v1"]}}`)
	var catalogPage applicationcatalogv1.PublicApplicationCatalogPage
	if code != http.StatusOK || protojson.Unmarshal(body, &catalogPage) != nil || len(catalogPage.GetApplications()) != 1 || catalogPage.GetApplications()[0].GetApplicationId() != application.GetId() || catalogPage.GetApplications()[0].GetLaunchTarget().GetChannel() != runtimev1.LaunchChannel_LAUNCH_CHANNEL_STABLE || catalogPage.GetApplications()[0].GetProfile().GetDisplayName() == "" || catalogPage.GetApplications()[0].GetFilter().GetMode().String() != "FILTER_MODE_ALLOW_ALL" {
		t.Fatalf("public catalog status=%d page=%v body=%s", code, &catalogPage, body)
	}
	catalogClient := applicationcatalogv1.NewApplicationCatalogServiceClient(connection)
	var catalogHeaders metadata.MD
	catalogDetail, err := catalogClient.GetPublicApplication(userCtx, &applicationcatalogv1.GetPublicApplicationRequest{ApplicationId: application.GetId(), Query: &applicationcatalogv1.CatalogRuntimeQuery{HostRpcApiMajor: 3, HostCapabilities: []string{"user.profile.v1"}}}, grpc.Header(&catalogHeaders))
	if err != nil || catalogDetail.GetLaunchTarget().GetChannel() != runtimev1.LaunchChannel_LAUNCH_CHANNEL_TEST {
		t.Fatalf("tester catalog detail=%v error=%v", catalogDetail, err)
	}
	if values := catalogHeaders.Get("cache-control"); len(values) != 1 || values[0] != "private, no-store" {
		t.Fatal("catalog gRPC cache policy missing")
	}
	if code, body := queryCatalogHTTP("private-forged", "/v1/catalog/applications:search", `{"runtime":{"hostRpcApiMajor":3}}`); code != http.StatusUnauthorized || !bytes.Contains(body, []byte(transport.ReasonInvalidAuthenticatedUser)) || bytes.Contains(body, []byte("private-forged")) {
		t.Fatalf("invalid catalog identity status=%d body=%s", code, body)
	}
	if code, body := resolveRuntimeHTTP("private-forged", validBody); code != http.StatusUnauthorized || !bytes.Contains(body, []byte(transport.ReasonInvalidAuthenticatedUser)) || bytes.Contains(body, []byte("private-forged")) {
		t.Fatalf("invalid runtime identity status=%d body=%s", code, body)
	}
	if code, body := resolveRuntimeHTTP("", `{"hostRpcApiMajor":3,"channel":"TEST"}`); code != http.StatusBadRequest || !bytes.Contains(body, []byte(transport.ReasonInvalidResolveLaunchTargetRequest)) {
		t.Fatalf("runtime injection status=%d body=%s", code, body)
	}
	for _, tc := range []struct {
		token, app, query, body, reason string
		code                            int
	}{
		{"", application.GetId(), "", validBody, transport.ReasonAuthenticatedUserRequired, 401},
		{"private-forged", application.GetId(), "", validBody, transport.ReasonInvalidAuthenticatedUser, 401},
		{adminToken, application.GetId(), "", validBody, transport.ReasonApplicationTesterRequired, 403},
		{reviewerToken, application.GetId(), "", `{"hostRpcApiMajor":99}`, transport.ReasonApplicationTesterRequired, 403},
		{userToken, "018f0000-0000-7000-8000-000000009999", "", validBody, transport.ReasonApplicationNotFound, 404},
		{userToken, "bad-id", "", validBody, transport.ReasonInvalidApplicationID, 400},
		{userToken, application.GetId(), "", `{"hostRpcApiMajor":0}`, transport.ReasonInvalidHostRPCAPIMajor, 400},
		{userToken, application.GetId(), "", `{"hostRpcApiMajor":3,"hostCapabilities":["private-forged"]}`, transport.ReasonInvalidHostCapabilities, 400},
		{userToken, application.GetId(), "", `{"hostRpcApiMajor":4}`, transport.ReasonApplicationTestTargetUnavailable, 404},
		{userToken, application.GetId(), "", `{"hostRpcApiMajor":3}`, transport.ReasonHostCapabilitiesInsufficient, 422},
	} {
		code, body := resolveHTTP(tc.token, tc.app, tc.query, tc.body)
		if code != tc.code || !bytes.Contains(body, []byte(tc.reason)) {
			t.Fatalf("status%d expected%d reason%s body%s", code, tc.code, tc.reason, body)
		}
	}
	for _, extra := range []string{"authId", "testerAuthId", "membershipId", "publicationId", "versionId", "launchUrl"} {
		code, _ := resolveHTTP(userToken, application.GetId(), "", `{"hostRpcApiMajor":3,"`+extra+`":"private-forged"}`)
		if code != 400 {
			t.Fatalf("body injection %s accepted", extra)
		}
	}
	code, _ = resolveHTTP(userToken, application.GetId(), "?hostRpcApiMajor=4", validBody)
	if code != 400 {
		t.Fatal("query override accepted")
	}
	missingRequest := proto.Clone(request).(*catalogv1.ResolveTestLaunchTargetRequest)
	missingRequest.Query.HostCapabilities = nil
	_, err = client.ResolveTestLaunchTarget(userCtx, missingRequest)
	if status.Code(err) != codes.FailedPrecondition || e2eErrorReason(status.Convert(err)) != transport.ReasonHostCapabilitiesInsufficient {
		t.Fatalf("gRPC capabilities %v", err)
	}
	_, err = client.ResolveTestLaunchTarget(adminCtx, request)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("admin implicitly authorized")
	}
	_, err = client.ResolveTestLaunchTarget(ctx, request)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatal("missing identity accepted")
	}
	unknown := proto.Clone(request).(*catalogv1.ResolveTestLaunchTargetRequest)
	unknown.ProtoReflect().SetUnknown([]byte{0x18, 0x01})
	_, err = client.ResolveTestLaunchTarget(userCtx, unknown)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal("unknown gRPC field accepted")
	}
	for _, collection := range collections {
		if !reflect.DeepEqual(before[collection], readAll(collection)) {
			t.Fatalf("resolve mutated %s", collection)
		}
	}
	if scopeServer.calls.Load() != scopeCalls || developerStatusServer.calls.Load() != developerCalls || resolver.calls.Load() != dnsCalls {
		t.Fatal("resolve invoked external dependency")
	}
	// Approved-looking content drift must not return a partial descriptor.
	if _, err := database.Collection("application_versions").UpdateOne(ctx, bson.M{"versionId": versionID}, bson.M{"$set": bson.M{"launchUrl": "https://private-forged.example/"}}); err != nil {
		t.Fatal(err)
	}
	code, body = resolveHTTP(userToken, application.GetId(), "", validBody)
	if code != 503 || !bytes.Contains(body, []byte(transport.ReasonApplicationTestPublicationInconsistent)) {
		t.Fatalf("inconsistency status%d body%s", code, body)
	}
	_, err = client.ResolveTestLaunchTarget(userCtx, request)
	if status.Code(err) != codes.Unavailable || e2eErrorReason(status.Convert(err)) != transport.ReasonApplicationTestPublicationInconsistent {
		t.Fatalf("inconsistency gRPC %v", err)
	}
	if code, body := resolveRuntimeHTTP("", validBody); code != http.StatusInternalServerError || !bytes.Contains(body, []byte(transport.ReasonApplicationRuntimeStateInconsistent)) {
		t.Fatalf("runtime inconsistency status=%d body=%s", code, body)
	}
	// Removal remains authorization-first even when publication is corrupt.
	_, err = memberships.RemoveApplicationTester(adminCtx, &testermembershipv1.RemoveApplicationTesterRequest{ApplicationId: application.GetId(), MembershipId: membership.GetMembership().GetMembershipId()})
	if err != nil {
		t.Fatal(err)
	}
	code, body = resolveHTTP(userToken, application.GetId(), "", validBody)
	if code != 403 || !bytes.Contains(body, []byte(transport.ReasonApplicationTesterRequired)) {
		t.Fatal("removed tester enumerated publication")
	}
}
