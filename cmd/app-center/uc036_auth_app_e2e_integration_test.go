package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

// The UC036 script runs current App source and a published Auth image.
func TestE2E_UCAPP036_RealAuthOAuthTestClear(t *testing.T) {
	authTarget := os.Getenv(authCenterIntegrationTargetEnv)
	authDatabaseName := os.Getenv(authCenterIntegrationDatabaseEnv)
	authHTTP := os.Getenv("UC036_AUTH_HTTP_ADDR")
	appHTTP := os.Getenv("UC036_APP_HTTP_ADDR")
	appGRPC := os.Getenv("UC036_APP_GRPC_ADDR")
	appCallers := os.Getenv("UC036_APP_SERVICE_CALLERS_B64")
	policyDigest := os.Getenv("UC036_ROUTE_POLICY_DIGEST")
	clockFile := os.Getenv("UC036_AUTH_CLOCK_FILE")
	serviceID := os.Getenv(config.ServiceIdentityIDEnv)
	serviceKID := os.Getenv(config.ServiceIdentityKIDEnv)
	servicePrivateKey := os.Getenv(config.ServiceIdentityPrivateKeyEnv)
	if authTarget == "" || authDatabaseName == "" || authHTTP == "" || appHTTP == "" || appGRPC == "" || appCallers == "" || policyDigest == "" || clockFile == "" || serviceID == "" || serviceKID == "" || servicePrivateKey == "" {
		t.Skip("run scripts/test-uc036-auth-app-integration.sh for the published-Auth UC036 gate")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client, database := e2eIsolatedDatabase(t, ctx, true)
	identityKey, identityPublicKey := e2eIdentity(t)
	const (
		ownerID    = "uc036-oauth-owner"
		reviewerID = "uc036-oauth-reviewer"
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
		"authId": ownerID, "developerHandle": "uc036-oauth-owner",
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
	created := appAPI.call(http.MethodPost, "/v1/applications", ownerToken, map[string]any{"name": "UC036_OAuth_Archival"})
	applicationID := uc033String(t, created, "id")
	prefix := "/v1/applications/" + applicationID

	profile := appAPI.call(http.MethodPost, prefix+"/profile-revisions", ownerToken, map[string]any{
		"displayName": "UC036 OAuth archival", "description": "Published Auth acceptance fixture", "icon": nil,
	})
	profileID := uc033String(t, profile, "profileRevisionId")
	profileSubmission := appAPI.call(http.MethodPost, prefix+"/profile-revisions/"+profileID+"/reviews", ownerToken, map[string]any{"expectedRevision": "1"})
	profileReviewID := uc033NestedString(t, profileSubmission, "review", "profileReviewId")
	appAPI.call(http.MethodPost, prefix+"/profile-revisions/"+profileID+"/reviews/"+profileReviewID+"/decision", reviewerToken, map[string]any{
		"expectedProfileRevisionRevision": "2", "expectedCurrentPublishedProfileRevisionId": nil,
		"expectedPolicyVersion": "app-profile-review-v1", "outcome": "APPROVE",
		"confirmedCheckIds": []string{"content-policy-reviewed", "icon-content-reviewed"},
	})

	callbacks := []string{"https://www.whut.edu.cn/uc036-test-one", "https://www.whut.edu.cn/uc036-test-two"}
	versions := make([]string, 2)
	for index, callback := range callbacks {
		major := index + 1
		version := appAPI.call(http.MethodPost, prefix+"/versions", ownerToken, map[string]any{
			"versionLabel": []string{"1.0", "2.0"}[index], "launchUrl": "https://example.edu/apps/uc036/",
			"rpcApiMinVersion": major, "rpcApiMaxVersionExclusive": major + 1,
			"requiredCapabilities": []string{}, "requiredScopes": []string{"openid"},
			"optionalScopes": []string{"offline_access", "items.read"},
			"oauthRedirects": map[string]any{"pkceRedirectUris": []string{}, "confidentialRedirectUris": []string{callback}},
		})
		versions[index] = uc033String(t, version, "versionId")
		submission := appAPI.call(http.MethodPost, prefix+"/versions/"+versions[index]+"/reviews", ownerToken, map[string]any{"expectedRevision": "1"})
		reviewID := uc033NestedString(t, submission, "review", "reviewId")
		appAPI.call(http.MethodPost, prefix+"/versions/"+versions[index]+"/reviews/"+reviewID+"/decision", reviewerToken, map[string]any{
			"outcome": "APPROVE", "expectedPolicyVersion": "app-version-review-v2",
			"confirmedCheckIds": []string{"content-policy-reviewed", "launch-url-content-reviewed", "requested-access-reviewed", "oauth-redirects-reviewed"},
		})
	}
	clients := map[string]*uc033OAuthClient{}
	for _, channel := range []string{"TEST", "STABLE", "GREY"} {
		registration := appAPI.call(http.MethodPost, prefix+"/oauth-registrations/OAUTH_CHANNEL_"+channel+"/clients", ownerToken, map[string]any{"type": "OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET"})
		clients[channel] = newUC033OAuthClient(t, ctx, authHTTP, callbacks[0], uc033NestedString(t, registration, "registration", "confidentialClient", "clientId"), uc033String(t, registration, "clientSecret"), sessionToken, policyDigest, clockFile)
		clients[channel].channel = channel
	}
	publication := appAPI.call(http.MethodPut, prefix+"/publications/1/test-slot", ownerToken, map[string]any{"versionId": versions[0]})
	revision := uc033NestedString(t, publication, "publication", "revision")
	publication = appAPI.call(http.MethodPut, prefix+"/publications/1/stable-slot", ownerToken, map[string]any{"versionId": versions[0], "expectedPublicationRevision": revision})
	revision = uc033NestedString(t, publication, "publication", "revision")
	publication = appAPI.call(http.MethodPut, prefix+"/publications/1/grey-rollout", ownerToken, map[string]any{"versionId": versions[0], "exposureBasisPoints": 10000, "expectedPublicationRevision": revision})
	revision = uc033NestedString(t, publication, "publication", "revision")
	appAPI.call(http.MethodPut, prefix+"/publications/2/test-slot", ownerToken, map[string]any{"versionId": versions[1]})
	join := appAPI.call(http.MethodPost, prefix+"/tester-join-links", ownerToken, map[string]any{"expectedActiveJoinLinkId": nil})
	parsed, err := url.Parse(uc033String(t, join, "joinUrl"))
	if err != nil {
		t.Fatal(err)
	}
	fragment, err := url.ParseQuery(parsed.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	if fragment.Get("joinLinkId") == "" || fragment.Get("secret") == "" {
		t.Fatal("invalid fixture join fragment")
	}
	joined := appAPI.call(http.MethodPost, "/v1/tester-join-links/"+fragment.Get("joinLinkId")+"/memberships", ownerToken, map[string]any{"secret": fragment.Get("secret")})
	membershipID := uc033NestedString(t, joined, "membership", "membershipId")

	tester := clients["TEST"]
	tester.portalSession()
	tester.refreshClock()
	testTokens := tester.exchange(tester.authorize(), http.StatusOK)
	staleCode := tester.authorize()
	if tester.userInfo(testTokens.accessToken) != http.StatusOK {
		t.Fatal("TEST UserInfo before clear")
	}
	tester.refreshClock()
	if err := tester.delegate(testTokens.accessToken); err != nil {
		t.Fatal("TEST delegate before clear", err)
	}
	tester.refreshClock()
	testTokens = tester.refresh(testTokens.refreshToken, http.StatusOK)
	otherMajor := newUC033OAuthClient(t, ctx, authHTTP, callbacks[1], tester.clientID, tester.clientSecret, sessionToken, policyDigest, clockFile)
	otherMajor.channel, otherMajor.major = "TEST", "2"
	surviving := map[*uc033OAuthClient]uc033OAuthTokens{}
	for _, oauth := range []*uc033OAuthClient{otherMajor, clients["STABLE"], clients["GREY"]} {
		oauth.refreshClock()
		surviving[oauth] = oauth.exchange(oauth.authorize(), http.StatusOK)
	}
	cleared := uc036ClearTest(t, ctx, appAPI.baseURL, prefix, ownerToken, revision)
	if cleared["changed"] != true || uc033NestedString(t, cleared, "history", "action") != "CLEAR_TEST_VERSION" {
		t.Fatal("clear response")
	}
	tester.refreshClock()
	authorizeStatus, authorizeHeaders, authorizeBody := tester.request(http.MethodGet, "/authorize?"+tester.authorizeQuery().Encode(), "", "", http.Header{"Cookie": []string{tester.cookie}})
	if authorizeStatus != http.StatusBadRequest || authorizeHeaders.Get("Location") != "" || !bytes.Contains(authorizeBody, []byte(`"invalid_grant"`)) {
		t.Fatalf("clear authorize status=%d", authorizeStatus)
	}
	uc036RejectToken(t, tester, url.Values{"grant_type": {"authorization_code"}, "code": {staleCode}, "redirect_uri": {tester.callback}, "code_verifier": {tester.verifier}})
	uc036RejectToken(t, tester, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {testTokens.refreshToken}})
	if tester.userInfo(testTokens.accessToken) != http.StatusUnauthorized {
		t.Fatal("TEST UserInfo after clear")
	}
	if err := tester.delegate(testTokens.accessToken); status.Code(err) != codes.Unauthenticated {
		t.Fatal("TEST delegate after clear", err)
	}
	for oauth, tokens := range surviving {
		oauth.refreshClock()
		if oauth.userInfo(tokens.accessToken) != http.StatusOK {
			t.Fatal(oauth.channel, oauth.major, "UserInfo changed")
		}
		if err := oauth.delegate(tokens.accessToken); err != nil {
			t.Fatal(oauth.channel, oauth.major, "delegation changed", err)
		}
		oauth.refreshClock()
		oauth.refresh(tokens.refreshToken, http.StatusOK)
		oauth.refreshClock()
		oauth.exchange(oauth.authorize(), http.StatusOK)
	}
	var member bson.M
	if err := database.Collection("application_tester_memberships").FindOne(ctx, bson.M{"membershipId": membershipID, "status": "ACTIVE"}).Decode(&member); err != nil {
		t.Fatal("Membership changed", err)
	}
	newRevision := uc033NestedString(t, cleared, "publication", "revision")
	appAPI.call(http.MethodPut, prefix+"/publications/1/test-slot", ownerToken, map[string]any{"versionId": versions[0], "expectedPublicationRevision": newRevision})
	tester.refreshClock()
	recovered := tester.exchange(tester.authorize(), http.StatusOK)
	if tester.userInfo(recovered.accessToken) != http.StatusOK {
		t.Fatal("TEST recovery UserInfo")
	}
	tester.refreshClock()
	if err := tester.delegate(recovered.accessToken); err != nil {
		t.Fatal("TEST recovery delegation", err)
	}
	tester.refreshClock()
	tester.refresh(recovered.refreshToken, http.StatusOK)
	t.Log("UC036 accepted with published Auth: exact-major TEST gates stop code/refresh/UserInfo/delegation; other major and STABLE/GREY remain live; reset reuses ACTIVE Membership")
}

func uc036ClearTest(t *testing.T, ctx context.Context, baseURL, prefix, identity, revision string) map[string]any {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, baseURL+prefix+"/publications/1/test-slot?expected_publication_revision="+revision, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(transport.IdentityHeader, identity)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("clear status=%d body=%s", response.StatusCode, body)
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func uc036RejectToken(t *testing.T, client *uc033OAuthClient, values url.Values) {
	t.Helper()
	code, _, body := client.request(http.MethodPost, "/token", values.Encode(), "application/x-www-form-urlencoded", client.basicAuth())
	if code != http.StatusBadRequest || !bytes.Contains(body, []byte(`"invalid_grant"`)) || bytes.Contains(body, []byte("access_token")) || bytes.Contains(body, []byte("id_token")) {
		t.Fatalf("clear token status=%d", code)
	}
}
