package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"testing"
	"time"
)

const appID = "01995000-0000-7000-8000-000000000001"
const profileID = "01995000-0000-7000-8000-000000000002"

type fakeID struct {
	calls int
	err   error
	id    domain.ApplicationProfileRevisionID
}

func (f *fakeID) NewUUIDv7() (domain.ApplicationProfileRevisionID, error) {
	f.calls++
	return f.id, f.err
}

type fakeClock struct {
	calls int
	at    time.Time
}

func (f *fakeClock) Now() time.Time { f.calls++; return f.at }

type fakeRepository struct {
	calls     int
	err       error
	nilResult bool
	actor     shared.AuthID
	draft     *domain.DraftApplicationProfileRevision
}

func (f *fakeRepository) CreateDraft(_ context.Context, actor shared.AuthID, draft *domain.DraftApplicationProfileRevision) (*domain.ApplicationProfileRevision, error) {
	f.calls++
	f.actor = actor
	f.draft = draft
	if f.err != nil || f.nilResult {
		return nil, f.err
	}
	return draft.AssignSequence(3)
}
func dependencies() (*fakeID, *fakeClock, *fakeRepository) {
	return &fakeID{id: profileID}, &fakeClock{at: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}, &fakeRepository{}
}
func approved() DeveloperIdentity {
	return DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved}
}
func command() CreateApplicationProfileRevisionCommand {
	return CreateApplicationProfileRevisionCommand{ApplicationID: appID, DisplayName: "Cafe\u0301"}
}
func TestBRPRF001002005006007Create(t *testing.T) {
	ids, clock, repo := dependencies()
	description, icon := "de\u0301tail", "<opaque>"
	c := command()
	c.Description = &description
	c.Icon = &icon
	got, err := NewCreateApplicationProfileRevisionHandler(ids, clock, repo).Handle(context.Background(), approved(), c)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProfileRevisionID() != profileID || got.ApplicationID() != appID || got.Sequence() != 3 || got.DisplayName().String() != "Café" || got.Description().String() != "détail" || got.Icon().String() != "<opaque>" || got.ReviewStatus() != domain.ReviewStatusDraft || got.Revision() != 1 || got.CreatedBy() != "admin" || got.UpdatedBy() != "admin" || !got.CreatedAt().Equal(clock.at) || !got.UpdatedAt().Equal(clock.at) {
		t.Fatal("incorrect draft result")
	}
	if ids.calls != 1 || clock.calls != 1 || repo.calls != 1 || repo.actor != "admin" {
		t.Fatal("dependency calls")
	}
}
func TestBRPRF004005ExplicitNulls(t *testing.T) {
	ids, clock, repo := dependencies()
	got, err := NewCreateApplicationProfileRevisionHandler(ids, clock, repo).Handle(context.Background(), approved(), command())
	if err != nil || got.Description() != nil || got.Icon() != nil {
		t.Fatalf("nulls: %v", err)
	}
}
func TestBRPRF002003004005RejectBeforeDependencies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity DeveloperIdentity
		command  CreateApplicationProfileRevisionCommand
		want     error
	}{
		{"no identity", DeveloperIdentity{}, command(), domain.ErrDeveloperIdentityRequired},
		{"ordinary user", DeveloperIdentity{AuthID: "admin"}, command(), domain.ErrDeveloperApprovalRequired},
		{"pending", DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, command(), domain.ErrDeveloperApprovalRequired},
		{"rejected", DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusRejected}, command(), domain.ErrDeveloperApprovalRequired},
		{"suspended", DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusSuspended}, command(), domain.ErrDeveloperApprovalRequired},
		{"bad app", approved(), CreateApplicationProfileRevisionCommand{ApplicationID: "bad", DisplayName: "x"}, domain.ErrInvalidApplicationID},
		{"name missing", approved(), CreateApplicationProfileRevisionCommand{ApplicationID: appID}, domain.ErrInvalidApplicationDisplayName},
		{"description empty", approved(), CreateApplicationProfileRevisionCommand{ApplicationID: appID, DisplayName: "x", Description: new(string)}, domain.ErrInvalidApplicationDescription},
		{"icon empty", approved(), CreateApplicationProfileRevisionCommand{ApplicationID: appID, DisplayName: "x", Icon: new(string)}, domain.ErrInvalidApplicationIcon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ids, clock, repo := dependencies()
			got, err := NewCreateApplicationProfileRevisionHandler(ids, clock, repo).Handle(context.Background(), tc.identity, tc.command)
			if got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got %v, err %v want %v", got, err, tc.want)
			}
			if ids.calls+clock.calls+repo.calls != 0 {
				t.Fatal("validation touched dependencies")
			}
		})
	}
}
func TestBRPRF002006007RepositoryFailureMapping(t *testing.T) {
	for _, tc := range []struct{ source, want error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrApplicationProfileWorkRevisionAlreadyExists, domain.ErrApplicationProfileWorkRevisionAlreadyExists},
		{port.ErrApplicationProfileStateInconsistent, domain.ErrApplicationProfileStateInconsistent},
		{errors.New("database secret"), domain.ErrInternal},
	} {
		ids, clock, repo := dependencies()
		repo.err = tc.source
		got, err := NewCreateApplicationProfileRevisionHandler(ids, clock, repo).Handle(context.Background(), approved(), command())
		if got != nil || !errors.Is(err, tc.want) {
			t.Fatalf("failure mapping: %v %v", got, err)
		}
	}
}
func TestBRPRF007DependencyFailureHasNoResult(t *testing.T) {
	for _, name := range []string{"id failure", "invalid id", "zero clock", "nil repository result", "missing dependency"} {
		t.Run(name, func(t *testing.T) {
			ids, clock, repo := dependencies()
			handler := NewCreateApplicationProfileRevisionHandler(ids, clock, repo)
			switch name {
			case "id failure":
				ids.err = errors.New("entropy failure")
			case "invalid id":
				ids.id = "bad"
			case "zero clock":
				clock.at = time.Time{}
			case "nil repository result":
				repo.nilResult = true
			case "missing dependency":
				handler.clock = nil
			}
			got, err := handler.Handle(context.Background(), approved(), command())
			if got != nil || !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("got=%v err=%v", got, err)
			}
			if name != "nil repository result" && repo.calls != 0 {
				t.Fatal("failed dependency reached repository")
			}
		})
	}
}
