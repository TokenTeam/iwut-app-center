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

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_review"
	profilev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_profile_revision"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/structpb"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP016_BR_PRF_023_032_ProfileDecision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	adminToken := e2eSignIdentity(t, key, "profile-admin", "APPROVED")
	reviewerToken := e2eSignReviewerIdentity(t, key, "profile-reviewer", "app.profile.review")
	versionReviewerToken := e2eSignReviewerIdentity(t, key, "version-reviewer", "app.version.review")
	conflictToken := e2eSignReviewerIdentity(t, key, "profile-admin", "app.profile.review")
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: database.Name(), config.HTTPAddrEnv: httpAddress, config.GRPCAddrEnv: grpcAddress, config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + keyPath, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget}))
	if err != nil {
		t.Fatal(err)
	}
	resolver := &catalogE2EDNSResolver{}
	app, cleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatal(err)
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
	client := profilev1.NewApplicationProfileRevisionClient(connection)
	adminCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	createApp := func(name string) string {
		t.Helper()
		code, body := e2eHTTPCreate(t, httpAddress, adminToken, name)
		var app applicationv1.CreateApplicationResponse
		if code != 201 || protojson.Unmarshal(body, &app) != nil {
			t.Fatalf("create app: %d %s", code, body)
		}
		return app.GetId()
	}
	reviewClient := reviewv1.NewApplicationProfileReviewClient(connection)
	reviewerCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reviewerToken))
	makeSubmission := func(appID, name string) *reviewv1.SubmitApplicationProfileRevisionReviewResponse {
		t.Helper()
		draft, err := client.CreateApplicationProfileRevision(adminCtx, &profilev1.CreateApplicationProfileRevisionRequest{ApplicationId: appID, Profile: &profilev1.ApplicationProfileContent{DisplayName: name, Description: structpb.NewNullValue(), Icon: structpb.NewNullValue()}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := reviewClient.SubmitApplicationProfileRevisionReview(adminCtx, &reviewv1.SubmitApplicationProfileRevisionReviewRequest{ApplicationId: appID, ProfileRevisionId: draft.ProfileRevisionId, Command: &reviewv1.SubmitApplicationProfileRevisionReviewCommand{ExpectedRevision: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	appID := createApp("Profile_Decision")
	submitted := makeSubmission(appID, "Published")
	request := &reviewv1.DecideApplicationProfileRevisionReviewRequest{ApplicationId: appID, ProfileRevisionId: submitted.ProfileRevision.ProfileRevisionId, ProfileReviewId: submitted.Review.ProfileReviewId, Command: &reviewv1.DecideApplicationProfileRevisionReviewCommand{ExpectedProfileRevisionRevision: 2, ExpectedCurrentPublishedProfileRevisionId: structpb.NewNullValue(), ExpectedPolicyVersion: "app-profile-review-v1", Outcome: reviewv1.ProfileReviewDecisionAction_APPROVE, ConfirmedCheckIds: []string{"icon-content-reviewed", "content-policy-reviewed"}}}
	commandJSON := `{"expectedProfileRevisionRevision":"2","expectedCurrentPublishedProfileRevisionId":null,"expectedPolicyVersion":"app-profile-review-v1","outcome":"APPROVE","confirmedCheckIds":["icon-content-reviewed","content-policy-reviewed"]}`
	decideHTTP := func(token, query, payload string) (int, string, []byte) {
		t.Helper()
		r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+httpAddress+"/v1/applications/"+appID+"/profile-revisions/"+request.ProfileRevisionId+"/reviews/"+request.ProfileReviewId+"/decision"+query, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		if token != "" {
			r.Header.Set(transport.IdentityHeader, token)
		}
		r.Header.Set("If-Match", `"999"`)
		res, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, res.Header.Get("ETag"), body
	}
	for _, tc := range []struct {
		token, query, payload, reason string
		code                          int
	}{
		{"", "?private", "{private", transport.ReasonReviewerIdentityRequired, 401},
		{"private", "", "{private", transport.ReasonInvalidReviewerIdentity, 401},
		{adminToken, "", commandJSON, transport.ReasonApplicationProfileReviewPermissionRequired, 403},
		{versionReviewerToken, "", commandJSON, transport.ReasonApplicationProfileReviewPermissionRequired, 403},
		{conflictToken, "", commandJSON, transport.ReasonApplicationProfileReviewConflictOfInterest, 403},
		{reviewerToken, "?outcome=APPROVE", commandJSON, transport.ReasonInvalidApplicationProfileReviewDecision, 400},
		{reviewerToken, "", strings.Replace(commandJSON, `"outcome":"APPROVE"`, `"outcome":"APPROVE","decidedBy":"private"`, 1), transport.ReasonInvalidApplicationProfileReviewDecision, 400},
		{reviewerToken, "", strings.Replace(commandJSON, `"expectedProfileRevisionRevision":"2"`, `"expectedProfileRevisionRevision":"1"`, 1), transport.ReasonApplicationProfileRevisionConflict, 409},
		{reviewerToken, "", strings.Replace(commandJSON, `"expectedCurrentPublishedProfileRevisionId":null,`, "", 1), transport.ReasonInvalidApplicationProfileReviewDecision, 400},
		{reviewerToken, "", strings.Replace(commandJSON, `"expectedCurrentPublishedProfileRevisionId":null`, `"expectedCurrentPublishedProfileRevisionId":"01995000-0000-7000-8000-000000000999"`, 1), transport.ReasonApplicationProfilePublicationConflict, 409},
		{reviewerToken, "", strings.Replace(commandJSON, `"app-profile-review-v1"`, `"unknown"`, 1), transport.ReasonProfileReviewPolicyUnavailable, 409},
		{reviewerToken, "", strings.Replace(commandJSON, `["icon-content-reviewed","content-policy-reviewed"]`, `["content-policy-reviewed"]`, 1), transport.ReasonProfileReviewChecksIncomplete, 400},
	} {
		code, _, body := decideHTTP(tc.token, tc.query, tc.payload)
		if code != tc.code || !bytes.Contains(body, []byte(tc.reason)) || bytes.Contains(body, []byte("private")) {
			t.Fatalf("want%d/%s got%d %s", tc.code, tc.reason, code, body)
		}
	}
	// Native gRPC enforces the same independent Reviewer capability and pointer presence.
	if _, err = reviewClient.DecideApplicationProfileRevisionReview(ctx, request); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	if _, err = reviewClient.DecideApplicationProfileRevisionReview(metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, versionReviewerToken)), request); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
	// The actual generated client must bind all three path IDs and encode only command.
	httpClient, err := khttp.NewClient(ctx, khttp.WithEndpoint("http://"+httpAddress))
	if err != nil {
		t.Fatal(err)
	}
	defer httpClient.Close()
	generated := reviewv1.NewApplicationProfileReviewHTTPClient(httpClient)
	headers := http.Header{transport.IdentityHeader: []string{reviewerToken}, "If-Match": []string{`"999"`}}
	approved, err := generated.DecideApplicationProfileRevisionReview(ctx, request, khttp.Header(&headers))
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get("ETag") != `"3"` || approved.ProfileRevision.ReviewStatus != "APPROVED" || approved.ProfileRevision.Revision != 3 || approved.Review.Status != "APPROVED" || approved.CurrentPublishedProfileRevisionId.GetStringValue() != request.ProfileRevisionId || approved.ProfileRevision.UpdatedBy != "profile-reviewer" || approved.ProfileRevision.CreatedBy != "profile-admin" || approved.Review.SubmittedBy != "profile-admin" {
		t.Fatalf("approved %v etag %s", approved, headers.Get("ETag"))
	}
	decision := approved.Review.Decision.GetStructValue().Fields
	if decision["outcome"].GetStringValue() != "APPROVED" || decision["policyVersion"].GetStringValue() != "app-profile-review-v1" || decision["confirmedCheckIds"].GetListValue().Values[0].GetStringValue() != "content-policy-reviewed" || decision["reason"].GetKind() == nil || decision["decidedBy"].GetStringValue() != "profile-reviewer" {
		t.Fatal(decision)
	}
	if _, err = reviewClient.DecideApplicationProfileRevisionReview(reviewerCtx, request); status.Code(err) != codes.Aborted {
		t.Fatalf("repeat=%v", err)
	}
	// REJECT uses no publication precondition and retains prior publication. Raw reason is immutable.
	second := makeSubmission(appID, "Rejected")
	reason := "Cafe\u0301 policy mismatch"
	request.ProfileRevisionId = second.ProfileRevision.ProfileRevisionId
	request.ProfileReviewId = second.Review.ProfileReviewId
	request.Command = &reviewv1.DecideApplicationProfileRevisionReviewCommand{ExpectedProfileRevisionRevision: 2, ExpectedPolicyVersion: "app-profile-review-v1", Outcome: reviewv1.ProfileReviewDecisionAction_REJECT, Reason: &reason}
	rejected, err := reviewClient.DecideApplicationProfileRevisionReview(reviewerCtx, request)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.ProfileRevision.ReviewStatus != "REJECTED" || rejected.ProfileRevision.Revision != 3 || rejected.Review.Decision.GetStructValue().Fields["reason"].GetStringValue() != reason || rejected.CurrentPublishedProfileRevisionId.GetStringValue() != approved.ProfileRevision.ProfileRevisionId {
		t.Fatal(rejected)
	}
	// A later draft has a fresh identity/sequence and cannot mutate terminal history.
	third := makeSubmission(appID, "Next")
	if third.ProfileRevision.Sequence != 3 || third.ProfileRevision.ProfileRevisionId == rejected.ProfileRevision.ProfileRevisionId {
		t.Fatal(third)
	}
	// An explicit string publication precondition can replace the old approved record.
	request.ProfileRevisionId = third.ProfileRevision.ProfileRevisionId
	request.ProfileReviewId = third.Review.ProfileReviewId
	request.Command = &reviewv1.DecideApplicationProfileRevisionReviewCommand{ExpectedProfileRevisionRevision: 2, ExpectedCurrentPublishedProfileRevisionId: structpb.NewStringValue(approved.ProfileRevision.ProfileRevisionId), ExpectedPolicyVersion: "app-profile-review-v1", Outcome: reviewv1.ProfileReviewDecisionAction_APPROVE, ConfirmedCheckIds: []string{"content-policy-reviewed", "icon-content-reviewed"}}
	next, err := reviewClient.DecideApplicationProfileRevisionReview(reviewerCtx, request)
	if err != nil || next.CurrentPublishedProfileRevisionId.GetStringValue() != third.ProfileRevision.ProfileRevisionId {
		t.Fatalf("replacement %v %v", next, err)
	}
	// Both real protocols expose the same finalized history while projections remain local.
	var projection bson.M
	if err := database.Collection("application_profiles").FindOne(ctx, bson.M{"applicationId": appID}).Decode(&projection); err != nil {
		t.Fatal(err)
	}
	if projection["workingProfileRevisionId"] != nil || projection["currentPublishedProfileRevisionId"] != third.ProfileRevision.ProfileRevisionId {
		t.Fatal(projection)
	}
	if count, err := database.Collection("application_publications").CountDocuments(ctx, bson.M{"applicationId": appID}); err != nil || count != 0 {
		t.Fatalf("publication side effect %d %v", count, err)
	}
	if resolver.calls.Load() != 0 {
		t.Fatal("unexpected external inspection")
	}
}
