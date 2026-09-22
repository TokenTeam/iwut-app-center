package usecase

import (
	"context"
	"errors"
	"fmt"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"reflect"
	"strings"
	"testing"
	"time"
)

const removalID domain.ApplicationTesterMembershipID = "01900000-0000-7000-8000-00000000000a"

type removalFixture struct {
	events             []string
	candidate          *domain.TesterRemovalCandidate
	loadErr, removeErr error
	result             *domain.RemoveApplicationTesterResult
	customResult       bool
	at                 time.Time
	application        shared.ApplicationID
	membership         domain.ApplicationTesterMembershipID
	admin              shared.AuthID
	written            *domain.ApplicationTesterMembership
}

func (f *removalFixture) Now() time.Time { f.events = append(f.events, "clock"); return f.at }
func (f *removalFixture) LoadRemovalCandidate(_ context.Context, appID shared.ApplicationID, membershipID domain.ApplicationTesterMembershipID, admin shared.AuthID) (*domain.TesterRemovalCandidate, error) {
	f.events = append(f.events, "load")
	f.application, f.membership, f.admin = appID, membershipID, admin
	return f.candidate, f.loadErr
}
func (f *removalFixture) Remove(_ context.Context, appID shared.ApplicationID, membershipID domain.ApplicationTesterMembershipID, admin shared.AuthID, at time.Time) (*domain.RemoveApplicationTesterResult, error) {
	f.events = append(f.events, "remove")
	if appID != f.application || membershipID != f.membership || admin != f.admin || !at.Equal(f.at) {
		return nil, errors.New("wrong operation inputs")
	}
	if f.removeErr != nil {
		return nil, f.removeErr
	}
	if f.customResult {
		return f.result, nil
	}
	m, err := f.candidate.Membership().Remove(admin, at)
	if err != nil {
		return nil, err
	}
	f.written = m
	return domain.NewRemoveApplicationTesterResult(m, true, f.candidate.ActiveTesterCount()-1, 100, f.candidate.ActiveJoinLinkExists())
}
func newRemovalFixture(t *testing.T, removed, link bool) *removalFixture {
	t.Helper()
	m, err := domain.NewActiveTesterMembership(removalID, app, "tester", first, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	count := int32(1)
	if removed {
		m, err = m.Remove("original-admin", now.Add(-time.Minute))
		count = 0
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := domain.NewTesterRemovalCandidate(m, count, link)
	if err != nil {
		t.Fatal(err)
	}
	return &removalFixture{candidate: c, at: now}
}
func runRemoval(f *removalFixture) (*domain.RemoveApplicationTesterResult, error) {
	return NewRemoveApplicationTesterHandler(f, f).Handle(context.Background(), identity, app, removalID)
}

func TestBRTST020021023024026028RemoveExactEpisodeWithServerAudit(t *testing.T) {
	for _, link := range []bool{true, false} {
		f := newRemovalFixture(t, false, link)
		r, err := runRemoval(f)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Removed() || r.ActiveTesterCount() != 0 || r.TesterLimit() != 100 || r.ActiveJoinLinkExists() != link || r.Membership().MembershipID() != removalID || *r.Membership().RemovedBy() != identity.AuthID || !r.Membership().RemovedAt().Equal(now) || r.Membership().JoinedViaJoinLinkID() != first || !r.Membership().JoinedAt().Equal(now.Add(-time.Hour)) {
			t.Fatal("bad removal result")
		}
		if !reflect.DeepEqual(f.events, []string{"load", "clock", "remove"}) {
			t.Fatal(f.events)
		}
	}
	// Administrator and tester roles can belong to the same opaque user.
	f := newRemovalFixture(t, false, true)
	m, _ := domain.NewActiveTesterMembership(removalID, app, identity.AuthID, first, now.Add(-time.Hour))
	f.candidate, _ = domain.NewTesterRemovalCandidate(m, 1, true)
	if r, err := runRemoval(f); err != nil || r.Membership().TesterAuthID() != identity.AuthID {
		t.Fatalf("admin self removal: %v", err)
	}
}

func TestBRTST022026IdempotentCandidateSkipsClockAndPreservesOriginalAudit(t *testing.T) {
	for _, link := range []bool{true, false} {
		f := newRemovalFixture(t, true, link)
		// A new episode may already occupy capacity; old deletion changes nothing.
		f.candidate, _ = domain.NewTesterRemovalCandidate(f.candidate.Membership(), 100, link)
		r, err := NewRemoveApplicationTesterHandler(nil, f).Handle(context.Background(), identity, app, removalID)
		if err != nil || r.Removed() || r.ActiveTesterCount() != 100 || r.ActiveJoinLinkExists() != link || !reflect.DeepEqual(r.Membership(), f.candidate.Membership()) || !reflect.DeepEqual(f.events, []string{"load"}) {
			t.Fatalf("idempotence: %v effects=%v", err, f.events)
		}
	}
}

func TestBRTST020021RemovalInputAndAuthorizationFailuresHaveNoEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		who  shared.DeveloperIdentity
		app  shared.ApplicationID
		id   domain.ApplicationTesterMembershipID
		want error
	}{
		{"missing_identity", shared.DeveloperIdentity{}, app, removalID, domain.ErrDeveloperIdentityRequired},
		{"ordinary_user", shared.DeveloperIdentity{AuthID: "ordinary"}, app, removalID, domain.ErrDeveloperApprovalRequired},
		{"pending", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, app, removalID, domain.ErrDeveloperApprovalRequired},
		{"rejected", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusRejected}, app, removalID, domain.ErrDeveloperApprovalRequired},
		{"suspended", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusSuspended}, app, removalID, domain.ErrDeveloperApprovalRequired},
		{"application", identity, "bad", removalID, domain.ErrInvalidApplicationId},
		{"membership", identity, app, "bad", domain.ErrInvalidTesterMembershipId},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemovalFixture(t, false, true)
			r, err := NewRemoveApplicationTesterHandler(f, f).Handle(context.Background(), tc.who, tc.app, tc.id)
			if r != nil || !errors.Is(err, tc.want) || len(f.events) > 0 {
				t.Fatalf("err=%v effects=%v", err, f.events)
			}
		})
	}
}

func TestBRTST020021022024028RepositoryFailureMappingAndPrivacy(t *testing.T) {
	for _, source := range []error{port.ErrApplicationAdminRequired, port.ErrApplicationTesterMembershipNotFound, port.ErrApplicationTesterStateInconsistent, errors.New("secret-index-other-tester")} {
		want := domain.ErrInternal
		switch source {
		case port.ErrApplicationAdminRequired:
			want = domain.ErrApplicationAdminRequired
		case port.ErrApplicationTesterMembershipNotFound:
			want = domain.ErrApplicationTesterMembershipNotFound
		case port.ErrApplicationTesterStateInconsistent:
			want = domain.ErrApplicationTesterStateInconsistent
		}
		for _, stage := range []string{"load", "remove"} {
			t.Run(stage+source.Error(), func(t *testing.T) {
				f := newRemovalFixture(t, false, true)
				if stage == "load" {
					f.loadErr = fmt.Errorf("secret-index-other-tester: %w", source)
				} else {
					f.removeErr = fmt.Errorf("secret-index-other-tester: %w", source)
				}
				r, err := runRemoval(f)
				if r != nil || !errors.Is(err, want) || strings.Contains(fmt.Sprintf("%+v", err), "secret-index") || errors.Unwrap(err) != nil || f.written != nil {
					t.Fatalf("bad failure: %v", err)
				}
				if stage == "load" && !reflect.DeepEqual(f.events, []string{"load"}) {
					t.Fatal(f.events)
				}
			})
		}
	}
}

func TestBRTST022027ConcurrentRemovalReturnsStoredAuditAndDiscardsPrefetchedClock(t *testing.T) {
	f := newRemovalFixture(t, false, true)
	m, _ := f.candidate.Membership().Remove("winning-admin", now.Add(-time.Second))
	f.result, _ = domain.NewRemoveApplicationTesterResult(m, false, 0, 100, true)
	f.customResult = true
	r, err := runRemoval(f)
	if err != nil || r.Removed() || *r.Membership().RemovedBy() != "winning-admin" || !r.Membership().RemovedAt().Equal(now.Add(-time.Second)) || !reflect.DeepEqual(f.events, []string{"load", "clock", "remove"}) {
		t.Fatalf("discarded original audit: %v", err)
	}
}

func TestBRTST021024028DefensiveRepositoryResultValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*removalFixture)
		want   error
	}{
		{"nil_candidate", func(f *removalFixture) { f.candidate = nil }, domain.ErrApplicationTesterStateInconsistent},
		{"zero_clock", func(f *removalFixture) { f.at = time.Time{} }, domain.ErrInternal},
		{"nil_result", func(f *removalFixture) { f.customResult = true }, domain.ErrApplicationTesterStateInconsistent},
		{"wrong_episode", func(f *removalFixture) {
			m, _ := domain.NewActiveTesterMembership("01900000-0000-7000-8000-00000000000b", app, "tester", first, now)
			f.candidate, _ = domain.NewTesterRemovalCandidate(m, 1, true)
		}, domain.ErrApplicationTesterStateInconsistent},
		{"wrong_result_actor", func(f *removalFixture) {
			m, _ := f.candidate.Membership().Remove("wrong-admin", now)
			f.result, _ = domain.NewRemoveApplicationTesterResult(m, true, 0, 100, true)
			f.customResult = true
		}, domain.ErrApplicationTesterStateInconsistent},
		{"changed_join_facts", func(f *removalFixture) {
			m, _ := domain.NewActiveTesterMembership(removalID, app, "another-user", first, now)
			m, _ = m.Remove(identity.AuthID, now)
			f.result, _ = domain.NewRemoveApplicationTesterResult(m, true, 0, 100, true)
			f.customResult = true
		}, domain.ErrApplicationTesterStateInconsistent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemovalFixture(t, false, true)
			tc.mutate(f)
			r, err := runRemoval(f)
			if r != nil || !errors.Is(err, tc.want) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestBRTST021RemovalNormalizesPathIDs(t *testing.T) {
	f := newRemovalFixture(t, false, true)
	_, err := NewRemoveApplicationTesterHandler(f, f).Handle(context.Background(), identity, app, domain.ApplicationTesterMembershipID(strings.ToUpper(removalID.String())))
	if err != nil || f.membership != removalID {
		t.Fatalf("%s: %v", f.membership, err)
	}
}
