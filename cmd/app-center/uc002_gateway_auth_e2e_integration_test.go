package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	av "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	vv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_version"
	idv "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/identity"
	scv "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/scope_catalog"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	aa "iwut-app-center/internal/adapter/auth"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	vd "iwut-app-center/internal/version/domain"
	vp "iwut-app-center/internal/version/port"
)

type uc002Clock struct{ at time.Time }

func (c *uc002Clock) Now() time.Time {
	if c.at.IsZero() {
		return time.Now()
	}
	return c.at
}

type uc002ObservedSource struct {
	source aa.ScopeCatalogSnapshotSource
	calls  int
}

func (s *uc002ObservedSource) FetchScopeCatalogSnapshot(ctx context.Context) (aa.ScopeCatalogSnapshot, error) {
	s.calls++
	return s.source.FetchScopeCatalogSnapshot(ctx)
}

// Fixture records represent an already authenticated device Session. Session ->
// USER issuance is performed by the real Auth service; no USER JWS is test-signed.
// Registration, mail delivery and password UI are separate Auth/Gateway packages.
func uc002SeedSession(t *testing.T, db *m.Database, subject, developer string) string {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := db.Collection("auth_principals").InsertOne(ctx, bson.M{"authId": subject, "principalType": "USER", "accountStatus": "ACTIVE", "accountRevision": int64(1), "minimumCredentialRevision": int64(1), "developerStatus": developer, "developerRevision": int64(1), "permissionRevision": int64(1), "permissions": bson.A{}, "createdAt": now, "updatedAt": now}); err != nil {
		t.Fatal("seed Auth principal failed")
	}
	key, fingerprint := uc033DeviceCredential(t)
	credentialID, sessionID := uuid.NewString(), uuid.NewString()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if _, err := db.Collection("auth_credentials").InsertOne(ctx, bson.M{"credentialId": credentialID, "authId": subject, "protocolVersion": "iwut-device-v1", "publicKey": key, "fingerprint": fingerprint, "credentialRevision": int64(1), "label": "", "labelRevision": int64(0), "createdAt": now}); err != nil {
		t.Fatal("seed Auth credential failed")
	}
	if _, err := db.Collection("auth_sessions").InsertOne(ctx, bson.M{"accountRevision": int64(1), "sessionId": sessionID, "authId": subject, "authenticationMethod": "DEVICE_CREDENTIAL", "credentialId": credentialID, "tokenDigest": digest[:], "authenticatedAt": now, "createdAt": now, "lastUsedAt": now, "expiresAt": now.Add(time.Hour)}); err != nil {
		t.Fatal("seed Auth Session failed")
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func uc002AuthConnection(t *testing.T, key []byte, id, kid, target string) *grpc.ClientConn {
	t.Helper()
	signer, err := aa.NewServiceIdentitySigner(aa.ServiceIdentitySignerConfig{ServiceID: id, KID: kid, Audience: "iwut-auth-center", TTL: time.Minute, PrivateKeyPEM: key, Clock: &uc002Clock{}})
	if err != nil {
		t.Fatal("service signer failed")
	}
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(signer.UnaryClientInterceptor))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func uc002HTTPRequest(t *testing.T, ctx context.Context, path, session string, body []byte, extra http.Header) (int, http.Header, []byte) {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+os.Getenv("UC002_EDGE_HTTP_ADDR")+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "api.uc002.test"
	r.Header.Set("Content-Type", "application/json")
	if session != "" {
		r.Header.Set("X-Iwut-Session", session)
	}
	for name, values := range extra {
		r.Header[name] = values
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
	if err != nil {
		t.Fatal("edge HTTP request failed")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("X-Iwut-Identity") != "" || bytes.Contains(data, []byte(session)) && session != "" {
		t.Fatal("terminal response disclosed credentials")
	}
	return resp.StatusCode, resp.Header, data
}
func uc002VersionRequest(app, label, scope string) *vv.CreateApplicationVersionRequest {
	return &vv.CreateApplicationVersionRequest{ApplicationId: app, VersionLabel: label, LaunchUrl: "https://apps.whut.edu.cn/uc002", RpcApiMinVersion: 1, RpcApiMaxVersionExclusive: 2, RequiredCapabilities: []string{}, RequiredScopes: []string{scope}, OptionalScopes: []string{}, OauthRedirects: &vv.OAuthRedirectConfiguration{PkceRedirectUris: []string{"https://apps.whut.edu.cn/uc002/callback"}, ConfidentialRedirectUris: []string{}}}
}
func uc002Web(t *testing.T, ctx context.Context, session string, request *vv.CreateApplicationVersionRequest) (*vv.CreateApplicationVersionResponse, int) {
	t.Helper()
	payload, err := proto.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+os.Getenv("UC002_EDGE_HTTP_ADDR")+vv.ApplicationVersion_CreateApplicationVersion_FullMethodName, bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "api.uc002.test"
	r.Header.Set("Content-Type", "application/grpc-web+proto")
	r.Header.Set("X-Grpc-Web", "1")
	if session != "" {
		r.Header.Set("X-Iwut-Session", session)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
	if err != nil {
		t.Fatal("gRPC-Web request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("gRPC-Web HTTP status=%d", resp.StatusCode)
	}
	if resp.Header.Get("X-Iwut-Identity") != "" {
		t.Fatal("gRPC-Web disclosed identity")
	}
	result := &vv.CreateApplicationVersionResponse{}
	code := -1
	readStatus := func(value string) {
		if value == "" {
			return
		}
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &code); err != nil {
			t.Fatal("invalid gRPC-Web status")
		}
	}
	// Trailers-only failures may carry status in HTTP headers; successful
	// responses normally carry it in the gRPC-Web trailers frame.
	readStatus(resp.Header.Get("Grpc-Status"))
	for len(body) >= 5 {
		size := int(binary.BigEndian.Uint32(body[1:5]))
		if size > len(body)-5 {
			t.Fatal("truncated gRPC-Web frame")
		}
		part := body[5 : 5+size]
		if body[0]&0x80 != 0 {
			for _, line := range strings.Split(string(part), "\r\n") {
				if strings.HasPrefix(strings.ToLower(line), "grpc-status:") {
					readStatus(strings.TrimSpace(line[len("grpc-status:"):]))
				}
			}
		} else if proto.Unmarshal(part, result) != nil {
			t.Fatal("invalid gRPC-Web data")
		}
		body = body[5+size:]
	}
	if code < 0 {
		readStatus(resp.Trailer.Get("Grpc-Status"))
	}
	if len(body) != 0 || code < 0 {
		t.Fatal("missing gRPC-Web status")
	}
	return result, code
}
func TestE2E_UCAPP002GatewayAuthIntegration(t *testing.T) {
	if os.Getenv("UC002_EDGE_HTTP_ADDR") == "" {
		t.Skip("run scripts/test-uc002-gateway-auth-integration.sh")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	mongo, db := e2eIsolatedDatabase(t, ctx, true)
	authDB := mongo.Database(os.Getenv("AUTH_CENTER_INTEGRATION_DATABASE"))
	const owner = "uc002-owner"
	ownerSession := uc002SeedSession(t, authDB, owner, "APPROVED")
	otherSession := uc002SeedSession(t, authDB, "uc002-other", "APPROVED")
	suspendedSession := uc002SeedSession(t, authDB, "uc002-suspended", "SUSPENDED")
	gatewayKey, err := os.ReadFile(os.Getenv("UC002_GATEWAY_KEY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	gatewayAuth := uc002AuthConnection(t, gatewayKey, "iwut-gateway", "uc002-gateway", os.Getenv("AUTH_CENTER_INTEGRATION_TARGET"))
	issued, err := idv.NewUserIdentityServiceClient(gatewayAuth).IssueUserIdentityFromSession(metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", ownerSession)), &idv.IssueUserIdentityFromSessionRequest{Audience: e2eAudience})
	if err != nil || issued.GetIdentityJws() == "" {
		t.Fatal("real Auth USER issuance failed", status.Code(err))
	}
	t.Log("BR-VER-002: real Auth issued App-audience USER from an active device Session")
	conf, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: os.Getenv("UC002_APP_HTTP_ADDR"), config.GRPCAddrEnv: os.Getenv("UC002_APP_GRPC_ADDR"), config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: "uc002-auth=" + os.Getenv("AUTH_CENTER_IDENTITY_PUBLIC_KEY_PATH"), config.AuthScopeCatalogTargetEnv: os.Getenv("AUTH_CENTER_INTEGRATION_TARGET"), config.ScopeCatalogCacheTTLEnv: "1ns", config.ServiceIdentityIDEnv: os.Getenv(config.ServiceIdentityIDEnv), config.ServiceIdentityKIDEnv: os.Getenv(config.ServiceIdentityKIDEnv), config.ServiceIdentityPrivateKeyEnv: os.Getenv(config.ServiceIdentityPrivateKeyEnv)}))
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
		_ = app.Stop()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("App stop timeout")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, conf.HTTPAddr, done, &runErr)
	code, _, body := uc002HTTPRequest(t, ctx, "/app-center/v1/applications", ownerSession, []byte(`{"name":"UC002_Real_Integration"}`), nil)
	var created av.CreateApplicationResponse
	if code != 201 || protojson.Unmarshal(body, &created) != nil {
		t.Fatalf("create application through Gateway status=%d", code)
	}
	appID := created.GetId()
	create := func(session, label, scope string, extra http.Header) (int, http.Header, []byte) {
		t.Helper()
		r := uc002VersionRequest(appID, label, scope)
		payload, e := protojson.Marshal(r)
		if e != nil {
			t.Fatal(e)
		}
		return uc002HTTPRequest(t, ctx, "/app-center/v1/applications/"+appID+"/versions", session, payload, extra)
	}
	check := func(r *vv.CreateApplicationVersionResponse, sequence int32) {
		t.Helper()
		id, err := uuid.Parse(r.GetVersionId())
		if err != nil || id.Version() != 7 {
			t.Fatal("Version identity is not UUIDv7")
		}
		if r.GetApplicationId() != appID || r.GetSequence() != sequence || r.GetReviewStatus() != "DRAFT" || r.GetRevision() != 1 || r.GetCreatedBy() != owner || r.GetUpdatedBy() != owner || r.GetCreatedAt() == nil || !r.GetCreatedAt().AsTime().Equal(r.GetUpdatedAt().AsTime()) || r.GetOauthRedirects() == nil {
			t.Fatal("created Version has incorrect identity, state or audit")
		}
		var stored bson.M
		if err := db.Collection("application_versions").FindOne(ctx, bson.M{"versionId": r.VersionId}).Decode(&stored); err != nil || stored["createdBy"] != owner {
			t.Fatal("Version missing or wrong attribution")
		}
		var oauth bson.M
		if err := db.Collection("application_version_oauth_configs").FindOne(ctx, bson.M{"applicationVersionId": r.VersionId}).Decode(&oauth); err != nil || oauth["applicationId"] != appID {
			t.Fatal("dependent OAuth configuration missing")
		}
	}
	// Traefik scrubs the forged header, exchanges the real Session and strips the prefix.
	code, h, body := create(ownerSession, "http", "items.read", http.Header{"X-Iwut-Identity": []string{"forged-user-jws"}})
	var first vv.CreateApplicationVersionResponse
	if code != 201 || h.Get("ETag") != `"1"` || protojson.Unmarshal(body, &first) != nil {
		t.Fatalf("HTTP create=%d", code)
	}
	check(&first, 1)
	edge, err := grpc.NewClient(os.Getenv("UC002_EDGE_GRPC_ADDR"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer edge.Close()
	client := vv.NewApplicationVersionClient(edge)
	sessionCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", ownerSession))
	native, err := client.CreateApplicationVersion(sessionCtx, uc002VersionRequest(appID, "native", "items.read"))
	if err != nil {
		t.Fatal("native gRPC create failed", status.Code(err))
	}
	check(native, 2)
	web, webCode := uc002Web(t, ctx, ownerSession, uc002VersionRequest(appID, "web", "items.read"))
	if webCode != 0 {
		t.Fatalf("gRPC-Web status=%d", webCode)
	}
	check(web, 3)
	before := uc002AppFacts(t, ctx, db, appID)
	for name, want := range map[string]int{"applications": 1, "application_versions": 3, "application_version_oauth_configs": 3, "application_reviews": 0, "application_publications": 0, "application_publication_history": 0} {
		if len(before[name]) != want {
			t.Fatalf("unexpected initial %s count", name)
		}
	}
	for _, tc := range []struct {
		session, label, scope, reason string
		extra                         http.Header
		want                          int
	}{
		{"", "missing", "items.read", "SESSION_INVALID", nil, 401},
		{otherSession, "other", "items.read", transport.ReasonApplicationAdminRequired, nil, 403},
		{suspendedSession, "suspended", "items.read", transport.ReasonDeveloperApprovalRequired, nil, 403},
		{ownerSession, "unknown", "items.unknown", transport.ReasonInvalidApplicationScope, nil, 400},
		{ownerSession, "disabled", "items.disabled", transport.ReasonInvalidApplicationScope, nil, 400},
		{ownerSession, "http", "items.read", transport.ReasonApplicationVersionLabelExists, nil, 409},
		{ownerSession, "forbidden", "items.read", "CREDENTIAL_NOT_ALLOWED", http.Header{"Authorization": []string{"Bearer forged"}}, 400},
	} {
		code, _, body := create(tc.session, tc.label, tc.scope, tc.extra)
		if code != tc.want || !bytes.Contains(body, []byte(tc.reason)) {
			t.Fatalf("negative %s got=%d expected=%d reason=%s", tc.label, code, tc.want, tc.reason)
		}
	}
	if _, err := client.CreateApplicationVersion(ctx, uc002VersionRequest(appID, "native-no-session", "items.read")); status.Code(err) != codes.Unauthenticated {
		t.Fatal("native gRPC accepted missing Session")
	}
	if _, code := uc002Web(t, ctx, "", uc002VersionRequest(appID, "web-no-session", "items.read")); code != 16 {
		t.Fatalf("gRPC-Web missing Session=%d", code)
	}
	if !reflect.DeepEqual(before, uc002AppFacts(t, ctx, db, appID)) {
		t.Fatal("rejected commands changed App facts or consumed sequence")
	}
	appKey, err := base64.StdEncoding.Strict().DecodeString(os.Getenv(config.ServiceIdentityPrivateKeyEnv))
	if err != nil {
		t.Fatal(err)
	}
	appAuth := uc002AuthConnection(t, appKey, "iwut-app-center", "uc002-app", os.Getenv("AUTH_CENTER_INTEGRATION_TARGET"))
	source, err := aa.NewGRPCScopeCatalogSnapshotSource(appAuth)
	if err != nil {
		t.Fatal(err)
	}
	observed := &uc002ObservedSource{source: source}
	clock := &uc002Clock{at: time.Now()}
	cache, err := aa.NewScopeCatalogCache(observed, clock, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := cache.EnsureAllRequestable(ctx, []vd.ScopeName{"items.read"})
	if err != nil || revision <= 0 {
		t.Fatal("persistent Auth Catalog could not be consumed")
	}
	var catalog bson.Raw
	if err := authDB.Collection("auth_oauth_catalog").FindOne(ctx, bson.M{"_id": "current"}).Decode(&catalog); err != nil {
		t.Fatal("production Auth Catalog document missing")
	}
	data, ok := catalog.Lookup("data").DocumentOK()
	if !ok {
		t.Fatal("invalid Auth Catalog fixture shape")
	}
	storedRevision, ok := data.Lookup("revision").Int64OK()
	if !ok || int64(revision) != storedRevision {
		t.Fatal("Scope snapshot does not match persistent revision")
	}
	changedAt := time.Now().UTC().Truncate(time.Millisecond)
	if _, err := authDB.Collection("auth_oauth_catalog").UpdateOne(ctx, bson.M{"_id": "current"}, bson.M{"$set": bson.M{"data.revision": storedRevision + 1, "data.generatedat": changedAt, "data.scopes.$[scope].enabled": false}}, options.UpdateOne().SetArrayFilters([]any{bson.M{"scope.name": "items.read"}})); err != nil {
		t.Fatal("isolated Catalog update failed")
	}
	got, err := cache.EnsureAllRequestable(ctx, []vd.ScopeName{"items.read"})
	if err != nil || got != revision || observed.calls != 1 {
		t.Fatal("TTL did not reuse the accepted snapshot")
	}
	clock.at = clock.at.Add(time.Minute)
	got, err = cache.EnsureAllRequestable(ctx, []vd.ScopeName{"items.read"})
	if !errors.Is(err, vp.ErrScopeNotRequestable) || int64(got) != storedRevision+1 || observed.calls != 2 {
		t.Fatal("expired cache did not consume the disabled Scope")
	}
	code, _, _ = create(ownerSession, "newly-disabled", "items.read", nil)
	if code != 400 {
		t.Fatalf("fresh App cache accepted disabled scope=%d", code)
	}
	// Auth keeps issuing USER identities while its Scope source is unavailable.
	if _, err := authDB.Collection("auth_oauth_catalog").DeleteOne(ctx, bson.M{"_id": "current"}); err != nil {
		t.Fatal(err)
	}
	clock.at = clock.at.Add(time.Minute)
	if _, err := cache.EnsureAllRequestable(ctx, []vd.ScopeName{"items.read"}); !errors.Is(err, vp.ErrScopeCatalogUnavailable) {
		t.Fatal("expired cache used stale data on Auth failure")
	}
	code, _, body = create(ownerSession, "catalog-down", "items.read", nil)
	if code != 503 || !bytes.Contains(body, []byte(transport.ReasonScopeCatalogUnavailable)) {
		t.Fatal("Catalog failure did not fail closed at App")
	}
	// Restoring an older positive revision is rejected by both real consumers.
	if _, err := authDB.Collection("auth_oauth_catalog").InsertOne(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.EnsureAllRequestable(ctx, []vd.ScopeName{"items.read"}); !errors.Is(err, vp.ErrScopeCatalogUnavailable) {
		t.Fatal("Catalog cache accepted revision rollback")
	}
	code, _, body = create(ownerSession, "catalog-regressed", "items.read", nil)
	if code != 503 || !bytes.Contains(body, []byte(transport.ReasonScopeCatalogUnavailable)) {
		t.Fatal("Catalog rollback was accepted")
	}
	if !reflect.DeepEqual(before, uc002AppFacts(t, ctx, db, appID)) {
		t.Fatal("Scope failures left partial Version/OAuth records or sequence gaps")
	}
	// A newer authoritative recovery succeeds without resetting the App cache.
	if _, err := authDB.Collection("auth_oauth_catalog").UpdateOne(ctx, bson.M{"_id": "current"}, bson.M{"$set": bson.M{"data.revision": storedRevision + 2, "data.generatedat": changedAt.Add(time.Second)}}); err != nil {
		t.Fatal(err)
	}
	code, _, body = create(ownerSession, "recovered", "items.read", nil)
	var recovered vv.CreateApplicationVersionResponse
	if code != 201 || protojson.Unmarshal(body, &recovered) != nil {
		t.Fatalf("recovered Catalog create=%d", code)
	}
	check(&recovered, 4)
	if resolver.calls.Load() != 0 {
		t.Fatal("UC002 performed DNS/launch/callback probing")
	}
	// Internal Scope and identity issuance RPCs must remain absent from the edge.
	anonymous, err := grpc.NewClient(os.Getenv("UC002_EDGE_GRPC_ADDR"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	if _, err := scv.NewScopeCatalogClient(anonymous).GetScopeCatalogSnapshot(ctx, &scv.GetScopeCatalogSnapshotRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatal("internal Scope RPC exposed at edge")
	}
	if _, err := idv.NewUserIdentityServiceClient(anonymous).IssueUserIdentityFromSession(ctx, &idv.IssueUserIdentityFromSessionRequest{Audience: e2eAudience}); status.Code(err) != codes.Unimplemented {
		t.Fatal("internal identity issuance RPC exposed at edge")
	}
	t.Log("BR-VER-001–009/018: HTTP/native gRPC/gRPC-Web, real USER issuance, persistent Catalog, TTL/rollback/outage/recovery and atomic Version/OAuth state passed")
}
func uc002AppFacts(t *testing.T, ctx context.Context, db *m.Database, app string) map[string][]bson.M {
	t.Helper()
	result := map[string][]bson.M{}
	for _, name := range []string{"applications", "application_versions", "application_version_oauth_configs", "application_reviews", "application_publications", "application_publication_history"} {
		filter := bson.M{"applicationId": app}
		if name == "applications" {
			filter = bson.M{"id": app}
		}
		cursor, err := db.Collection(name).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
		if err != nil {
			t.Fatal(err)
		}
		var rows []bson.M
		if cursor.All(ctx, &rows) != nil {
			t.Fatal("App facts could not be read")
		}
		result[name] = rows
	}
	return result
}
