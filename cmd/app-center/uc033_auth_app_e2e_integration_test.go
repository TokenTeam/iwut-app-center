package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	oauthdelegationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/oauth_delegation"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

const (
	uc033AuthHTTPEnv          = "UC033_AUTH_HTTP_ADDR"
	uc033AppHTTPEnv           = "UC033_APP_HTTP_ADDR"
	uc033AppGRPCEnv           = "UC033_APP_GRPC_ADDR"
	uc033AppCallersEnv        = "UC033_APP_SERVICE_CALLERS_B64"
	uc033RoutePolicyDigestEnv = "UC033_ROUTE_POLICY_DIGEST"
	uc033AuthClockFileEnv     = "UC033_AUTH_CLOCK_FILE"
)

// TestE2E_UCAPP033_RealAuthOAuthArchival is launched by
// scripts/test-uc033-auth-app-integration.sh. The App server is current source;
// Auth is the published image and is treated entirely as a black box here.
func TestE2E_UCAPP033_RealAuthOAuthArchival(t *testing.T) {
	authTarget := os.Getenv(authCenterIntegrationTargetEnv)
	authDatabaseName := os.Getenv(authCenterIntegrationDatabaseEnv)
	authHTTP := os.Getenv(uc033AuthHTTPEnv)
	appHTTP := os.Getenv(uc033AppHTTPEnv)
	appGRPC := os.Getenv(uc033AppGRPCEnv)
	appCallers := os.Getenv(uc033AppCallersEnv)
	policyDigest := os.Getenv(uc033RoutePolicyDigestEnv)
	clockFile := os.Getenv(uc033AuthClockFileEnv)
	serviceID := os.Getenv(config.ServiceIdentityIDEnv)
	serviceKID := os.Getenv(config.ServiceIdentityKIDEnv)
	servicePrivateKey := os.Getenv(config.ServiceIdentityPrivateKeyEnv)
	if authTarget == "" || authDatabaseName == "" || authHTTP == "" || appHTTP == "" || appGRPC == "" || appCallers == "" || policyDigest == "" || clockFile == "" || serviceID == "" || serviceKID == "" || servicePrivateKey == "" {
		t.Skip("run scripts/test-uc033-auth-app-integration.sh for the published-Auth UC033 gate")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, database := e2eIsolatedDatabase(t, ctx, true)
	identityKey, identityPublicKey := e2eIdentity(t)
	const (
		ownerID    = "uc033-oauth-owner"
		reviewerID = "uc033-oauth-reviewer"
	)
	ownerToken := e2eSignIdentity(t, identityKey, ownerID, "APPROVED")
	reviewerToken := e2eSignReviewerIdentity(t, identityKey, reviewerID, "app.profile.review", "app.version.review")

	authDatabase := client.Database(authDatabaseName)
	if _, err := authDatabase.Collection("auth_principals").InsertOne(ctx, bson.M{
		"authId": ownerID, "principalType": "USER", "accountStatus": "ACTIVE", "accountRevision": int64(1),
		"developerRevision": int64(1), "developerStatus": "APPROVED", "permissions": bson.A{},
		"permissionRevision": int64(1), "createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed Auth principal: %v", err)
	}
	if _, err := authDatabase.Collection("auth_developer_handles").InsertOne(ctx, bson.M{
		"authId": ownerID, "developerHandle": "uc033-oauth-owner",
	}); err != nil {
		t.Fatalf("seed Auth developer handle: %v", err)
	}
	sessionToken := uc033SeedAuthSession(t, ctx, authDatabase, ownerID)

	resolver := &e2eDNSResolver{addresses: map[string][]netip.Addr{
		"example.edu": {netip.MustParseAddr("8.8.8.8")},
	}}
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:                     os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:                database.Name(),
		config.HTTPAddrEnv:                     appHTTP,
		config.GRPCAddrEnv:                     appGRPC,
		config.IdentityIssuerEnv:               e2eIssuer,
		config.IdentityAudienceEnv:             e2eAudience,
		config.IdentityPublicKeysEnv:           e2eKeyID + "=" + identityPublicKey,
		config.AuthScopeCatalogTargetEnv:       authTarget,
		config.ScopeCatalogCacheTTLEnv:         "1ns",
		config.ServiceIdentityIDEnv:            serviceID,
		config.ServiceIdentityKIDEnv:           serviceKID,
		config.ServiceIdentityPrivateKeyEnv:    servicePrivateKey,
		config.ServiceCallersEnv:               appCallers,
		config.ApplicationOperationsEnabledEnv: "true",
	}))
	if err != nil {
		t.Fatalf("load App configuration: %v", err)
	}
	app, appCleanup, err := wireAppWithResolver(configuration, resolver)
	if err != nil {
		t.Fatalf("wire App: %v", err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() {
		runErr = app.Run()
		close(runDone)
	}()
	t.Cleanup(func() {
		if stopErr := app.Stop(); stopErr != nil {
			t.Errorf("stop App: %v", stopErr)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("App did not stop within 15s")
		}
		appCleanup()
	})
	e2eWaitForHTTPListener(t, ctx, appHTTP, runDone, &runErr)
	appConnection := e2eDialGRPC(t, ctx, appGRPC, runDone, &runErr)
	t.Cleanup(func() { _ = appConnection.Close() })

	appAPI := &uc033AppAPI{t: t, ctx: ctx, baseURL: "http://" + appHTTP}
	created := appAPI.call(http.MethodPost, "/v1/applications", ownerToken, map[string]any{"name": "UC033_OAuth_Archival"})
	applicationID := uc033String(t, created, "id")
	prefix := "/v1/applications/" + applicationID

	profile := appAPI.call(http.MethodPost, prefix+"/profile-revisions", ownerToken, map[string]any{
		"displayName": "UC033 OAuth archival", "description": "Published Auth acceptance fixture", "icon": nil,
	})
	profileID := uc033String(t, profile, "profileRevisionId")
	profileSubmission := appAPI.call(http.MethodPost, prefix+"/profile-revisions/"+profileID+"/reviews", ownerToken, map[string]any{"expectedRevision": "1"})
	profileReviewID := uc033NestedString(t, profileSubmission, "review", "profileReviewId")
	appAPI.call(http.MethodPost, prefix+"/profile-revisions/"+profileID+"/reviews/"+profileReviewID+"/decision", reviewerToken, map[string]any{
		"expectedProfileRevisionRevision": "2", "expectedCurrentPublishedProfileRevisionId": nil,
		"expectedPolicyVersion": "app-profile-review-v1", "outcome": "APPROVE",
		"confirmedCheckIds": []string{"content-policy-reviewed", "icon-content-reviewed"},
	})

	callback := "https://www.whut.edu.cn/uc033-oauth-callback"
	version := appAPI.call(http.MethodPost, prefix+"/versions", ownerToken, map[string]any{
		"versionLabel": "1.0.0", "launchUrl": "https://example.edu/apps/uc033/",
		"rpcApiMinVersion": 1, "rpcApiMaxVersionExclusive": 2,
		"requiredCapabilities": []string{}, "requiredScopes": []string{"openid"},
		"optionalScopes": []string{"offline_access", "items.read"},
		"oauthRedirects": map[string]any{"pkceRedirectUris": []string{}, "confidentialRedirectUris": []string{callback}},
	})
	versionID := uc033String(t, version, "versionId")
	versionSubmission := appAPI.call(http.MethodPost, prefix+"/versions/"+versionID+"/reviews", ownerToken, map[string]any{"expectedRevision": "1"})
	versionReviewID := uc033NestedString(t, versionSubmission, "review", "reviewId")
	appAPI.call(http.MethodPost, prefix+"/versions/"+versionID+"/reviews/"+versionReviewID+"/decision", reviewerToken, map[string]any{
		"outcome": "APPROVE", "expectedPolicyVersion": "app-version-review-v2",
		"confirmedCheckIds": []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed", "oauth-redirects-reviewed"},
	})

	registration := appAPI.call(http.MethodPost, prefix+"/oauth-registrations/OAUTH_CHANNEL_STABLE/clients", ownerToken, map[string]any{"type": "OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET"})
	clientID := uc033NestedString(t, registration, "registration", "confidentialClient", "clientId")
	clientSecret := uc033String(t, registration, "clientSecret")
	// A publication is born through its TEST slot, whose own redirect/client
	// invariant must be satisfied even though the OAuth scenario uses STABLE.
	appAPI.call(http.MethodPost, prefix+"/oauth-registrations/OAUTH_CHANNEL_TEST/clients", ownerToken, map[string]any{"type": "OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET"})
	testPublication := appAPI.call(http.MethodPut, prefix+"/publications/1/test-slot", ownerToken, map[string]any{"versionId": versionID})
	publicationRevision := uc033NestedString(t, testPublication, "publication", "revision")
	appAPI.call(http.MethodPut, prefix+"/publications/1/stable-slot", ownerToken, map[string]any{"versionId": versionID, "expectedPublicationRevision": publicationRevision})

	oauth := newUC033OAuthClient(t, ctx, authHTTP, callback, clientID, clientSecret, sessionToken, policyDigest, clockFile)
	oauth.portalSession()
	oauth.refreshClock()
	immediateTokens := oauth.exchange(oauth.authorize(), http.StatusOK)
	oauth.refreshClock()
	boundTokens := oauth.exchange(oauth.authorize(), http.StatusOK)
	staleImmediateCode := oauth.authorize()
	staleBoundCode := oauth.authorize()

	if got := oauth.userInfo(immediateTokens.accessToken); got != http.StatusOK {
		t.Fatalf("pre-archive UserInfo status = %d, want 200", got)
	}
	oauth.refreshClock()
	if err := oauth.delegate(immediateTokens.accessToken); err != nil {
		t.Fatalf("pre-archive delegation: %v", err)
	}
	oauth.refreshClock()
	immediateTokens = oauth.refresh(immediateTokens.refreshToken, http.StatusOK)

	archived := appAPI.call(http.MethodPost, prefix+":archive", ownerToken, map[string]any{"expectedArchiveRevision": "1"})
	if statusValue := uc033NestedString(t, archived, "state", "archiveStatus"); statusValue != "APPLICATION_ARCHIVE_STATUS_ARCHIVED" {
		t.Fatalf("archive status = %q", statusValue)
	}

	oauth.assertAuthorizeRejected("immediate")
	oauth.exchange(staleImmediateCode, http.StatusUnauthorized)
	oauth.refresh(immediateTokens.refreshToken, http.StatusUnauthorized)
	if got := oauth.userInfo(immediateTokens.accessToken); got != http.StatusUnauthorized {
		t.Fatalf("immediate archived UserInfo status = %d, want 401", got)
	}
	if err := oauth.delegate(immediateTokens.accessToken); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("immediate archived delegation = %v, want UNAUTHENTICATED", err)
	}

	t.Log("waiting beyond the documented 5s App snapshot + 5s delegation + 1s skew bound")
	select {
	case <-time.After(12 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	oauth.refreshClock()
	oauth.assertAuthorizeRejected("after-bound")
	oauth.exchange(staleBoundCode, http.StatusUnauthorized)
	oauth.refresh(boundTokens.refreshToken, http.StatusUnauthorized)
	if got := oauth.userInfo(boundTokens.accessToken); got != http.StatusUnauthorized {
		t.Fatalf("after-bound archived UserInfo status = %d, want 401", got)
	}
	if err := oauth.delegate(boundTokens.accessToken); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("after-bound archived delegation = %v, want UNAUTHENTICATED", err)
	}

	unarchived := appAPI.call(http.MethodPost, prefix+":unarchive", ownerToken, map[string]any{"expectedArchiveRevision": "2"})
	if statusValue := uc033NestedString(t, unarchived, "state", "archiveStatus"); statusValue != "APPLICATION_ARCHIVE_STATUS_UNARCHIVED" {
		t.Fatalf("unarchive status = %q", statusValue)
	}
	oauth.refreshClock()
	freshTokens := oauth.exchange(oauth.authorize(), http.StatusOK)
	if got := oauth.userInfo(freshTokens.accessToken); got != http.StatusOK {
		t.Fatalf("post-unarchive fresh UserInfo status = %d, want 200", got)
	}
	t.Log("UC033 real Auth boundary accepted: authorize, code exchange, refresh, UserInfo and delegation fail closed while archived and recover only from current facts")
}

type uc033AppAPI struct {
	t       *testing.T
	ctx     context.Context
	baseURL string
}

func (a *uc033AppAPI) call(method, path, identity string, body any) map[string]any {
	a.t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		a.t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(a.ctx, method, a.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		a.t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(transport.IdentityHeader, identity)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		a.t.Fatalf("App %s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		a.t.Fatal(err)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		a.t.Fatalf("App %s %s: status=%d body=%s", method, path, response.StatusCode, payload)
	}
	var result map[string]any
	if err := json.Unmarshal(payload, &result); err != nil {
		a.t.Fatalf("decode App %s %s: %v; body=%s", method, path, err, payload)
	}
	return result
}

type uc033OAuthTokens struct {
	accessToken  string
	refreshToken string
}

type uc033OAuthClient struct {
	t            *testing.T
	ctx          context.Context
	baseURL      string
	callback     string
	clientID     string
	clientSecret string
	cookie       string
	verifier     string
	challenge    string
	policyDigest string
	clockFile    string
	authTarget   string
	serviceKey   *rsa.PrivateKey
	serviceKID   string
	channel      string
	major        string
}

func newUC033OAuthClient(t *testing.T, ctx context.Context, authHTTP, callback, clientID, clientSecret, sessionToken, policyDigest, clockFile string) *uc033OAuthClient {
	t.Helper()
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	digest := sha256.Sum256([]byte(verifier))
	return &uc033OAuthClient{
		t: t, ctx: ctx, baseURL: "http://" + authHTTP + "/oidc", callback: callback,
		clientID: clientID, clientSecret: clientSecret, cookie: "__Host-iwut-portal=" + sessionToken,
		verifier: verifier, challenge: base64.RawURLEncoding.EncodeToString(digest[:]),
		policyDigest: policyDigest, clockFile: clockFile, authTarget: os.Getenv(authCenterIntegrationTargetEnv),
		serviceKey: uc033ServicePrivateKey(t), serviceKID: os.Getenv(config.ServiceIdentityKIDEnv),
		channel: "STABLE", major: "1",
	}
}

func (o *uc033OAuthClient) request(method, path, body, contentType string, headers http.Header) (int, http.Header, []byte) {
	o.t.Helper()
	request, err := http.NewRequestWithContext(o.ctx, method, o.baseURL+path, strings.NewReader(body))
	if err != nil {
		o.t.Fatal(err)
	}
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		o.t.Fatalf("Auth %s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		o.t.Fatal(err)
	}
	return response.StatusCode, response.Header.Clone(), payload
}

func (o *uc033OAuthClient) portalSession() {
	o.t.Helper()
	code, headers, body := o.request(http.MethodPost, "/portal/session", "{}", "application/json", http.Header{
		"Origin":         []string{"https://auth.example.test"},
		"X-Iwut-Session": []string{strings.TrimPrefix(o.cookie, "__Host-iwut-portal=")},
	})
	if code != http.StatusOK || !strings.Contains(headers.Get("Set-Cookie"), "HttpOnly") {
		o.t.Fatalf("portal session: status=%d headers=%v body=%s", code, headers, body)
	}
}

func (o *uc033OAuthClient) authorizeQuery() url.Values {
	state := uc033Opaque(o.t)
	nonce := uc033Opaque(o.t)
	return url.Values{
		"response_type": {"code"}, "client_id": {o.clientID}, "redirect_uri": {o.callback},
		"scope": {"openid offline_access items.read"}, "state": {state}, "nonce": {nonce},
		"code_challenge": {o.challenge}, "code_challenge_method": {"S256"},
		"iwut_channel": {o.channel}, "iwut_rpc_api_major": {o.major}, "prompt": {"consent"},
	}
}

func (o *uc033OAuthClient) authorize() string {
	o.t.Helper()
	query := o.authorizeQuery()
	code, _, body := o.request(http.MethodGet, "/authorize?"+query.Encode(), "", "", http.Header{"Cookie": []string{o.cookie}})
	if code != http.StatusOK {
		o.t.Fatalf("authorize: status=%d body=%s", code, body)
	}
	field := func(name string) string {
		match := regexp.MustCompile(`name="` + name + `" value="([^"]+)"`).FindSubmatch(body)
		if len(match) != 2 {
			o.t.Fatalf("authorize consent missing %s: %s", name, body)
		}
		return string(match[1])
	}
	interaction, csrf := field("interaction"), field("csrf")
	values := url.Values{"interaction": {interaction}, "csrf": {csrf}, "decision": {"approve"}}
	for _, scope := range []string{"openid", "offline_access", "items.read"} {
		values.Add("scope", scope)
	}
	code, headers, body := o.request(http.MethodPost, "/portal/interactions/"+interaction+"/confirm", values.Encode(), "application/x-www-form-urlencoded", http.Header{
		"Cookie": []string{o.cookie}, "Origin": []string{"https://auth.example.test"},
	})
	if code != http.StatusSeeOther {
		o.t.Fatalf("confirm: status=%d body=%s", code, body)
	}
	location, err := url.Parse(headers.Get("Location"))
	if err != nil || location.Query().Get("code") == "" || location.Query().Get("state") != query.Get("state") || location.Query().Get("iss") != "https://auth.example.test/oidc" {
		o.t.Fatalf("invalid authorization callback %q: %v", headers.Get("Location"), err)
	}
	return location.Query().Get("code")
}

func (o *uc033OAuthClient) assertAuthorizeRejected(phase string) {
	o.t.Helper()
	code, headers, body := o.request(http.MethodGet, "/authorize?"+o.authorizeQuery().Encode(), "", "", http.Header{"Cookie": []string{o.cookie}})
	if code != http.StatusUnauthorized || headers.Get("Location") != "" || !bytes.Contains(body, []byte("invalid_client")) {
		o.t.Fatalf("%s archived authorize: status=%d location=%q body=%s", phase, code, headers.Get("Location"), body)
	}
}

func (o *uc033OAuthClient) exchange(authorizationCode string, want int) uc033OAuthTokens {
	o.t.Helper()
	values := url.Values{
		"grant_type": {"authorization_code"}, "code": {authorizationCode}, "redirect_uri": {o.callback}, "code_verifier": {o.verifier},
	}
	code, _, body := o.request(http.MethodPost, "/token", values.Encode(), "application/x-www-form-urlencoded", o.basicAuth())
	if code != want {
		o.t.Fatalf("code exchange: status=%d want=%d body=%s", code, want, body)
	}
	if want != http.StatusOK {
		if !bytes.Contains(body, []byte("invalid_client")) {
			o.t.Fatalf("archived code exchange did not hide the client: %s", body)
		}
		return uc033OAuthTokens{}
	}
	return uc033DecodeTokens(o.t, body)
}

func (o *uc033OAuthClient) refresh(refreshToken string, want int) uc033OAuthTokens {
	o.t.Helper()
	values := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	code, _, body := o.request(http.MethodPost, "/token", values.Encode(), "application/x-www-form-urlencoded", o.basicAuth())
	if code != want {
		o.t.Fatalf("refresh: status=%d want=%d body=%s", code, want, body)
	}
	if want != http.StatusOK {
		if !bytes.Contains(body, []byte("invalid_client")) {
			o.t.Fatalf("archived refresh did not hide the client: %s", body)
		}
		return uc033OAuthTokens{}
	}
	return uc033DecodeTokens(o.t, body)
}

func (o *uc033OAuthClient) userInfo(accessToken string) int {
	o.t.Helper()
	code, _, _ := o.request(http.MethodGet, "/userinfo", "", "", http.Header{"Authorization": []string{"Bearer " + accessToken}})
	return code
}

func (o *uc033OAuthClient) delegate(accessToken string) error {
	o.t.Helper()
	connection, err := grpc.NewClient(o.authTarget, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer connection.Close()
	callContext := metadata.NewOutgoingContext(o.ctx, metadata.Pairs(
		"authorization", "Bearer "+uc033SignServiceIdentity(o.t, o.serviceKey, o.serviceKID, "gateway", "iwut-auth-center"),
		"x-iwut-access-token", accessToken,
	))
	_, err = oauthdelegationv1.NewOAuthDelegationServiceClient(connection).IssueDelegationContext(callContext, &oauthdelegationv1.IssueDelegationContextRequest{
		RouteId: "resource-read", PolicyDigest: o.policyDigest, Protocol: "HTTP", Method: "GET", TargetPath: "/items/123",
	})
	return err
}

func (o *uc033OAuthClient) refreshClock() {
	o.t.Helper()
	payload, err := json.Marshal(map[string]any{"observedAt": time.Now().UTC(), "offsetMillis": 0})
	if err != nil {
		o.t.Fatal(err)
	}
	if err := os.WriteFile(o.clockFile, payload, 0o600); err != nil {
		o.t.Fatalf("refresh Auth clock observation: %v", err)
	}
}

func (o *uc033OAuthClient) basicAuth() http.Header {
	value := url.QueryEscape(o.clientID) + ":" + url.QueryEscape(o.clientSecret)
	return http.Header{"Authorization": []string{"Basic " + base64.StdEncoding.EncodeToString([]byte(value))}}
}

func uc033SeedAuthSession(t *testing.T, ctx context.Context, database *drivermongo.Database, ownerID string) string {
	t.Helper()
	publicKey, fingerprint := uc033DeviceCredential(t)
	rawSession := make([]byte, 32)
	if _, err := rand.Read(rawSession); err != nil {
		t.Fatal(err)
	}
	sessionToken := base64.RawURLEncoding.EncodeToString(rawSession)
	sessionDigest := sha256.Sum256(rawSession)
	now := time.Now().UTC().Truncate(time.Millisecond)
	credentialID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := database.Collection("auth_credentials").InsertOne(ctx, bson.M{
		"credentialId": credentialID, "authId": ownerID, "protocolVersion": "iwut-device-v1",
		"publicKey": publicKey, "fingerprint": fingerprint, "createdAt": now,
	}); err != nil {
		t.Fatalf("seed Auth credential: %v", err)
	}
	if _, err := database.Collection("auth_sessions").InsertOne(ctx, bson.M{
		"accountRevision": int64(1), "sessionId": sessionID, "authId": ownerID,
		"authenticationMethod": "DEVICE_CREDENTIAL", "credentialId": credentialID,
		"tokenDigest": sessionDigest[:], "authenticatedAt": now, "createdAt": now,
		"lastUsedAt": now, "expiresAt": now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed Auth session: %v", err)
	}
	return sessionToken
}

func uc033Opaque(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func uc033DecodeTokens(t *testing.T, payload []byte) uc033OAuthTokens {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("decode token response: %v; body=%s", err, payload)
	}
	access, accessOK := raw["access_token"].(string)
	refresh, refreshOK := raw["refresh_token"].(string)
	if !accessOK || access == "" || !refreshOK || refresh == "" {
		t.Fatalf("incomplete token response: %s", payload)
	}
	return uc033OAuthTokens{accessToken: access, refreshToken: refresh}
}

func uc033String(t *testing.T, value map[string]any, path ...string) string {
	t.Helper()
	current := any(value)
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("%s is not an object in %#v", strings.Join(path, "."), value)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("missing %s in %#v", strings.Join(path, "."), value)
		}
	}
	result, ok := current.(string)
	if !ok || result == "" {
		t.Fatalf("%s is not a non-empty string in %#v", strings.Join(path, "."), value)
	}
	return result
}

func uc033NestedString(t *testing.T, value map[string]any, path ...string) string {
	t.Helper()
	return uc033String(t, value, path...)
}

func uc033ServicePrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	raw, err := base64.StdEncoding.Strict().DecodeString(os.Getenv(config.ServiceIdentityPrivateKeyEnv))
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(raw)
	if block == nil || strings.TrimSpace(string(rest)) != "" {
		t.Fatal("invalid App service private key PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("App service private key is not RSA")
	}
	return key
}

func uc033SignServiceIdentity(t *testing.T, key *rsa.PrivateKey, kid, serviceID, audience string) string {
	t.Helper()
	now := time.Now().UTC()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid})
	claims, _ := json.Marshal(map[string]any{
		"iss": serviceID, "sub": serviceID, "aud": audience, "iat": now.Unix(), "nbf": now.Add(-time.Second).Unix(),
		"exp": now.Add(time.Minute).Unix(), "jti": uuid.NewString(),
	})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func uc033DeviceCredential(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := elliptic.Marshal(key.Curve, key.X, key.Y)
	fingerprint := sha256.Sum256(e2eLengthPrefixedFrame([]byte("iwut-device-public-key-v1"), publicKey))
	return publicKey, fingerprint[:]
}
