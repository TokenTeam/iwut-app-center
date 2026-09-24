package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	testerjoinlinkv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	testermembershipv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP010_BR_TST_020_028_RemovalAndRejoin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "removal-admin", "APPROVED")
	otherAdminToken := e2eSignIdentity(t, key, "different-admin", "APPROVED")
	now := time.Now().UTC()
	ordinaryToken := e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": "removal-user", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "removal-user"})
	reviewerToken := e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": "removal-reviewer", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "removal-reviewer", "permissions": []string{"app.version.review"}})
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
	code, body := e2eHTTPCreate(t, httpAddress, adminToken, "Tester_Removal_E2E_App")
	if code != 201 {
		t.Fatalf("create application status %d", code)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(body, &application); err != nil {
		t.Fatal(err)
	}
	appID := application.GetId()
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, ordinaryToken))
	link, err := testerjoinlinkv1.NewTesterJoinLinkClient(connection).CreateOrRotateTesterJoinLink(adminCtx, &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: appID, Command: &testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{}})
	if err != nil {
		t.Fatal("create join link failed")
	}
	parsed, err := url.Parse(link.GetJoinUrl())
	if err != nil {
		t.Fatal("parse join URL")
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal("parse fragment")
	}
	linkID, secret := link.GetJoinLink().GetJoinLinkId(), fragment.Get("secret")
	client := testermembershipv1.NewTesterMembershipClient(connection)
	join := func(identity context.Context) *testermembershipv1.JoinApplicationAsTesterResponse {
		r, err := client.JoinApplicationAsTester(identity, &testermembershipv1.JoinApplicationAsTesterRequest{JoinLinkId: linkID, Command: &testermembershipv1.JoinApplicationAsTesterCommand{Secret: secret}})
		if err != nil {
			t.Fatal("join failed")
		}
		return r
	}
	first := join(userCtx)
	own := join(adminCtx)
	removeHTTP := func(token, applicationID, membershipID, query, payload string) (int, []byte) {
		request, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://"+httpAddress+"/v1/applications/"+applicationID+"/tester-memberships/"+membershipID+query, bytes.NewBufferString(payload))
		if err != nil {
			t.Fatal("build removal request")
		}
		request.Header.Set(transport.IdentityHeader, token)
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal("remove HTTP failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal("read response")
		}
		if response.Header.Get("Cache-Control") != "no-store" || bytes.Contains(body, []byte(secret)) || bytes.Contains(body, []byte("private-")) || bytes.Contains(body, []byte("tokenHash")) {
			t.Fatal("cache/redaction boundary failed")
		}
		return response.StatusCode, body
	}
	firstID := first.GetMembership().GetMembershipId()
	for _, tc := range []struct {
		token, app, id, query, body, reason string
		code                                int
	}{
		{"", appID, firstID, "", "", transport.ReasonDeveloperIdentityRequired, 401},
		{"private-JWS", appID, firstID, "", "", transport.ReasonInvalidDeveloperIdentity, 401},
		{ordinaryToken, appID, firstID, "", "", transport.ReasonDeveloperApprovalRequired, 403},
		{reviewerToken, appID, firstID, "", "", transport.ReasonDeveloperApprovalRequired, 403},
		{otherAdminToken, appID, firstID, "", "", transport.ReasonApplicationAdminRequired, 403},
		{adminToken, "bad-id", firstID, "", "", transport.ReasonInvalidApplicationID, 400},
		{adminToken, appID, "bad-id", "", "", transport.ReasonInvalidTesterMembershipId, 400},
		{adminToken, appID, "018f0000-0000-7000-8000-000000009999", "", "", transport.ReasonApplicationTesterMembershipNotFound, 404},
		{adminToken, "018f0000-0000-7000-8000-000000009999", firstID, "", "", transport.ReasonApplicationTesterMembershipNotFound, 404},
		{adminToken, appID, firstID, "?revokeJoinLink=true", "", transport.ReasonInvalidRemoveTesterRequest, 400},
		{adminToken, appID, firstID, "", `{"removedBy":"private-forged"}`, transport.ReasonInvalidRemoveTesterRequest, 400},
	} {
		code, body := removeHTTP(tc.token, tc.app, tc.id, tc.query, tc.body)
		if code != tc.code || !bytes.Contains(body, []byte(tc.reason)) {
			t.Fatalf("remove negative status %d wanted %d reason %s", code, tc.code, body)
		}
	}
	// A current administrator may remove their own tester membership.
	ownRequest := &testermembershipv1.RemoveApplicationTesterRequest{ApplicationId: appID, MembershipId: own.GetMembership().GetMembershipId()}
	ownRemoved, err := client.RemoveApplicationTester(adminCtx, ownRequest)
	if err != nil || !ownRemoved.GetRemoved() || ownRemoved.GetCapacity().GetActiveTesterCount() != 1 {
		t.Fatal("gRPC self tester removal failed")
	}
	ownRepeated, err := client.RemoveApplicationTester(adminCtx, ownRequest)
	if err != nil || ownRepeated.GetRemoved() || !proto.Equal(ownRemoved.GetMembership(), ownRepeated.GetMembership()) {
		t.Fatal("gRPC idempotent audit changed")
	}
	_, err = client.RemoveApplicationTester(userCtx, ownRequest)
	if status.Code(err) != codes.PermissionDenied || e2eErrorReason(status.Convert(err)) != transport.ReasonDeveloperApprovalRequired {
		t.Fatal("gRPC ordinary user removal allowed")
	}
	reviewerCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reviewerToken))
	_, err = client.RemoveApplicationTester(reviewerCtx, ownRequest)
	if status.Code(err) != codes.PermissionDenied || e2eErrorReason(status.Convert(err)) != transport.ReasonDeveloperApprovalRequired {
		t.Fatal("gRPC reviewer-only removal allowed")
	}
	_, err = client.RemoveApplicationTester(ctx, ownRequest)
	if status.Code(err) != codes.Unauthenticated || e2eErrorReason(status.Convert(err)) != transport.ReasonDeveloperIdentityRequired {
		t.Fatal("gRPC missing identity mapping failed")
	}
	// Fill all slots, then remove and rejoin through the still-valid link.
	collection := database.Collection("application_tester_memberships")
	documents := make([]any, 0, 99)
	for i := 0; i < 99; i++ {
		documents = append(documents, bson.M{"membershipId": fmt.Sprintf("018f0000-0000-7000-8000-%012x", i+1), "applicationId": appID, "testerAuthId": fmt.Sprintf("removal-seed-%d", i), "status": "ACTIVE", "joinedViaJoinLinkId": linkID, "joinedAt": time.Now().UTC(), "removedBy": nil, "removedAt": nil})
	}
	if _, err := collection.InsertMany(ctx, documents); err != nil {
		t.Fatal("seed capacity failed")
	}
	code, body = removeHTTP(adminToken, appID, firstID, "", "")
	if code != 200 {
		t.Fatalf("first removal status %d", code)
	}
	var removed testermembershipv1.RemoveApplicationTesterResponse
	if err := protojson.Unmarshal(body, &removed); err != nil {
		t.Fatal("decode removal")
	}
	m := removed.GetMembership()
	if !removed.GetRemoved() || !removed.GetActiveJoinLinkExists() || removed.GetCapacity().GetActiveTesterCount() != 99 || removed.GetCapacity().GetTesterLimit() != 100 || m.GetStatus() != "REMOVED" || m.GetRemovedBy() != "removal-admin" || m.GetRemovedAt() == nil || !proto.Equal(m.GetJoinedAt(), first.GetMembership().GetJoinedAt()) || m.GetJoinedViaJoinLinkId() != linkID {
		t.Fatal("removal result mismatch")
	}
	rejoined := join(userCtx)
	if !rejoined.GetJoined() || rejoined.GetMembership().GetMembershipId() == firstID || rejoined.GetCapacity().GetActiveTesterCount() != 100 {
		t.Fatal("freed capacity did not permit a new episode")
	}
	code, body = removeHTTP(adminToken, appID, firstID, "", "")
	if code != 200 {
		t.Fatalf("old-episode idempotency status %d", code)
	}
	var repeated testermembershipv1.RemoveApplicationTesterResponse
	if err := protojson.Unmarshal(body, &repeated); err != nil {
		t.Fatal("decode repeat")
	}
	if repeated.GetRemoved() || !proto.Equal(repeated.GetMembership(), removed.GetMembership()) || repeated.GetCapacity().GetActiveTesterCount() != 100 {
		t.Fatal("old episode retry touched new episode or audit")
	}
	count, err := collection.CountDocuments(ctx, bson.M{"membershipId": rejoined.GetMembership().GetMembershipId(), "status": "ACTIVE"})
	if err != nil || count != 1 {
		t.Fatal("new episode was removed")
	}
	linkCount, err := database.Collection("application_tester_join_links").CountDocuments(ctx, bson.M{"applicationId": appID, "joinLinkId": linkID, "status": "ACTIVE"})
	if err != nil || linkCount != 1 {
		t.Fatal("removal mutated join link")
	}
	for _, name := range []string{"application_versions", "application_publications", "application_reviews"} {
		count, err := database.Collection(name).CountDocuments(ctx, bson.M{"applicationId": appID})
		if err != nil || count != 0 {
			t.Fatal("removal touched unrelated lifecycle")
		}
	}
	// Corrupt capacity through setup only; the public boundary must classify the
	// detected invariant violation as INTERNAL with the dedicated stable reason.
	if _, err := collection.InsertOne(ctx, bson.M{"membershipId": "018f0000-0000-7000-8000-000000009998", "applicationId": appID, "testerAuthId": "inconsistent-extra", "status": "ACTIVE", "joinedViaJoinLinkId": linkID, "joinedAt": time.Now().UTC(), "removedBy": nil, "removedAt": nil}); err != nil {
		t.Fatal("seed inconsistent state")
	}
	code, body = removeHTTP(adminToken, appID, firstID, "", "")
	if code != 500 || !bytes.Contains(body, []byte(transport.ReasonApplicationTesterStateInconsistent)) {
		t.Fatal("HTTP inconsistency mapping mismatch")
	}
	_, err = client.RemoveApplicationTester(adminCtx, ownRequest)
	if status.Code(err) != codes.Internal || e2eErrorReason(status.Convert(err)) != transport.ReasonApplicationTesterStateInconsistent {
		t.Fatal("gRPC inconsistency mapping mismatch")
	}
}
