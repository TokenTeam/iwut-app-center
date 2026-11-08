package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	appv1 "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestE2E_UCAPP050_IncomingTransfersLocalImages(t *testing.T) {
	if os.Getenv("UC050_LOCAL_IMAGES") != "1" {
		t.Skip("run test-uc002-gateway-auth-integration.sh --incoming-transfers with fixed local images")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	source, target := registerAuthFixture(t, ctx), registerAuthFixture(t, ctx)
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnv)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	authDB := client.Database(os.Getenv("AUTH_CENTER_INTEGRATION_DATABASE"))
	// Qualification is controlled fixture data after formal registration/device
	// login. Auth remains the sole writer of credentials, email and Sessions.
	result, err := authDB.Collection("auth_principals").UpdateMany(ctx, bson.M{"authId": bson.M{"$in": bson.A{source.authID, target.authID}}}, bson.M{"$set": bson.M{"developerStatus": "APPROVED", "developerRevision": int64(1), "updatedAt": time.Now().UTC().Truncate(time.Millisecond)}})
	if err != nil || result.ModifiedCount != 2 {
		t.Fatal("controlled Developer qualification", err)
	}
	listener, err := net.Listen("tcp", os.Getenv("UC041_ADMIN_FORWARD_AUTH_ADDR"))
	if err != nil {
		t.Fatal(err)
	}
	forward := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("iwut_uc050_fixture")
		if r.URL.Path != "/internal/forward-auth" || err != nil || (cookie.Value != source.session && cookie.Value != target.session) {
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
	sourceCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", source.session))
	targetCtx := metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", target.session))
	transferClient := pb.NewApplicationAdminTransferServiceClient(edge)
	application, err := appv1.NewApplicationClient(edge).CreateApplication(sourceCtx, &appv1.CreateApplicationRequest{Name: "Incoming_Real_Session"})
	if err != nil {
		t.Fatal("real App create", err)
	}
	ownership, err := transferClient.GetApplicationOwnership(sourceCtx, &pb.GetApplicationOwnershipRequest{ApplicationId: application.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := transferClient.InitiateApplicationAdminTransfer(sourceCtx, &pb.InitiateApplicationAdminTransferRequest{ApplicationId: application.GetId(), Command: &pb.InitiateApplicationAdminTransferCommand{ToAuthId: target.authID, ExpectedOwnershipRevision: ownership.GetOwnership().GetOwnershipRevision()}})
	if err != nil {
		t.Fatal("real initiation", err)
	}
	request := &pb.ListIncomingApplicationAdminTransfersRequest{Query: &pb.IncomingApplicationAdminTransferQuery{PageSize: 20}}
	check := func(page *pb.IncomingApplicationAdminTransferPage) {
		t.Helper()
		if len(page.GetItems()) != 1 || page.Items[0].GetTransferId() != pending.GetTransfer().GetTransferId() || page.Items[0].GetApplicationName() != application.GetName() || page.Items[0].GetToAuthId() != target.authID || page.Items[0].GetFromAuthId() != source.authID {
			t.Fatal("wrong participant projection")
		}
	}
	page, err := transferClient.ListIncomingApplicationAdminTransfers(targetCtx, request)
	if err != nil {
		t.Fatal("native incoming query", err)
	}
	check(page)
	body := []byte(`{"pageSize":20}`)
	const path = "/app-center/v1/application-admin-transfers/incoming:search"
	code, headers, raw := gatewayHTTP(t, ctx, http.MethodPost, path, target.session, body, nil)
	if code != 200 || headers.Get("Cache-Control") != "private, no-store" || protojson.Unmarshal(raw, page) != nil {
		t.Fatal("public HTTP query", code)
	}
	check(page)
	var web pb.IncomingApplicationAdminTransferPage
	if code := gatewayWeb(t, ctx, target.session, pb.ApplicationAdminTransferService_ListIncomingApplicationAdminTransfers_FullMethodName, request, &web); code != int(codes.OK) {
		t.Fatal("gRPC-Web incoming query", code)
	}
	check(&web)
	consoleCall := func(host string, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+os.Getenv("UC002_EDGE_HTTP_ADDR")+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "iwut_uc050_fixture", Value: target.session})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil || res.StatusCode != want {
			t.Fatal("Console surface", host, res.StatusCode, err)
		}
		if want == 200 {
			var p pb.IncomingApplicationAdminTransferPage
			if protojson.Unmarshal(data, &p) != nil {
				t.Fatal("Console body")
			}
			check(&p)
		}
	}
	consoleCall("developer.uc002.test", 200)
	consoleCall("admin.uc002.test", 404)
	code, _, _ = gatewayHTTP(t, ctx, http.MethodPost, path, "", body, nil)
	if code != 401 {
		t.Fatal("missing Session", code)
	}
	if own, err := transferClient.ListIncomingApplicationAdminTransfers(sourceCtx, request); err != nil || len(own.GetItems()) != 0 {
		t.Fatal("sender enumerated incoming", err)
	}
	result, err = authDB.Collection("auth_principals").UpdateOne(ctx, bson.M{"authId": target.authID}, bson.M{"$set": bson.M{"developerStatus": "SUSPENDED", "updatedAt": time.Now().UTC().Truncate(time.Millisecond)}, "$inc": bson.M{"developerRevision": int64(1)}})
	if err != nil || result.ModifiedCount != 1 {
		t.Fatal("qualification change", err)
	}
	page, err = transferClient.ListIncomingApplicationAdminTransfers(targetCtx, request)
	if err != nil {
		t.Fatal("same real Session suspended read", err)
	}
	check(page)
	if _, err := transferClient.AcceptApplicationAdminTransfer(targetCtx, &pb.AcceptApplicationAdminTransferRequest{TransferId: pending.GetTransfer().GetTransferId(), Command: &pb.AcceptApplicationAdminTransferCommand{ConfidentialCredentialHandling: pb.ConfidentialCredentialHandling_CONFIDENTIAL_CREDENTIAL_HANDLING_KEEP}}); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("suspended recipient bypassed acceptance", err)
	}
	if _, err := transferClient.CancelApplicationAdminTransfer(sourceCtx, &pb.CancelApplicationAdminTransferRequest{TransferId: pending.GetTransfer().GetTransferId()}); err != nil {
		t.Fatal("real cancel", err)
	}
	page, err = transferClient.ListIncomingApplicationAdminTransfers(targetCtx, request)
	if err != nil || len(page.GetItems()) != 0 {
		t.Fatal("resolved inbox", err)
	}
}
