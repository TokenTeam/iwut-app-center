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

	appv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	ops "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_operations"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_platform_query"
	permission "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/application_operations_permission"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestE2E_UCAPP051_PlatformApplicationsLocalImages(t *testing.T) {
	if os.Getenv("UC051_LOCAL_IMAGES") != "1" {
		t.Skip("run gateway-auth integration --platform-applications with fixed local images")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	manager, owner, reader := registerAuthFixture(t, ctx), registerAuthFixture(t, ctx), registerAuthFixture(t, ctx)
	output, err := exec.CommandContext(ctx, "docker", "exec", os.Getenv("UC002_AUTH_CONTAINER_NAME"), "/app/auth-center", "bootstrap-platform-admin", "--auth-id", manager.authID).CombinedOutput()
	if path := os.Getenv("UC002_BOOTSTRAP_LOG"); path != "" {
		_ = os.WriteFile(path, output, 0600)
	}
	var bootstrap struct {
		Applied bool   `json:"applied"`
		AuthID  string `json:"authId"`
	}
	if err != nil || json.Unmarshal(output, &bootstrap) != nil || !bootstrap.Applied || bootstrap.AuthID != manager.authID {
		t.Fatal("administrator bootstrap", err)
	}
	mongoClient, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnv)))
	if err != nil {
		t.Fatal(err)
	}
	defer mongoClient.Disconnect(context.Background())
	// Only qualification is controlled fixture data. Registration, credentials,
	// Sessions, administrator bootstrap and permission mutations use Auth itself.
	updated, err := mongoClient.Database(os.Getenv("AUTH_CENTER_INTEGRATION_DATABASE")).Collection("auth_principals").UpdateOne(ctx, bson.M{"authId": owner.authID}, bson.M{"$set": bson.M{"developerStatus": "APPROVED", "developerRevision": int64(1), "updatedAt": time.Now().UTC().Truncate(time.Millisecond)}})
	if err != nil || updated.ModifiedCount != 1 {
		t.Fatal("owner qualification", err)
	}
	listener, err := net.Listen("tcp", os.Getenv("UC041_ADMIN_FORWARD_AUTH_ADDR"))
	if err != nil {
		t.Fatal(err)
	}
	forward := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("iwut_uc051_fixture")
		if r.URL.Path != "/internal/forward-auth" || err != nil || (cookie.Value != manager.session && cookie.Value != owner.session && cookie.Value != reader.session) {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("X-Iwut-Session", cookie.Value)
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() { _ = forward.Serve(listener) }()
	t.Cleanup(func() { _ = forward.Close() })
	edge, err := grpc.NewClient(os.Getenv("UC002_EDGE_GRPC_ADDR"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Close()
	sessionContext := func(session string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", session))
	}
	managerCtx, ownerCtx, readerCtx := sessionContext(manager.session), sessionContext(owner.session), sessionContext(reader.session)
	application, err := appv1.NewApplicationClient(edge).CreateApplication(ownerCtx, &appv1.CreateApplicationRequest{Name: "Platform_Real_Grant"})
	if err != nil {
		t.Fatal("real application", err)
	}
	query := pb.NewApplicationPlatformQueryServiceClient(edge)
	request := &pb.SearchPlatformApplicationsRequest{Query: &pb.SearchPlatformApplicationsQuery{}}
	requireDenied := func(call context.Context) {
		t.Helper()
		if _, err := query.SearchPlatformApplications(call, request); status.Code(err) != codes.PermissionDenied {
			t.Fatal("missing exact search permission", err)
		}
	}
	for _, call := range []context.Context{managerCtx, ownerCtx, readerCtx} {
		requireDenied(call)
	}
	permissions := permission.NewApplicationOperationsPermissionServiceClient(edge)
	initial, err := permissions.GetApplicationOperationsPermissions(managerCtx, &permission.GetApplicationOperationsPermissionsRequest{SubjectAuthId: reader.authID})
	if err != nil {
		t.Fatal("read current grants", err)
	}
	revision := initial.PermissionRevision
	manage := func(id string, action permission.ApplicationOperationsPermissionAction) {
		t.Helper()
		result, err := permissions.ManageApplicationOperationsPermission(managerCtx, &permission.ManageApplicationOperationsPermissionRequest{SubjectAuthId: reader.authID, Permission: id, Action: action, ExpectedPermissionRevision: proto.Int64(revision), Reason: "platform application directory integration"})
		if err != nil || result.PermissionRevision != revision+1 {
			t.Fatal("formal permission mutation", id, err)
		}
		revision = result.PermissionRevision
	}
	grant, revoke := permission.ApplicationOperationsPermissionAction_APPLICATION_OPERATIONS_PERMISSION_ACTION_GRANT, permission.ApplicationOperationsPermissionAction_APPLICATION_OPERATIONS_PERMISSION_ACTION_REVOKE
	manage("app.application.suspend", grant)
	requireDenied(readerCtx)
	manage("app.application.restore", grant)
	requireDenied(readerCtx)
	manage("app.application.search", grant)
	own, err := permissions.GetOwnApplicationOperationsPermissions(readerCtx, &permission.GetOwnApplicationOperationsPermissionsRequest{})
	if err != nil || len(own.EffectivePermissions) != 3 || own.PermissionRevision != revision {
		t.Fatal("own discovery", err)
	}
	check := func(page *pb.PlatformApplicationPage) {
		t.Helper()
		if len(page.GetItems()) != 1 || page.Items[0].ApplicationId != application.Id || page.Items[0].AdminAuthId != owner.authID {
			t.Fatal("minimum directory projection")
		}
	}
	page, err := query.SearchPlatformApplications(readerCtx, request)
	if err != nil {
		t.Fatal("same Session after grant", err)
	}
	check(page)
	const path = "/app-center/v1/platform/applications:search"
	code, headers, raw := gatewayHTTP(t, ctx, http.MethodPost, path, reader.session, []byte(`{}`), nil)
	if code != 200 || headers.Get("Cache-Control") != "private, no-store" || protojson.Unmarshal(raw, page) != nil {
		t.Fatal("HTTP search", code)
	}
	check(page)
	var web pb.PlatformApplicationPage
	if code := gatewayWeb(t, ctx, reader.session, pb.ApplicationPlatformQueryService_SearchPlatformApplications_FullMethodName, request, &web); code != int(codes.OK) {
		t.Fatal("gRPC-Web search", code)
	}
	check(&web)
	summary, err := query.GetPlatformApplicationSummary(readerCtx, &pb.GetPlatformApplicationSummaryRequest{ApplicationId: application.Id})
	if err != nil || summary.Name != application.Name {
		t.Fatal("native summary", err)
	}
	detailPath := "/app-center/v1/platform/applications/" + application.Id
	code, _, raw = gatewayHTTP(t, ctx, http.MethodGet, detailPath, reader.session, nil, nil)
	if code != 200 || protojson.Unmarshal(raw, summary) != nil || summary.ApplicationId != application.Id {
		t.Fatal("HTTP summary", code)
	}
	var webSummary pb.PlatformApplicationSummary
	if code := gatewayWeb(t, ctx, reader.session, pb.ApplicationPlatformQueryService_GetPlatformApplicationSummary_FullMethodName, &pb.GetPlatformApplicationSummaryRequest{ApplicationId: application.Id}, &webSummary); code != int(codes.OK) || webSummary.ApplicationId != application.Id {
		t.Fatal("gRPC-Web summary", code)
	}
	for _, host := range []string{"admin.uc002.test", "developer.uc002.test"} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+os.Getenv("UC002_EDGE_HTTP_ADDR")+path, bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "iwut_uc051_fixture", Value: reader.session})
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		want := 200
		if host == "developer.uc002.test" {
			want = 404
		}
		if readErr != nil || response.StatusCode != want {
			t.Fatal("Console surface", host, response.StatusCode)
		}
		if want == 200 {
			if protojson.Unmarshal(body, page) != nil {
				t.Fatal("Console response")
			}
			check(page)
		}
	}
	manage("app.application.suspend", revoke)
	manage("app.application.restore", revoke)
	if _, err := ops.NewApplicationOperationsServiceClient(edge).GetApplicationPlatformAvailability(readerCtx, &ops.GetApplicationPlatformAvailabilityRequest{ApplicationId: application.Id}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("search became operation permission", err)
	}
	if page, err := query.SearchPlatformApplications(readerCtx, request); err != nil {
		t.Fatal("independent operation revocation", err)
	} else {
		check(page)
	}
	manage("app.application.search", revoke)
	requireDenied(readerCtx)
	own, err = permissions.GetOwnApplicationOperationsPermissions(readerCtx, &permission.GetOwnApplicationOperationsPermissionsRequest{})
	if err != nil || len(own.EffectivePermissions) != 0 || own.PermissionRevision != revision {
		t.Fatal("same Session after revoke", err)
	}
	code, _, _ = gatewayHTTP(t, ctx, http.MethodPost, path, "", []byte(`{}`), nil)
	if code != 401 {
		t.Fatal("missing Session", code)
	}
}
