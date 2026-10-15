package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"testing"
	"time"

	av "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	rv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review"
	rq "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_review_query"
	vv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	rp "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/reviewer_permission"
	sp "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/system_principal"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"iwut-app-center/internal/config"
)

type versionReviewGatewayFixture struct {
	t                            *testing.T
	ctx                          context.Context
	db, authDB                   *m.Database
	owner, session, otherSession string
	appID                        string
	edge                         *grpc.ClientConn
	client                       vv.ApplicationVersionClient
	resolver                     *catalogE2EDNSResolver
}

func newVersionReviewGatewayFixture(t *testing.T) *versionReviewGatewayFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	t.Cleanup(cancel)
	mongo, db := e2eIsolatedDatabase(t, ctx, true)
	f := &versionReviewGatewayFixture{t: t, ctx: ctx, db: db, authDB: mongo.Database(os.Getenv("AUTH_CENTER_INTEGRATION_DATABASE")), owner: "version-review-owner"}
	f.session = uc002SeedSession(t, f.authDB, f.owner, "APPROVED")
	f.otherSession = uc002SeedSession(t, f.authDB, "version-review-other", "APPROVED")
	conf, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(),
		config.HTTPAddrEnv: os.Getenv("UC002_APP_HTTP_ADDR"), config.GRPCAddrEnv: os.Getenv("UC002_APP_GRPC_ADDR"),
		config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience,
		config.IdentityPublicKeysEnv:     "uc002-auth=" + os.Getenv("AUTH_CENTER_IDENTITY_PUBLIC_KEY_PATH"),
		config.AuthScopeCatalogTargetEnv: os.Getenv("AUTH_CENTER_INTEGRATION_TARGET"), config.ScopeCatalogCacheTTLEnv: "1ns",
		config.ServiceIdentityIDEnv: os.Getenv(config.ServiceIdentityIDEnv), config.ServiceIdentityKIDEnv: os.Getenv(config.ServiceIdentityKIDEnv),
		config.ServiceIdentityPrivateKeyEnv: os.Getenv(config.ServiceIdentityPrivateKeyEnv),
	}))
	if err != nil {
		t.Fatal(err)
	}
	f.resolver = &catalogE2EDNSResolver{base: e2eDNSResolver{addresses: map[string][]netip.Addr{"apps.whut.edu.cn": {netip.MustParseAddr("8.8.8.8")}}}}
	app, cleanup, err := wireAppWithResolver(conf, f.resolver)
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
			t.Fatal("App stop timeout")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, conf.HTTPAddr, done, &runErr)
	code, _, body := uc002HTTPRequest(t, ctx, "/app-center/v1/applications", f.session, []byte(`{"name":"Version_Review_Integration"}`), nil)
	var created av.CreateApplicationResponse
	if code != 201 || protojson.Unmarshal(body, &created) != nil {
		t.Fatalf("create Application=%d", code)
	}
	f.appID = created.Id
	f.edge, err = grpc.NewClient(os.Getenv("UC002_EDGE_GRPC_ADDR"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.edge.Close() })
	f.client = vv.NewApplicationVersionClient(f.edge)
	return f
}
func (f *versionReviewGatewayFixture) sessionContext() context.Context {
	return metadata.NewOutgoingContext(f.ctx, metadata.Pairs("x-iwut-session", f.session))
}
func (f *versionReviewGatewayFixture) create(label string) *vv.CreateApplicationVersionResponse {
	f.t.Helper()
	r, err := f.client.CreateApplicationVersion(f.sessionContext(), uc002VersionRequest(f.appID, label, "items.read"))
	if err != nil {
		f.t.Fatal("create Version", status.Code(err))
	}
	return r
}
func (f *versionReviewGatewayFixture) replacement(label string) *vv.DraftApplicationVersionReplacement {
	return &vv.DraftApplicationVersionReplacement{VersionLabel: label, LaunchUrl: "https://apps.whut.edu.cn/edited", RpcApiMinVersion: 1, RpcApiMaxVersionExclusive: 3, RequiredCapabilities: []string{"camera.read.v1"}, RequiredScopes: []string{"items.write"}, OptionalScopes: []string{"items.read"}, OauthRedirects: &vv.OAuthRedirectConfiguration{PkceRedirectUris: []string{"https://apps.whut.edu.cn/oauth/b", "https://apps.whut.edu.cn/oauth/a"}, ConfidentialRedirectUris: []string{"https://apps.whut.edu.cn/confidential"}}}
}
func (f *versionReviewGatewayFixture) updateHTTP(id string, revision int64, replacement *vv.DraftApplicationVersionReplacement, session string) (int, http.Header, []byte) {
	f.t.Helper()
	body, err := protojson.Marshal(replacement)
	if err != nil {
		f.t.Fatal(err)
	}
	return gatewayHTTP(f.t, f.ctx, http.MethodPut, "/app-center/v1/applications/"+f.appID+"/versions/"+id, session, body, http.Header{"If-Match": []string{fmt.Sprintf(`"%d"`, revision)}})
}
func (f *versionReviewGatewayFixture) unchanged(before map[string][]bson.M) {
	f.t.Helper()
	if !reflect.DeepEqual(before, uc002AppFacts(f.t, f.ctx, f.db, f.appID)) {
		f.t.Fatal("failed command left partial business state")
	}
}
func (f *versionReviewGatewayFixture) scope(name string, enabled bool) int64 {
	f.t.Helper()
	var catalog struct {
		Data struct {
			Revision int64 `bson:"revision"`
		} `bson:"data"`
	}
	coll := f.authDB.Collection("auth_oauth_catalog")
	if err := coll.FindOne(f.ctx, bson.M{"_id": "current"}).Decode(&catalog); err != nil {
		f.t.Fatal(err)
	}
	result, err := coll.UpdateOne(f.ctx, bson.M{"_id": "current", "data.scopes.name": name}, bson.M{"$set": bson.M{"data.scopes.$.enabled": enabled, "data.generatedat": time.Now().UTC().Truncate(time.Millisecond)}, "$inc": bson.M{"data.revision": int64(1)}})
	if err != nil || result.MatchedCount != 1 {
		f.t.Fatal("update isolated Auth Catalog fixture failed")
	}
	return catalog.Data.Revision + 1
}

// Reject dependent writes to exercise the real transaction abort after Version
// mutation. Restore the exact original validator before subsequent commands.
func (f *versionReviewGatewayFixture) rejectWrites(collection string) func() {
	f.t.Helper()
	specs, err := f.db.ListCollectionSpecifications(f.ctx, bson.M{"name": collection})
	if err != nil || len(specs) != 1 {
		f.t.Fatal("read original fixture validator failed")
	}
	var options struct {
		Validator bson.Raw `bson:"validator"`
	}
	if err = bson.Unmarshal(specs[0].Options, &options); err != nil {
		f.t.Fatal(err)
	}
	if err = f.db.RunCommand(f.ctx, bson.D{{Key: "collMod", Value: collection}, {Key: "validator", Value: bson.M{"$expr": bson.M{"$eq": bson.A{1, 0}}}}}).Err(); err != nil {
		f.t.Fatal(err)
	}
	restored := false
	restore := func() {
		f.t.Helper()
		if restored {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := f.db.RunCommand(ctx, bson.D{{Key: "collMod", Value: collection}, {Key: "validator", Value: options.Validator}}).Err(); err != nil {
			f.t.Fatal("restore fixture validator failed")
		}
		restored = true
	}
	f.t.Cleanup(restore)
	return restore
}

func TestE2E_UCAPP003_004_005_BR_VER_010_017_BR_REV_001_020_GatewayAuthIntegration(t *testing.T) {
	if os.Getenv("UC002_EDGE_HTTP_ADDR") == "" {
		t.Skip("run scripts/test-uc002-gateway-auth-integration.sh")
	}
	f := newVersionReviewGatewayFixture(t)
	draft := f.create("draft")
	var createdAudit struct {
		CreatedAt time.Time `bson:"createdAt"`
	}
	if err := f.db.Collection("application_versions").FindOne(f.ctx, bson.M{"versionId": draft.VersionId}).Decode(&createdAudit); err != nil {
		t.Fatal(err)
	}
	f.create("label-conflict")
	replacement := f.replacement("edited")
	code, h, body := f.updateHTTP(draft.VersionId, 1, replacement, f.session)
	var edited vv.UpdateApplicationVersionResponse
	if code != 200 || h.Get("ETag") != `"2"` || protojson.Unmarshal(body, &edited) != nil {
		t.Fatalf("BR-VER-011 HTTP edit=%d", code)
	}
	if edited.Revision != 2 || edited.VersionId != draft.VersionId || edited.Sequence != draft.Sequence || edited.CreatedBy != draft.CreatedBy || !edited.CreatedAt.AsTime().Equal(createdAudit.CreatedAt) || edited.UpdatedBy != f.owner || edited.ReviewStatus != "DRAFT" || !reflect.DeepEqual(edited.RequiredScopes, []string{"items.write"}) || !reflect.DeepEqual(edited.OptionalScopes, []string{"items.read"}) || !reflect.DeepEqual(edited.OauthRedirects.PkceRedirectUris, []string{"https://apps.whut.edu.cn/oauth/a", "https://apps.whut.edu.cn/oauth/b"}) {
		t.Fatalf("BR-VER-011/012/014 replacement: revision=%d id=%t sequence=%t creator=%t createdAt=%t actor=%t draft=%t required=%t optional=%t redirects=%t", edited.Revision, edited.VersionId == draft.VersionId, edited.Sequence == draft.Sequence, edited.CreatedBy == draft.CreatedBy, edited.CreatedAt.AsTime().Equal(createdAudit.CreatedAt), edited.UpdatedBy == f.owner, edited.ReviewStatus == "DRAFT", reflect.DeepEqual(edited.RequiredScopes, []string{"items.write"}), reflect.DeepEqual(edited.OptionalScopes, []string{"items.read"}), reflect.DeepEqual(edited.OauthRedirects.PkceRedirectUris, []string{"https://apps.whut.edu.cn/oauth/a", "https://apps.whut.edu.cn/oauth/b"}))
	}
	var storedOAuth struct {
		OAuth struct {
			PKCE         []string `bson:"pkceRedirectUris"`
			Confidential []string `bson:"confidentialRedirectUris"`
		} `bson:"oauthRedirects"`
	}
	if err := f.db.Collection("application_version_oauth_configs").FindOne(f.ctx, bson.M{"applicationVersionId": draft.VersionId}).Decode(&storedOAuth); err != nil || !reflect.DeepEqual(storedOAuth.OAuth.PKCE, edited.OauthRedirects.PkceRedirectUris) || !reflect.DeepEqual(storedOAuth.OAuth.Confidential, edited.OauthRedirects.ConfidentialRedirectUris) {
		t.Fatal("BR-VER-018 dependent OAuth config not synchronized")
	}
	before := uc002AppFacts(t, f.ctx, f.db, f.appID)
	// A differently ordered full replacement is a true no-op through native gRPC.
	noop, err := f.client.UpdateApplicationVersion(f.sessionContext(), &vv.UpdateApplicationVersionRequest{ApplicationId: f.appID, VersionId: draft.VersionId, ExpectedRevision: 2, Replacement: replacement})
	if err != nil || !proto.Equal(noop, &edited) {
		t.Fatal("BR-VER-013 native no-op changed revision or audit")
	}
	// A successful no-op still acquires the adapter-only ownership fence.
	afterNoop := uc002AppFacts(t, f.ctx, f.db, f.appID)
	before["applications"][0]["coordinationRevision"] = afterNoop["applications"][0]["coordinationRevision"]
	f.unchanged(before)
	for _, tc := range []struct {
		name        string
		revision    int64
		session     string
		replacement *vv.DraftApplicationVersionReplacement
		want        int
	}{
		{"stale", 1, f.session, replacement, 412},
		{"other-admin", 2, f.otherSession, replacement, 403},
		{"label-conflict", 2, f.session, f.replacement("label-conflict"), 409},
	} {
		code, _, _ := f.updateHTTP(draft.VersionId, tc.revision, tc.replacement, tc.session)
		if code != tc.want {
			t.Fatalf("BR-VER-013/016 %s=%d", tc.name, code)
		}
		f.unchanged(before)
	}
	f.scope("items.write", false)
	code, _, _ = f.updateHTTP(draft.VersionId, 2, replacement, f.session)
	if code != 400 {
		t.Fatalf("BR-VER-015 disabled Scope edit=%d", code)
	}
	f.unchanged(before)
	f.scope("items.write", true)
	// Removing the real provider document must fail closed without any write.
	var catalog bson.Raw
	coll := f.authDB.Collection("auth_oauth_catalog")
	if err := coll.FindOne(f.ctx, bson.M{"_id": "current"}).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.DeleteOne(f.ctx, bson.M{"_id": "current"}); err != nil {
		t.Fatal(err)
	}
	code, _, _ = f.updateHTTP(draft.VersionId, 2, replacement, f.session)
	if code != 503 {
		t.Fatalf("BR-VER-015 Catalog outage edit=%d", code)
	}
	f.unchanged(before)
	if _, err := coll.InsertOne(f.ctx, catalog); err != nil {
		t.Fatal(err)
	}
	rollback := f.replacement("rollback")
	restore := f.rejectWrites("application_version_oauth_configs")
	code, _, _ = f.updateHTTP(draft.VersionId, 2, rollback, f.session)
	if code != 500 {
		t.Fatalf("BR-VER-016 forced OAuth transaction failure=%d", code)
	}
	restore()
	f.unchanged(before)
	// Same-revision writers through gRPC-Web and native gRPC: exactly one commits.
	start := make(chan struct{})
	results := make(chan bool, 2)
	go func() {
		<-start
		_, err := f.client.UpdateApplicationVersion(f.sessionContext(), &vv.UpdateApplicationVersionRequest{ApplicationId: f.appID, VersionId: draft.VersionId, ExpectedRevision: 2, Replacement: f.replacement("race-native")})
		if err != nil && status.Code(err) != codes.Aborted {
			t.Errorf("native OCC status=%s", status.Code(err))
		}
		results <- err == nil
	}()
	// Use gRPC-Web for the competing transport; helpers are goroutine-safe except
	// Fatal, so perform it on the test goroutine.
	close(start)
	web := &vv.UpdateApplicationVersionResponse{}
	webCode := gatewayWeb(t, f.ctx, f.session, vv.ApplicationVersion_UpdateApplicationVersion_FullMethodName, &vv.UpdateApplicationVersionRequest{ApplicationId: f.appID, VersionId: draft.VersionId, ExpectedRevision: 2, Replacement: f.replacement("race-web")}, web)
	if webCode != 0 && webCode != int(codes.Aborted) {
		t.Fatalf("gRPC-Web OCC=%d", webCode)
	}
	results <- webCode == 0
	successes := 0
	for range 2 {
		if <-results {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("BR-VER-013 concurrent edits did not produce one winner")
	}
	if f.resolver.calls.Load() != 0 {
		t.Fatal("draft editing performed network preflight")
	}
	t.Log("UC003 BR-VER-010–017/018: real Session/Scope edit, HTTP/native/gRPC-Web, no-op/OCC, OAuth sync and transaction rollback passed")

	// UC004 consumes the Version actually committed by the preceding OCC race.
	current, err := f.client.GetApplicationVersion(f.sessionContext(), &vv.GetApplicationVersionRequest{ApplicationId: f.appID, VersionId: draft.VersionId})
	if err != nil || current.GetVersion().GetRevision() != 3 {
		t.Fatal("read edited Version before submission failed")
	}
	before = uc002AppFacts(t, f.ctx, f.db, f.appID)
	for _, tc := range []struct {
		session  string
		revision int64
		want     int
	}{{"", 3, 401}, {f.otherSession, 3, 403}, {f.session, 2, 409}} {
		code, _, _ = f.submitHTTP(draft.VersionId, tc.revision, tc.session)
		if code != tc.want {
			t.Fatalf("BR-REV-001/003 rejected submission=%d want=%d", code, tc.want)
		}
		f.unchanged(before)
	}
	f.scope("items.write", false)
	code, _, _ = f.submitHTTP(draft.VersionId, 3, f.session)
	if code != 422 {
		t.Fatalf("BR-REV-006 disabled Scope submission=%d", code)
	}
	f.unchanged(before)
	submissionCatalogRevision := f.scope("items.write", true)
	if err := coll.FindOne(f.ctx, bson.M{"_id": "current"}).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.DeleteOne(f.ctx, bson.M{"_id": "current"}); err != nil {
		t.Fatal(err)
	}
	code, _, _ = f.submitHTTP(draft.VersionId, 3, f.session)
	if code != 503 {
		t.Fatalf("BR-REV-006 Catalog outage submission=%d", code)
	}
	f.unchanged(before)
	if _, err := coll.InsertOne(f.ctx, catalog); err != nil {
		t.Fatal(err)
	}
	restore = f.rejectWrites("application_reviews")
	code, _, _ = f.submitHTTP(draft.VersionId, 3, f.session)
	if code != 500 {
		t.Fatalf("BR-REV-009 forced Review insertion failure=%d", code)
	}
	restore()
	f.unchanged(before)
	code, h, body = f.submitHTTP(draft.VersionId, 3, f.session)
	var submitted rv.SubmitApplicationVersionReviewResponse
	if code != 201 || protojson.Unmarshal(body, &submitted) != nil || h.Get("Location") == "" {
		t.Fatalf("edited Version submit=%d", code)
	}
	review := submitted.GetReview()
	snapshot := review.GetSnapshot()
	v := current.GetVersion()
	if review.GetStatus() != "PENDING" || review.GetSourceVersionRevision() != 3 || review.GetAttempt() != 1 || review.GetScopeCatalogRevision() != submissionCatalogRevision || review.GetPreflightPolicyVersion() != "submit-v1" || review.GetSubmittedBy() != f.owner || review.GetDecision() != nil || review.GetDraftRestoration() != nil || submitted.GetVersion().GetRevision() != 4 || submitted.GetVersion().GetReviewStatus() != "SUBMITTED" || snapshot.GetVersionLabel() != v.GetVersionLabel() || snapshot.GetLaunchUrl() != v.GetLaunchUrl() || snapshot.GetRpcApiMaxVersionExclusive() != v.GetRpcApiMaxVersionExclusive() || !reflect.DeepEqual(snapshot.GetRequiredScopes(), v.GetRequiredScopes()) || !reflect.DeepEqual(snapshot.GetOptionalScopes(), v.GetOptionalScopes()) || !proto.Equal(snapshot.GetOauthRedirects(), v.GetOauthRedirects()) {
		t.Fatal("BR-REV-003–006 immutable edited snapshot or audit mismatch")
	}
	afterSubmission := uc002AppFacts(t, f.ctx, f.db, f.appID)
	code, _, _ = f.submitHTTP(draft.VersionId, 4, f.session)
	if code != 409 {
		t.Fatalf("BR-REV-002 duplicate submission=%d", code)
	}
	code, _, _ = f.updateHTTP(draft.VersionId, 4, f.replacement("too-late"), f.session)
	if code != 409 {
		t.Fatalf("BR-VER-010 edit after submission=%d", code)
	}
	f.unchanged(afterSubmission)
	reviewClient := rv.NewApplicationReviewClient(f.edge)
	nativeDraft := f.create("native-review")
	nativeReview, err := reviewClient.SubmitApplicationVersionReview(f.sessionContext(), f.submission(nativeDraft.VersionId, 1))
	if err != nil || nativeReview.GetReview().GetStatus() != "PENDING" {
		t.Fatal("native gRPC Review submission failed", status.Code(err))
	}
	webDraft := f.create("web-review")
	webReview := &rv.SubmitApplicationVersionReviewResponse{}
	if code := gatewayWeb(t, f.ctx, f.session, rv.ApplicationReview_SubmitApplicationVersionReview_FullMethodName, f.submission(webDraft.VersionId, 1), webReview); code != 0 || webReview.GetReview().GetStatus() != "PENDING" {
		t.Fatal("gRPC-Web Review submission failed")
	}
	t.Log("UC004 BR-REV-001–009: edited immutable snapshot, real Catalog revision/disable/outage, three transports and atomic Review/Version rollback passed")
	f.verifyReviewDecisions(review, nativeReview.GetReview(), webReview.GetReview())
}

func (f *versionReviewGatewayFixture) submission(id string, revision int64) *rv.SubmitApplicationVersionReviewRequest {
	return &rv.SubmitApplicationVersionReviewRequest{ApplicationId: f.appID, VersionId: id, Command: &rv.SubmitApplicationVersionReviewCommand{ExpectedRevision: revision}}
}
func (f *versionReviewGatewayFixture) submitHTTP(id string, revision int64, session string) (int, http.Header, []byte) {
	f.t.Helper()
	body, err := protojson.Marshal(f.submission(id, revision).Command)
	if err != nil {
		f.t.Fatal(err)
	}
	return uc002HTTPRequest(f.t, f.ctx, "/app-center/v1/applications/"+f.appID+"/versions/"+id+"/reviews", session, body, nil)
}

func (f *versionReviewGatewayFixture) grant(actor, subject, permission string, action rp.ReviewerPermissionAction, revision int64) int64 {
	f.t.Helper()
	request := &rp.ManageApplicationReviewPermissionRequest{SubjectAuthId: subject, Permission: permission, Action: action, ExpectedRevision: proto.Int64(revision), Reason: "integration acceptance"}
	body, err := protojson.Marshal(request)
	if err != nil {
		f.t.Fatal(err)
	}
	code, _, body := gatewayHTTP(f.t, f.ctx, http.MethodPut, "/auth-center/v1/users/"+subject+"/application-review-permissions/"+permission, actor, body, nil)
	var response rp.ApplicationReviewPermissions
	if code != 200 || protojson.Unmarshal(body, &response) != nil || response.PermissionRevision != revision+1 {
		f.t.Fatalf("real Auth permission mutation status=%d", code)
	}
	return response.PermissionRevision
}
func (f *versionReviewGatewayFixture) decision(review *rv.ApplicationReviewResource, approve bool) *rv.DecideApplicationVersionReviewRequest {
	command := &rv.DecideApplicationVersionReviewCommand{Outcome: rv.ReviewDecisionAction_APPROVE, ExpectedPolicyVersion: "app-version-review-v2", ConfirmedCheckIds: []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed", "oauth-redirects-reviewed"}}
	if !approve {
		command.Outcome = rv.ReviewDecisionAction_REJECT
		command.ConfirmedCheckIds = []string{}
		command.Reason = proto.String("integration rejection")
	}
	return &rv.DecideApplicationVersionReviewRequest{ApplicationId: f.appID, VersionId: review.VersionId, ReviewId: review.ReviewId, Command: command}
}
func (f *versionReviewGatewayFixture) decideHTTP(review *rv.ApplicationReviewResource, session string, approve bool) (int, []byte) {
	f.t.Helper()
	request := f.decision(review, approve)
	body, err := protojson.Marshal(request.Command)
	if err != nil {
		f.t.Fatal(err)
	}
	code, _, response := uc002HTTPRequest(f.t, f.ctx, "/app-center/v1/applications/"+f.appID+"/versions/"+review.VersionId+"/reviews/"+review.ReviewId+"/decision", session, body, nil)
	return code, response
}
func (f *versionReviewGatewayFixture) verifyReviewDecisions(edited, native, web *rv.ApplicationReviewResource) {
	t := f.t
	const manager = "version-review-manager"
	managerSession := uc002SeedSession(t, f.authDB, manager, "APPROVED")
	// The pinned published Auth image uses its accepted verified-email binding.
	if _, err := f.authDB.Collection("auth_email_bindings").InsertOne(f.ctx, bson.M{"authId": manager, "email": "version-review-manager@whut.edu.cn", "revision": int64(1), "verifiedAt": time.Now().UTC().Truncate(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	// Provision only fixture authority through Auth's real explicit bootstrap CLI.
	// Reviewer grants below always use the real authenticated Auth command API.
	output, err := exec.CommandContext(f.ctx, "docker", "exec", "--env", "AUTH_EMAIL_RECOVERY_READY=true", "--env", "AUTH_EMAIL_LOGIN_ENABLED=true", os.Getenv("UC002_AUTH_CONTAINER_NAME"), "/app/auth-center", "bootstrap-platform-admin", "--auth-id", manager).CombinedOutput()
	if path := os.Getenv("UC002_BOOTSTRAP_LOG"); path != "" {
		_ = os.WriteFile(path, output, 0600)
	}
	var bootstrap struct {
		Applied bool `json:"applied"`
	}
	if err != nil || json.Unmarshal(output, &bootstrap) != nil || !bootstrap.Applied {
		t.Fatal("real Auth administrator bootstrap failed", err)
	}
	const reviewer = "version-review-reviewer"
	reviewerSession := uc002SeedSession(t, f.authDB, reviewer, "APPROVED")
	profileSession := uc002SeedSession(t, f.authDB, "version-review-profile-reviewer", "APPROVED")
	// Reviewer access must work for ordinary USERs without Developer capability.
	if _, err := f.authDB.Collection("auth_principals").UpdateMany(f.ctx, bson.M{"authId": bson.M{"$in": bson.A{reviewer, "version-review-profile-reviewer"}}}, bson.M{"$set": bson.M{"developerStatus": nil, "developerRevision": int64(0)}}); err != nil {
		t.Fatal(err)
	}
	before := uc002AppFacts(t, f.ctx, f.db, f.appID)
	for _, session := range []string{reviewerSession, managerSession} {
		if code, _ := f.decideHTTP(edited, session, true); code != 403 {
			t.Fatalf("ungranted or manager-only Reviewer status=%d", code)
		}
		f.unchanged(before)
	}
	f.grant(managerSession, reviewer, "app.version.review", rp.ReviewerPermissionAction_REVIEWER_PERMISSION_ACTION_GRANT, 1)
	f.grant(managerSession, "version-review-profile-reviewer", "app.profile.review", rp.ReviewerPermissionAction_REVIEWER_PERMISSION_ACTION_GRANT, 1)
	if code, _ := f.decideHTTP(edited, profileSession, true); code != 403 {
		t.Fatalf("profile permission authorized Version decision=%d", code)
	}
	f.unchanged(before)
	// The inverse permission boundary rejects before looking up a Profile review.
	profileID, _ := uuid.NewV7()
	profileReviewID, _ := uuid.NewV7()
	code, _, _ := uc002HTTPRequest(t, f.ctx, "/app-center/v1/applications/"+f.appID+"/profile-revisions/"+profileID.String()+"/reviews/"+profileReviewID.String()+"/decision", reviewerSession, []byte(`{"expectedProfileRevisionRevision":"1","outcome":"REJECT","expectedPolicyVersion":"app-profile-review-v1","confirmedCheckIds":[],"reason":"integration rejection"}`), nil)
	if code != 403 {
		t.Fatalf("version permission authorized Profile decision=%d", code)
	}
	f.unchanged(before)
	f.grant(managerSession, f.owner, "app.version.review", rp.ReviewerPermissionAction_REVIEWER_PERMISSION_ACTION_GRANT, 1)
	if code, _ := f.decideHTTP(edited, f.session, true); code != 403 {
		t.Fatalf("BR-REV-011 owner Reviewer bypassed conflict=%d", code)
	}
	f.unchanged(before)
	// Gateway exchanges this same Session on each request, so revocation must be
	// visible to every newly issued USER rather than merely to future logins.
	f.grant(managerSession, reviewer, "app.version.review", rp.ReviewerPermissionAction_REVIEWER_PERMISSION_ACTION_REVOKE, 2)
	if code, _ := f.decideHTTP(edited, reviewerSession, true); code != 403 {
		t.Fatalf("revoked Reviewer authorized decision=%d", code)
	}
	f.unchanged(before)
	f.grant(managerSession, reviewer, "app.version.review", rp.ReviewerPermissionAction_REVIEWER_PERMISSION_ACTION_GRANT, 3)
	// Read the actual pending attempt and active policy through the new Gateway
	// Reviewer query route before confirming the checks used in this decision.
	grantedCtx := metadata.NewOutgoingContext(f.ctx, metadata.Pairs("x-iwut-session", reviewerSession))
	detail, err := rq.NewApplicationReviewQueryServiceClient(f.edge).GetApplicationVersionReview(grantedCtx, &rq.GetApplicationVersionReviewRequest{ApplicationId: f.appID, VersionId: edited.VersionId, ReviewId: edited.ReviewId})
	checks := f.decision(edited, true).Command.ConfirmedCheckIds
	slices.Sort(checks)
	actualChecks := slices.Clone(detail.GetCurrentPolicy().GetRequiredCheckIds())
	slices.Sort(actualChecks)
	if err != nil || !detail.GetDecisionEligibility().GetEligible() || detail.GetCurrentPolicy().GetVersion() != "app-version-review-v2" || !reflect.DeepEqual(actualChecks, checks) || !proto.Equal(detail.GetReview().GetSnapshot(), edited.Snapshot) {
		t.Fatalf("real Reviewer detail: status=%s eligible=%t policy=%s checks=%v snapshotEqual=%t", status.Code(err), detail.GetDecisionEligibility().GetEligible(), detail.GetCurrentPolicy().GetVersion(), detail.GetCurrentPolicy().GetRequiredCheckIds(), proto.Equal(detail.GetReview().GetSnapshot(), edited.Snapshot))
	}
	if count, err := f.authDB.Collection("auth_permission_audit_events").CountDocuments(f.ctx, bson.M{"subjectAuthId": reviewer, "actorAuthId": manager, "permission": "app.version.review"}); err != nil || count != 3 {
		t.Fatal("real Auth grants/revoke did not leave immutable audit events")
	}
	// Fail the Version write after the Review mutation and prove a real rollback.
	restore := f.rejectWrites("application_versions")
	code, body := f.decideHTTP(edited, reviewerSession, true)
	if code != 500 {
		t.Fatalf("BR-REV-012/013 forced decision rollback=%d", code)
	}
	restore()
	f.unchanged(before)
	code, body = f.decideHTTP(edited, reviewerSession, true)
	var approved rv.DecideApplicationVersionReviewResponse
	if code != 200 || protojson.Unmarshal(body, &approved) != nil || approved.GetReview().GetStatus() != "APPROVED" || approved.GetVersion().GetRevision() != edited.SourceVersionRevision+2 || approved.GetReview().GetDecision().GetDecidedBy() != reviewer || approved.GetReview().GetDecision().GetApprovalValidation().GetScopeCatalogRevision() <= 0 || !proto.Equal(approved.GetReview().GetSnapshot(), edited.Snapshot) {
		t.Fatalf("real Reviewer approval=%d", code)
	}
	before = uc002AppFacts(t, f.ctx, f.db, f.appID)
	if code, _ := f.decideHTTP(edited, reviewerSession, false); code != 409 {
		t.Fatalf("BR-REV-012 duplicate decision=%d", code)
	}
	f.unchanged(before)
	// REJECT works while Catalog is unavailable, and does not perform DNS reads.
	var catalog bson.Raw
	coll := f.authDB.Collection("auth_oauth_catalog")
	if err := coll.FindOne(f.ctx, bson.M{"_id": "current"}).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.DeleteOne(f.ctx, bson.M{"_id": "current"}); err != nil {
		t.Fatal(err)
	}
	reads := f.resolver.calls.Load()
	client := rv.NewApplicationReviewClient(f.edge)
	reviewerCtx := metadata.NewOutgoingContext(f.ctx, metadata.Pairs("x-iwut-session", reviewerSession))
	rejected, err := client.DecideApplicationVersionReview(reviewerCtx, f.decision(native, false))
	if err != nil || rejected.GetReview().GetStatus() != "REJECTED" || rejected.GetReview().GetDecision().GetDecidedBy() != reviewer || rejected.GetReview().GetDecision().GetApprovalValidation() != nil || rejected.GetReview().GetDecision().GetReason() != "integration rejection" || !proto.Equal(rejected.GetReview().GetSnapshot(), native.Snapshot) || f.resolver.calls.Load() != reads {
		t.Fatal("real Reviewer rejection failed", status.Code(err))
	}
	before = uc002AppFacts(t, f.ctx, f.db, f.appID)
	if code, _ := f.decideHTTP(web, reviewerSession, true); code != 503 {
		t.Fatalf("BR-REV-018 Catalog outage approval=%d", code)
	}
	f.unchanged(before)
	if _, err := coll.InsertOne(f.ctx, catalog); err != nil {
		t.Fatal(err)
	}
	f.scope("items.read", false)
	if code, _ := f.decideHTTP(web, reviewerSession, true); code != 422 {
		t.Fatalf("BR-REV-018 disabled Scope approval=%d", code)
	}
	f.unchanged(before)
	approvalCatalogRevision := f.scope("items.read", true)
	f.resolver.base.addresses["apps.whut.edu.cn"] = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if code, _ := f.decideHTTP(web, reviewerSession, true); code != 422 {
		t.Fatalf("BR-REV-018 private DNS approval=%d", code)
	}
	f.unchanged(before)
	f.resolver.base.addresses["apps.whut.edu.cn"] = []netip.Addr{netip.MustParseAddr("8.8.8.8")}
	webResult := &rv.DecideApplicationVersionReviewResponse{}
	if code := gatewayWeb(t, f.ctx, reviewerSession, rv.ApplicationReview_DecideApplicationVersionReview_FullMethodName, f.decision(web, true), webResult); code != 0 || webResult.GetReview().GetStatus() != "APPROVED" || webResult.GetReview().GetDecision().GetApprovalValidation().GetScopeCatalogRevision() != approvalCatalogRevision || !proto.Equal(webResult.GetReview().GetSnapshot(), web.Snapshot) {
		t.Fatal("gRPC-Web approval failed")
	}
	// Auth remains the source of Developer state and SYSTEM attribution.
	autoDraft := f.create("system-rejection")
	autoPending, err := client.SubmitApplicationVersionReview(f.sessionContext(), f.submission(autoDraft.VersionId, 1))
	if err != nil {
		t.Fatal("prepare real PENDING for SYSTEM decision", status.Code(err))
	}
	if _, err := f.authDB.Collection("auth_principals").UpdateOne(f.ctx, bson.M{"authId": f.owner}, bson.M{"$set": bson.M{"developerStatus": "SUSPENDED"}, "$inc": bson.M{"developerRevision": int64(1)}}); err != nil {
		t.Fatal(err)
	}
	reads = f.resolver.calls.Load()
	auto, err := client.DecideApplicationVersionReview(reviewerCtx, f.decision(autoPending.Review, true))
	if err != nil || auto.GetReview().GetStatus() != "REJECTED" || f.resolver.calls.Load() != reads {
		t.Fatal("real Auth Developer suspension did not auto-reject", status.Code(err))
	}
	key, err := base64.StdEncoding.Strict().DecodeString(os.Getenv(config.ServiceIdentityPrivateKeyEnv))
	if err != nil {
		t.Fatal(err)
	}
	conn := uc002AuthConnection(t, key, "iwut-app-center", "uc002-app", os.Getenv("AUTH_CENTER_INTEGRATION_TARGET"))
	system, err := sp.NewSystemPrincipalDirectoryClient(conn).ResolveSystemPrincipal(f.ctx, &sp.ResolveSystemPrincipalRequest{Purpose: sp.SystemPrincipalPurpose_SYSTEM_PRINCIPAL_PURPOSE_APP_CENTER_REVIEW_AUTO_REJECTION})
	if err != nil || system.GetAuthId() == "" || auto.GetReview().GetDecision().GetDecidedBy() != system.GetAuthId() || auto.GetVersion().GetUpdatedBy() != system.GetAuthId() {
		t.Fatal("SYSTEM attribution differs from real Auth resolution")
	}
	for _, name := range []string{"application_publications", "application_publication_history"} {
		if count, err := f.db.Collection(name).CountDocuments(f.ctx, bson.M{"applicationId": f.appID}); err != nil || count != 0 {
			t.Fatal("BR-REV-019 decision published a Version")
		}
	}
	t.Log("UC005 BR-REV-010–020: real Auth grants/revoke/USER, permission isolation/conflict, real PENDING approve/reject, rollback, dependency gates and Auth SYSTEM auto-rejection passed")
}
