package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"strings"
	"testing"
	"time"
)

type platformQueryFake struct {
	calls int
	query domain.PlatformApplicationQuery
	page  domain.PlatformApplicationPage
	item  domain.PlatformApplicationSummary
	err   error
}

func (f *platformQueryFake) SearchPlatformApplications(_ context.Context, _ shared.AuthID, q domain.PlatformApplicationQuery, _ time.Time) (domain.PlatformApplicationPage, error) {
	f.calls++
	f.query = q
	return f.page, f.err
}
func (f *platformQueryFake) GetPlatformApplicationSummary(context.Context, shared.ApplicationID) (domain.PlatformApplicationSummary, error) {
	f.calls++
	return f.item, f.err
}
func TestPlatformQueries_UCAPP051_BRAPP063_AuthorizationBeforeInput(t *testing.T) {
	for _, permissions := range [][]string{nil, {PermissionApplicationSuspend}, {PermissionApplicationRestore}, {"app.version.review"}, {"platform.admin"}, {PermissionApplicationSearch + ".other"}} {
		f := &platformQueryFake{}
		clock := &incomingClock{}
		h := NewPlatformApplicationQueries(f, clock)
		identity := shared.TrustedIdentity{AuthID: "user", DeveloperStatus: shared.DeveloperStatusApproved, Permissions: permissions}
		if _, err := h.Search(t.Context(), identity, domain.PlatformApplicationQuery{PageSize: -1}); !errors.Is(err, domain.ErrApplicationPlatformSearchRequired) {
			t.Fatal(permissions, err)
		}
		if _, err := h.Get(t.Context(), identity, "invalid"); !errors.Is(err, domain.ErrApplicationPlatformSearchRequired) {
			t.Fatal(permissions, err)
		}
		if f.calls != 0 || clock.calls != 0 {
			t.Fatal("unauthorized side effect")
		}
	}
	h := NewPlatformApplicationQueries(nil, nil)
	if _, err := h.Search(t.Context(), shared.TrustedIdentity{}, domain.PlatformApplicationQuery{}); !errors.Is(err, domain.ErrUserIdentityRequired) {
		t.Fatal(err)
	}
}
func TestPlatformQueries_UCAPP051_BRAPP064065066_ValidationAndFailClosed(t *testing.T) {
	at := time.Date(2026, 11, 8, 1, 0, 0, 0, time.UTC)
	identity := shared.TrustedIdentity{AuthID: "reader", Permissions: []string{PermissionApplicationSearch}}
	for _, q := range []domain.PlatformApplicationQuery{{PageSize: -1}, {PageSize: 101}, {NamePrefix: ".*"}, {ApplicationID: "bad"}, {LifecycleStatuses: []string{"UNKNOWN"}}, {PlatformAvailabilityStatuses: []string{"ACTIVE"}}, {ArchiveStatuses: []string{""}}, {PageToken: strings.Repeat("x", 4097)}} {
		f := &platformQueryFake{}
		h := NewPlatformApplicationQueries(f, &incomingClock{at: at})
		if _, err := h.Search(t.Context(), identity, q); !errors.Is(err, domain.ErrInvalidPlatformApplicationQuery) || f.calls != 0 {
			t.Fatal(q, err)
		}
	}
	f := &platformQueryFake{page: domain.PlatformApplicationPage{Items: []domain.PlatformApplicationSummary{}, AsOf: at, NextPageToken: "continuation"}}
	h := NewPlatformApplicationQueries(f, &incomingClock{at: at.Add(time.Nanosecond)})
	if p, err := h.Search(t.Context(), identity, domain.PlatformApplicationQuery{NamePrefix: "APP", LifecycleStatuses: []string{"CLOSED", "ACTIVE", "CLOSED"}}); err != nil || p.Items == nil || f.query.PageSize != 20 || f.query.NamePrefix != "app" || strings.Join(f.query.LifecycleStatuses, ",") != "ACTIVE,CLOSED" {
		t.Fatal("normalize/empty continuation", err)
	}
	f.page.Items = nil
	if _, err := h.Search(t.Context(), identity, domain.PlatformApplicationQuery{}); !errors.Is(err, domain.ErrPlatformApplicationStateInconsistent) {
		t.Fatal("partial result", err)
	}
	f.err = errors.New("private storage details")
	if _, err := h.Get(t.Context(), identity, "019a0000-0000-7000-8000-000000000001"); !errors.Is(err, domain.ErrPlatformApplicationUnavailable) {
		t.Fatal(err)
	}
}
