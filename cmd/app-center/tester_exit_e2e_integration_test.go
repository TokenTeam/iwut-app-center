package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	av "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	cv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/test_application_catalog"
	lv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_join_link"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/tester_membership"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP040BRTST044051SelfExitRealProtocols(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "exit-admin", "APPROVED")
	userToken := e2eSignReviewerIdentity(t, key, "exit-user")
	pendingToken := e2eSignIdentity(t, key, "exit-user", "PENDING")
	reviewerToken := e2eSignReviewerIdentity(t, key, "other-reviewer", "app.version.review")
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	conf, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &catalogE2EDNSResolver{}
	app, cleanup, err := wireAppWithResolver(conf, resolver)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(done) }()
	t.Cleanup(func() {
		if err := app.Stop(); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("stop timeout")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, done, &runErr)
	conn := e2eDialGRPC(t, ctx, grpcAddress, done, &runErr)
	defer conn.Close()
	code, body := e2eHTTPCreate(t, httpAddress, adminToken, "Self_Exit_E2E")
	var created av.CreateApplicationResponse
	if code != 201 || protojson.Unmarshal(body, &created) != nil {
		t.Fatalf("create %d", code)
	}
	appID := created.GetId()
	userCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, userToken))
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	members := pb.NewTesterMembershipClient(conn)
	link, err := lv.NewTesterJoinLinkClient(conn).CreateOrRotateTesterJoinLink(adminCtx, &lv.CreateOrRotateTesterJoinLinkRequest{ApplicationId: appID, Command: &lv.CreateOrRotateTesterJoinLinkCommand{}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(link.GetJoinUrl())
	fragment, _ := url.ParseQuery(parsed.Fragment)
	secret := fragment.Get("secret")
	join := func(who context.Context) *pb.JoinApplicationAsTesterResponse {
		t.Helper()
		r, err := members.JoinApplicationAsTester(who, &pb.JoinApplicationAsTesterRequest{JoinLinkId: link.GetJoinLink().GetJoinLinkId(), Command: &pb.JoinApplicationAsTesterCommand{Secret: secret}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := join(userCtx)
	own := join(adminCtx)
	id := first.Membership.MembershipId
	catalog := cv.NewTestApplicationCatalogServiceClient(conn)
	runtime := &cv.RuntimeQuery{HostRpcApiMajor: 1}
	if r, err := catalog.GetMyTestApplication(userCtx, &cv.GetMyTestApplicationRequest{ApplicationId: appID, Runtime: runtime}); err != nil || r.GetItem().GetMembership().GetMembershipId() != id {
		t.Fatal("participation missing", err)
	}
	raw := func(token, a, id, query, payload string) (int, []byte) {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+a+"/tester-memberships/"+id+":leave"+query, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		r.Header.Set("Content-Type", "application/json")
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cache=%s", resp.Header.Get("Cache-Control"))
		}
		for _, hidden := range []string{secret, "removedBy", "testerAuthId", "capacity", "joinedViaJoinLinkId", "tokenHash", "activeTesterCount", "exit-admin", "exit-user"} {
			if bytes.Contains(b, []byte(hidden)) {
				t.Fatalf("disclosed %s", hidden)
			}
		}
		return resp.StatusCode, b
	}
	for _, tc := range []struct {
		token, a, id, query, payload, reason string
		code                                 int
	}{
		{"", "bad", "bad", "?x=1", "invalid", transport.ReasonUserIdentityRequired, 401},
		{"invalid-jws", "bad", "bad", "", "invalid", transport.ReasonInvalidUserIdentity, 401},
		{userToken, "bad", id, "", "", transport.ReasonInvalidApplicationID, 400},
		{userToken, appID, "bad", "", "", transport.ReasonInvalidTesterMembershipId, 400},
		{adminToken, appID, id, "", "", transport.ReasonApplicationTesterMembershipNotFound, 404},
		{reviewerToken, appID, id, "", "", transport.ReasonApplicationTesterMembershipNotFound, 404},
		{userToken, "01900000-0000-7000-8000-000000009999", id, "", "", transport.ReasonApplicationTesterMembershipNotFound, 404},
		{userToken, appID, "01900000-0000-7000-8000-000000009999", "", "", transport.ReasonApplicationTesterMembershipNotFound, 404},
		{userToken, appID, id, "?membership_id=" + own.Membership.MembershipId, "", "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", 400},
		{userToken, appID, id, "?", "", "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", 400},
		{userToken, appID, id, "", "{}", "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", 400},
		{userToken, appID, id, "", "null", "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", 400},
		{userToken, appID, id, "", " ", "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", 400},
		{userToken, appID, id, "", `{"removedBy":"forged"}`, "ERROR_REASON_INVALID_TESTER_EXIT_REQUEST", 400},
	} {
		code, b := raw(tc.token, tc.a, tc.id, tc.query, tc.payload)
		if code != tc.code || !bytes.Contains(b, []byte(tc.reason)) {
			t.Fatalf("HTTP %d %s want %d %s", code, b, tc.code, tc.reason)
		}
	}
	request := &pb.LeaveApplicationAsTesterRequest{ApplicationId: appID, MembershipId: id}
	var headers metadata.MD
	for _, tc := range []struct {
		who     context.Context
		request *pb.LeaveApplicationAsTesterRequest
		code    codes.Code
	}{
		{ctx, &pb.LeaveApplicationAsTesterRequest{ApplicationId: "bad"}, codes.Unauthenticated},
		{adminCtx, request, codes.NotFound},
		{userCtx, &pb.LeaveApplicationAsTesterRequest{ApplicationId: appID, MembershipId: "bad"}, codes.InvalidArgument},
	} {
		_, err := members.LeaveApplicationAsTester(tc.who, tc.request, grpc.Header(&headers))
		if status.Code(err) != tc.code || len(headers.Get("cache-control")) != 1 || headers.Get("cache-control")[0] != "private, no-store" {
			t.Fatalf("gRPC boundary %v headers=%v", err, headers)
		}
	}
	unknown := &pb.LeaveApplicationAsTesterRequest{ApplicationId: appID, MembershipId: id}
	unknown.ProtoReflect().SetUnknown([]byte{0x18, 0x01})
	if _, err := members.LeaveApplicationAsTester(userCtx, unknown, grpc.Header(&headers)); status.Code(err) != codes.InvalidArgument {
		t.Fatal("unknown accepted", err)
	}
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	generated := pb.NewTesterMembershipHTTPClient(httpClient)
	h := http.Header{transport.IdentityHeader: []string{pendingToken}}
	left, err := generated.LeaveApplicationAsTester(ctx, request, khttp.Header(&h))
	if err != nil || !left.GetLeft() || left.GetMembership().GetMembershipId() != id || left.GetMembership().GetStatus() != "REMOVED" || h.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("generated HTTP exit %v", err)
	}
	again, err := members.LeaveApplicationAsTester(userCtx, request, grpc.Header(&headers))
	if err != nil || again.GetLeft() || !again.Membership.RemovedAt.AsTime().Equal(left.Membership.RemovedAt.AsTime()) || headers.Get("cache-control")[0] != "private, no-store" {
		t.Fatal("gRPC retry", err)
	}
	if _, err := catalog.GetMyTestApplication(userCtx, &cv.GetMyTestApplicationRequest{ApplicationId: appID, Runtime: runtime}); status.Code(err) != codes.NotFound {
		t.Fatal("removed detail visible", err)
	}
	if page, err := catalog.ListMyTestApplications(userCtx, &cv.ListMyTestApplicationsRequest{Query: &cv.ListQuery{Runtime: runtime}}); err != nil || len(page.GetItems()) != 0 {
		t.Fatal("removed listed", err)
	}
	rejoined := join(userCtx)
	if rejoined.Membership.MembershipId == id {
		t.Fatal("reused episode")
	}
	code, body = raw(userToken, appID, id, "", "")
	var old pb.LeaveApplicationAsTesterResponse
	if code != 200 || protojson.Unmarshal(body, &old) != nil || old.Left {
		t.Fatal("old retry")
	}
	if current, err := catalog.GetMyTestApplication(userCtx, &cv.GetMyTestApplicationRequest{ApplicationId: appID, Runtime: runtime}); err != nil || current.Item.Membership.MembershipId != rejoined.Membership.MembershipId {
		t.Fatal("old exit affected new", err)
	}
	// Current admin exits their own episode as a USER, independently of another tester.
	code, body = raw(adminToken, appID, own.Membership.MembershipId, "", "")
	var ownLeft pb.LeaveApplicationAsTesterResponse
	if code != 200 || protojson.Unmarshal(body, &ownLeft) != nil || !ownLeft.Left {
		t.Fatal("admin self-exit")
	}
	// An offline application with a known retained handle still permits self-exit.
	if _, err = db.Collection("applications").UpdateOne(ctx, bson.M{"id": appID}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSED", "archiveStatus": "ARCHIVED", "platformAvailabilityStatus": "SUSPENDED"}}); err != nil {
		t.Fatal(err)
	}
	h = http.Header{transport.IdentityHeader: []string{userToken}}
	if r, err := generated.LeaveApplicationAsTester(ctx, &pb.LeaveApplicationAsTesterRequest{ApplicationId: appID, MembershipId: rejoined.Membership.MembershipId}, khttp.Header(&h)); err != nil || !r.Left {
		t.Fatal("retained exit", err)
	}
	// Inject required-source damage; even terminal no-ops must fail closed.
	if _, err = db.Collection("application_tester_join_links").UpdateOne(ctx, bson.M{"joinLinkId": link.GetJoinLink().GetJoinLinkId()}, bson.M{"$set": bson.M{"createdAt": "bad"}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	code, body = raw(userToken, appID, id, "", "")
	if code != 500 || !bytes.Contains(body, []byte(transport.ReasonApplicationTesterStateInconsistent)) {
		t.Fatal("corrupt source", code, string(body))
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("exit called DNS")
	}
}
