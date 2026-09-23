package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	testerjoinlinkv1 "iwut-app-center/api/gen/go/app_center/v1/tester_join_link"
	testermembershipv1 "iwut-app-center/api/gen/go/app_center/v1/tester_membership"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP011_BR_TST_029_036_Revocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "revocation-admin", "APPROVED")
	otherAdminToken := e2eSignIdentity(t, key, "different-admin", "APPROVED")
	now := time.Now().UTC()
	ordinaryToken := e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": "revocation-user", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "revocation-user"})
	reviewerToken := e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": "revocation-reviewer", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "revocation-reviewer", "permissions": []string{"app.version.review"}})
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: database.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
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
	code, body := e2eHTTPCreate(t, httpAddress, adminToken, "Tester_Revocation_E2E_App")
	if code != 201 {
		t.Fatalf("create application status %d", code)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(body, &application); err != nil {
		t.Fatal(err)
	}
	appID := application.GetId()
	otherCode, otherBody := e2eHTTPCreate(t, httpAddress, adminToken, "Tester_Revocation_Other_App")
	var otherApp applicationv1.CreateApplicationResponse
	if otherCode != 201 || protojson.Unmarshal(otherBody, &otherApp) != nil {
		t.Fatal("create other app failed")
	}

	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinaryToken))

	linkClient := testerjoinlinkv1.NewTesterJoinLinkClient(connection)
	membershipClient := testermembershipv1.NewTesterMembershipClient(connection)
	createLink := func(previous *string) *testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse {
		r, err := linkClient.CreateOrRotateTesterJoinLink(adminCtx, &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: appID, Command: &testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{ExpectedActiveJoinLinkId: previous}})
		if err != nil {
			t.Fatal("create link failed")
		}
		return r
	}
	old := createLink(nil)
	oldID := old.GetJoinLink().GetJoinLinkId()
	current := createLink(&oldID)
	currentID := current.GetJoinLink().GetJoinLinkId()
	parsed, err := url.Parse(current.GetJoinUrl())
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	secret := fragment.Get("secret")
	joinReq := &testermembershipv1.JoinApplicationAsTesterRequest{JoinLinkId: currentID, Command: &testermembershipv1.JoinApplicationAsTesterCommand{Secret: secret}}
	membership, err := membershipClient.JoinApplicationAsTester(userCtx, joinReq)
	if err != nil {
		t.Fatal("join failed")
	}
	before := bson.M{}
	collection := database.Collection("application_tester_memberships")
	if err := collection.FindOne(ctx, bson.M{"membershipId": membership.GetMembership().GetMembershipId()}).Decode(&before); err != nil {
		t.Fatal(err)
	}
	revokeHTTP := func(token, applicationID, linkID, query, payload string) (int, []byte) {
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://"+httpAddress+"/v1/applications/"+applicationID+"/tester-join-links/"+linkID+query, bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(transport.IdentityHeader, token)
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal("revoke HTTP failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte("private-")) || bytes.Contains(body, []byte("tokenHash")) || bytes.Contains(body, []byte("joinUrl")) {
			t.Fatal("cache/redaction boundary failed")
		}
		return response.StatusCode, body
	}
	for _, tc := range []struct {
		token, app, id, query, body, reason string
		code                                int
	}{
		{"", appID, currentID, "", "", transport.ReasonDeveloperIdentityRequired, 401},
		{"private-JWS", appID, currentID, "", "", transport.ReasonInvalidDeveloperIdentity, 401},
		{ordinaryToken, appID, currentID, "", "", transport.ReasonDeveloperApprovalRequired, 403},
		{reviewerToken, appID, currentID, "", "", transport.ReasonDeveloperApprovalRequired, 403},
		{otherAdminToken, appID, currentID, "", "", transport.ReasonApplicationAdminRequired, 403},
		{adminToken, otherApp.GetId(), currentID, "", "", transport.ReasonApplicationTesterJoinLinkNotFound, 404},
		{adminToken, "bad-id", currentID, "", "", transport.ReasonInvalidApplicationID, 400},
		{adminToken, appID, "bad-id", "", "", transport.ReasonInvalidTesterJoinLinkId, 400},
		{adminToken, appID, "018f0000-0000-7000-8000-000000009999", "", "", transport.ReasonApplicationTesterJoinLinkNotFound, 404},
		{adminToken, "018f0000-0000-7000-8000-000000009999", currentID, "", "", transport.ReasonApplicationTesterJoinLinkNotFound, 404},
		{adminToken, appID, currentID, "?removeTesters=true", "", transport.ReasonInvalidRevokeTesterJoinLinkRequest, 400},
		{adminToken, appID, currentID, "", `{"revokedBy":"private-forged"}`, transport.ReasonInvalidRevokeTesterJoinLinkRequest, 400},
	} {
		code, body := revokeHTTP(tc.token, tc.app, tc.id, tc.query, tc.body)
		if code != tc.code || !bytes.Contains(body, []byte(tc.reason)) {
			t.Fatalf("negative response %d expected %d reason %s", code, tc.code, tc.reason)
		}
	}
	// The exact old ROTATED identity must preserve its audit and leave the current link usable.
	oldRequest := &testerjoinlinkv1.RevokeTesterJoinLinkRequest{ApplicationId: appID, JoinLinkId: oldID}
	var headers metadata.MD
	rotated, err := linkClient.RevokeTesterJoinLink(adminCtx, oldRequest, grpc.Header(&headers))
	if err != nil || rotated.GetRevoked() || rotated.GetJoinLink().GetRevocationReason() != "ROTATED" || rotated.GetJoinLink().GetReplacedByJoinLinkId() != currentID {
		t.Fatal("old ROTATED result mismatch")
	}
	if v := headers.Get("cache-control"); len(v) != 1 || v[0] != "no-store" {
		t.Fatal("gRPC cache policy missing")
	}
	joined, err := membershipClient.JoinApplicationAsTester(userCtx, joinReq)
	if err != nil || joined.GetJoined() {
		t.Fatal("old revoke touched active replacement")
	}
	currentRequest := &testerjoinlinkv1.RevokeTesterJoinLinkRequest{ApplicationId: appID, JoinLinkId: currentID}
	for _, tc := range []struct {
		ctx    context.Context
		code   codes.Code
		reason string
	}{
		{userCtx, codes.PermissionDenied, transport.ReasonDeveloperApprovalRequired},
		{metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reviewerToken)), codes.PermissionDenied, transport.ReasonDeveloperApprovalRequired},
		{ctx, codes.Unauthenticated, transport.ReasonDeveloperIdentityRequired},
	} {
		_, err := linkClient.RevokeTesterJoinLink(tc.ctx, currentRequest)
		if status.Code(err) != tc.code || e2eErrorReason(status.Convert(err)) != tc.reason {
			t.Fatal("gRPC auth boundary failed")
		}
	}
	unknown := proto.Clone(currentRequest).(*testerjoinlinkv1.RevokeTesterJoinLinkRequest)
	unknown.ProtoReflect().SetUnknown([]byte{0x18, 0x01})
	_, err = linkClient.RevokeTesterJoinLink(adminCtx, unknown)
	if status.Code(err) != codes.InvalidArgument || e2eErrorReason(status.Convert(err)) != transport.ReasonInvalidRevokeTesterJoinLinkRequest {
		t.Fatal("gRPC unknown command accepted")
	}
	code, body = revokeHTTP(adminToken, appID, currentID, "", "")
	if code != 200 {
		t.Fatalf("first revoke status %d", code)
	}
	var revoked testerjoinlinkv1.RevokeTesterJoinLinkResponse
	if err := protojson.Unmarshal(body, &revoked); err != nil {
		t.Fatal(err)
	}
	link := revoked.GetJoinLink()
	if !revoked.GetRevoked() || link.GetStatus() != "REVOKED" || link.GetRevocationReason() != "MANUAL" || link.GetRevokedBy() != "revocation-admin" || link.GetRevokedAt() == nil || link.ReplacedByJoinLinkId != nil || !proto.Equal(link.GetCreatedAt(), current.GetJoinLink().GetCreatedAt()) {
		t.Fatal("manual audit mismatch")
	}
	repeated, err := linkClient.RevokeTesterJoinLink(adminCtx, currentRequest)
	if err != nil || repeated.GetRevoked() || !proto.Equal(repeated.GetJoinLink(), revoked.GetJoinLink()) {
		t.Fatal("gRPC retry changed audit")
	}
	code, body = revokeHTTP(adminToken, appID, currentID, "", "")
	var httpRepeat testerjoinlinkv1.RevokeTesterJoinLinkResponse
	if code != 200 || protojson.Unmarshal(body, &httpRepeat) != nil || httpRepeat.GetRevoked() || !proto.Equal(httpRepeat.GetJoinLink(), revoked.GetJoinLink()) {
		t.Fatal("HTTP repeat mismatch")
	}
	_, err = membershipClient.JoinApplicationAsTester(userCtx, joinReq)
	if status.Code(err) != codes.NotFound || e2eErrorReason(status.Convert(err)) != transport.ReasonTesterJoinLinkInvalid {
		t.Fatal("revoked credential accepted")
	}
	after := bson.M{}
	if err := collection.FindOne(ctx, bson.M{"membershipId": membership.GetMembership().GetMembershipId()}).Decode(&after); err != nil {
		t.Fatal(err)
	}
	// Compare BSON values structurally; map serialization order is not stable.
	if !reflect.DeepEqual(before, after) {
		t.Fatal("membership mutated")
	}
	newLink := createLink(nil)
	if newLink.GetJoinLink().GetJoinLinkId() == currentID {
		t.Fatal("new link reused revoked identity")
	}
	count, err := collection.CountDocuments(ctx, bson.M{"applicationId": appID, "status": "ACTIVE"})
	if err != nil || count != 1 {
		t.Fatal("revocation changed membership capacity")
	}

	// Invalid stored audit is an internal invariant error, never a public conflict.
	if _, err := database.Collection("application_tester_join_links").UpdateOne(ctx, bson.M{"joinLinkId": newLink.GetJoinLink().GetJoinLinkId()}, bson.M{"$set": bson.M{"revokedBy": "private-invalid-audit"}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal("seed malformed audit failed")
	}
	code, body = revokeHTTP(adminToken, appID, newLink.GetJoinLink().GetJoinLinkId(), "", "")
	if code != 500 || !bytes.Contains(body, []byte(transport.ReasonApplicationTesterJoinLinkStateInconsistent)) {
		t.Fatal("HTTP inconsistency mapping failed")
	}
	_, err = linkClient.RevokeTesterJoinLink(adminCtx, &testerjoinlinkv1.RevokeTesterJoinLinkRequest{ApplicationId: appID, JoinLinkId: newLink.GetJoinLink().GetJoinLinkId()})
	if status.Code(err) != codes.Internal || e2eErrorReason(status.Convert(err)) != transport.ReasonApplicationTesterJoinLinkStateInconsistent {
		t.Fatal("gRPC inconsistency mapping failed")
	}
}
