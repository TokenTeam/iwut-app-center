package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	queryv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_query"
	resourcev1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_resource"
	versionv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/script_version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"iwut-app-center/internal/script/domain"
)

func TestE2E_UCAPP041_042_043_045_ScriptManagementHTTPAndGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	key, keyPath := e2eIdentity(t)
	addresses := e2eReserveAddresses(t, 2)
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:      os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv: database.Name(),
		config.HTTPAddrEnv:      addresses[0], config.GRPCAddrEnv: addresses[1],
		config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + keyPath,
		config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget,
	}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireApp(configuration)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(done)
	}()
	t.Cleanup(func() {
		defer cleanup()
		if err := app.Stop(); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Error(runErr)
			}
		case <-time.After(15 * time.Second):
			t.Error("App did not stop")
		}
	})
	e2eWaitForHTTPListener(t, ctx, addresses[0], done, &runErr)
	connection := e2eDialGRPC(t, ctx, addresses[1], done, &runErr)
	t.Cleanup(func() { _ = connection.Close() })
	maintainer := e2eSignReviewerIdentity(t, key, "script-maintainer", domain.MaintainPermission)
	developer := e2eSignIdentity(t, key, "developer-without-maintain", "APPROVED")
	maintainerContext := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, maintainer))
	resourceClient := resourcev1.NewScriptResourceServiceClient(connection)
	versionClient := versionv1.NewScriptVersionServiceClient(connection)

	httpCall := func(method, path, token string, body []byte, expectedStatus int, target proto.Message) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, "http://"+addresses[0]+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set(transport.IdentityHeader, token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 7<<20))
		if err != nil || response.StatusCode != expectedStatus || response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("%s %s got status %d, cache %q, error %v; expected %d and private, no-store",
				method, path, response.StatusCode, response.Header.Get("Cache-Control"), err, expectedStatus)
		}
		if target != nil {
			if err := protojson.Unmarshal(raw, target); err != nil {
				t.Fatal("decode response:", err)
			}
		}
	}
	marshal := func(message proto.Message) []byte {
		t.Helper()
		data, err := protojson.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	const resourcePath = "/v1/script-resources"
	httpCall(http.MethodPost, resourcePath, "", []byte("{"), http.StatusUnauthorized, nil)
	httpCall(http.MethodPost, resourcePath, developer, []byte("{"), http.StatusForbidden, nil)
	httpCall(http.MethodPost, resourcePath+"?name=Override", maintainer, []byte(`{"name":"A","purpose":"B"}`), http.StatusBadRequest, nil)
	httpCall(http.MethodPost, resourcePath, maintainer, []byte(`{"name":"A","name":"B","purpose":"C"}`), http.StatusBadRequest, nil)
	httpCall(http.MethodPost, resourcePath, maintainer, []byte(`{"name":"A","purpose":"B","permissions":["app.script.maintain"]}`), http.StatusBadRequest, nil)
	var resource resourcev1.ScriptResource
	httpCall(http.MethodPost, resourcePath, maintainer, []byte(`{"name":"  Crawler  ","purpose":"Purpose"}`), http.StatusOK, &resource)
	if resource.GetResourceId() == "" || resource.GetName() != "Crawler" || resource.GetRevision() != 1 {
		t.Fatal("invalid resource creation response")
	}
	updated, err := resourceClient.ReplaceScriptResourceMetadata(maintainerContext, &resourcev1.ReplaceScriptResourceMetadataRequest{
		ResourceId: resource.GetResourceId(),
		Command:    &resourcev1.ReplaceScriptResourceMetadataCommand{ExpectedRevision: 1, Name: "Crawler 2", Purpose: "New purpose"},
	})
	if err != nil || updated.GetName() != "Crawler 2" || updated.GetRevision() != 2 {
		t.Fatalf("native metadata replace returned %v", err)
	}

	versionsPath := resourcePath + "/" + resource.GetResourceId() + "/versions"
	const original = "\ufeff{\r\n  \"b\": 2, \"a\":1\r\n}\r\n "
	body := marshal(&versionv1.CreateScriptVersionDraftInput{VersionLabel: "draft-1", ContentFormat: "json", ContentText: original})
	var draft versionv1.ScriptVersion
	httpCall(http.MethodPost, versionsPath, maintainer, body, http.StatusOK, &draft)
	assertScriptDraftBytes(t, &draft, original, 1)
	httpCall(http.MethodPost, versionsPath, maintainer, body, http.StatusConflict, nil)
	httpCall(http.MethodPost, versionsPath, maintainer, []byte(`{"versionLabel":"bad","contentFormat":"json","contentText":"\ud800"}`), http.StatusBadRequest, nil)

	const replacement = "{\"a\":1,\"b\":2}"
	replace := &versionv1.ReplaceScriptVersionDraftRequest{
		ResourceId: resource.GetResourceId(), VersionId: draft.GetVersionId(),
		Command: &versionv1.ReplaceScriptVersionDraftInput{ExpectedRevision: 1, ContentFormat: "json", ContentText: replacement},
	}
	modified, err := versionClient.ReplaceScriptVersionDraft(maintainerContext, replace)
	if err != nil {
		t.Fatal(err)
	}
	assertScriptDraftBytes(t, modified, replacement, 2)
	if _, err := versionClient.ReplaceScriptVersionDraft(maintainerContext, replace); status.Code(err) != codes.Aborted {
		t.Fatal("stale native replacement did not conflict")
	}
	replace.Command.ExpectedRevision = 2
	var noop versionv1.ScriptVersion
	httpCall(http.MethodPut, versionsPath+"/"+draft.GetVersionId()+"/draft", maintainer, marshal(replace.Command), http.StatusOK, &noop)
	if !proto.Equal(&noop, modified) {
		t.Fatal("no-op did not return persisted response")
	}
	forged := proto.CloneOf(replace)
	forged.Command.ProtoReflect().SetUnknown([]byte{0x20, 0x01})
	if _, err := versionClient.ReplaceScriptVersionDraft(maintainerContext, forged); status.Code(err) != codes.InvalidArgument {
		t.Fatal("unknown gRPC command field accepted")
	}

	large := strings.Repeat("\x01", domain.MaxContentBytes-1) + "x"
	var largeDraft versionv1.ScriptVersion
	httpCall(http.MethodPost, versionsPath, maintainer, marshal(&versionv1.CreateScriptVersionDraftInput{
		VersionLabel: "boundary", ContentFormat: "json", ContentText: large,
	}), http.StatusOK, &largeDraft)
	assertScriptDraftBytes(t, &largeDraft, large, 1)

	submitPath := versionsPath + "/" + draft.GetVersionId() + "/reviews"
	httpCall(http.MethodPost, submitPath, maintainer, []byte(`{"expectedRevision":"2","policyVersion":"caller-selected"}`), http.StatusBadRequest, nil)
	var submitted versionv1.ScriptVersionReviewSubmission
	httpCall(http.MethodPost, submitPath, maintainer, []byte(`{"expectedRevision":"2"}`), http.StatusOK, &submitted)
	assertScriptSubmission(t, &submitted, 3)
	httpCall(http.MethodPost, submitPath, maintainer, []byte(`{"expectedRevision":"2"}`), http.StatusConflict, nil)
	secondSubmission := &versionv1.SubmitScriptVersionReviewRequest{
		ResourceId: resource.GetResourceId(), VersionId: largeDraft.GetVersionId(),
		Command: &versionv1.SubmitScriptVersionReviewInput{ExpectedRevision: 1},
	}
	developerContext := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, developer))
	if _, err := versionClient.SubmitScriptVersionReview(developerContext, secondSubmission); status.Code(err) != codes.PermissionDenied {
		t.Fatal("developer without script maintain could submit", status.Code(err))
	}
	second, err := versionClient.SubmitScriptVersionReview(maintainerContext, secondSubmission)
	if err != nil {
		t.Fatal("native script submission:", status.Code(err))
	}
	assertScriptSubmission(t, second, 2)
	replace.Command.ExpectedRevision = 3
	if _, err := versionClient.ReplaceScriptVersionDraft(maintainerContext, replace); status.Code(err) != codes.Aborted {
		t.Fatal("submitted script could still be edited", status.Code(err))
	}
	reviewer := e2eSignReviewerIdentity(t, key, "script-reviewer", "app.script.review")
	conflicted := e2eSignReviewerIdentity(t, key, "script-maintainer", "app.script.review")
	publisher := e2eSignReviewerIdentity(t, key, "script-publisher", "app.script.publish")
	suspender := e2eSignReviewerIdentity(t, key, "script-suspender", "app.script.suspend")
	restorer := e2eSignReviewerIdentity(t, key, "script-restorer", "app.script.restore")
	reviewPath := submitPath + "/" + submitted.GetReviewId()
	for _, caller := range []string{maintainer, reviewer, publisher, suspender, restorer} {
		var metadata resourcev1.ScriptResource
		httpCall(http.MethodGet, resourcePath+"/"+resource.GetResourceId(), caller, nil, http.StatusOK, &metadata)
		if metadata.GetResourceId() != resource.GetResourceId() {
			t.Fatal("wrong resource")
		}
	}
	httpCall(http.MethodGet, versionsPath+"?limit=bad", developer, nil, http.StatusForbidden, nil)
	httpCall(http.MethodGet, versionsPath+"?limit=bad", "", nil, http.StatusUnauthorized, nil)
	httpCall(http.MethodGet, versionsPath+"?limit=0", maintainer, nil, http.StatusBadRequest, nil)
	httpCall(http.MethodGet, versionsPath+"?limit=1&limit=2", maintainer, nil, http.StatusBadRequest, nil)
	httpCall(http.MethodGet, versionsPath+"?cursor%20state=x", maintainer, nil, http.StatusBadRequest, nil)
	httpCall(http.MethodGet, versionsPath+"?resourceId="+resource.GetResourceId(), maintainer, nil, http.StatusBadRequest, nil)

	var resources queryv1.ListScriptResourcesResponse
	httpCall(http.MethodGet, resourcePath+"?limit=1", suspender, nil, http.StatusOK, &resources)
	if len(resources.GetItems()) != 1 {
		t.Fatal("resource list is missing resource")
	}
	var summary queryv1.GetScriptVersionResponse
	httpCall(http.MethodGet, versionsPath+"/"+draft.GetVersionId(), publisher, nil, http.StatusOK, &summary)
	if summary.ContentText != nil || summary.GetVersion().GetDigest() != modified.GetDigest() {
		t.Fatal("publisher received text or lost digest")
	}
	httpCall(http.MethodGet, reviewPath, publisher, nil, http.StatusForbidden, nil)
	httpCall(http.MethodGet, versionsPath+"/"+draft.GetVersionId(), suspender, nil, http.StatusForbidden, nil)
	httpCall(http.MethodGet, reviewPath, developer, nil, http.StatusForbidden, nil)
	httpCall(http.MethodGet, "/v1/script-reviews/pending", maintainer, nil, http.StatusForbidden, nil)
	var queue queryv1.ListPendingScriptReviewsResponse
	httpCall(http.MethodGet, "/v1/script-reviews/pending?resourceId="+resource.GetResourceId()+"&limit=1", reviewer, nil, http.StatusOK, &queue)
	if len(queue.GetItems()) != 1 || queue.GetNextCursor() == "" {
		t.Fatal("pending queue did not apply limit")
	}

	var detail queryv1.GetScriptReviewResponse
	httpCall(http.MethodGet, reviewPath, conflicted, nil, http.StatusOK, &detail)
	if detail.GetCanDecide() || len(detail.GetConflicts()) != 2 || detail.GetContentText() != replacement ||
		detail.GetVersion().GetRevision() != 3 || len(detail.GetChecks()) != 4 || detail.GetChecks()[0].GetDescription() == "" {
		t.Fatal("conflicted reviewer detail lost snapshot, checks or conflict")
	}
	reviewerContext := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, reviewer))
	queryClient := queryv1.NewScriptQueryServiceClient(connection)
	largeReview, err := queryClient.GetScriptReview(reviewerContext, &queryv1.GetScriptReviewRequest{
		ResourceId: resource.GetResourceId(), VersionId: largeDraft.GetVersionId(), ReviewId: second.GetReviewId(),
	})
	if err != nil || !largeReview.GetCanDecide() || largeReview.GetContentText() != large {
		t.Fatal("native reviewer detail could not retrieve exact boundary bytes", err)
	}

	e2eAssertCollectionCount(t, database, "script_resources", 1)
	e2eAssertCollectionCount(t, database, "script_resource_audits", 2)
	e2eAssertCollectionCount(t, database, "script_versions", 2)
	e2eAssertCollectionCount(t, database, "script_version_audits", 5)
	e2eAssertCollectionCount(t, database, "script_reviews", 2)
}

func assertScriptSubmission(t *testing.T, result *versionv1.ScriptVersionReviewSubmission, revision int64) {
	t.Helper()
	if result.GetReviewId() == "" || result.GetRevision() != revision || result.GetState() != versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_SUBMITTED ||
		result.GetPolicyVersion() != domain.InitialPolicyVersion || result.GetSubmittedAt() == nil || !result.GetSubmittedAt().IsValid() {
		t.Fatalf("invalid script submission at revision %d", revision)
	}
}

func assertScriptDraftBytes(t *testing.T, draft *versionv1.ScriptVersion, text string, revision int64) {
	t.Helper()
	sum := sha256.Sum256([]byte(text))
	if draft.GetVersionId() == "" || draft.GetState() != versionv1.ScriptVersionState_SCRIPT_VERSION_STATE_DRAFT ||
		draft.GetContentFormat() != "json" || draft.GetContentText() != text ||
		draft.GetDigest() != hex.EncodeToString(sum[:]) || draft.GetRevision() != revision {
		t.Fatalf("draft byte or state mismatch at revision %d", revision)
	}
}
