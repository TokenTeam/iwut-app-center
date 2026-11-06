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
	"testing"
	"time"

	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	resourcev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_resource"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_version"
	permissionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/script_maintainer_permission"
	permissionsv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/script_permission"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestE2E_UCAPP041_042_043_045_ScriptManagementLocalImages(t *testing.T) {
	forwardAuthAddress := os.Getenv("UC041_ADMIN_FORWARD_AUTH_ADDR")
	if forwardAuthAddress == "" {
		t.Skip("run scripts/test-uc002-gateway-auth-integration.sh --script-management")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	manager := registerAuthFixture(t, ctx)
	maintainer := registerAuthFixture(t, ctx)

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
		if r.URL.Path != "/internal/forward-auth" || err != nil || (cookie.Value != manager.session && cookie.Value != maintainer.session) {
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
		req, err := http.NewRequestWithContext(ctx, method, "http://"+os.Getenv("UC002_EDGE_HTTP_ADDR")+path, bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "iwut_uc041_fixture", Value: session})
		// The Console edge must scrub this untrusted caller-supplied identity.
		req.Header.Set("X-Iwut-Identity", "forged-test-identity")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 7<<20))
		if err != nil || response.StatusCode != wantStatus {
			t.Fatalf("%s %s on %s returned %d, want %d (%v)", method, path, host, response.StatusCode, wantStatus, err)
		}
		if response.Header.Get("X-Iwut-Identity") != "" || bytes.Contains(body, []byte(session)) {
			t.Fatal("edge response leaked credentials")
		}
		if result != nil {
			if err := protojson.Unmarshal(body, result); err != nil {
				t.Fatal("decode business response:", err)
			}
		}
		return response.Header
	}
	edge, err := grpc.NewClient(os.Getenv("UC002_EDGE_GRPC_ADDR"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Close()
	managerRPC := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", manager.session))
	maintainerRPC := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", maintainer.session))

	const adminHost = "admin.uc002.test"
	const appResources = "/app-center/v1/script-resources"
	create := &resourcev1.CreateScriptResourceRequest{Name: "Crawler", Purpose: "Controlled fixture"}
	for _, session := range []string{maintainer.session, manager.session} {
		httpCall(adminHost, http.MethodPost, appResources, session, create, nil, http.StatusForbidden)
	}
	var before permissionv1.ScriptMaintainerPermission
	httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-maintainer-permissions:get", manager.session,
		&permissionv1.GetScriptMaintainerPermissionRequest{SubjectAuthId: maintainer.authID}, &before, http.StatusOK)
	if before.GetGranted() || before.GetEffective() || before.GetPermissionRevision() < 1 {
		t.Fatal("maintainer permission should start absent")
	}
	grant := &permissionv1.ManageScriptMaintainerPermissionRequest{
		SubjectAuthId: maintainer.authID, ExpectedPermissionRevision: proto.Int64(before.GetPermissionRevision()),
		Action: permissionv1.ScriptMaintainerPermissionAction_SCRIPT_MAINTAINER_PERMISSION_ACTION_GRANT,
		Reason: "local script management integration",
	}
	var granted permissionv1.ScriptMaintainerPermission
	httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-maintainer-permissions:manage", manager.session, grant, &granted, http.StatusOK)
	if !granted.GetGranted() || !granted.GetEffective() || granted.GetPermissionRevision() != before.GetPermissionRevision()+1 {
		t.Fatal("real Auth grant did not take effect")
	}
	var resource resourcev1.ScriptResource
	header := httpCall(adminHost, http.MethodPost, appResources, maintainer.session, create, &resource, http.StatusOK)
	if resource.GetResourceId() == "" || resource.GetRevision() != 1 || header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("unexpected resource response")
	}
	httpCall("developer.uc002.test", http.MethodPost, appResources, maintainer.session, create, nil, http.StatusNotFound)
	resourceRPC := resourcev1.NewScriptResourceServiceClient(edge)
	updatedResource, err := resourceRPC.ReplaceScriptResourceMetadata(maintainerRPC, &resourcev1.ReplaceScriptResourceMetadataRequest{
		ResourceId: resource.GetResourceId(),
		Command:    &resourcev1.ReplaceScriptResourceMetadataCommand{Name: "Crawler", Purpose: "Updated fixture", ExpectedRevision: 1},
	})
	if err != nil || updatedResource.GetRevision() != 2 || updatedResource.GetPurpose() != "Updated fixture" {
		t.Fatal("native gRPC metadata replacement failed", status.Code(err))
	}

	const original = "\ufeff{\r\n \"z\":2,\"a\":1\r\n}\r\n"
	createDraft := &versionv1.CreateScriptVersionDraftInput{VersionLabel: "draft-1", ContentFormat: "json", ContentText: original}
	var draft versionv1.ScriptVersion
	httpCall(adminHost, http.MethodPost, appResources+"/"+resource.GetResourceId()+"/versions", maintainer.session, createDraft, &draft, http.StatusOK)
	assertScriptDraftBytes(t, &draft, original, 1)
	const edited = "{\"a\":1,\"z\":2}"
	update := &versionv1.ReplaceScriptVersionDraftRequest{
		ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(),
		Command: &versionv1.ReplaceScriptVersionDraftInput{ExpectedRevision: 1, ContentFormat: "json", ContentText: edited},
	}
	var modified versionv1.ScriptVersion
	if code := gatewayWeb(t, ctx, maintainer.session, versionv1.ScriptVersionService_ReplaceScriptVersionDraft_FullMethodName, update, &modified); code != int(codes.OK) {
		t.Fatalf("gRPC-Web draft replacement returned %d", code)
	}
	assertScriptDraftBytes(t, &modified, edited, 2)
	httpCall(adminHost, http.MethodPut, appResources+"/"+resource.GetResourceId()+"/versions/"+draft.GetVersionId()+"/draft",
		maintainer.session, update.GetCommand(), nil, http.StatusConflict)
	versionPath := appResources + "/" + resource.GetResourceId() + "/versions/" + draft.GetVersionId()
	submit := &versionv1.SubmitScriptVersionReviewInput{ExpectedRevision: 2}
	var submission versionv1.ScriptVersionReviewSubmission
	httpCall(adminHost, http.MethodPost, versionPath+"/reviews", maintainer.session, submit, &submission, http.StatusOK)
	assertScriptSubmission(t, &submission, 3)
	httpCall(adminHost, http.MethodPost, versionPath+"/reviews", maintainer.session, submit, nil, http.StatusConflict)
	update.Command.ExpectedRevision = 3
	httpCall(adminHost, http.MethodPut, versionPath+"/draft", maintainer.session, update.GetCommand(), nil, http.StatusConflict)

	grant.Action = permissionv1.ScriptMaintainerPermissionAction_SCRIPT_MAINTAINER_PERMISSION_ACTION_REVOKE
	grant.ExpectedPermissionRevision = proto.Int64(granted.GetPermissionRevision())
	revoked, err := permissionv1.NewScriptMaintainerPermissionServiceClient(edge).ManageScriptMaintainerPermission(managerRPC, grant)
	if err != nil || revoked.GetGranted() || revoked.GetEffective() || revoked.GetPermissionRevision() != granted.GetPermissionRevision()+1 {
		t.Fatal("real Auth native gRPC revoke failed", status.Code(err))
	}
	httpCall(adminHost, http.MethodPost, appResources, maintainer.session, create, nil, http.StatusForbidden)
	update.Command.ContentText = "{}"
	if _, err := versionv1.NewScriptVersionServiceClient(edge).ReplaceScriptVersionDraft(maintainerRPC, update); status.Code(err) != codes.PermissionDenied {
		t.Fatal("the same Session could write after revoke", status.Code(err))
	}
	httpCall(adminHost, http.MethodPost, versionPath+"/reviews", maintainer.session, submit, nil, http.StatusForbidden)

	const queuePath = "/app-center/v1/script-reviews/pending"
	reviewPath := versionPath + "/reviews/" + submission.GetReviewId()
	resourcePath := appResources + "/" + resource.GetResourceId()
	queryClient := queryv1.NewScriptQueryServiceClient(edge)
	versionQuery := &queryv1.GetScriptVersionRequest{ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId()}
	reviewQuery := &queryv1.GetScriptReviewRequest{
		ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(), ReviewId: submission.GetReviewId(),
	}
	httpCall(adminHost, http.MethodGet, queuePath, maintainer.session, nil, nil, http.StatusForbidden)
	httpCall(adminHost, http.MethodGet, resourcePath, maintainer.session, nil, nil, http.StatusForbidden)
	var scriptPermissions permissionsv1.ScriptPermissions
	httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-permissions:get", manager.session,
		&permissionsv1.GetScriptPermissionsRequest{SubjectAuthId: maintainer.authID}, &scriptPermissions, http.StatusOK)
	if len(scriptPermissions.GetPermissions()) != 5 || scriptPermissions.GetPermissionRevision() != revoked.GetPermissionRevision() {
		t.Fatal("Auth did not return current independent permissions")
	}
	setPermission := func(permission permissionsv1.ScriptPermission, action permissionsv1.ScriptPermissionAction) {
		t.Helper()
		var result permissionsv1.ScriptPermissions
		httpCall(adminHost, http.MethodPost, "/auth-center/v1/script-permissions:manage", manager.session,
			&permissionsv1.ManageScriptPermissionRequest{
				SubjectAuthId: maintainer.authID, Permission: permission, Action: action,
				ExpectedPermissionRevision: proto.Int64(scriptPermissions.GetPermissionRevision()),
				Reason:                     "UC045 role isolation",
			}, &result, http.StatusOK)
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
	publisherView, err := queryClient.GetScriptVersion(maintainerRPC, versionQuery)
	if err != nil || publisherView.ContentText != nil || publisherView.GetVersion().GetDigest() != modified.GetDigest() {
		t.Fatal("native publisher read exposed content or lost summary", err)
	}
	httpCall(adminHost, http.MethodGet, reviewPath, maintainer.session, nil, nil, http.StatusForbidden)
	setPermission(reviewPermission, grantPermission)

	var resources queryv1.ListScriptResourcesResponse
	httpCall(adminHost, http.MethodGet, appResources, maintainer.session, nil, &resources, http.StatusOK)
	if len(resources.GetItems()) != 1 {
		t.Fatal("resource query failed")
	}
	var versions queryv1.ListScriptVersionsResponse
	httpCall(adminHost, http.MethodGet, resourcePath+"/versions", maintainer.session, nil, &versions, http.StatusOK)
	if len(versions.GetItems()) != 1 {
		t.Fatal("version query failed")
	}
	var queue queryv1.ListPendingScriptReviewsResponse
	header = httpCall(adminHost, http.MethodGet, queuePath, maintainer.session, nil, &queue, http.StatusOK)
	if len(queue.GetItems()) != 1 || header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("Admin pending queue lost result or cache policy")
	}
	httpCall("developer.uc002.test", http.MethodGet, queuePath, maintainer.session, nil, nil, http.StatusNotFound)
	var detail queryv1.GetScriptReviewResponse
	if code := gatewayWeb(t, ctx, maintainer.session, queryv1.ScriptQueryService_GetScriptReview_FullMethodName, reviewQuery, &detail); code != int(codes.OK) ||
		detail.GetContentText() != edited || len(detail.GetChecks()) != 4 || detail.GetCanDecide() || len(detail.GetConflicts()) != 2 {
		t.Fatal("gRPC-Web reviewer could not retrieve snapshot and conflicts", code)
	}
	setPermission(reviewPermission, revokePermission)
	httpCall(adminHost, http.MethodGet, reviewPath, maintainer.session, nil, nil, http.StatusForbidden)
	if view, err := queryClient.GetScriptVersion(maintainerRPC, versionQuery); err != nil || view.ContentText != nil {
		t.Fatal("review revoke changed independent publisher access", err)
	}
	setPermission(publishPermission, revokePermission)

	for _, permission := range []permissionsv1.ScriptPermission{
		permissionsv1.ScriptPermission_SCRIPT_PERMISSION_SUSPEND,
		permissionsv1.ScriptPermission_SCRIPT_PERMISSION_RESTORE,
	} {
		setPermission(permission, grantPermission)
		var resourceView resourcev1.ScriptResource
		httpCall(adminHost, http.MethodGet, resourcePath, maintainer.session, nil, &resourceView, http.StatusOK)
		if resourceView.GetResourceId() != resource.GetResourceId() {
			t.Fatal("operator cannot read resource")
		}
		httpCall(adminHost, http.MethodGet, versionPath, maintainer.session, nil, nil, http.StatusForbidden)
		setPermission(permission, revokePermission)
	}
	httpCall(adminHost, http.MethodGet, resourcePath, maintainer.session, nil, nil, http.StatusForbidden)

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
}
