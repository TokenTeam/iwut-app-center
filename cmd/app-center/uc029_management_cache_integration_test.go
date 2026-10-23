package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	av "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application"
	tv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	mv "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_management"
	"google.golang.org/protobuf/encoding/protojson"

	"iwut-app-center/internal/adapter/transport"
)

// The caller is the existing real Auth/Gateway fixture. Compare independent
// HTTP requests to App itself and the actual Traefik -> Gateway -> App chain;
// no proxy, response or identity is substituted for either path.
func uc029ManagementCacheIntegration(t *testing.T, ctx context.Context, ownerSession, otherSession, ownerToken, otherToken, firstID string) {
	t.Helper()
	code, _, body := uc002HTTPRequest(t, ctx, "/app-center/v1/applications", ownerSession, []byte(`{"name":"UC029_Cache_Transfer"}`), nil)
	var second av.CreateApplicationResponse
	if code != 201 || protojson.Unmarshal(body, &second) != nil {
		t.Fatalf("create second management resource status=%d", code)
	}
	request := func(edge bool, method, path, session, token, payload string) (int, http.Header, []byte) {
		t.Helper()
		if edge {
			return gatewayHTTP(t, ctx, method, "/app-center"+path, session, []byte(payload), nil)
		}
		r, err := http.NewRequestWithContext(ctx, method, "http://"+os.Getenv("UC002_APP_HTTP_ADDR")+path, strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(transport.IdentityHeader, token)
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, response.Header, b
	}
	check := func(label, method, path, session, token, payload, reason string, want int) {
		t.Helper()
		for _, edge := range []bool{false, true} {
			source := "app-direct"
			if edge {
				source = "gateway-edge"
			}
			code, headers, b := request(edge, method, path, session, token, payload)
			if code != want || reason != "" && !bytes.Contains(b, []byte(reason)) {
				t.Errorf("%s %s status/reason mismatch: status=%d want=%d reason=%s", source, label, code, want, reason)
			}
			if headers.Get("Cache-Control") != "private, no-store" {
				t.Errorf("%s %s status=%d cache=%q, want private, no-store", source, label, code, headers.Get("Cache-Control"))
			}
			t.Logf("%s %s status=%d cache=%q", source, label, code, headers.Get("Cache-Control"))
		}
	}
	const listPath = "/v1/applications:search"
	code, _, body = request(false, http.MethodPost, listPath, ownerSession, ownerToken, `{"pageSize":1}`)
	var page mv.OwnedApplicationPage
	if code != 200 || protojson.Unmarshal(body, &page) != nil || page.GetNextPageToken() == "" {
		t.Fatal("real owned pagination did not produce a continuation")
	}
	check("list-success", http.MethodPost, listPath, ownerSession, ownerToken, `{"pageSize":1}`, "", 200)
	check("owner-detail", http.MethodGet, "/v1/applications/"+firstID, ownerSession, ownerToken, "", "", 200)
	check("hidden-non-owner", http.MethodGet, "/v1/applications/"+firstID, otherSession, otherToken, "", transport.ReasonApplicationManagementNotFound, 404)
	check("hidden-nonexistent", http.MethodGet, "/v1/applications/01890f47-0000-7000-8000-000000000099", otherSession, otherToken, "", transport.ReasonApplicationManagementNotFound, 404)
	for _, tc := range []struct {
		label, token, filter string
	}{
		{"malformed-cursor", "invalid-cursor", ""},
		{"lifecycle-rebinding", page.GetNextPageToken(), `,"lifecycleStatuses":["ACTIVE"]`},
		{"platform-rebinding", page.GetNextPageToken(), `,"platformAvailabilityStatuses":["AVAILABLE"]`},
		{"archive-rebinding", page.GetNextPageToken(), `,"archiveStatuses":["UNARCHIVED"]`},
	} {
		cursor, err := json.Marshal(tc.token)
		if err != nil {
			t.Fatal(err)
		}
		check(tc.label, http.MethodPost, listPath, ownerSession, ownerToken, `{"pageSize":1,"pageToken":`+string(cursor)+tc.filter+`}`, transport.ReasonInvalidApplicationManagementPageToken, 400)
	}
	cursor, err := json.Marshal(page.GetNextPageToken())
	if err != nil {
		t.Fatal(err)
	}
	check("caller-rebinding", http.MethodPost, listPath, otherSession, otherToken, `{"pageSize":1,"pageToken":`+string(cursor)+`}`, transport.ReasonInvalidApplicationManagementPageToken, 400)
	// Use formal commands, rather than changing adminId in Mongo, to prove that
	// ownership transfer still hides the resource from the old administrator.
	code, _, body = uc002HTTPRequest(t, ctx, "/app-center/v1/applications/"+second.GetId()+"/admin-transfers", ownerSession, []byte(`{"toAuthId":"uc002-other","expectedOwnershipRevision":"1"}`), nil)
	var initiated tv.InitiateApplicationAdminTransferResponse
	if code != 201 || protojson.Unmarshal(body, &initiated) != nil || initiated.GetTransfer().GetTransferId() == "" {
		t.Fatalf("formal management transfer initiation status=%d", code)
	}
	code, _, body = uc002HTTPRequest(t, ctx, "/app-center/v1/application-admin-transfers/"+initiated.GetTransfer().GetTransferId()+":accept", otherSession, []byte(`{"confidentialCredentialHandling":"CONFIDENTIAL_CREDENTIAL_HANDLING_KEEP"}`), nil)
	var accepted tv.AcceptApplicationAdminTransferResponse
	if code != 200 || protojson.Unmarshal(body, &accepted) != nil || accepted.GetTransfer().GetStatus() != tv.ApplicationAdminTransferStatus_APPLICATION_ADMIN_TRANSFER_STATUS_ACCEPTED {
		t.Fatalf("formal management transfer acceptance status=%d", code)
	}
	check("old-admin-after-transfer", http.MethodGet, "/v1/applications/"+second.GetId(), ownerSession, ownerToken, "", transport.ReasonApplicationManagementNotFound, 404)
	check("new-admin-after-transfer", http.MethodGet, "/v1/applications/"+second.GetId(), otherSession, otherToken, "", "", 200)
}
