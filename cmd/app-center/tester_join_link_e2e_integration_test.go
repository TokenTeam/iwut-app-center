package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	testerjoinlinkv1 "iwut-app-center/api/gen/go/app_center/v1/tester_join_link"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP008_BR_TST_001_009_CreateAndRotate(t *testing.T) {
	for _, prefixOverride := range []string{"", "http://localhost:4321/custom/join/"} {
		name := "default"
		if prefixOverride != "" {
			name = "custom"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()

			_, database := e2eIsolatedDatabase(t, ctx, true)
			privateKey, publicKeyPath := e2eIdentity(t)
			const (
				adminID = "auth-e2e-review-admin"
			)
			adminToken := e2eSignIdentity(t, privateKey, adminID, "APPROVED")

			addresses := e2eReserveAddresses(t, 2)
			httpAddress, grpcAddress := addresses[0], addresses[1]
			envValues := map[string]string{
				config.MongoURIEnv:               os.Getenv(mongoIntegrationURIEnv),
				config.MongoDatabaseEnv:          database.Name(),
				config.HTTPAddrEnv:               httpAddress,
				config.GRPCAddrEnv:               grpcAddress,
				config.IdentityIssuerEnv:         e2eIssuer,
				config.IdentityAudienceEnv:       e2eAudience,
				config.IdentityPublicKeysEnv:     e2eKeyID + "=" + publicKeyPath,
				config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget,
				config.ScopeCatalogCacheTTLEnv:   "1ns",
			}
			if prefixOverride != "" {
				envValues[config.TesterJoinURLPrefixEnv] = prefixOverride
			}
			configuration, err := config.Load(e2eEnvironment(envValues))
			if err != nil {
				t.Fatalf("load configuration: %v", err)
			}
			app, appCleanup, err := wireAppWithResolver(configuration, &e2eDNSResolver{})
			if err != nil {
				t.Fatalf("wireAppWithResolver() error = %v", err)
			}
			runDone := make(chan struct{})
			var runErr error
			go func() {
				runErr = app.Run()
				close(runDone)
			}()
			t.Cleanup(func() {
				if stopErr := app.Stop(); stopErr != nil {
					t.Errorf("app.Stop() error = %v", stopErr)
				}
				select {
				case <-runDone:
				case <-time.After(15 * time.Second):
					t.Error("app.Run() did not stop within 15s")
				}
				if runErr != nil && !errors.Is(runErr, context.Canceled) {
					t.Errorf("app.Run() error = %v", runErr)
				}
				appCleanup()
			})
			e2eWaitForHTTPListener(t, ctx, httpAddress, runDone, &runErr)
			connection := e2eDialGRPC(t, ctx, grpcAddress, runDone, &runErr)
			t.Cleanup(func() { _ = connection.Close() })

			createStatus, applicationBody := e2eHTTPCreate(t, httpAddress, adminToken, "Tester_Join_Link_E2E_App")
			if createStatus != http.StatusCreated {
				t.Fatalf("create predecessor application status = %d; body = %s", createStatus, applicationBody)
			}
			var application applicationv1.CreateApplicationResponse
			if err := protojson.Unmarshal(applicationBody, &application); err != nil {
				t.Fatalf("decode predecessor application: %v", err)
			}

			post := func(token string, expected *string) (int, http.Header, []byte) {
				payload, err := json.Marshal(map[string]any{"expectedActiveJoinLinkId": expected})
				if err != nil {
					t.Fatal("encode request failed")
				}
				path := fmt.Sprintf("http://%s/v1/applications/%s/tester-join-links", httpAddress, application.GetId())
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(payload))
				if err != nil {
					t.Fatal("create request failed")
				}
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set(transport.IdentityHeader, token)
				response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
				if err != nil {
					t.Fatal("request failed")
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatal("read response failed")
				}
				return response.StatusCode, response.Header, body
			}
			for _, tc := range []struct {
				token string
				code  int
			}{
				{"", 401}, {e2eSignIdentity(t, privateKey, "other-admin", "APPROVED"), 403}, {e2eSignIdentity(t, privateKey, adminID, "PENDING"), 403},
			} {
				code, _, body := post(tc.token, nil)
				if code != tc.code || bytes.Contains(body, []byte("joinUrl")) || bytes.Contains(body, []byte("tokenHash")) {
					t.Fatalf("unexpected authorization response status=%d", code)
				}
			}
			checkURL := func(response *testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse) [32]byte {
				t.Helper()
				parsed, err := url.Parse(response.GetJoinUrl())
				if err != nil {
					t.Fatal("join URL is invalid")
				}
				fragment, err := url.ParseQuery(parsed.Fragment)
				if err != nil || len(fragment) != 2 || len(fragment["joinLinkId"]) != 1 || len(fragment["secret"]) != 1 || fragment.Get("joinLinkId") != response.GetJoinLink().GetJoinLinkId() {
					t.Fatal("join fragment contract mismatch")
				}
				secret := fragment.Get("secret")
				raw, err := base64.RawURLEncoding.DecodeString(secret)
				if err != nil || len(raw) != 32 {
					t.Fatal("secret encoding mismatch")
				}
				parsed.Fragment = ""
				wantPrefix := prefixOverride
				if wantPrefix == "" {
					wantPrefix = "https://app.example/tester/join"
				}
				if parsed.String() != wantPrefix || parsed.RawQuery != "" || strings.Contains(parsed.Path, secret) {
					t.Fatal("join prefix or credential placement mismatch")
				}
				return sha256.Sum256(raw)
			}
			code, headers, body := post(adminToken, nil)
			if code != 201 || headers.Get("Cache-Control") != "no-store" {
				t.Fatalf("create status/cache-control=%d/%s", code, headers.Get("Cache-Control"))
			}
			var created testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse
			if err := protojson.Unmarshal(body, &created); err != nil {
				t.Fatal("decode create response failed")
			}
			if created.GetJoinLink().GetStatus() != "ACTIVE" || created.ReplacedJoinLinkId != nil || bytes.Contains(body, []byte("tokenHash")) {
				t.Fatal("public create metadata mismatch")
			}
			originalHash := checkURL(&created)
			code, _, body = post(adminToken, nil)
			if code != 409 || !bytes.Contains(body, []byte(transport.ReasonApplicationTesterJoinLinkAlreadyExists)) || bytes.Contains(body, []byte("joinUrl")) {
				t.Fatalf("duplicate response status=%d", code)
			}
			previous := created.GetJoinLink().GetJoinLinkId()
			code, headers, body = post(adminToken, &previous)
			if code != 201 || headers.Get("Cache-Control") != "no-store" {
				t.Fatalf("rotation status/cache-control=%d/%s", code, headers.Get("Cache-Control"))
			}
			var rotated testerjoinlinkv1.CreateOrRotateTesterJoinLinkResponse
			if err := protojson.Unmarshal(body, &rotated); err != nil {
				t.Fatal("decode rotate response failed")
			}
			rotatedHash := checkURL(&rotated)
			if rotated.GetReplacedJoinLinkId() != previous || rotated.GetJoinLink().GetJoinLinkId() == previous || rotatedHash == originalHash {
				t.Fatal("rotation did not replace credentials")
			}
			client := testerjoinlinkv1.NewTesterJoinLinkClient(connection)
			grpcCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, adminToken))
			_, err = client.CreateOrRotateTesterJoinLink(grpcCtx, &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: application.GetId(), Command: &testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{ExpectedActiveJoinLinkId: &previous}})
			if status.Code(err) != codes.Aborted || e2eErrorReason(status.Convert(err)) != transport.ReasonApplicationTesterJoinLinkChanged {
				t.Fatal("stale gRPC command did not fail safely")
			}
			active := rotated.GetJoinLink().GetJoinLinkId()
			var grpcHeaders metadata.MD
			final, err := client.CreateOrRotateTesterJoinLink(grpcCtx, &testerjoinlinkv1.CreateOrRotateTesterJoinLinkRequest{ApplicationId: application.GetId(), Command: &testerjoinlinkv1.CreateOrRotateTesterJoinLinkCommand{ExpectedActiveJoinLinkId: &active}}, grpc.Header(&grpcHeaders))
			if err != nil {
				t.Fatal("gRPC rotation failed")
			}
			if values := grpcHeaders.Get("cache-control"); len(values) != 1 || values[0] != "no-store" {
				t.Fatal("gRPC sensitive response cache directive missing")
			}
			finalHash := checkURL(final)
			collection := database.Collection("application_tester_join_links")
			for _, check := range []struct {
				id          string
				hash        [32]byte
				state       string
				replacement string
			}{
				{previous, originalHash, "REVOKED", active}, {active, rotatedHash, "REVOKED", final.GetJoinLink().GetJoinLinkId()}, {final.GetJoinLink().GetJoinLinkId(), finalHash, "ACTIVE", ""},
			} {
				var document bson.M
				if err := collection.FindOne(ctx, bson.M{"joinLinkId": check.id}).Decode(&document); err != nil {
					t.Fatal("read join link failed")
				}
				binary, ok := document["tokenHash"].(bson.Binary)
				if !ok || !bytes.Equal(binary.Data, check.hash[:]) || document["status"] != check.state {
					t.Fatal("stored hash or lifecycle mismatch")
				}
				if check.state == "REVOKED" && (document["revocationReason"] != "ROTATED" || document["replacedByJoinLinkId"] != check.replacement || document["revokedBy"] != adminID) {
					t.Fatal("revocation audit mismatch")
				}
				for _, forbidden := range []string{"secret", "rawToken", "joinUrl", "token"} {
					if _, ok := document[forbidden]; ok {
						t.Fatal("credential plaintext persisted")
					}
				}
			}
			count, err := collection.CountDocuments(ctx, bson.M{"applicationId": application.GetId(), "status": "ACTIVE"})
			if err != nil || count != 1 {
				t.Fatal("single active link invariant failed")
			}
			for _, collectionName := range []string{"application_publications", "application_tester_memberships"} {
				count, err := database.Collection(collectionName).CountDocuments(ctx, bson.M{"applicationId": application.GetId()})
				if err != nil || count != 0 {
					t.Fatal("join link operation changed unrelated capability")
				}
			}
		})
	}
}
