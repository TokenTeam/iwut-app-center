package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	av "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	pv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	ma "iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

func TestE2E_UCAPP039_ProfileManagement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "profile-admin", "APPROVED")
	pendingToken := e2eSignIdentity(t, key, "profile-admin", "PENDING")
	otherToken := e2eSignIdentity(t, key, "independent-reviewer", "APPROVED")
	now := time.Now().UTC()
	userToken := e2eSignIdentityClaims(t, key, map[string]any{"iss": e2eIssuer, "sub": "profile-admin", "aud": e2eAudience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(), "exp": now.Add(2 * time.Minute).Unix(), "jti": "ordinary-user"})
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, e := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
	if e != nil {
		t.Fatal(e)
	}
	resolver := &catalogE2EDNSResolver{}
	app, cleanup, e := wireAppWithResolver(configuration, resolver)
	if e != nil {
		t.Fatal(e)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(runDone) }()
	t.Cleanup(func() {
		if e := app.Stop(); e != nil {
			t.Error(e)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("stop timeout")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	conn := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = conn.Close() })
	client := pv.NewApplicationProfileRevisionClient(conn)
	code, body := e2eHTTPCreate(t, httpAddress, adminToken, "Managed_Profile_E2E")
	var created av.CreateApplicationResponse
	if code != 201 || protojson.Unmarshal(body, &created) != nil {
		t.Fatalf("create=%d %s", code, body)
	}
	appID := created.GetId()
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, userToken))
	writeCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	draft, e := client.CreateApplicationProfileRevision(writeCtx, &pv.CreateApplicationProfileRevisionRequest{ApplicationId: appID, Profile: &pv.ApplicationProfileContent{DisplayName: "Café", Description: structpb.NewStringValue("full description"), Icon: structpb.NewNullValue()}})
	if e != nil {
		t.Fatal(e)
	}
	path := "/v1/applications/" + appID + "/profile-revisions"
	get := func(token, suffix, payload string) (int, http.Header, []byte) {
		t.Helper()
		r, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+httpAddress+path+suffix, strings.NewReader(payload))
		if e != nil {
			t.Fatal(e)
		}
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		resp, e := (&http.Client{Timeout: 15 * time.Second}).Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		if resp.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cache=%s", resp.Header.Get("Cache-Control"))
		}
		return resp.StatusCode, resp.Header, b
	}
	for _, tc := range []struct {
		token, suffix, payload, reason string
		code                           int
	}{
		{"", "?pageSize=0", "", transport.ReasonUserIdentityRequired, 401},
		{"invalid-jws", "?unknown=1", "", transport.ReasonInvalidUserIdentity, 401},
		{otherToken, "?pageToken=invalid", "", transport.ReasonProfileManagementNotFound, 404},
		{userToken, "?pageSize=0", "", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "?pageSize=101", "", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "?pageSize=1&pageSize=2", "", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "?page_size=1", "", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "?unknown=1", "", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "?reviewStatus=approved", "", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "?pageToken=invalid", "", transport.ReasonInvalidProfileManagementPageToken, 400},
		{userToken, "", "{}", transport.ReasonInvalidProfileManagementQuery, 400},
		{userToken, "/bad-id", "", transport.ReasonProfileManagementNotFound, 404},
		{userToken, "/" + draft.GetProfileRevisionId() + "?reviewPageSize=0", "", transport.ReasonInvalidProfileManagementQuery, 400},
	} {
		code, _, b := get(tc.token, tc.suffix, tc.payload)
		if code != tc.code || !bytes.Contains(b, []byte(tc.reason)) {
			t.Fatalf("%s: want %d %s got %d %s", tc.suffix, tc.code, tc.reason, code, b)
		}
	}
	for _, token := range []string{userToken, pendingToken} {
		code, _, b := get(token, "?pageSize=1&reviewStatus=DRAFT", "")
		var page pv.OwnedApplicationProfileRevisionPage
		if code != 200 || protojson.Unmarshal(b, &page) != nil || len(page.GetItems()) != 1 || page.GetItems()[0].GetDescription().GetStringValue() != "full description" {
			t.Fatalf("USER page=%d %s", code, b)
		}
	}
	for _, tc := range []struct {
		token string
		size  int32
		code  codes.Code
	}{{"", 0, codes.Unauthenticated}, {otherToken, 0, codes.NotFound}, {userToken, -1, codes.InvalidArgument}, {userToken, 101, codes.InvalidArgument}, {userToken, 0, codes.OK}, {pendingToken, 1, codes.OK}} {
		var headers metadata.MD
		_, e := client.ListApplicationProfileRevisions(metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, tc.token)), &pv.ListApplicationProfileRevisionsRequest{ApplicationId: appID, PageSize: tc.size}, grpc.Header(&headers))
		if status.Code(e) != tc.code {
			t.Fatalf("gRPC=%v want %v", e, tc.code)
		}
		if len(headers.Get("cache-control")) != 1 || headers.Get("cache-control")[0] != "private, no-store" {
			t.Fatal("gRPC cache", headers)
		}
	}
	httpClient, e := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if e != nil {
		t.Fatal(e)
	}
	defer httpClient.Close()
	generated := pv.NewApplicationProfileRevisionHTTPClient(httpClient)
	headers := http.Header{}
	headers.Set(transport.IdentityHeader, userToken)
	filter := "DRAFT"
	page, e := generated.ListApplicationProfileRevisions(ctx, &pv.ListApplicationProfileRevisionsRequest{ApplicationId: appID, ReviewStatus: &filter, PageSize: 1}, khttp.Header(&headers))
	if e != nil || len(page.GetItems()) != 1 {
		t.Fatalf("generated client=%v %v", page, e)
	}
	headers = http.Header{transport.IdentityHeader: []string{userToken}}
	detail, e := generated.GetApplicationProfileRevision(ctx, &pv.GetApplicationProfileRevisionRequest{ApplicationId: appID, ProfileRevisionId: draft.GetProfileRevisionId(), ReviewPageSize: 1}, khttp.Header(&headers))
	if e != nil || detail.GetCreatedBy() != "profile-admin" || detail.GetLatestReview() != nil || len(detail.GetReviews()) != 0 {
		t.Fatalf("detail=%v %v", detail, e)
	}
	// Produce a terminal rejected revision through the existing command repository.
	id, _ := pd.ParseApplicationProfileRevisionID(draft.GetProfileRevisionId())
	aid, _ := shared.ParseApplicationID(appID)
	repo := ma.NewApplicationProfileRevisionRepository(db)
	reviewID := pd.ApplicationProfileReviewID("01995000-0000-7000-8000-000000000999")
	sub, e := repo.SubmitDraft(ctx, aid, id, "profile-admin", 1, reviewID, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	reason := "administrator may read this complete reason"
	_, e = repo.DecideReview(ctx, pp.ProfileReviewDecisionInput{ApplicationID: aid, ProfileRevisionID: id, ProfileReviewID: reviewID, ReviewerID: "private-reviewer-identity", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: sub.ProfileRevision.Revision(), PolicyVersion: pd.InitialProfileReviewPolicyVersion, Outcome: "REJECT", Reason: &reason, DecidedAt: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	code, _, b := get(userToken, "/"+id.String(), "")
	var terminal pv.OwnedApplicationProfileRevisionDetail
	if code != 200 || protojson.Unmarshal(b, &terminal) != nil || terminal.GetLatestReview().GetDecision().GetReason().GetStringValue() != reason || !proto.Equal(terminal.GetLatestReview(), terminal.GetReviews()[0]) {
		t.Fatalf("terminal=%d %s", code, b)
	}
	for _, forbidden := range []string{"private-reviewer-identity", "updatedBy", "submittedBy", "decidedBy", "confirmedCheckIds", "snapshot", "requiredCheckIds", "decisionEligibility"} {
		if bytes.Contains(b, []byte(forbidden)) {
			t.Fatal("private field", forbidden)
		}
	}
	var revisionSource, reviewSource bson.M
	if e = db.Collection("application_profile_revisions").FindOne(ctx, bson.M{"profileRevisionId": id.String()}).Decode(&revisionSource); e != nil {
		t.Fatal(e)
	}
	if e = db.Collection("application_profile_reviews").FindOne(ctx, bson.M{"profileReviewId": reviewID.String()}).Decode(&reviewSource); e != nil {
		t.Fatal(e)
	}
	for i, newID := range []string{"01995000-0000-7000-8000-000000000991", "01995000-0000-7000-8000-000000000992"} {
		clone := bson.M{}
		for k, v := range revisionSource {
			if k != "_id" {
				clone[k] = v
			}
		}
		clone["profileRevisionId"] = newID
		clone["sequence"] = int32(i + 2)
		if _, e = db.Collection("application_profile_revisions").InsertOne(ctx, clone); e != nil {
			t.Fatal(e)
		}
		history := bson.M{}
		for k, v := range reviewSource {
			if k != "_id" {
				history[k] = v
			}
		}
		history["profileRevisionId"] = newID
		history["profileReviewId"] = strings.Replace(newID, "00000000099", "00000000098", 1)
		if _, e = db.Collection("application_profile_reviews").InsertOne(ctx, history); e != nil {
			t.Fatal(e)
		}
	}
	largePageText := strings.Repeat("\"", 700000)
	if _, e = db.Collection("application_profile_revisions").UpdateMany(ctx, bson.M{"applicationId": appID}, bson.M{"$set": bson.M{"description": largePageText}}, options.UpdateMany().SetBypassDocumentValidation(true)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Collection("application_profile_reviews").UpdateMany(ctx, bson.M{"applicationId": appID}, bson.M{"$set": bson.M{"snapshot.description": largePageText}}, options.UpdateMany().SetBypassDocumentValidation(true)); e != nil {
		t.Fatal(e)
	}
	for _, protocol := range []string{"HTTP", "gRPC"} {
		token := ""
		for sequence := int32(3); sequence >= 1; sequence-- {
			var page *pv.OwnedApplicationProfileRevisionPage
			if protocol == "HTTP" {
				code, _, b := get(userToken, "?pageSize=3&pageToken="+token, "")
				page = &pv.OwnedApplicationProfileRevisionPage{}
				if code != 200 || len(b) > pd.ProfileManagementResponseBudget || protojson.Unmarshal(b, page) != nil {
					t.Fatalf("bounded HTTP=%d bytes=%d", code, len(b))
				}
			} else {
				page, e = client.ListApplicationProfileRevisions(adminCtx, &pv.ListApplicationProfileRevisionsRequest{ApplicationId: appID, PageSize: 3, PageToken: token})
				if e != nil {
					t.Fatal(e)
				}
			}
			if len(page.Items) != 1 || page.Items[0].Sequence != sequence || page.Items[0].Description.GetStringValue() != largePageText || proto.Size(page) > pd.ProfileManagementResponseBudget {
				t.Fatalf("budget skips %s: sequence=%d count=%d", protocol, sequence, len(page.Items))
			}
			token = page.NextPageToken.GetStringValue()
		}
		if token != "" {
			t.Fatal("budget loop", protocol)
		}
	}
	// JSON escaping and repeated history/latest fields count toward the same
	// actual wire budget. Preserve oversized historical source content verbatim.
	long := strings.Repeat("\"\\\n", pd.ProfileManagementResponseBudget/3)
	for _, change := range []struct {
		collection string
		set        bson.M
	}{{"application_profile_revisions", bson.M{"description": long}}, {"application_profile_reviews", bson.M{"snapshot.description": long}}} {
		if _, e = db.Collection(change.collection).UpdateMany(ctx, bson.M{"applicationId": appID}, bson.M{"$set": change.set}, options.UpdateMany().SetBypassDocumentValidation(true)); e != nil {
			t.Fatal(e)
		}
	}
	for _, suffix := range []string{"", "/" + id.String()} {
		code, _, b = get(userToken, suffix, "")
		if code != 413 || !bytes.Contains(b, []byte(transport.ReasonProfileManagementResponseTooLarge)) {
			t.Fatalf("oversize %s=%d %s", suffix, code, b)
		}
	}
	if _, e = client.GetApplicationProfileRevision(adminCtx, &pv.GetApplicationProfileRevisionRequest{ApplicationId: appID, ProfileRevisionId: id.String()}); status.Code(e) != codes.ResourceExhausted {
		t.Fatal("native oversize", e)
	}
	// A malformed necessary review never yields a partial oversized response.
	_, e = db.Collection("application_profile_reviews").UpdateOne(ctx, bson.M{"profileReviewId": reviewID.String()}, bson.M{"$set": bson.M{"sourceRevision": int64(7)}})
	if e != nil {
		t.Fatal(e)
	}
	code, _, b = get(userToken, "", "")
	if code != 500 || !bytes.Contains(b, []byte(transport.ReasonProfileManagementStateInconsistent)) {
		t.Fatalf("corruption=%d %s", code, b)
	}
}
