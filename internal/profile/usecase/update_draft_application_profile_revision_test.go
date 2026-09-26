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

type updateRepo struct {
	calls int
	err   error
	actor shared.AuthID
	at    time.Time
	app   shared.ApplicationID
	id    domain.ApplicationProfileRevisionID
}

func (r *updateRepo) ReplaceDraft(_ context.Context, app shared.ApplicationID, id domain.ApplicationProfileRevisionID, actor shared.AuthID, expected int64, replacement domain.DraftApplicationProfileReplacement, at time.Time) (*domain.ApplicationProfileRevision, error) {
	r.calls++
	r.actor = actor
	r.at = at
	r.app = app
	r.id = id
	if r.err != nil {
		return nil, r.err
	}
	name, _ := domain.NewApplicationDisplayName("old")
	d, _ := domain.NewDraftApplicationProfileRevision(id, app, name, nil, nil, "creator", time.Unix(10, 0))
	v, _ := d.AssignSequence(1)
	return v.ReplaceDraft(expected, replacement, actor, at)
}
func updateCommand() UpdateDraftApplicationProfileRevisionCommand {
	return UpdateDraftApplicationProfileRevisionCommand{ApplicationID: appID, ProfileRevisionID: profileID, ExpectedRevision: 1, DisplayName: "Cafe\u0301"}
}
func TestBRPRF009010012014UpdateUseCase(t *testing.T) {
	_, clock, _ := dependencies()
	repo := &updateRepo{}
	got, e := NewUpdateDraftApplicationProfileRevisionHandler(clock, repo).Handle(t.Context(), approved(), updateCommand())
	if e != nil || got.DisplayName().String() != "Café" || got.Revision() != 2 || got.CreatedBy() != "creator" || repo.actor != "admin" || repo.app != appID || repo.id != profileID || !repo.at.Equal(clock.at) || got.UpdatedBy() != "admin" {
		t.Fatalf("result=%v err=%v", got, e)
	}
}
func TestBRPRF011012013RejectBeforeWrite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity DeveloperIdentity
		mutate   func(*UpdateDraftApplicationProfileRevisionCommand)
		want     error
	}{
		{"identity", DeveloperIdentity{}, func(*UpdateDraftApplicationProfileRevisionCommand) {}, domain.ErrDeveloperIdentityRequired},
		{"approval", DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, func(*UpdateDraftApplicationProfileRevisionCommand) {}, domain.ErrDeveloperApprovalRequired},
		{"app", approved(), func(c *UpdateDraftApplicationProfileRevisionCommand) { c.ApplicationID = "bad" }, domain.ErrInvalidApplicationID},
		{"id", approved(), func(c *UpdateDraftApplicationProfileRevisionCommand) { c.ProfileRevisionID = "bad" }, domain.ErrInvalidApplicationProfileRevisionID},
		{"revision", approved(), func(c *UpdateDraftApplicationProfileRevisionCommand) { c.ExpectedRevision = 0 }, domain.ErrApplicationProfileExpectedRevisionRequired},
		{"name", approved(), func(c *UpdateDraftApplicationProfileRevisionCommand) { c.DisplayName = "" }, domain.ErrInvalidApplicationDisplayName},
		{"description", approved(), func(c *UpdateDraftApplicationProfileRevisionCommand) { v := ""; c.Description = &v }, domain.ErrInvalidApplicationDescription},
		{"icon", approved(), func(c *UpdateDraftApplicationProfileRevisionCommand) { v := "\u200b"; c.Icon = &v }, domain.ErrInvalidApplicationIcon},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, clock, _ := dependencies()
			repo := &updateRepo{}
			c := updateCommand()
			tc.mutate(&c)
			_, e := NewUpdateDraftApplicationProfileRevisionHandler(clock, repo).Handle(t.Context(), tc.identity, c)
			if !errors.Is(e, tc.want) || repo.calls != 0 || clock.calls != 0 {
				t.Fatalf("err=%v calls=%d/%d", e, repo.calls, clock.calls)
			}
		})
	}
}
func TestBRPRF008011013RepositoryErrors(t *testing.T) {
	for _, tc := range []struct{ from, to error }{{port.ErrApplicationProfileRevisionNotFound, domain.ErrApplicationProfileRevisionNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired}, {port.ErrApplicationProfileRevisionNotDraft, domain.ErrApplicationProfileRevisionNotDraft}, {port.ErrApplicationProfileRevisionConflict, domain.ErrApplicationProfileRevisionConflict}, {port.ErrApplicationProfileStateInconsistent, domain.ErrApplicationProfileStateInconsistent}, {errors.New("db"), domain.ErrInternal}} {
		_, clock, _ := dependencies()
		repo := &updateRepo{err: tc.from}
		_, e := NewUpdateDraftApplicationProfileRevisionHandler(clock, repo).Handle(t.Context(), approved(), updateCommand())
		if !errors.Is(e, tc.to) {
			t.Fatal(e)
		}
	}
}
