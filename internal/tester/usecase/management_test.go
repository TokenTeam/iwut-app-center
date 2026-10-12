package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"strings"
	"testing"
)

type managementFake struct {
	app    shared.ApplicationID
	caller shared.AuthID
	status string
	size   int32
	token  string
	err    error
}

func (f *managementFake) GetManagement(_ context.Context, c shared.AuthID, a shared.ApplicationID) (*domain.TesterManagement, error) {
	f.caller = c
	f.app = a
	return &domain.TesterManagement{ApplicationID: a, TesterLimit: 100}, f.err
}
func (f *managementFake) ListMemberships(_ context.Context, c shared.AuthID, a shared.ApplicationID, s domain.MembershipQueryStatus, n int32, p string) (*domain.TesterMembershipPage, error) {
	f.caller = c
	f.app = a
	f.status = string(s)
	f.size = n
	f.token = p
	return &domain.TesterMembershipPage{}, f.err
}
func (f *managementFake) ListJoinLinks(_ context.Context, c shared.AuthID, a shared.ApplicationID, s domain.JoinLinkQueryStatus, n int32, p string) (*domain.TesterJoinLinkPage, error) {
	f.caller = c
	f.app = a
	f.status = string(s)
	f.size = n
	f.token = p
	return &domain.TesterJoinLinkPage{}, f.err
}
func TestBR_TST_037_039_041_ManagementQueryValidationAndDefaults(t *testing.T) {
	const app = "01890f47-0000-7000-8000-000000003701"
	identity := shared.AuthenticatedUserIdentity{AuthID: "ordinary-admin"}
	f := &managementFake{}
	q := NewManagementQuery(f)
	if _, err := q.Get(t.Context(), identity, app); err != nil || f.caller != identity.AuthID {
		t.Fatal("ordinary admin failed")
	}
	for _, kind := range []string{"membership", "link"} {
		list := func(input TesterManagementListQuery) error {
			if kind == "membership" {
				_, err := q.ListMemberships(t.Context(), identity, input)
				return err
			}
			_, err := q.ListJoinLinks(t.Context(), identity, input)
			return err
		}
		if err := list(TesterManagementListQuery{ApplicationID: app}); err != nil || f.size != 20 || (kind == "membership" && f.status != "ACTIVE") || (kind == "link" && f.status != "ALL") {
			t.Fatal("defaults")
		}
		for _, size := range []int32{1, 100} {
			if err := list(TesterManagementListQuery{ApplicationID: app, PageSize: size}); err != nil {
				t.Fatal(err)
			}
		}
		for _, input := range []TesterManagementListQuery{{ApplicationID: "bad"}, {ApplicationID: app, PageSize: -1}, {ApplicationID: app, PageSize: 101}, {ApplicationID: app, Status: "unknown"}, {ApplicationID: app, Status: map[string]string{"membership": "REVOKED", "link": "REMOVED"}[kind]}} {
			if err := list(input); !errors.Is(err, domain.ErrInvalidApplicationTesterManagementQuery) {
				t.Fatal("invalid input accepted")
			}
		}
		if err := list(TesterManagementListQuery{ApplicationID: app, PageToken: strings.Repeat("x", 4097)}); !errors.Is(err, domain.ErrInvalidApplicationTesterManagementPageToken) {
			t.Fatal("oversized token accepted")
		}
	}
	if _, err := q.Get(t.Context(), shared.AuthenticatedUserIdentity{}, "bad"); !errors.Is(err, domain.ErrAuthenticatedUserRequired) {
		t.Fatal("identity priority")
	}
}
func TestBR_TST_042_ManagementQueryErrors(t *testing.T) {
	for _, tc := range []struct{ source, target error }{{port.ErrManagementNotFound, domain.ErrApplicationTesterManagementNotFound}, {port.ErrManagementUnavailable, domain.ErrApplicationTesterManagementUnavailable}, {port.ErrManagementStateInconsistent, domain.ErrApplicationTesterManagementStateInconsistent}, {port.ErrInvalidManagementPageToken, domain.ErrInvalidApplicationTesterManagementPageToken}} {
		if !errors.Is(mapTesterManagementError(tc.source), tc.target) {
			t.Fatal("error mapping")
		}
	}
}
