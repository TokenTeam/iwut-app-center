package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	authoringv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_authoring"
	distributionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_distribution"
	publicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_publication"
	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	resourcev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_resource"
	reviewv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_review"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_version"
	permissionsv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/script_permission"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	scriptdomain "iwut-app-center/internal/script/domain"
)

func TestE2E_UCAPP041_042_043_044_045_046_047_ScriptManagementLocalImages(t *testing.T) {
	forwardAuthAddress := os.Getenv("UC041_ADMIN_FORWARD_AUTH_ADDR")
	if forwardAuthAddress == "" {
		t.Skip("run scripts/test-uc002-gateway-auth-integration.sh --script-management")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	manager := registerAuthFixture(t, ctx)
	author := registerAuthFixture(t, ctx)
	reviewer := registerAuthFixture(t, ctx)

	output, err := exec.CommandContext(ctx, "docker", "exec", os.Getenv("UC002_AUTH_CONTAINER_NAME"),
		"/app/auth-center", "bootstrap-platform-admin", "--auth-id", manager.authID).CombinedOutput()
	if logPath := os.Getenv("UC002_BOOTSTRAP_LOG"); logPath != "" {
		_ = os.WriteFile(logPath, output, 0600)
	}
	var bootstrap struct {
		Applied bool   `json:"applied"`
		AuthID  string `json:"authId"`
	}
	if err != nil || json.Unmarshal(output, &bootstrap) != nil || !bootstrap.Applied || bootstrap.AuthID != manager.authID {
		t.Fatal("real Auth administrator bootstrap failed", err)
	}

	// Supply only the Console Session to the edge. The gateway must obtain
	// the audience-specific USER JWS from Auth for each business request.
	listener, err := net.Listen("tcp", forwardAuthAddress)
	if err != nil {
		t.Fatal(err)
	}
	forwardAuth := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("iwut_uc041_fixture")
		if r.URL.Path != "/internal/forward-auth" || err != nil || (cookie.Value != manager.session && cookie.Value != author.session && cookie.Value != reviewer.session) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Iwut-Session", cookie.Value)
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = forwardAuth.Serve(listener) }()
	t.Cleanup(func() { _ = forwardAuth.Close() })

	httpCall := func(host, method, path, session string, input, result proto.Message, wantStatus int) http.Header {
		t.Helper()
		var payload []byte
		if input != nil {
			var err error
			payload, err = protojson.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		permissionRetryUntil := time.Now().Add(70 * time.Second)
		for {
			req, err := http.NewRequestWithContext(ctx, method, "http://"+os.Getenv("UC002_EDGE_HTTP_ADDR")+path, bytes.NewReader(payload))
			if err != nil {
				t.Fatal(err)
			}
			req.Host = host
			req.Header.Set("Content-Type", "application/json")
			if host == "api.uc002.test" {
				req.Header.Set("X-Iwut-Session", session)
			} else {
				req.AddCookie(&http.Cookie{Name: "iwut_uc041_fixture", Value: session})
			}
			// The Console edge must scrub this untrusted caller-supplied identity.
			req.Header.Set("X-Iwut-Identity", "forged-test-identity")
			response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, 7<<20))
			_ = response.Body.Close()
			if response.Header.Get("X-Iwut-Identity") != "" || (session != "" && bytes.Contains(body, []byte(session))) {
				t.Fatal("edge response leaked credentials")
			}
			// Auth permits ten permission writes per actor per minute. A rate
			// rejection happens before mutation, so retry only that exact error
			// with the original payload and OCC revision, within a bounded wait.
			var failure struct {
				Reason string `json:"reason"`
			}
			if err == nil && response.StatusCode == http.StatusTooManyRequests && wantStatus == http.StatusOK &&
				method == http.MethodPost && path == "/auth-center/v1/script-permissions:manage" &&
				json.Unmarshal(body, &failure) == nil && failure.Reason == "SCRIPT_PERMISSION_RATE_LIMITED" &&
				time.Now().Add(5*time.Second).Before(permissionRetryUntil) {
				t.Log("Auth permission write limited; retrying the same command after five seconds")
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					t.Fatal("waiting for Auth permission write budget:", ctx.Err())
				case <-timer.C:
				}
				continue
			}
			if err != nil || response.StatusCode != wantStatus {
				t.Fatalf("%s %s on %s returned %d, want %d (%v)", method, path, host, response.StatusCode, wantStatus, err)
			}
			if result != nil {
				if err := protojson.Unmarshal(body, result); err != nil {
					t.Fatal("decode business response:", err)
				}
			}
			return response.Header
		}
	}
	edge, err := grpc.NewClient(os.Getenv("UC002_EDGE_GRPC_ADDR"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Close()
	authorRPC := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", author.session))

	const adminHost = "admin.uc002.test"
	const developerHost = "developer.uc002.test"
	const appResources = "/app-center/v1/script-resources"
	const authorResources = "/app-center/v1/users/me/script-resources"
	create := &resourcev1.CreateScriptResourceRequest{Name: "Crawler", Purpose: "Controlled fixture"}
	httpCall(developerHost, http.MethodPost, authorResources, author.session, create, nil, http.StatusForbidden)
	httpCall(adminHost, http.MethodPost, appResources, manager.session, create, nil, http.StatusNotFound)
	authClient, err := mongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnv)))
	if err != nil {
		t.Fatal(err)
	}
	defer authClient.Disconnect(context.Background())
	principals := authClient.Database(os.Getenv("AUTH_CENTER_INTEGRATION_DATABASE")).Collection("auth_principals")
	setDeveloper := func(state string) {
		t.Helper()
		result, err := principals.UpdateOne(ctx, bson.M{"authId": author.authID}, bson.M{"$set": bson.M{"developerStatus": state, "updatedAt": time.Now().UTC().Truncate(time.Millisecond)}, "$inc": bson.M{"developerRevision": int64(1)}})
		if err != nil || result.ModifiedCount != 1 {
			t.Fatal("set developer fixture state", err)
		}
	}
	setDeveloper("APPROVED")
	var resource resourcev1.ScriptResource
	header := httpCall(developerHost, http.MethodPost, authorResources, author.session, create, &resource, http.StatusOK)
	if resource.GetResourceId() == "" || resource.GetRevision() != 1 || header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("unexpected resource response")
	}
	httpCall(adminHost, http.MethodPost, authorResources, author.session, create, nil, http.StatusNotFound)
	resourceRPC := authoringv1.NewScriptAuthoringServiceClient(edge)
	updatedResource, err := resourceRPC.ReplaceOwnScriptResourceMetadata(authorRPC, &resourcev1.ReplaceScriptResourceMetadataRequest{
		ResourceId: resource.GetResourceId(),
		Command:    &resourcev1.ReplaceScriptResourceMetadataCommand{Name: "Crawler", Purpose: "Updated fixture", ExpectedRevision: 1},
	})
	if err != nil || updatedResource.GetRevision() != 2 || updatedResource.GetPurpose() != "Updated fixture" {
		t.Fatal("native gRPC metadata replacement failed", status.Code(err))
	}

	const original = "\ufeff{\r\n \"z\":2,\"a\":1\r\n}\r\n"
	createDraft := &versionv1.CreateScriptVersionDraftInput{VersionLabel: "draft-1", ContentFormat: "json", ContentText: original}
	var draft versionv1.ScriptVersion
	httpCall(developerHost, http.MethodPost, authorResources+"/"+resource.GetResourceId()+"/versions", author.session, createDraft, &draft, http.StatusOK)
	assertScriptDraftBytes(t, &draft, original, 1)
	const edited = "{\"a\":1,\"z\":2}"
	update := &versionv1.ReplaceScriptVersionDraftRequest{
		ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(),
		Command: &versionv1.ReplaceScriptVersionDraftInput{ExpectedRevision: 1, ContentFormat: "json", ContentText: edited},
	}
	var modified versionv1.ScriptVersion
	if code := gatewayWeb(t, ctx, author.session, authoringv1.ScriptAuthoringService_ReplaceOwnScriptVersionDraft_FullMethodName, update, &modified); code != int(codes.OK) {
		t.Fatalf("gRPC-Web draft replacement returned %d", code)
	}
	assertScriptDraftBytes(t, &modified, edited, 2)
	httpCall(developerHost, http.MethodPut, authorResources+"/"+resource.GetResourceId()+"/versions/"+draft.GetVersionId()+"/draft",
		author.session, update.GetCommand(), nil, http.StatusConflict)
	versionPath := appResources + "/" + resource.GetResourceId() + "/versions/" + draft.GetVersionId()
	ownVersionPath := authorResources + "/" + resource.GetResourceId() + "/versions/" + draft.GetVersionId()
	submit := &versionv1.SubmitScriptVersionReviewInput{ExpectedRevision: 2}
	var submission versionv1.ScriptVersionReviewSubmission
	httpCall(developerHost, http.MethodPost, ownVersionPath+"/reviews", author.session, submit, &submission, http.StatusOK)
	assertScriptSubmission(t, &submission, 3)
	httpCall(developerHost, http.MethodPost, ownVersionPath+"/reviews", author.session, submit, nil, http.StatusConflict)
	update.Command.ExpectedRevision = 3
	httpCall(developerHost, http.MethodPut, ownVersionPath+"/draft", author.session, update.GetCommand(), nil, http.StatusConflict)

	setDeveloper("SUSPENDED")
	httpCall(developerHost, http.MethodPost, authorResources, author.session, create, nil, http.StatusForbidden)
	if _, err := resourceRPC.ReplaceOwnScriptVersionDraft(authorRPC, update); status.Code(err) != codes.PermissionDenied {
		t.Fatal("author could write after suspension", err)
	}
	var ownReview authoringv1.OwnScriptReviewResult
	httpCall(developerHost, http.MethodGet, ownVersionPath+"/reviews/"+submission.GetReviewId(), author.session, nil, &ownReview, http.StatusOK)
	if ownReview.GetReviewId() != submission.GetReviewId() {
		t.Fatal("suspended author lost review read")
	}
	setDeveloper("APPROVED")

	const queuePath = "/app-center/v1/script-reviews/pending"
	reviewPath := versionPath + "/reviews/" + submission.GetReviewId()
	resourcePath := appResources + "/" + resource.GetResourceId()
	queryClient := queryv1.NewScriptQueryServiceClient(edge)
	versionQuery := &queryv1.GetScriptVersionRequest{ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId()}
	reviewQuery := &queryv1.GetScriptReviewRequest{
		ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(), ReviewId: submission.GetReviewId(),
	}
	httpCall(adminHost, http.MethodGet, queuePath, author.session, nil, nil, http.StatusForbidden)
	httpCall(adminHost, http.MethodGet, resourcePath, author.session, nil, nil, http.StatusForbidden)
	scriptPermissions := new(permissionsv1.ScriptPermissions)
	httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-permissions:get", manager.session,
		&permissionsv1.GetScriptPermissionsRequest{SubjectAuthId: author.authID}, scriptPermissions, http.StatusOK)
	if len(scriptPermissions.GetPermissions()) != 4 || scriptPermissions.GetPermissionRevision() < 1 {
		t.Fatal("Auth did not return current independent permissions")
	}
	setPermission := func(permission permissionsv1.ScriptPermission, action permissionsv1.ScriptPermissionAction) {
		t.Helper()
		result := new(permissionsv1.ScriptPermissions)
		httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-permissions:manage", manager.session,
			&permissionsv1.ManageScriptPermissionRequest{
				SubjectAuthId: author.authID, Permission: permission, Action: action,
				ExpectedPermissionRevision: proto.Int64(scriptPermissions.GetPermissionRevision()),
				Reason:                     "UC045 role isolation",
			}, result, http.StatusOK)
		if result.GetPermissionRevision() != scriptPermissions.GetPermissionRevision()+1 {
			t.Fatal("independent grant or revoke did not change revision")
		}
		scriptPermissions = result
	}

	const grantPermission = permissionsv1.ScriptPermissionAction_SCRIPT_PERMISSION_ACTION_GRANT
	const revokePermission = permissionsv1.ScriptPermissionAction_SCRIPT_PERMISSION_ACTION_REVOKE
	const reviewPermission = permissionsv1.ScriptPermission_SCRIPT_PERMISSION_REVIEW
	const publishPermission = permissionsv1.ScriptPermission_SCRIPT_PERMISSION_PUBLISH
	setPermission(publishPermission, grantPermission)
	publisherView, err := queryClient.GetScriptVersion(authorRPC, versionQuery)
	if err != nil || publisherView.ContentText != nil || publisherView.GetVersion().GetDigest() != modified.GetDigest() {
		t.Fatal("native publisher read exposed content or lost summary", err)
	}
	httpCall(adminHost, http.MethodGet, reviewPath, author.session, nil, nil, http.StatusForbidden)
	setPermission(reviewPermission, grantPermission)

	var resources queryv1.ListScriptResourcesResponse
	httpCall(adminHost, http.MethodGet, appResources, author.session, nil, &resources, http.StatusOK)
	if len(resources.GetItems()) != 1 {
		t.Fatal("resource query failed")
	}
	var versions queryv1.ListScriptVersionsResponse
	httpCall(adminHost, http.MethodGet, resourcePath+"/versions", author.session, nil, &versions, http.StatusOK)
	if len(versions.GetItems()) != 1 {
		t.Fatal("version query failed")
	}
	var queue queryv1.ListPendingScriptReviewsResponse
	header = httpCall(adminHost, http.MethodGet, queuePath, author.session, nil, &queue, http.StatusOK)
	if len(queue.GetItems()) != 1 || header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("Admin pending queue lost result or cache policy")
	}
	httpCall("developer.uc002.test", http.MethodGet, queuePath, author.session, nil, nil, http.StatusNotFound)
	var detail queryv1.GetScriptReviewResponse
	if code := gatewayWeb(t, ctx, author.session, queryv1.ScriptQueryService_GetScriptReview_FullMethodName, reviewQuery, &detail); code != int(codes.OK) ||
		detail.GetContentText() != edited || len(detail.GetChecks()) != 4 || detail.GetCanDecide() || len(detail.GetConflicts()) != 2 {
		t.Fatal("gRPC-Web reviewer could not retrieve snapshot and conflicts", code)
	}
	setPermission(reviewPermission, revokePermission)
	httpCall(adminHost, http.MethodGet, reviewPath, author.session, nil, nil, http.StatusForbidden)
	if view, err := queryClient.GetScriptVersion(authorRPC, versionQuery); err != nil || view.ContentText != nil {
		t.Fatal("review revoke changed independent publisher access", err)
	}
	setPermission(publishPermission, revokePermission)

	for _, permission := range []permissionsv1.ScriptPermission{
		permissionsv1.ScriptPermission_SCRIPT_PERMISSION_SUSPEND,
		permissionsv1.ScriptPermission_SCRIPT_PERMISSION_RESTORE,
	} {
		setPermission(permission, grantPermission)
		var resourceView resourcev1.ScriptResource
		httpCall(adminHost, http.MethodGet, resourcePath, author.session, nil, &resourceView, http.StatusOK)
		if resourceView.GetResourceId() != resource.GetResourceId() {
			t.Fatal("operator cannot read resource")
		}
		httpCall(adminHost, http.MethodGet, versionPath, author.session, nil, nil, http.StatusForbidden)
		setPermission(permission, revokePermission)
	}
	httpCall(adminHost, http.MethodGet, resourcePath, author.session, nil, nil, http.StatusForbidden)

	client, err := mongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnv)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	database := client.Database(os.Getenv("UC041_APP_DATABASE"))
	e2eAssertCollectionCount(t, database, "script_resources", 1)
	e2eAssertCollectionCount(t, database, "script_resource_audits", 2)
	e2eAssertCollectionCount(t, database, "script_versions", 1)
	e2eAssertCollectionCount(t, database, "script_version_audits", 3)
	e2eAssertCollectionCount(t, database, "script_reviews", 1)
	// A separately registered reviewer receives only the independent review permission.
	reviewerPermissions := new(permissionsv1.ScriptPermissions)
	httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-permissions:get", manager.session, &permissionsv1.GetScriptPermissionsRequest{SubjectAuthId: reviewer.authID}, reviewerPermissions, http.StatusOK)
	httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-permissions:manage", manager.session, &permissionsv1.ManageScriptPermissionRequest{
		SubjectAuthId: reviewer.authID, Permission: reviewPermission, Action: grantPermission, ExpectedPermissionRevision: proto.Int64(reviewerPermissions.GetPermissionRevision()), Reason: "independent script reviewer"}, reviewerPermissions, http.StatusOK)
	reviewerRPC := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", reviewer.session))
	independent, err := queryClient.GetScriptReview(reviewerRPC, reviewQuery)
	if err != nil || !independent.GetCanDecide() {
		t.Fatal("independent reviewer qualification", err)
	}
	checks := make([]string, 0, len(independent.GetChecks()))
	for _, check := range independent.GetChecks() {
		if check.GetMandatory() {
			checks = append(checks, check.GetId())
		}
	}
	approved, err := reviewv1.NewScriptReviewServiceClient(edge).DecideScriptVersionReview(reviewerRPC, &reviewv1.DecideScriptVersionReviewRequest{
		ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(), ReviewId: submission.GetReviewId(), Command: &reviewv1.DecideScriptVersionReviewInput{
			ExpectedVersionRevision: independent.GetVersion().GetRevision(), PolicyVersion: independent.GetPolicyVersion(), Decision: reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_APPROVE, ConfirmedCheckIds: checks}})
	if err != nil || approved.GetDecision().GetDecidedBy() != reviewer.authID {
		t.Fatal("real independent script approval", err)
	}
	const publicationBase = "/app-center/v1/script-resources/"
	publicationPath := publicationBase + resource.GetResourceId() + "/publication"
	publishPath := versionPath + "/publication"
	publishInput := &publicationv1.ScriptPublicationRevisionInput{ExpectedPublicationRevision: 1}
	httpCall(adminHost, http.MethodPost, publishPath, author.session, publishInput, nil, http.StatusForbidden)
	setPermission(publishPermission, grantPermission)
	var published publicationv1.ScriptPublicationCommandResult
	httpCall(adminHost, http.MethodPost, publishPath, author.session, publishInput, &published, http.StatusOK)
	if published.GetState().GetHead().GetVersionId() != draft.GetVersionId() || !published.GetChanged() {
		t.Fatal("real Auth publish permission")
	}
	httpCall("developer.uc002.test", http.MethodPost, publishPath, author.session, publishInput, nil, http.StatusNotFound)
	var history queryv1.ListScriptPublicationHistoryResponse
	if code := gatewayWeb(t, ctx, author.session, queryv1.ScriptQueryService_ListScriptPublicationHistory_FullMethodName, &queryv1.ListScriptPublicationHistoryRequest{ResourceId: resource.GetResourceId()}, &history); code != int(codes.OK) || len(history.GetItems()) != 1 {
		t.Fatal("publication history through gRPC-Web", code)
	}
	setPermission(publishPermission, revokePermission)
	if _, err := publicationv1.NewScriptPublicationServiceClient(edge).ClearScriptHead(authorRPC, &publicationv1.ClearScriptHeadRequest{ResourceId: resource.GetResourceId(), Command: &publicationv1.ScriptPublicationRevisionInput{ExpectedPublicationRevision: 2}}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("same Session retained publication permission", err)
	}
	const suspendPermission = permissionsv1.ScriptPermission_SCRIPT_PERMISSION_SUSPEND
	const restorePermission = permissionsv1.ScriptPermission_SCRIPT_PERMISSION_RESTORE
	setPermission(suspendPermission, grantPermission)
	httpCall(adminHost, http.MethodPost, publicationBase+resource.GetResourceId()+"/distribution/suspend", author.session, &publicationv1.ScriptAvailabilityInput{ExpectedAvailabilityRevision: 1, Reason: "real suspension"}, &published, http.StatusOK)
	if published.GetState().GetAvailability() != publicationv1.ScriptDistributionAvailability_SCRIPT_DISTRIBUTION_AVAILABILITY_SUSPENDED {
		t.Fatal("real suspend")
	}
	httpCall(adminHost, http.MethodGet, publicationPath+"/history", author.session, nil, nil, http.StatusForbidden)
	setPermission(suspendPermission, revokePermission)
	setPermission(restorePermission, grantPermission)
	if _, err := publicationv1.NewScriptPublicationServiceClient(edge).SuspendScriptDistribution(authorRPC, &publicationv1.SuspendScriptDistributionRequest{ResourceId: resource.GetResourceId(), Command: &publicationv1.ScriptAvailabilityInput{ExpectedAvailabilityRevision: 2, Reason: "not a suspender"}}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("restore implied suspend", err)
	}
	if code := gatewayWeb(t, ctx, author.session, publicationv1.ScriptPublicationService_RestoreScriptDistribution_FullMethodName, &publicationv1.RestoreScriptDistributionRequest{ResourceId: resource.GetResourceId(), Command: &publicationv1.ScriptAvailabilityInput{ExpectedAvailabilityRevision: 2, Reason: "real restore"}}, &published); code != int(codes.OK) {
		t.Fatal("real restore through gRPC-Web", code)
	}
	if published.GetState().GetHead().GetVersionId() != draft.GetVersionId() || published.GetState().GetPublicationRevision() != 2 || published.GetState().GetAvailabilityRevision() != 3 {
		t.Fatal("restore changed head")
	}
	setPermission(restorePermission, revokePermission)
	e2eAssertCollectionCount(t, database, "script_published_versions", 1)
	e2eAssertCollectionCount(t, database, "script_publication_events", 3)

	const publicHost = "api.uc002.test"
	publicContentPath := "/app-center/v1/scripts/" + resource.GetResourceId() + "/versions/" + draft.GetVersionId() + "/content?expectedDigest=" + modified.GetDigest()
	var publicContent distributionv1.ScriptContent
	publicHeader := httpCall(publicHost, http.MethodGet, publicContentPath, author.session, nil, &publicContent, http.StatusOK)
	if publicContent.GetContentText() != edited || publicHeader.Get("Cache-Control") != "private, no-store" {
		t.Fatal("ordinary real USER independent content")
	}
	httpCall(publicHost, http.MethodGet, publicContentPath, "", nil, nil, http.StatusUnauthorized)
	httpCall(adminHost, http.MethodGet, publicContentPath, author.session, nil, nil, http.StatusNotFound)
	httpCall("developer.uc002.test", http.MethodGet, publicContentPath, author.session, nil, nil, http.StatusNotFound)
	var library distributionv1.PublishedScriptPage
	httpCall(publicHost, http.MethodGet, "/app-center/v1/scripts", author.session, nil, &library, http.StatusOK)
	if len(library.GetItems()) != 1 || library.GetItems()[0].GetHead().GetVersionId() != draft.GetVersionId() {
		t.Fatal("independent recommendation")
	}
	distribution := distributionv1.NewScriptDistributionServiceClient(edge)
	getPublic := &distributionv1.GetPublishedScriptContentRequest{ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(), ExpectedDigest: modified.GetDigest()}
	if got, err := distribution.GetPublishedScriptContent(authorRPC, getPublic); err != nil || got.GetContentText() != edited {
		t.Fatal("native independent distribution", err)
	}
	if code := gatewayWeb(t, ctx, author.session, distributionv1.ScriptDistributionService_GetPublishedScriptContent_FullMethodName, getPublic, &publicContent); code != int(codes.OK) || publicContent.GetContentText() != edited {
		t.Fatal("gRPC-Web independent distribution", code)
	}
	// The associated positive paths are covered by normal-Wire App E2E and
	// the real Mongo qualification matrix. Here all four PUBLIC routes also
	// prove real Session exchange and App's uniform missing-target boundary.
	runtime := &distributionv1.ScriptRuntimeQuery{Mode: distributionv1.ScriptRuntimeMode_SCRIPT_RUNTIME_MODE_UNIFIED, HostRpcApiMajor: 1}
	missingManifest := &distributionv1.ResolveApplicationScriptManifestRequest{ApplicationId: resource.GetResourceId(), ApplicationVersionId: draft.GetVersionId(), Query: &distributionv1.ResolveApplicationScriptManifestInput{Runtime: runtime}}
	if _, err := distribution.ResolveApplicationScriptManifest(authorRPC, missingManifest); status.Code(err) != codes.NotFound {
		t.Fatal("associated manifest route", err)
	}
	missingContent := &distributionv1.GetApplicationScriptContentRequest{ApplicationId: resource.GetResourceId(), ApplicationVersionId: draft.GetVersionId(), ScriptVersionId: draft.GetVersionId(), Query: &distributionv1.GetApplicationScriptContentInput{Runtime: runtime, ExpectedDigest: modified.GetDigest()}}
	if code := gatewayWeb(t, ctx, author.session, distributionv1.ScriptDistributionService_GetApplicationScriptContent_FullMethodName, missingContent, &distributionv1.ApplicationScriptContent{}); code != int(codes.NotFound) {
		t.Fatal("associated content route", code)
	}
	// The author submits a maximum-sized version. Publishing remains an independent grant.
	maxText := strings.Repeat("\x01", scriptdomain.MaxContentBytes)
	maxVersion, err := authoringv1.NewScriptAuthoringServiceClient(edge).CreateOwnScriptVersionDraft(authorRPC, &versionv1.CreateScriptVersionDraftRequest{ResourceId: resource.GetResourceId(), Draft: &versionv1.CreateScriptVersionDraftInput{VersionLabel: "max-distribution", ContentFormat: "json", ContentText: maxText}})
	if err != nil {
		t.Fatal(err)
	}
	maxSubmission, err := authoringv1.NewScriptAuthoringServiceClient(edge).SubmitOwnScriptVersionReview(authorRPC, &versionv1.SubmitScriptVersionReviewRequest{ResourceId: resource.GetResourceId(), VersionId: maxVersion.GetVersionId(), Command: &versionv1.SubmitScriptVersionReviewInput{ExpectedRevision: 1}})
	if err != nil {
		t.Fatal(err)
	}
	maxReview, err := queryClient.GetScriptReview(reviewerRPC, &queryv1.GetScriptReviewRequest{ResourceId: resource.GetResourceId(), VersionId: maxVersion.GetVersionId(), ReviewId: maxSubmission.GetReviewId()})
	if err != nil {
		t.Fatal(err)
	}
	maxChecks := []string{}
	for _, check := range maxReview.GetChecks() {
		if check.GetMandatory() {
			maxChecks = append(maxChecks, check.GetId())
		}
	}
	if _, err := reviewv1.NewScriptReviewServiceClient(edge).DecideScriptVersionReview(reviewerRPC, &reviewv1.DecideScriptVersionReviewRequest{ResourceId: resource.GetResourceId(), VersionId: maxVersion.GetVersionId(), ReviewId: maxSubmission.GetReviewId(), Command: &reviewv1.DecideScriptVersionReviewInput{ExpectedVersionRevision: maxReview.GetVersion().GetRevision(), PolicyVersion: maxReview.GetPolicyVersion(), Decision: reviewv1.ScriptReviewAction_SCRIPT_REVIEW_ACTION_APPROVE, ConfirmedCheckIds: maxChecks}}); err != nil {
		t.Fatal(err)
	}
	setPermission(publishPermission, grantPermission)
	if _, err := publicationv1.NewScriptPublicationServiceClient(edge).PublishScriptVersion(authorRPC, &publicationv1.PublishScriptVersionRequest{ResourceId: resource.GetResourceId(), VersionId: maxVersion.GetVersionId(), Command: &publicationv1.ScriptPublicationRevisionInput{ExpectedPublicationRevision: 2}}); err != nil {
		t.Fatal(err)
	}
	setPermission(publishPermission, revokePermission)
	maxGet := &distributionv1.GetPublishedScriptContentRequest{ResourceId: resource.GetResourceId(), VersionId: maxVersion.GetVersionId(), ExpectedDigest: maxVersion.GetDigest()}
	maxPath := "/app-center/v1/scripts/" + resource.GetResourceId() + "/versions/" + maxVersion.GetVersionId() + "/content?expectedDigest=" + maxVersion.GetDigest()
	httpCall(publicHost, http.MethodGet, maxPath, author.session, nil, &publicContent, http.StatusOK)
	if publicContent.GetContentText() != maxText || publicContent.GetByteLength() != scriptdomain.MaxContentBytes {
		t.Fatal("maximum HTTP JSON bytes through three images")
	}
	if got, err := distribution.GetPublishedScriptContent(authorRPC, maxGet); err != nil || got.GetContentText() != maxText {
		t.Fatal("maximum native bytes through three images", err)
	}
	if code := gatewayWeb(t, ctx, author.session, distributionv1.ScriptDistributionService_GetPublishedScriptContent_FullMethodName, maxGet, &publicContent); code != int(codes.OK) || publicContent.GetContentText() != maxText {
		t.Fatal("maximum gRPC-Web bytes through three images", code)
	}
	e2eAssertCollectionCount(t, database, "script_published_versions", 2)
	e2eAssertCollectionCount(t, database, "script_publication_events", 4)

}
