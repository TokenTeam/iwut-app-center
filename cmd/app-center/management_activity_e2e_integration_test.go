package main

import (
	"context"
	"errors"
	av "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management_activity"
	pv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/structpb"
	ma "iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestE2E_UCAPP052_ManagementActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	addresses := e2eReserveAddresses(t, 2)
	cfg, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: addresses[0], config.GRPCAddrEnv: addresses[1], config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
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
		_ = app.Stop()
		select {
		case <-done:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Error(runErr)
			}
		case <-time.After(15 * time.Second):
			t.Error("App stop timeout")
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, addresses[0], done, &runErr)
	conn := e2eDialGRPC(t, ctx, addresses[1], done, &runErr)
	defer conn.Close()
	token := e2eSignIdentity(t, key, "activity-owner", "APPROVED")
	writeCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, token))
	created, err := av.NewApplicationClient(conn).CreateApplication(writeCtx, &av.CreateApplicationRequest{Name: "Activity_E2E"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := pv.NewApplicationProfileRevisionClient(conn).CreateApplicationProfileRevision(writeCtx, &pv.CreateApplicationProfileRevisionRequest{ApplicationId: created.Id, Profile: &pv.ApplicationProfileContent{DisplayName: "Profile", Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}})
	if err != nil {
		t.Fatal(err)
	}
	aid, _ := shared.ParseApplicationID(created.Id)
	rid := pd.ApplicationProfileRevisionID(draft.ProfileRevisionId)
	reviewID := pd.ApplicationProfileReviewID("01995000-0000-7000-8000-000000000999")
	repo := ma.NewApplicationProfileRevisionRepository(db)
	sub, err := repo.SubmitDraft(ctx, aid, rid, "activity-owner", 1, reviewID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reason := "original rejection reason"
	if _, err = repo.DecideReview(ctx, pp.ProfileReviewDecisionInput{ApplicationID: aid, ProfileRevisionID: rid, ProfileReviewID: reviewID, ReviewerID: "private-reviewer", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: sub.ProfileRevision.Revision(), PolicyVersion: pd.InitialProfileReviewPolicyVersion, Outcome: "REJECT", Reason: &reason, DecidedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	token = e2eSignIdentity(t, key, "activity-owner", "SUSPENDED")
	ownerCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, token))
	client := pb.NewApplicationManagementActivityServiceClient(conn)
	var headers metadata.MD
	page, err := client.ListApplicationManagementActivity(ownerCtx, &pb.ListApplicationManagementActivityRequest{ApplicationId: created.Id}, grpc.Header(&headers))
	if err != nil || len(page.GetItems()) != 2 || len(headers.Get("cache-control")) == 0 {
		t.Fatal("native owner history/cache", err)
	}
	decisionID := page.Items[0].ActivityId
	detail, err := client.GetApplicationManagementActivity(ownerCtx, &pb.GetApplicationManagementActivityRequest{ApplicationId: created.Id, ActivityId: decisionID})
	if err != nil || detail.GetRejectionReason().GetStringValue() != reason {
		t.Fatal("native original reason", err)
	}
	submitted, err := client.GetApplicationManagementActivity(ownerCtx, &pb.GetApplicationManagementActivityRequest{ApplicationId: created.Id, ActivityId: page.Items[1].ActivityId})
	if err != nil || submitted.RejectionReason == nil {
		t.Fatal("native null", err)
	}
	if _, ok := submitted.RejectionReason.Kind.(*structpb.Value_NullValue); !ok {
		t.Fatal("null presence")
	}
	path := "/v1/applications/" + created.Id + "/management-activity"
	for _, tc := range []struct {
		token, method, suffix, body string
		want                        int
	}{{"", "POST", ":search", "bad json", 401}, {"invalid-jws", "POST", ":search", "bad json", 401}, {token, "POST", ":search", "{}", 200}, {token, "POST", ":search", "{\"query\":{}}", 400}, {token, "POST", ":search?x=1", "{}", 400}, {token, "GET", "/" + decisionID, "", 200}, {token, "GET", "/" + decisionID, "{}", 400}} {
		code, h, raw := e2eHTTPAdminTransfer(t, addresses[0], tc.token, tc.method, path+tc.suffix, []byte(tc.body))
		if code != tc.want || h.Get("Cache-Control") != "private, no-store" {
			t.Fatal("HTTP contract", tc.want, code, string(raw))
		}
		if code == 200 {
			for _, secret := range []string{"private-reviewer", "submittedBy", "decidedBy", "policyBinding", "snapshot", "confirmedCheckIds"} {
				if strings.Contains(string(raw), secret) {
					t.Fatal("disclosure", secret)
				}
			}
			if tc.method == "POST" && strings.Contains(string(raw), reason) {
				t.Fatal("list exposed reason")
			}
		}
	}
	code, _, raw := e2eHTTPAdminTransfer(t, addresses[0], token, "GET", path+"/"+page.Items[1].ActivityId, nil)
	if code != 200 || !strings.Contains(string(raw), `"rejectionReason":null`) {
		t.Fatal("HTTP explicit null", code, string(raw))
	}
	typedConn, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+addresses[0]))
	if err != nil {
		t.Fatal(err)
	}
	defer typedConn.Close()
	typed := pb.NewApplicationManagementActivityServiceHTTPClient(typedConn)
	httpHeaders := http.Header{transport.IdentityHeader: []string{token}}
	p, err := typed.ListApplicationManagementActivity(ctx, &pb.ListApplicationManagementActivityRequest{ApplicationId: created.Id, Query: &pb.ListApplicationManagementActivityQuery{PageSize: 1}}, khttp.Header(&httpHeaders))
	if err != nil || len(p.GetItems()) != 1 || p.NextPageToken == "" {
		t.Fatal("generated HTTP search binding", err)
	}
	httpHeaders.Set(transport.IdentityHeader, token)
	if d, err := typed.GetApplicationManagementActivity(ctx, &pb.GetApplicationManagementActivityRequest{ApplicationId: created.Id, ActivityId: decisionID}, khttp.Header(&httpHeaders)); err != nil || d.GetActivity().GetActivityId() != decisionID {
		t.Fatal("generated detail binding", err)
	}
	for _, permissions := range [][]string{nil, {"platform.admin"}, {"app.version.review"}, {"app.application.search"}, {"app.application.suspend"}} {
		other := e2eSignReviewerIdentity(t, key, "other", permissions...)
		otherCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, other))
		if _, err := client.GetApplicationManagementActivity(otherCtx, &pb.GetApplicationManagementActivityRequest{ApplicationId: created.Id, ActivityId: decisionID}); status.Code(err) != codes.NotFound {
			t.Fatal("owner privacy", err)
		}
	}
	unknown := &pb.ListApplicationManagementActivityRequest{ApplicationId: created.Id, Query: &pb.ListApplicationManagementActivityQuery{}}
	unknown.Query.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	if _, err := client.ListApplicationManagementActivity(ownerCtx, unknown); status.Code(err) != codes.InvalidArgument {
		t.Fatal("unknown native field", err)
	}
	if _, err = db.Collection("applications").UpdateOne(ctx, bson.M{"id": created.Id}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSED", "platformAvailabilityStatus": "SUSPENDED", "archiveStatus": "ARCHIVED"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListApplicationManagementActivity(ownerCtx, &pb.ListApplicationManagementActivityRequest{ApplicationId: created.Id}); err != nil {
		t.Fatal("closed history", err)
	}
	if _, err = db.Collection("applications").UpdateOne(ctx, bson.M{"id": created.Id}, bson.M{"$set": bson.M{"adminId": "new-owner", "ownershipRevision": int64(2)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListApplicationManagementActivity(ownerCtx, &pb.ListApplicationManagementActivityRequest{ApplicationId: created.Id, Query: &pb.ListApplicationManagementActivityQuery{PageToken: "bad"}}); status.Code(err) != codes.NotFound {
		t.Fatal("old owner before token", err)
	}
}
