package usecase

import (
	"context"
	"errors"
	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"testing"
	"time"
)

type activityQueryFake struct {
	calls  int
	query  ad.ManagementActivityQuery
	page   ad.ManagementActivityPage
	detail ad.ManagementActivityDetail
	err    error
}

func (f *activityQueryFake) ListManagementActivity(_ context.Context, _ shared.AuthID, _ shared.ApplicationID, q ad.ManagementActivityQuery, _ time.Time) (ad.ManagementActivityPage, error) {
	f.calls++
	f.query = q
	return f.page, f.err
}
func (f *activityQueryFake) GetManagementActivity(context.Context, shared.AuthID, shared.ApplicationID, ad.ManagementActivityLocator, time.Time) (ad.ManagementActivityDetail, error) {
	f.calls++
	return f.detail, f.err
}
func TestManagementActivity_BRAPP067070_AuthorizationAndFailClosed(t *testing.T) {
	at := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	app := "019a0000-0000-7000-8000-000000000001"
	f := &activityQueryFake{page: ad.ManagementActivityPage{Items: []ad.ManagementActivity{}, AsOf: at}}
	c := &incomingClock{at: at.Add(time.Nanosecond)}
	h := NewManagementActivityQueries(f, c)
	if _, err := h.List(t.Context(), shared.TrustedIdentity{}, "bad", ad.ManagementActivityQuery{PageSize: -1}); !errors.Is(err, ad.ErrUserIdentityRequired) || f.calls != 0 || c.calls != 0 {
		t.Fatal("identity first", err)
	}
	if _, err := h.Get(t.Context(), shared.TrustedIdentity{}, "bad", "bad"); !errors.Is(err, ad.ErrUserIdentityRequired) {
		t.Fatal(err)
	}
	for _, status := range []shared.DeveloperStatus{shared.DeveloperStatusPending, shared.DeveloperStatusSuspended, shared.DeveloperStatusWithdrawn} {
		identity := shared.TrustedIdentity{AuthID: "owner", DeveloperStatus: status}
		if p, err := h.List(t.Context(), identity, app, ad.ManagementActivityQuery{}); err != nil || p.Items == nil || f.query.PageSize != 20 {
			t.Fatal("no developer gate", err)
		}
	}
	identity := shared.TrustedIdentity{AuthID: "reader", Permissions: []string{"platform.admin", "app.version.review", "app.application.search"}}
	f.err = ad.ErrManagementActivityNotFound
	if _, err := h.List(t.Context(), identity, app, ad.ManagementActivityQuery{}); !errors.Is(err, ad.ErrManagementActivityNotFound) {
		t.Fatal("repository authorizes actual owner", err)
	}
	for _, err := range []error{ad.ErrManagementActivityStateInconsistent, ad.ErrInvalidManagementActivityPageToken, ad.ErrManagementActivityUnavailable, ad.ErrManagementActivityResponseTooLarge} {
		f.err = err
		if _, got := h.List(t.Context(), identity, app, ad.ManagementActivityQuery{}); !errors.Is(got, err) {
			t.Fatal(got, err)
		}
	}
	f.err = errors.New("private storage cause")
	if _, err := h.List(t.Context(), identity, app, ad.ManagementActivityQuery{}); !errors.Is(err, ad.ErrManagementActivityUnavailable) {
		t.Fatal(err)
	}
	f.err = nil
	f.page.NextPageToken = "unexpected-empty-continuation"
	if _, err := h.List(t.Context(), identity, app, ad.ManagementActivityQuery{}); !errors.Is(err, ad.ErrManagementActivityStateInconsistent) {
		t.Fatal(err)
	}
	f.page.NextPageToken = ""
	f.page.Items = nil
	if _, err := h.List(t.Context(), identity, app, ad.ManagementActivityQuery{}); !errors.Is(err, ad.ErrManagementActivityStateInconsistent) {
		t.Fatal(err)
	}
	before := f.calls
	for _, q := range []ad.ManagementActivityQuery{{PageSize: -1}, {Kinds: []ad.ManagementActivityKind{"bad"}}} {
		if _, err := h.List(t.Context(), identity, app, q); !errors.Is(err, ad.ErrInvalidManagementActivityQuery) {
			t.Fatal(err)
		}
	}
	if f.calls != before {
		t.Fatal("invalid query touched storage")
	}
}
