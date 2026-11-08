package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type incomingQueryFake struct {
	called bool
	query  domain.IncomingTransferQuery
	page   domain.IncomingApplicationAdminTransferPage
	err    error
}

func (f *incomingQueryFake) ListIncoming(_ context.Context, _ shared.AuthID, q domain.IncomingTransferQuery, at time.Time) (domain.IncomingApplicationAdminTransferPage, error) {
	f.called = true
	f.query = q
	return f.page, f.err
}

type incomingClock struct {
	calls int
	at    time.Time
}

func (c *incomingClock) Now() time.Time { c.calls++; return c.at }
func TestIncomingApplicationTransfers_UCAPP050_BRAPP059062_Guards(t *testing.T) {
	at := time.Date(2026, 11, 8, 1, 2, 3, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		identity shared.AuthenticatedUserIdentity
		size     int32
		token    string
		want     error
	}{
		{"identity before input", shared.AuthenticatedUserIdentity{}, -1, "", domain.ErrIncomingTransferIdentityRequired},
		{"negative size", shared.AuthenticatedUserIdentity{AuthID: "target"}, -1, "", domain.ErrInvalidIncomingTransferQuery},
		{"large size", shared.AuthenticatedUserIdentity{AuthID: "target"}, 101, "", domain.ErrInvalidIncomingTransferQuery},
		{"large token", shared.AuthenticatedUserIdentity{AuthID: "target"}, 1, strings.Repeat("x", 4097), domain.ErrInvalidIncomingTransferQuery},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &incomingQueryFake{}
			clock := &incomingClock{at: at}
			_, err := NewIncomingApplicationAdminTransfers(repo, clock).List(t.Context(), tc.identity, tc.size, tc.token)
			if !errors.Is(err, tc.want) || repo.called || clock.calls != 0 {
				t.Fatalf("guards err=%v repo=%v clock=%d", err, repo.called, clock.calls)
			}
		})
	}
	repo := &incomingQueryFake{page: domain.IncomingApplicationAdminTransferPage{Items: []domain.IncomingApplicationAdminTransfer{}, AsOf: at}}
	clock := &incomingClock{at: at.Add(123 * time.Nanosecond)}
	page, err := NewIncomingApplicationAdminTransfers(repo, clock).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "ordinary-user"}, 0, "")
	if err != nil || page.Items == nil || repo.query.PageSize != 20 || clock.calls != 1 {
		t.Fatal("ordinary user/default/clock", err)
	}
}
func TestIncomingApplicationTransfers_UCAPP050_BRAPP061062_FailClosed(t *testing.T) {
	at := time.Date(2026, 11, 8, 1, 2, 3, 0, time.UTC)
	item := domain.IncomingApplicationAdminTransfer{TransferID: "019a0000-0000-7000-8000-000000000001", ApplicationID: "019a0000-0000-7000-8000-000000000002", ApplicationName: "Private_App", FromAuthID: "source", ToAuthID: "target", Status: domain.ApplicationAdminTransferPending, RequestedAt: at.Add(-time.Hour), ExpiresAt: at.Add(-time.Hour).Add(domain.ApplicationAdminTransferLifetime)}
	for _, tc := range []struct {
		name   string
		change func(*domain.IncomingApplicationAdminTransferPage)
	}{
		{"wrong caller", func(p *domain.IncomingApplicationAdminTransferPage) { p.Items[0].ToAuthID = "other" }},
		{"terminal", func(p *domain.IncomingApplicationAdminTransferPage) {
			p.Items[0].Status = domain.ApplicationAdminTransferAccepted
		}},
		{"expired", func(p *domain.IncomingApplicationAdminTransferPage) {
			p.Items[0].RequestedAt = at.Add(-domain.ApplicationAdminTransferLifetime)
			p.Items[0].ExpiresAt = at
		}},
		{"wrong asof", func(p *domain.IncomingApplicationAdminTransferPage) { p.AsOf = at.Add(time.Millisecond) }},
		{"duplicate", func(p *domain.IncomingApplicationAdminTransferPage) { p.Items = append(p.Items, p.Items[0]) }},
		{"nil items", func(p *domain.IncomingApplicationAdminTransferPage) { p.Items = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := domain.IncomingApplicationAdminTransferPage{Items: []domain.IncomingApplicationAdminTransfer{item}, AsOf: at}
			tc.change(&page)
			repo := &incomingQueryFake{page: page}
			got, err := NewIncomingApplicationAdminTransfers(repo, &incomingClock{at: at}).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "target"}, 20, "")
			if !errors.Is(err, domain.ErrApplicationAdminTransferStateInconsistent) || got.Items != nil {
				t.Fatalf("partial/invalid result: %#v %v", got, err)
			}
		})
	}
	for _, failure := range []error{errors.New("private storage failure"), domain.ErrInvalidIncomingTransferPageToken, domain.ErrApplicationAdminTransferStateInconsistent} {
		repo := &incomingQueryFake{err: failure}
		_, err := NewIncomingApplicationAdminTransfers(repo, &incomingClock{at: at}).List(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "target"}, 20, "")
		want := failure
		if failure.Error() == "private storage failure" {
			want = domain.ErrIncomingTransferUnavailable
		}
		if err != want {
			t.Fatalf("sanitized error %v", err)
		}
	}
}
