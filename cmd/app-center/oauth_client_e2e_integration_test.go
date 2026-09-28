package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	applicationv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	oauthclientv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/oauth_client"
	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"

	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP018_ManageOAuthClientsOverHTTPAndGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	_, database := e2eIsolatedDatabase(t, ctx, true)
	privateKey, publicKeyPath := e2eIdentity(t)
	const adminID = "oauth-e2e-admin"
	adminToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")
	addresses := e2eReserveAddresses(t, 2)
	httpAddress, grpcAddress := addresses[0], addresses[1]
	configuration, err := config.Load(e2eEnvironment(map[string]string{
		config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
		config.MongoDatabaseEnv:          database.Name(),
		config.HTTPAddrEnv:               httpAddress,
		config.GRPCAddrEnv:               grpcAddress,
		config.IdentityIssuerEnv:         e2eIssuer,
		config.IdentityAudienceEnv:       e2eAudience,
		config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
		config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget,
		config.ScopeCatalogCacheTTLEnv:   "1ns",
	}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireAppWithResolver(configuration, &e2eDNSResolver{})
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(runDone) }()
	t.Cleanup(func() {
		if err := app.Stop(); err != nil {
			t.Errorf("stop app: %v", err)
		}
		select {
		case <-runDone:
		case <-time.After(15 * time.Second):
			t.Error("app did not stop")
		}
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Errorf("run app: %v", runErr)
		}
		cleanup()
	})
	e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
	connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
	t.Cleanup(func() { _ = connection.Close() })

	createStatus, applicationBody := e2eHTTPCreate(t, httpAddress, adminToken, "OAuth_Client_E2E_App")
	if createStatus != http.StatusCreated {
		t.Fatalf("create application status=%d body=%s", createStatus, applicationBody)
	}
	var application applicationv1.CreateApplicationResponse
	if err := protojson.Unmarshal(applicationBody, &application); err != nil {
		t.Fatal(err)
	}

	doHTTP := func(method, path, token string, body []byte) (int, http.Header, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, method, "http://"+httpAddress+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			request.Header.Set(transport.IdentityHeader, token)
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		payload, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, payload
	}

	registerPath := fmt.Sprintf("/v1/applications/%s/oauth-registrations/OAUTH_CHANNEL_TEST/clients", application.GetId())
	statusCode, headers, payload := doHTTP(http.MethodPost, registerPath, adminToken, []byte(`{"type":"OAUTH_CLIENT_TYPE_PUBLIC_PKCE"}`))
	if statusCode != http.StatusCreated || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("public register status=%d cache=%q body=%s", statusCode, headers.Get("Cache-Control"), payload)
	}
	var publicRegistration oauthclientv1.RegisterOAuthClientResponse
	if err := protojson.Unmarshal(payload, &publicRegistration); err != nil {
		t.Fatal(err)
	}
	if publicRegistration.ClientSecret != nil || publicRegistration.GetRegistration().GetPublicClient().GetClientId() == "" || publicRegistration.GetRegistration().GetRegistrationRevision() != 1 {
		t.Fatalf("public registration=%v", &publicRegistration)
	}

	client := oauthclientv1.NewOAuthClientServiceClient(connection)
	grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
	expectedRegistrationRevision := int64(1)
	var registerHeaders metadata.MD
	confidentialRegistration, err := client.RegisterOAuthClient(grpcCtx, &oauthclientv1.RegisterOAuthClientRequest{
		ApplicationId: application.GetId(), Channel: oauthclientv1.OAuthChannel_OAUTH_CHANNEL_TEST,
		Command: &oauthclientv1.RegisterOAuthClientCommand{Type: oauthclientv1.OAuthClientType_OAUTH_CLIENT_TYPE_CONFIDENTIAL_SECRET, ExpectedRegistrationRevision: &expectedRegistrationRevision},
	}, grpc.Header(&registerHeaders))
	if err != nil {
		t.Fatal(err)
	}
	if values := registerHeaders.Get("cache-control"); len(values) != 1 || values[0] != "no-store" {
		t.Fatalf("gRPC cache headers=%v", registerHeaders)
	}
	firstSecret := confidentialRegistration.GetClientSecret()
	confidentialClientID := confidentialRegistration.GetRegistration().GetConfidentialClient().GetClientId()
	if firstSecret == "" || confidentialClientID == "" || confidentialRegistration.GetRegistration().GetRegistrationRevision() != 2 {
		t.Fatalf("confidential registration=%v", confidentialRegistration)
	}
	decodedSecret, err := base64.RawURLEncoding.DecodeString(firstSecret)
	if err != nil || len(decodedSecret) != 32 {
		t.Fatalf("secret encoding length=%d err=%v", len(decodedSecret), err)
	}

	registrationPath := fmt.Sprintf("/v1/applications/%s/oauth-registrations/OAUTH_CHANNEL_TEST", application.GetId())
	statusCode, headers, payload = doHTTP(http.MethodGet, registrationPath, adminToken, nil)
	if statusCode != http.StatusOK || headers.Get("Cache-Control") != "no-store" || bytes.Contains(payload, []byte(firstSecret)) || bytes.Contains(payload, []byte("secretDigest")) || bytes.Contains(payload, []byte("clientSecret")) {
		t.Fatalf("registration read status=%d cache=%q body=%s", statusCode, headers.Get("Cache-Control"), payload)
	}

	credential, err := client.GetOAuthClientCredentialMetadata(grpcCtx, &oauthclientv1.GetOAuthClientCredentialMetadataRequest{ClientId: confidentialClientID})
	if err != nil || credential.GetCredential().GetCredentialRevision() != 1 {
		t.Fatalf("credential=%v err=%v", credential, err)
	}
	rotated, err := client.RotateOAuthClientSecret(grpcCtx, &oauthclientv1.RotateOAuthClientSecretRequest{ClientId: confidentialClientID, Command: &oauthclientv1.RotateOAuthClientSecretCommand{ExpectedCredentialRevision: 1}})
	if err != nil || rotated.GetCredential().GetCredentialRevision() != 2 || rotated.GetClientSecret() == "" || rotated.GetClientSecret() == firstSecret {
		t.Fatalf("rotation=%v err=%v", rotated, err)
	}

	statusPath := fmt.Sprintf("/v1/oauth-clients/%s/status", confidentialClientID)
	statusCode, headers, payload = doHTTP(http.MethodPut, statusPath, adminToken, []byte(`{"expectedRegistrationRevision":"2","status":"OAUTH_CLIENT_STATUS_DISABLED"}`))
	if statusCode != http.StatusOK || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("disable status=%d cache=%q body=%s", statusCode, headers.Get("Cache-Control"), payload)
	}
	var disabled oauthclientv1.SetOAuthClientStatusResponse
	if err := protojson.Unmarshal(payload, &disabled); err != nil {
		t.Fatal(err)
	}
	if !disabled.GetChanged() || disabled.GetRegistration().GetRegistrationRevision() != 3 || disabled.GetRegistration().GetConfidentialClient().GetAuthorizationEpoch() != 2 {
		t.Fatalf("disabled=%v", &disabled)
	}

	otherToken := e2eSignIdentity(t, privateKey, "other-oauth-admin", "APPROVED")
	statusCode, headers, payload = doHTTP(http.MethodGet, registrationPath, otherToken, nil)
	if statusCode != http.StatusForbidden || headers.Get("Cache-Control") != "no-store" || !bytes.Contains(payload, []byte(transport.ReasonApplicationAdminRequired)) {
		t.Fatalf("other admin status=%d cache=%q body=%s", statusCode, headers.Get("Cache-Control"), payload)
	}

	var document bson.M
	if err := database.Collection("oauth_client_credentials").FindOne(ctx, bson.M{"clientId": confidentialClientID}).Decode(&document); err != nil {
		t.Fatal(err)
	}
	binary, ok := document["secretDigest"].(bson.Binary)
	if !ok || len(binary.Data) != 32 || document["credentialRevision"] != int64(2) {
		t.Fatalf("stored credential=%v", document)
	}
	secondSecret := rotated.GetClientSecret()
	wantDigest := sha256.Sum256([]byte("iwut-oauth-client-secret-v1\x00" + confidentialClientID + "\x00" + secondSecret))
	if !bytes.Equal(binary.Data, wantDigest[:]) {
		t.Fatal("stored digest does not match the rotated secret")
	}
	for _, forbidden := range []string{"secret", "clientSecret", "plainSecret"} {
		if _, exists := document[forbidden]; exists {
			t.Fatalf("stored plaintext field %s", forbidden)
		}
	}
}
