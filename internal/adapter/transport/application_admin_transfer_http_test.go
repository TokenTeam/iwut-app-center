package transport

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
)

func TestApplicationAdminTransferHTTP_BR_APP_014_016_017_StatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
		code   int
	}{
		{name: "quota", reason: "ERROR_REASON_APPLICATION_QUOTA_EXCEEDED", code: http.StatusConflict},
		{name: "expired", reason: "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_EXPIRED", code: http.StatusConflict},
		{name: "not pending", reason: "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_NOT_PENDING", code: http.StatusConflict},
		{name: "owner exit", reason: "ERROR_REASON_ACCOUNT_OWNER_EXIT_IN_PROGRESS", code: http.StatusConflict},
		{name: "source ineligible", reason: "ERROR_REASON_SOURCE_DEVELOPER_INELIGIBLE", code: http.StatusUnprocessableEntity},
		{name: "target ineligible", reason: "ERROR_REASON_TARGET_DEVELOPER_INELIGIBLE", code: http.StatusUnprocessableEntity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "/v1/application-admin-transfers/01890f47-0000-7000-8000-000000000301:accept", strings.NewReader(`{"confidentialCredentialHandling":"CONFIDENTIAL_CREDENTIAL_HANDLING_KEEP"}`))
			response := httptest.NewRecorder()
			applicationAdminTransferSafeErrorEncoder(response, request, transportStatus(codes.FailedPrecondition, test.reason, "safe"))
			if response.Code != test.code || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), test.reason) {
				t.Fatalf("response = status:%d cache:%q body:%s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
			}
		})
	}
}

func TestApplicationAdminTransferHTTP_BR_APP_019_StrictQueryAndActionBody(t *testing.T) {
	t.Parallel()

	valid := httptest.NewRequest(http.MethodPost, "/v1/application-admin-transfers/01890f47-0000-7000-8000-000000000301:reject", strings.NewReader(`{"transferId":"01890f47-0000-7000-8000-000000000301"}`))
	if !isApplicationAdminTransferRequest(valid) || !validApplicationAdminTransferHTTPInput(valid) {
		t.Fatal("generated-client reject body was not accepted")
	}
	tests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/v1/application-admin-transfers/01890f47-0000-7000-8000-000000000301?private=value", nil),
		httptest.NewRequest(http.MethodGet, "/v1/applications/01890f47-0000-7000-8000-000000000302/ownership", strings.NewReader(`{"private":"value"}`)),
		httptest.NewRequest(http.MethodPost, "/v1/application-admin-transfers/01890f47-0000-7000-8000-000000000301:cancel", strings.NewReader(`{"transferId":"different"}`)),
		httptest.NewRequest(http.MethodPost, "/v1/application-admin-transfers/01890f47-0000-7000-8000-000000000301:reject", strings.NewReader(`{"private":"value"}`)),
	}
	for _, request := range tests {
		if !isApplicationAdminTransferRequest(request) || validApplicationAdminTransferHTTPInput(request) {
			t.Fatalf("request %s %s unexpectedly accepted", request.Method, request.URL)
		}
	}
}
