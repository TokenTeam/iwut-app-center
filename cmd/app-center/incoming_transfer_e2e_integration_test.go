package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	appv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	developerstatusv1 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/developer_status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"

	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
)

func TestE2E_UCAPP050_IncomingApplicationTransfersHTTPAndGRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	key, path := e2eIdentity(t)
	addresses := e2eReserveAddresses(t, 3)
	_, auth := e2eStartAuthServer(t, addresses[2])
	auth.SetStatus("incoming-e2e-target", developerstatusv1.DeveloperStatus_DEVELOPER_STATUS_APPROVED)
	cfg, err := config.Load(e2eEnvironment(map[string]string{config.MongoURIEnv: os.Getenv(mongoIntegrationURIEnv), config.MongoDatabaseEnv: db.Name(), config.HTTPAddrEnv: addresses[0], config.GRPCAddrEnv: addresses[1], config.IdentityIssuerEnv: e2eIssuer, config.IdentityAudienceEnv: e2eAudience, config.IdentityPublicKeysEnv: e2eKeyID + "=" + path, config.AuthScopeCatalogTargetEnv: addresses[2]}))
	if err != nil {
		t.Fatal(err)
	}
	app, cleanup, err := wireApp(cfg)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var runErr error
	go func() { runErr = app.Run(); close(done) }()
	t.Cleanup(func() {
		defer cleanup()
		_ = app.Stop()
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
	defer connection.Close()
	source := e2eSignIdentity(t, key, "incoming-e2e-source", "APPROVED")
	target := e2eSignReviewerIdentity(t, key, "incoming-e2e-target")
	sourceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, source))
	targetCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, target))
	transfers := pb.NewApplicationAdminTransferServiceClient(connection)
	empty, err := transfers.ListIncomingApplicationAdminTransfers(targetCtx, &pb.ListIncomingApplicationAdminTransfersRequest{})
	if err != nil || len(empty.GetItems()) != 0 {
		t.Fatal("ordinary empty query", err)
	}
	created, err := appv1.NewApplicationClient(connection).CreateApplication(sourceCtx, &appv1.CreateApplicationRequest{Name: "Incoming_Transfer_App"})
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := transfers.GetApplicationOwnership(sourceCtx, &pb.GetApplicationOwnershipRequest{ApplicationId: created.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := transfers.InitiateApplicationAdminTransfer(sourceCtx, &pb.InitiateApplicationAdminTransferRequest{ApplicationId: created.GetId(), Command: &pb.InitiateApplicationAdminTransferCommand{ToAuthId: "incoming-e2e-target", ExpectedOwnershipRevision: ownership.GetOwnership().GetOwnershipRevision()}})
	if err != nil {
		t.Fatal(err)
	}
	// A read must not depend on Auth's availability; accepting still does.
	auth.unavailable.Store(true)
	authReads := auth.calls.Load()
	const searchPath = "/v1/application-admin-transfers/incoming:search"
	for _, developerState := range []string{"", "PENDING", "SUSPENDED", "WITHDRAWN"} {
		token := target
		if developerState != "" {
			token = e2eSignIdentity(t, key, "incoming-e2e-target", developerState)
		}
		code, headers, raw := e2eHTTPAdminTransfer(t, addresses[0], token, http.MethodPost, searchPath, []byte(`{"pageSize":1}`))
		var page pb.IncomingApplicationAdminTransferPage
		if code != 200 || headers.Get("Cache-Control") != "private, no-store" || protojson.Unmarshal(raw, &page) != nil || len(page.Items) != 1 {
			t.Fatalf("http state %s status %d", developerState, code)
		}
		item := page.Items[0]
		if item.GetTransferId() != submitted.GetTransfer().GetTransferId() || item.GetApplicationId() != created.GetId() || item.GetApplicationName() != created.GetName() || item.GetToAuthId() != "incoming-e2e-target" || item.GetFromAuthId() != "incoming-e2e-source" {
			t.Fatal("wrong projection")
		}
		for _, private := range []string{"clientSecret", "sourceOwnershipRevision", "credential", "quota", "permissions", "nameKey"} {
			if strings.Contains(string(raw), private) {
				t.Fatal("private field disclosed", private)
			}
		}
	}
	if auth.calls.Load() != authReads {
		t.Fatal("incoming read called Auth")
	}
	page, err := transfers.ListIncomingApplicationAdminTransfers(targetCtx, &pb.ListIncomingApplicationAdminTransfersRequest{})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("native query", err)
	}
	if _, err := transfers.AcceptApplicationAdminTransfer(targetCtx, &pb.AcceptApplicationAdminTransferRequest{TransferId: submitted.GetTransfer().GetTransferId(), Command: &pb.AcceptApplicationAdminTransferCommand{ConfidentialCredentialHandling: pb.ConfidentialCredentialHandling_CONFIDENTIAL_CREDENTIAL_HANDLING_KEEP}}); status.Code(err) != codes.Unavailable {
		t.Fatal("list bypassed fresh acceptance", err)
	}
	code, headers, raw := e2eHTTPAdminTransfer(t, addresses[0], "", http.MethodPost, searchPath, []byte(`{"toAuthId":"other"}`))
	if code != 401 || headers.Get("Cache-Control") != "private, no-store" || !strings.Contains(string(raw), "USER_IDENTITY_REQUIRED") {
		t.Fatal("auth before input", code)
	}
	for _, body := range []string{`{"toAuthId":"other"}`, `{"pageSize":-1}`, `{"pageToken":"not-a-cursor"}`, `{} {}`, `null`} {
		code, headers, _ := e2eHTTPAdminTransfer(t, addresses[0], target, http.MethodPost, searchPath, []byte(body))
		if code != 400 || headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal("strict HTTP input", code)
		}
	}
	req := &pb.ListIncomingApplicationAdminTransfersRequest{Query: &pb.IncomingApplicationAdminTransferQuery{}}
	req.Query.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	if _, err := transfers.ListIncomingApplicationAdminTransfers(targetCtx, req); status.Code(err) != codes.InvalidArgument {
		t.Fatal("native unknown field", err)
	}
	outsider := metadata.NewOutgoingContext(ctx, metadata.Pairs(transport.IdentityHeader, e2eSignReviewerIdentity(t, key, "incoming-outsider")))
	other, err := transfers.ListIncomingApplicationAdminTransfers(outsider, &pb.ListIncomingApplicationAdminTransfersRequest{})
	if err != nil || len(other.Items) != 0 {
		t.Fatal("other user enumerated", err)
	}
	if _, err := transfers.CancelApplicationAdminTransfer(sourceCtx, &pb.CancelApplicationAdminTransferRequest{TransferId: submitted.GetTransfer().GetTransferId()}); err != nil {
		t.Fatal(err)
	}
	code, headers, raw = e2eHTTPAdminTransfer(t, addresses[0], target, http.MethodPost, searchPath, []byte(`{}`))
	if code != 200 || headers.Get("Cache-Control") != "private, no-store" || !strings.Contains(string(raw), `"items":[]`) {
		t.Fatal("resolved explicit empty", code, string(raw))
	}
}
