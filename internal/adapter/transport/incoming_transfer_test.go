package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_admin_transfer"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type incomingTransportFake struct{ called bool }

func (f *incomingTransportFake) List(context.Context, shared.AuthenticatedUserIdentity, int32, string) (domain.IncomingApplicationAdminTransferPage, error) {
	f.called = true
	return domain.IncomingApplicationAdminTransferPage{Items: []domain.IncomingApplicationAdminTransfer{}, AsOf: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)}, nil
}
func TestIncomingTransferTransport_UCAPP050_BRAPP059062(t *testing.T) {
	fake := &incomingTransportFake{}
	service := NewApplicationAdminTransferService(nil, fake)
	if _, err := service.ListIncomingApplicationAdminTransfers(t.Context(), nil); status.Code(err) != codes.Unauthenticated || fake.called {
		t.Fatal("identity first", err)
	}
	ctx := withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "ordinary-target"})
	unknown := &pb.ListIncomingApplicationAdminTransfersRequest{Query: &pb.IncomingApplicationAdminTransferQuery{}}
	unknown.Query.ProtoReflect().SetUnknown(protowire.AppendVarint(protowire.AppendTag(nil, 99, protowire.VarintType), 1))
	if _, err := service.ListIncomingApplicationAdminTransfers(ctx, unknown); status.Code(err) != codes.InvalidArgument || fake.called {
		t.Fatal("unknown nested input", err)
	}
	if page, err := service.ListIncomingApplicationAdminTransfers(ctx, &pb.ListIncomingApplicationAdminTransfersRequest{}); err != nil || page.Items == nil || !fake.called {
		t.Fatal("ordinary user/default empty", err)
	}
}
func TestIncomingTransferHTTP_UCAPP050_BRAPP062_StrictBodyAndErrors(t *testing.T) {
	for _, tc := range []struct {
		body, query, media string
		valid              bool
	}{
		{`{}`, "", "application/json", true}, {`{"pageSize":1,"pageToken":"abc"}`, "", "application/json", true},
		{`{"toAuthId":"someone"}`, "", "application/json", false}, {`{"query":{"pageSize":1}}`, "", "application/json", false},
		{`{} {}`, "", "application/json", false}, {`null`, "", "application/json", false}, {`[]`, "", "application/json", false},
		{`{}`, "pageSize=2", "application/json", false}, {`{}`, "", "text/plain", false}, {strings.Repeat(" ", 16*1024) + `{}`, "", "application/json", false},
	} {
		t.Run(tc.body[:min(len(tc.body), 30)]+tc.query+tc.media, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, incomingTransferPath, strings.NewReader(tc.body))
			r.URL.RawQuery = tc.query
			r.Header.Set("Content-Type", tc.media)
			if got := validIncomingTransferHTTPBody(httptest.NewRecorder(), r); got != tc.valid {
				t.Fatalf("valid=%v want %v", got, tc.valid)
			}
		})
	}
	for _, tc := range []struct {
		err    error
		code   int
		reason string
	}{
		{domain.ErrIncomingTransferIdentityRequired, 401, ReasonUserIdentityRequired},
		{domain.ErrInvalidIncomingTransferQuery, 400, "ERROR_REASON_INVALID_INCOMING_APPLICATION_TRANSFER_QUERY"},
		{domain.ErrInvalidIncomingTransferPageToken, 400, "ERROR_REASON_INVALID_INCOMING_APPLICATION_TRANSFER_PAGE_TOKEN"},
		{domain.ErrApplicationAdminTransferStateInconsistent, 500, "ERROR_REASON_APPLICATION_ADMIN_TRANSFER_STATE_INCONSISTENT"},
		{errors.New("private backend details"), 503, "ERROR_REASON_INCOMING_APPLICATION_TRANSFER_UNAVAILABLE"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, incomingTransferPath, nil)
		incomingTransferHTTPError(w, r, incomingTransferError(tc.err))
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), tc.reason) || strings.Contains(w.Body.String(), "private backend details") {
			t.Fatal("safe error/cache", w.Code)
		}
	}
}
