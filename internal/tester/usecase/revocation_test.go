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

type revocationFixture struct {
	candidate          *domain.TesterJoinLinkRevocationCandidate
	result             *domain.RevokeTesterJoinLinkResult
	customResult       bool
	loadErr, revokeErr error
	events             []string
	at                 time.Time
	application        shared.ApplicationID
	linkID             domain.ApplicationTesterJoinLinkID
	admin              shared.AuthID
}

func (f *revocationFixture) Now() time.Time { f.events = append(f.events, "clock"); return f.at }
func (f *revocationFixture) LoadRevocationCandidate(_ context.Context, appID shared.ApplicationID, linkID domain.ApplicationTesterJoinLinkID, admin shared.AuthID) (*domain.TesterJoinLinkRevocationCandidate, error) {
	f.events = append(f.events, "load")
	f.application, f.linkID, f.admin = appID, linkID, admin
	return f.candidate, f.loadErr
}
func (f *revocationFixture) Revoke(_ context.Context, appID shared.ApplicationID, linkID domain.ApplicationTesterJoinLinkID, admin shared.AuthID, at time.Time) (*domain.RevokeTesterJoinLinkResult, error) {
	f.events = append(f.events, "revoke")
	if appID != f.application || linkID != f.linkID || admin != f.admin || !at.Equal(f.at) {
		return nil, errors.New("wrong operation inputs")
	}
	if f.revokeErr != nil {
		return nil, f.revokeErr
	}
	if f.customResult {
		return f.result, nil
	}
	l, err := f.candidate.JoinLink().Revoke(admin, at)
	if err != nil {
		return nil, err
	}
	return domain.NewRevokeTesterJoinLinkResult(l, true)
}
func newRevocationFixture(t *testing.T, status string) *revocationFixture {
	t.Helper()
	l, err := domain.NewActiveTesterJoinLink(first, app, domain.NewTesterJoinTokenHash([32]byte{42}), "creator", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	switch status {
	case "MANUAL":
		l, err = l.Revoke("original-admin", now.Add(-time.Minute))
	case "ROTATED":
		l, err = l.Rotate(second, "original-admin", now.Add(-time.Minute))
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := domain.NewTesterJoinLinkRevocationCandidate(l)
	if err != nil {
		t.Fatal(err)
	}
	return &revocationFixture{candidate: c, at: now}
}
func runRevocation(f *revocationFixture) (*domain.RevokeTesterJoinLinkResult, error) {
	return NewRevokeTesterJoinLinkHandler(f, f).Handle(context.Background(), identity, app, first)
}

func TestBRTST029030031035036RevokeExactLinkWithServerAudit(t *testing.T) {
	f := newRevocationFixture(t, "ACTIVE")
	r, err := runRevocation(f)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Revoked() || r.JoinLink().JoinLinkID() != first || *r.JoinLink().RevokedBy() != identity.AuthID || !r.JoinLink().RevokedAt().Equal(now) || *r.JoinLink().RevocationReason() != domain.RevocationReasonManual || r.JoinLink().ReplacedByJoinLinkID() != nil || !reflect.DeepEqual(f.events, []string{"load", "clock", "revoke"}) {
		t.Fatal("bad revoke", f.events)
	}
}
func TestBRTST030032RevokedCandidatesPreserveAuditWithoutClock(t *testing.T) {
	for _, status := range []string{"MANUAL", "ROTATED"} {
		t.Run(status, func(t *testing.T) {
			f := newRevocationFixture(t, status)
			r, err := NewRevokeTesterJoinLinkHandler(nil, f).Handle(context.Background(), identity, app, first)
			if err != nil || r.Revoked() || !reflect.DeepEqual(r.JoinLink(), f.candidate.JoinLink()) || !reflect.DeepEqual(f.events, []string{"load"}) {
				t.Fatal("idempotence", err, f.events)
			}
		})
	}
}
func TestBRTST029030RevocationRejectsIdentityAndPathBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		who  shared.DeveloperIdentity
		app  shared.ApplicationID
		link domain.ApplicationTesterJoinLinkID
		want error
	}{
		{"missing", shared.DeveloperIdentity{}, app, first, domain.ErrDeveloperIdentityRequired},
		{"ordinary", shared.DeveloperIdentity{AuthID: "user"}, app, first, domain.ErrDeveloperApprovalRequired},
		{"pending", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, app, first, domain.ErrDeveloperApprovalRequired},
		{"rejected", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusRejected}, app, first, domain.ErrDeveloperApprovalRequired},
		{"suspended", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusSuspended}, app, first, domain.ErrDeveloperApprovalRequired},
		{"app", identity, "bad", first, domain.ErrInvalidApplicationId},
		{"link", identity, app, "bad", domain.ErrInvalidTesterJoinLinkId},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRevocationFixture(t, "ACTIVE")
			r, err := NewRevokeTesterJoinLinkHandler(f, f).Handle(context.Background(), tc.who, tc.app, tc.link)
			if r != nil || !errors.Is(err, tc.want) || len(f.events) > 0 {
				t.Fatal(err, f.events)
			}
		})
	}
}
func TestBRTST029030032036RevocationRepositoryFailureMappingAndRedaction(t *testing.T) {
	for _, pair := range []struct{ source, want error }{
		{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrApplicationTesterJoinLinkNotFound, domain.ErrApplicationTesterJoinLinkNotFound},
		{port.ErrApplicationTesterJoinLinkStateInconsistent, domain.ErrApplicationTesterJoinLinkStateInconsistent},
		{errors.New("secret-tokenHash-database-error"), domain.ErrInternal},
	} {
		for _, stage := range []string{"load", "revoke"} {
			t.Run(stage+pair.want.Error(), func(t *testing.T) {
				f := newRevocationFixture(t, "ACTIVE")
				if stage == "load" {
					f.loadErr = fmt.Errorf("secret-tokenHash: %w", pair.source)
				} else {
					f.revokeErr = fmt.Errorf("secret-tokenHash: %w", pair.source)
				}
				r, err := runRevocation(f)
				if r != nil || !errors.Is(err, pair.want) || errors.Unwrap(err) != nil || strings.Contains(fmt.Sprintf("%+v", err), "secret") {
					t.Fatal("unsafe failure", err)
				}
				if stage == "load" && !reflect.DeepEqual(f.events, []string{"load"}) {
					t.Fatal(f.events)
				}
			})
		}
	}
}
func TestBRTST030032034ConcurrentRotationOrRevocationKeepsWinningAudit(t *testing.T) {
	for _, status := range []string{"MANUAL", "ROTATED"} {
		t.Run(status, func(t *testing.T) {
			f := newRevocationFixture(t, "ACTIVE")
			winning := newRevocationFixture(t, status).candidate.JoinLink()
			f.customResult = true
			f.result, _ = domain.NewRevokeTesterJoinLinkResult(winning, false)
			r, err := runRevocation(f)
			if err != nil || r.Revoked() || !reflect.DeepEqual(r.JoinLink(), winning) || !reflect.DeepEqual(f.events, []string{"load", "clock", "revoke"}) {
				t.Fatal("lost winning audit", err)
			}
		})
	}
}
func TestBRTST030036DefensiveRevocationRepositoryValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*revocationFixture)
		want   error
	}{
		{"nil_candidate", func(f *revocationFixture) { f.candidate = nil }, domain.ErrApplicationTesterJoinLinkStateInconsistent},
		{"zero_clock", func(f *revocationFixture) { f.at = time.Time{} }, domain.ErrInternal},
		{"nil_result", func(f *revocationFixture) { f.customResult = true }, domain.ErrApplicationTesterJoinLinkStateInconsistent},
		{"wrong_candidate", func(f *revocationFixture) {
			l, _ := domain.NewActiveTesterJoinLink(second, app, domain.NewTesterJoinTokenHash([32]byte{42}), "creator", now)
			f.candidate, _ = domain.NewTesterJoinLinkRevocationCandidate(l)
		}, domain.ErrApplicationTesterJoinLinkStateInconsistent},
		{"wrong_actor", func(f *revocationFixture) {
			l, _ := f.candidate.JoinLink().Revoke("wrong-admin", now)
			f.result, _ = domain.NewRevokeTesterJoinLinkResult(l, true)
			f.customResult = true
		}, domain.ErrApplicationTesterJoinLinkStateInconsistent},
		{"changed_creation", func(f *revocationFixture) {
			l, _ := domain.NewActiveTesterJoinLink(first, app, domain.NewTesterJoinTokenHash([32]byte{99}), "different-creator", now)
			l, _ = l.Revoke(identity.AuthID, now)
			f.result, _ = domain.NewRevokeTesterJoinLinkResult(l, true)
			f.customResult = true
		}, domain.ErrApplicationTesterJoinLinkStateInconsistent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRevocationFixture(t, "ACTIVE")
			tc.mutate(f)
			r, err := runRevocation(f)
			if r != nil || !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	f := newRevocationFixture(t, "ACTIVE")
	if r, err := NewRevokeTesterJoinLinkHandler(nil, f).Handle(context.Background(), identity, app, first); r != nil || !errors.Is(err, domain.ErrInternal) {
		t.Fatal(err)
	}
	var missing *RevokeTesterJoinLinkHandler
	if r, err := missing.Handle(context.Background(), identity, app, first); r != nil || !errors.Is(err, domain.ErrInternal) {
		t.Fatal(err)
	}
}
func TestBRTST030RevocationNormalizesPathIDs(t *testing.T) {
	f := newRevocationFixture(t, "ACTIVE")
	id := domain.ApplicationTesterJoinLinkID("01900000-0000-7000-8000-00000000abcd")
	l, _ := domain.NewActiveTesterJoinLink(id, app, domain.NewTesterJoinTokenHash([32]byte{42}), "creator", now)
	f.candidate, _ = domain.NewTesterJoinLinkRevocationCandidate(l)
	_, err := NewRevokeTesterJoinLinkHandler(f, f).Handle(context.Background(), identity, app, domain.ApplicationTesterJoinLinkID(strings.ToUpper(id.String())))
	if err != nil || f.linkID != id {
		t.Fatal(err)
	}
}
