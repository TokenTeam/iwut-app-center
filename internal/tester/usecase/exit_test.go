package usecase

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
)

type exitFixture struct {
	candidate         *domain.ApplicationTesterMembership
	result            *domain.LeaveApplicationAsTesterResult
	at                time.Time
	events            []string
	loadErr, writeErr error
}

func (f *exitFixture) Now() time.Time { f.events = append(f.events, "clock"); return f.at }
func (f *exitFixture) LoadExitCandidate(_ context.Context, a shared.ApplicationID, id domain.ApplicationTesterMembershipID, who shared.AuthID) (*domain.ApplicationTesterMembership, error) {
	f.events = append(f.events, "load")
	if a != app || id != removalID || who != "tester" {
		return nil, port.ErrApplicationTesterMembershipNotFound
	}
	return f.candidate, f.loadErr
}
func (f *exitFixture) Leave(_ context.Context, a shared.ApplicationID, id domain.ApplicationTesterMembershipID, who shared.AuthID, at time.Time) (*domain.LeaveApplicationAsTesterResult, error) {
	f.events = append(f.events, "write")
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	if f.result != nil {
		return f.result, nil
	}
	m, err := f.candidate.Remove(who, at.Truncate(time.Millisecond))
	if err != nil {
		return nil, err
	}
	return domain.NewLeaveApplicationAsTesterResult(m, true)
}
func newExitFixture(t *testing.T) *exitFixture {
	t.Helper()
	m, err := domain.NewActiveTesterMembership(removalID, app, "tester", first, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return &exitFixture{candidate: m, at: now}
}
func runExit(f *exitFixture) (*domain.LeaveApplicationAsTesterResult, error) {
	return NewLeaveApplicationAsTesterHandler(f, f).Handle(context.Background(), shared.AuthenticatedUserIdentity{AuthID: "tester"}, app, removalID)
}
func TestUCAPP040BRTST044046UserAuditAndNoClockIdempotency(t *testing.T) {
	f := newExitFixture(t)
	r, err := runExit(f)
	if err != nil || !r.Left() || *r.Membership().RemovedBy() != "tester" || !r.Membership().RemovedAt().Equal(now) || !reflect.DeepEqual(f.events, []string{"load", "clock", "write"}) {
		t.Fatalf("result=%v error=%v events=%v", r, err, f.events)
	}
	for _, actor := range []shared.AuthID{"tester", "original-admin"} {
		f := newExitFixture(t)
		f.candidate, _ = f.candidate.Remove(actor, now.Add(-time.Minute))
		f.at = time.Time{}
		r, err := NewLeaveApplicationAsTesterHandler(nil, f).Handle(context.Background(), shared.AuthenticatedUserIdentity{AuthID: "tester"}, app, removalID)
		if err != nil || r.Left() || !reflect.DeepEqual(r.Membership(), f.candidate) || !reflect.DeepEqual(f.events, []string{"load"}) {
			t.Fatalf("no-op actor=%s err=%v", actor, err)
		}
	}
}
func TestUCAPP040BRTST044045InputIdentityPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name string
		who  shared.AuthID
		a    shared.ApplicationID
		id   domain.ApplicationTesterMembershipID
		want error
	}{
		{"identity-first", "", "bad", "bad", domain.ErrUserIdentityRequired},
		{"application", "tester", "bad", removalID, domain.ErrInvalidApplicationId},
		{"membership", "tester", app, "bad", domain.ErrInvalidTesterMembershipId},
		{"other", "other", app, removalID, domain.ErrApplicationTesterMembershipNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExitFixture(t)
			r, err := NewLeaveApplicationAsTesterHandler(f, f).Handle(t.Context(), shared.AuthenticatedUserIdentity{AuthID: tc.who}, tc.a, tc.id)
			if r != nil || !errors.Is(err, tc.want) || len(f.events) > 1 {
				t.Fatalf("err=%v events=%v", err, f.events)
			}
		})
	}
}
func TestUCAPP040BRTST046051InvalidClockHasNoWrite(t *testing.T) {
	for _, at := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(0, 12, 1, 0, 0, 0, 0, time.UTC), now.Add(-2 * time.Hour)} {
		f := newExitFixture(t)
		f.at = at
		r, err := runExit(f)
		if r != nil || !errors.Is(err, domain.ErrApplicationTesterStateInconsistent) || !reflect.DeepEqual(f.events, []string{"load", "clock"}) {
			t.Fatalf("bad time err=%v events=%v", err, f.events)
		}
	}
}
func TestUCAPP040BRTST046048CompetitorPreservesOriginalAudit(t *testing.T) {
	f := newExitFixture(t)
	winner, _ := f.candidate.Remove("winning-admin", now.Add(-time.Minute))
	f.result, _ = domain.NewLeaveApplicationAsTesterResult(winner, false)
	r, err := runExit(f)
	if err != nil || r.Left() || !reflect.DeepEqual(r.Membership(), winner) {
		t.Fatalf("competitor=%v err=%v", r, err)
	}
}
func TestUCAPP040BRTST050051RepositoryFailuresAreRedacted(t *testing.T) {
	for _, tc := range []struct{ source, want error }{
		{port.ErrApplicationTesterMembershipNotFound, domain.ErrApplicationTesterMembershipNotFound},
		{port.ErrApplicationTesterStateInconsistent, domain.ErrApplicationTesterStateInconsistent},
		{errors.New("private database identity credential"), domain.ErrApplicationTesterExitUnavailable},
	} {
		for _, phase := range []string{"load", "write"} {
			f := newExitFixture(t)
			if phase == "load" {
				f.loadErr = tc.source
			} else {
				f.writeErr = tc.source
			}
			r, err := runExit(f)
			if r != nil || !errors.Is(err, tc.want) || err.Error() == "private database identity credential" {
				t.Fatalf("phase=%s err=%v", phase, err)
			}
		}
	}
}
