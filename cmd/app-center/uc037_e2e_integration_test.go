package main

import (
	"bytes"
	"context"
	"errors"
	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	linkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	managementv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_management"
	membershipv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
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
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestE2E_UCAPP037_BR_TST_037_040_043_RealProtocolsAndGeneratedClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	const admin = "tester-management-admin"
	developer := e2eSignIdentity(t, key, admin, "APPROVED")
	ordinary := e2eSignReviewerIdentity(t, key, admin)
	other := e2eSignReviewerIdentity(t, key, "other")
	tester := e2eSignReviewerIdentity(t, key, "tester")
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
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
		if err := app.Stop(); err != nil {
			t.Error(err)
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
	defer connection.Close()
	code, body := e2eHTTPCreate(t, httpAddress, developer, "Tester_Management_E2E")
	if code != 201 {
		t.Fatal("create application")
	}
	var application applicationv1.CreateApplicationResponse
	if protojson.Unmarshal(body, &application) != nil {
		t.Fatal("application response")
	}
	appID := application.Id
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, developer))
	ordinaryCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinary))
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, tester))
	links := linkv1.NewTesterJoinLinkClient(connection)
	members := membershipv1.NewTesterMembershipClient(connection)
	client := managementv1.NewApplicationTesterManagementServiceClient(connection)
	link, err := links.CreateOrRotateTesterJoinLink(adminCtx, &linkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: appID, Command: &linkv1.CreateOrRotateTesterJoinLinkCommand{}})
	if err != nil {
		t.Fatal("create link")
	}
	parsed, _ := url.Parse(link.JoinUrl)
	fragment, _ := url.ParseQuery(parsed.Fragment)
	secret := fragment.Get("secret")
	joined, err := members.JoinApplicationAsTester(userCtx, &membershipv1.JoinApplicationAsTesterRequest{JoinLinkId: link.JoinLink.JoinLinkId, Command: &membershipv1.JoinApplicationAsTesterCommand{Secret: secret}})
	if err != nil {
		t.Fatal("join")
	}
	var headers metadata.MD
	overview, err := client.GetApplicationTesterManagement(ordinaryCtx, &managementv1.GetApplicationTesterManagementRequest{ApplicationId: appID}, grpc.Header(&headers))
	if err != nil || overview.Capacity.ActiveTesterCount != 1 || overview.ActiveJoinLink.JoinLinkId != link.JoinLink.JoinLinkId || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatal("overview")
	}
	page, err := client.ListApplicationTesterMemberships(ordinaryCtx, &managementv1.ListApplicationTesterMembershipsRequest{ApplicationId: appID})
	if err != nil || len(page.Items) != 1 || page.Items[0].MembershipId != joined.Membership.MembershipId {
		t.Fatal("default active page")
	}
	// Generated HTTP callers exercise exactly the same paths and nested query body.
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	generated := managementv1.NewApplicationTesterManagementServiceHTTPClient(httpClient)
	h := http.Header{transport.IdentityHeader: []string{ordinary}}
	generatedOverview, err := generated.GetApplicationTesterManagement(ctx, &managementv1.GetApplicationTesterManagementRequest{ApplicationId: appID}, khttp.Header(&h))
	if err != nil || generatedOverview.Capacity.ActiveTesterCount != 1 || h.Get("Cache-Control") != "private, no-store" {
		t.Fatal("generated overview", err)
	}
	h = http.Header{transport.IdentityHeader: []string{ordinary}}
	generatedPage, err := generated.ListApplicationTesterMemberships(ctx, &managementv1.ListApplicationTesterMembershipsRequest{ApplicationId: appID, Query: &managementv1.MembershipQuery{PageSize: 1}}, khttp.Header(&h))
	if err != nil || len(generatedPage.Items) != 1 {
		t.Fatal("generated membership", err)
	}
	h = http.Header{transport.IdentityHeader: []string{ordinary}}
	generatedLinks, err := generated.ListApplicationTesterJoinLinks(ctx, &managementv1.ListApplicationTesterJoinLinksRequest{ApplicationId: appID, Query: &managementv1.JoinLinkQuery{PageSize: 100}}, khttp.Header(&h))
	if err != nil || len(generatedLinks.Items) != 1 {
		t.Fatal("generated links", err)
	}
	rawCall := func(method, path, token, body string) (int, []byte) {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, method, "http://"+httpAddress+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("response cache")
		}
		for _, private := range []string{secret, "tokenHash", "token_hash", "joinUrl", "join_url", "createdBy", "created_by", "revokedBy", "revoked_by", "removedBy", "removed_by"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("credential or actor disclosure")
			}
		}
		return response.StatusCode, raw
	}
	base := "/v1/applications/" + appID
	for _, tc := range []struct {
		method, suffix, token, body string
		code                        int
	}{{"GET", "/tester-management", "", "", 401}, {"GET", "/tester-management", other, "", 404}, {"GET", "/tester-management?application_id=other", ordinary, "", 400}, {"GET", "/tester-management", ordinary, "{}", 400}, {"POST", "/tester-memberships:search?status=ACTIVE", ordinary, "{}", 400}, {"POST", "/tester-memberships:search", ordinary, `{"application_id":"other"}`, 400}, {"POST", "/tester-memberships:search", ordinary, `{"status":99}`, 400}, {"POST", "/tester-memberships:search", ordinary, `{"pageSize":101}`, 400}, {"POST", "/tester-join-links:search", ordinary, `{"query":{}}`, 400}, {"POST", "/tester-join-links:search", ordinary, `{"pageToken":"bad"}`, 400}, {"GET", "/tester-management", ordinary, "", 200}} {
		code, _ := rawCall(tc.method, base+tc.suffix, tc.token, tc.body)
		if code != tc.code {
			t.Fatalf("HTTP %s status=%d expected=%d", tc.suffix, code, tc.code)
		}
	}
	for _, tc := range []struct {
		ctx     context.Context
		request *managementv1.GetApplicationTesterManagementRequest
		code    codes.Code
	}{{ctx, &managementv1.GetApplicationTesterManagementRequest{ApplicationId: appID}, codes.Unauthenticated}, {metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, other)), &managementv1.GetApplicationTesterManagementRequest{ApplicationId: appID}, codes.NotFound}, {ordinaryCtx, &managementv1.GetApplicationTesterManagementRequest{ApplicationId: "bad"}, codes.InvalidArgument}} {
		headers = nil
		_, err := client.GetApplicationTesterManagement(tc.ctx, tc.request, grpc.Header(&headers))
		if status.Code(err) != tc.code || len(headers.Get("cache-control")) != 1 || headers.Get("cache-control")[0] != "private, no-store" {
			t.Fatal("grpc error/cache", err)
		}
	}
	// A successful query does not authorize a USER to invoke Developer commands.
	if _, err = members.RemoveApplicationTester(ordinaryCtx, &membershipv1.RemoveApplicationTesterRequest{ApplicationId: appID, MembershipId: joined.Membership.MembershipId}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("query unlocked write permission")
	}
	var before bson.M
	if err = db.Collection("applications").FindOne(ctx, bson.M{"id": appID}).Decode(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = client.GetApplicationTesterManagement(ordinaryCtx, &managementv1.GetApplicationTesterManagementRequest{ApplicationId: appID}); err != nil {
			t.Fatal(err)
		}
	}
	var after bson.M
	if err = db.Collection("applications").FindOne(ctx, bson.M{"id": appID}).Decode(&after); err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("query mutated application")
	}
	if code, _ := rawCall("POST", base+"/tester-memberships:search", ordinary, `{"status":1,"status":2}`); code != 400 {
		t.Fatal("duplicate HTTP JSON field accepted")
	}
	// The BSON date type can express values outside protobuf Timestamp's range.
	// Both protocols must fail in the repository, before trying to marshal it.
	filter := bson.M{"membershipId": joined.Membership.MembershipId}
	if _, err = db.Collection("application_tester_memberships").UpdateOne(ctx, filter, bson.M{"$set": bson.M{"joinedAt": bson.DateTime(253402300800000)}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	if code, _ := rawCall("GET", base+"/tester-management", ordinary, ""); code != 500 {
		t.Fatal("out of range BSON not INTERNAL")
	}
	headers = nil
	_, err = client.ListApplicationTesterMemberships(ordinaryCtx, &managementv1.ListApplicationTesterMembershipsRequest{ApplicationId: appID}, grpc.Header(&headers))
	if status.Code(err) != codes.Internal || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatal("gRPC corruption status/cache")
	}
	if _, err = db.Collection("application_tester_memberships").UpdateOne(ctx, filter, bson.M{"$set": bson.M{"joinedAt": joined.Membership.JoinedAt.AsTime()}}); err != nil {
		t.Fatal(err)
	}
	// Disconnect only this service's client; the isolated fixture client remains
	// available for cleanup. Driver failure is UNAVAILABLE rather than corruption.
	cleanup()
	if code, _ := rawCall("GET", base+"/tester-management", ordinary, ""); code != 503 {
		t.Fatal("driver failure not unavailable")
	}
	headers = nil
	_, err = client.GetApplicationTesterManagement(ordinaryCtx, &managementv1.GetApplicationTesterManagementRequest{ApplicationId: appID}, grpc.Header(&headers))
	if status.Code(err) != codes.Unavailable || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatal("gRPC unavailable status/cache")
	}
}
