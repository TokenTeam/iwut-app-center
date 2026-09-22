package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"io"
	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	testerjoinlinkv1 "iwut-app-center/api/gen/go/app_center/v1/tester_join_link"
	testermembershipv1 "iwut-app-center/api/gen/go/app_center/v1/tester_membership"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestE2E_UCAPP009_BR_TST_010_014_018_019_OrdinaryUserMembership(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "membership-admin", "APPROVED")
	ordinaryToken := func(subject string) string {
		now := time.Now().UTC()
		return e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": subject, "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "membership-" + subject})
	}
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: database.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget,
	}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireAppWithResolver(configuration, &e2eDNSResolver{})
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
	t.Cleanup(func() { _ = connection.Close() })
	code, body := e2eHTTPCreate(t, httpAddress, adminToken, "Tester_Membership_E2E_App")
	if code != 201 {
		t.Fatalf("application status=%d", code)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(body, &application); err != nil {
		t.Fatal(err)
	}
	linkClient := testerjoinlinkv1.NewTesterJoinLinkClient(connection)
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	createLink := func(previous *string) *testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse {
		result, err := linkClient.CreateOrRotateTesterJoinLink(adminCtx, &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: application.GetId(), Command: &testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{ExpectedActiveJoinLinkId: previous}})
		if err != nil {
			t.Fatal("create/rotate link failed")
		}
		return result
	}
	link := createLink(nil)
	secretFrom := func(link *testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse) string {
		u, err := url.Parse(link.GetJoinUrl())
		if err != nil {
			t.Fatal("invalid URL")
		}
		values, err := url.ParseQuery(u.Fragment)
		if err != nil {
			t.Fatal("invalid fragment")
		}
		return values.Get("secret")
	}
	secret := secretFrom(link)
	linkID := link.GetJoinLink().GetJoinLinkId()
	joinHTTP := func(token, id, credential string) (int, http.Header, []byte) {
		payload, err := json.Marshal(map[string]string{"secret": credential, "authId": "untrusted-body-subject", "applicationId": "untrusted-body-app"})
		if err != nil {
			t.Fatal("encode request")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/tester-join-links/"+id+"/memberships", bytes.NewReader(payload))
		if err != nil {
			t.Fatal("build join request")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(transport.IdentityHeader, token)
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal("join HTTP failed")
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal("read join response")
		}
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte("tokenHash")) || bytes.Contains(data, []byte("untrusted-body")) {
			t.Fatal("credential or untrusted identity leaked")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("no-store missing")
		}
		return response.StatusCode, response.Header, data
	}
	for _, tc := range []struct {
		token, credential, reason string
		code                      int
	}{
		{"", secret, transport.ReasonAuthenticatedUserRequired, 401},
		{ordinaryToken("ordinary-1"), "bad-secret", transport.ReasonInvalidTesterJoinSecret, 400},
		{ordinaryToken("ordinary-1"), base64.RawURLEncoding.EncodeToString(make([]byte, 32)), transport.ReasonTesterJoinLinkInvalid, 404},
	} {
		code, _, body := joinHTTP(tc.token, linkID, tc.credential)
		if code != tc.code || !bytes.Contains(body, []byte(tc.reason)) {
			t.Fatalf("join negative status=%d wanted=%d", code, tc.code)
		}
	}
	code, _, body = joinHTTP(ordinaryToken("ordinary-1"), linkID, secret)
	if code != 201 {
		t.Fatalf("new join status=%d", code)
	}
	var first testermembershipv1.JoinApplicationAsTesterResponse
	if err := protojson.Unmarshal(body, &first); err != nil {
		t.Fatal("decode membership")
	}
	if !first.GetJoined() || first.GetMembership().GetTesterAuthId() != "ordinary-1" || first.GetMembership().GetApplicationId() != application.GetId() || first.GetCapacity().GetActiveTesterCount() != 1 || first.GetCapacity().GetTesterLimit() != 100 {
		t.Fatal("new membership mismatch")
	}
	code, _, body = joinHTTP(ordinaryToken("ordinary-1"), linkID, secret)
	if code != 200 {
		t.Fatalf("idempotent status=%d", code)
	}
	var repeated testermembershipv1.JoinApplicationAsTesterResponse
	if err := protojson.Unmarshal(body, &repeated); err != nil {
		t.Fatal("decode idempotent")
	}
	if repeated.GetJoined() || repeated.GetMembership().GetMembershipId() != first.GetMembership().GetMembershipId() || !repeated.GetMembership().GetJoinedAt().AsTime().Equal(first.GetMembership().GetJoinedAt().AsTime()) {
		t.Fatal("idempotency changed episode")
	}
	membershipClient := testermembershipv1.NewTesterMembershipClient(connection)
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinaryToken("ordinary-2")))
	var headers metadata.MD
	second, err := membershipClient.JoinApplicationAsTester(userCtx, &testermembershipv1.JoinApplicationAsTesterRequest{JoinLinkId: linkID, Command: &testermembershipv1.JoinApplicationAsTesterCommand{Secret: secret}}, grpc.Header(&headers))
	if err != nil || !second.GetJoined() || second.GetCapacity().GetActiveTesterCount() != 2 || second.GetMembership().GetTesterAuthId() != "ordinary-2" {
		t.Fatal("gRPC ordinary user join failed")
	}
	if values := headers.Get("cache-control"); len(values) != 1 || values[0] != "no-store" {
		t.Fatal("gRPC no-store missing")
	}
	rotated := createLink(&linkID)
	code, _, body = joinHTTP(ordinaryToken("ordinary-1"), linkID, secret)
	if code != 404 || !bytes.Contains(body, []byte(transport.ReasonTesterJoinLinkInvalid)) {
		t.Fatal("revoked link allowed idempotent join")
	}
	_, err = membershipClient.JoinApplicationAsTester(userCtx, &testermembershipv1.JoinApplicationAsTesterRequest{JoinLinkId: linkID, Command: &testermembershipv1.JoinApplicationAsTesterCommand{Secret: secret}})
	if status.Code(err) != codes.NotFound || e2eErrorReason(status.Convert(err)) != transport.ReasonTesterJoinLinkInvalid {
		t.Fatal("gRPC revoked link mapping mismatch")
	}
	linkID = rotated.GetJoinLink().GetJoinLinkId()
	secret = secretFrom(rotated)
	// Seed the remaining slots directly as setup, then exercise the actual
	// endpoint at full capacity and verify existing users remain idempotent.
	documents := make([]any, 0, 98)
	for i := 0; i < 98; i++ {
		documents = append(documents, bson.M{"membershipId": fmt.Sprintf("018f0000-0000-7000-8000-%012x", i+1), "applicationId": application.GetId(), "testerAuthId": fmt.Sprintf("seed-tester-%d", i), "status": "ACTIVE", "joinedViaJoinLinkId": linkID, "joinedAt": time.Now().UTC(), "removedBy": nil, "removedAt": nil})
	}
	collection := database.Collection("application_tester_memberships")
	if _, err := collection.InsertMany(ctx, documents); err != nil {
		t.Fatal("seed capacity failed")
	}
	code, _, body = joinHTTP(ordinaryToken("ordinary-3"), linkID, secret)
	if code != 409 || !bytes.Contains(body, []byte(transport.ReasonApplicationTesterLimitReached)) {
		t.Fatal("full list did not reject new user")
	}
	code, _, body = joinHTTP(ordinaryToken("ordinary-1"), linkID, secret)
	if code != 200 {
		t.Fatal("full list rejected existing user")
	}
	if err := protojson.Unmarshal(body, &repeated); err != nil {
		t.Fatal("decode full idempotent")
	}
	if repeated.GetCapacity().GetActiveTesterCount() != 100 || repeated.GetMembership().GetMembershipId() != first.GetMembership().GetMembershipId() {
		t.Fatal("full list idempotent result mismatch")
	}
	count, err := collection.CountDocuments(ctx, bson.M{"applicationId": application.GetId(), "status": "ACTIVE"})
	if err != nil || count != 100 {
		t.Fatal("membership capacity invariant failed")
	}
	for _, name := range []string{"application_versions", "application_publications"} {
		count, err := database.Collection(name).CountDocuments(ctx, bson.M{"applicationId": application.GetId()})
		if err != nil || count != 0 {
			t.Fatal("membership unexpectedly required version or publication")
		}
	}
}
