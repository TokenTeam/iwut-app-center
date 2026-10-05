package main

import (
	"context"
	"encoding/base64"
	"errors"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/account_owner_exit"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestE2E_UCAPP025_OwnerExitProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, path := e2eIdentity(t)
	addresses := e2eReserveAddresses(t, 2)
	registry, _ := base64.StdEncoding.DecodeString(e2eServiceCallerRegistryB64())
	registry = []byte(strings.Replace(string(registry), `"app.oauth.redirects.read"`, `"app.oauth.redirects.read","app.account-owner-exit.prepare","app.account-owner-exit.finish","app.account-owner-exit.read"`, 1))
	c, e := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: addresses[0], config.GRPCAddrEnv: addresses[1], config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + path, config.AuthScopeCatalogTargetEnv: e2eUnusedAuthTarget, config.ServiceCallersEnv: base64.StdEncoding.EncodeToString(registry), "APP_CENTER_ACCOUNT_OWNER_EXIT_ENABLED": "true"}))
	if e != nil {
		t.Fatal(e)
	}
	app, cleanup, e := wireAppWithResolver(c, &e2eDNSResolver{})
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(done) }()
	t.Cleanup(func() {
		_ = app.Stop()
		<-done
		cleanup()
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Error(runErr)
		}
	})
	e2eWaitForHTTPListener(t, ctx, addresses[0], done, &runErr)
	conn := e2eDialGRPC(t, ctx, addresses[1], done, &runErr)
	defer conn.Close()
	client := pb.NewAccountOwnerExitServiceClient(conn)
	request := &pb.PrepareAccountOwnerExitRequest{AuthId: "departing", OperationId: uuid.NewString(), Purpose: pb.Purpose_PURPOSE_ACCOUNT_CLOSURE}
	if _, e = client.PrepareAccountOwnerExit(ctx, request); status.Code(e) != codes.Unauthenticated {
		t.Fatal(e)
	}
	noPermission := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+e2eSignServiceIdentity(t, "iwut-auth-read-only", e2eAudience)))
	if _, e = client.PrepareAccountOwnerExit(noPermission, request); status.Code(e) != codes.PermissionDenied {
		t.Fatal(e)
	}
	serviceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+e2eSignServiceIdentity(t, "iwut-auth-center", e2eAudience)))
	prepared, e := client.PrepareAccountOwnerExit(serviceCtx, request)
	if e != nil || prepared.GetOutcome() != pb.Outcome_OUTCOME_PREPARED {
		t.Fatalf("%v %v", prepared, e)
	}
	token := e2eSignIdentity(t, key, "departing", "APPROVED")
	code, body := e2eHTTPCreate(t, addresses[0], token, "forbidden_late_creation")
	if code != http.StatusBadRequest {
		t.Fatalf("late create %d %s", code, body)
	}
	finished, e := client.FinishAccountOwnerExit(serviceCtx, &pb.FinishAccountOwnerExitRequest{AuthId: request.AuthId, OperationId: request.OperationId, Purpose: request.Purpose, ReceiptId: prepared.ReceiptId, Decision: pb.Decision_DECISION_COMMITTED})
	if e != nil || finished.CleanupState != pb.CleanupState_CLEANUP_STATE_PENDING {
		t.Fatalf("%v %v", finished, e)
	}
	// Explicitly drive the same production usecase worker step; no timing sleep.
	handlers := provideOwnerExitHandlers(db, conn)
	if e = handlers.Reconcile(ctx); e != nil {
		t.Fatal(e)
	}
	state, e := client.GetAccountOwnerExitStatus(serviceCtx, &pb.GetAccountOwnerExitStatusRequest{AuthId: request.AuthId, OperationId: request.OperationId})
	if e != nil || state.CleanupState != pb.CleanupState_CLEANUP_STATE_COMPLETE {
		t.Fatalf("%v %v", state, e)
	}
	withdrawn := e2eSignIdentity(t, key, "former-developer", "WITHDRAWN")
	code, body = e2eHTTPCreate(t, addresses[0], withdrawn, "not_a_developer")
	if code != http.StatusForbidden {
		t.Fatalf("WITHDRAWN must parse as a user %d %s", code, body)
	}
	// No HTTP annotation: the internal provider is absent from HTTP even with service credentials.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addresses[0]+"/v1/account-owner-exits:prepare", nil)
	req.Header.Set(transport.ServiceAuthorizationHeader, "Bearer "+e2eSignServiceIdentity(t, "iwut-auth-center", e2eAudience))
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatal(res.StatusCode)
	}
}
