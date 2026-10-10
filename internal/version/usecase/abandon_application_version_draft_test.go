package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

type abandonmentIDs struct{ calls int }

func (g *abandonmentIDs) NewAbandonmentUUIDv7() (domain.ApplicationVersionAbandonmentID, error) {
	g.calls++
	return "018f7777-7777-7777-8777-777777777774", nil
}

type abandonmentClock struct {
	calls int
	at    time.Time
}

func (c *abandonmentClock) Now() time.Time { c.calls++; return c.at }

type abandonmentRepository struct {
	calls         int
	err           error
	invokeFactory int
	facts         []*domain.ApplicationVersionAbandonment
}

func (r *abandonmentRepository) AbandonDraft(_ context.Context, app shared.ApplicationID, version domain.ApplicationVersionID, admin shared.AuthID, revision int64, factory port.ApplicationVersionAbandonmentFactory) (*domain.AbandonApplicationVersionDraftResult, error) {
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	sequence, _ := domain.NewVersionSequence(3)
	label, _ := domain.NewVersionLabel("v3")
	for i := 0; i < r.invokeFactory; i++ {
		fact, err := factory(sequence, label)
		if err != nil {
			return nil, err
		}
		r.facts = append(r.facts, fact)
	}
	if r.invokeFactory == 0 {
		fact, _ := domain.NewApplicationVersionAbandonment("018f7777-7777-7777-8777-777777777775", app, version, sequence, label, revision, admin, time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC))
		return &domain.AbandonApplicationVersionDraftResult{Abandonment: fact, Changed: false}, nil
	}
	return &domain.AbandonApplicationVersionDraftResult{Abandonment: r.facts[0], Changed: true}, nil
}

func TestAbandonApplicationVersionDraft_BR_VER_026_030_UsesUserIdentityAndLazyStableAudit(t *testing.T) {
	ids := &abandonmentIDs{}
	clock := &abandonmentClock{at: time.Date(2026, 10, 10, 1, 2, 3, 0, time.UTC)}
	repository := &abandonmentRepository{invokeFactory: 2}
	handler := NewAbandonApplicationVersionDraftHandler(ids, clock, repository)
	result, err := handler.Handle(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "auth-current"}, AbandonApplicationVersionDraftCommand{
		ApplicationID: "018f7777-7777-7777-8777-777777777771", VersionID: "018f7777-7777-7777-8777-777777777773", ExpectedRevision: 2,
	})
	if err != nil || result == nil || !result.Changed {
		t.Fatalf("Handle() = (%v, %v)", result, err)
	}
	if ids.calls != 1 || clock.calls != 1 || len(repository.facts) != 2 || repository.facts[0] != repository.facts[1] {
		t.Fatalf("lazy factory calls ids=%d clock=%d facts=%v", ids.calls, clock.calls, repository.facts)
	}
}

func TestAbandonApplicationVersionDraft_BR_VER_030_NoOpDoesNotAdvanceClockOrID(t *testing.T) {
	ids := &abandonmentIDs{}
	clock := &abandonmentClock{}
	handler := NewAbandonApplicationVersionDraftHandler(ids, clock, &abandonmentRepository{})
	result, err := handler.Handle(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "auth-current"}, AbandonApplicationVersionDraftCommand{
		ApplicationID: "018f7777-7777-7777-8777-777777777771", VersionID: "018f7777-7777-7777-8777-777777777773", ExpectedRevision: 2,
	})
	if err != nil || result == nil || result.Changed || ids.calls != 0 || clock.calls != 0 {
		t.Fatalf("no-op = (%v,%v), ids=%d clock=%d", result, err, ids.calls, clock.calls)
	}
}

func TestAbandonApplicationVersionDraft_BR_VER_026_ValidationAndErrorMapping(t *testing.T) {
	valid := AbandonApplicationVersionDraftCommand{ApplicationID: "018f7777-7777-7777-8777-777777777771", VersionID: "018f7777-7777-7777-8777-777777777773", ExpectedRevision: 1}
	for _, test := range []struct {
		name                string
		identity            shared.AuthenticatedUserIdentity
		command             AbandonApplicationVersionDraftCommand
		repositoryErr, want error
	}{
		{"identity", shared.AuthenticatedUserIdentity{}, valid, nil, domain.ErrUserIdentityRequired},
		{"invalid input", shared.AuthenticatedUserIdentity{AuthID: "a"}, AbandonApplicationVersionDraftCommand{}, nil, domain.ErrInvalidApplicationVersionAbandonment},
		{"hidden", shared.AuthenticatedUserIdentity{AuthID: "a"}, valid, port.ErrApplicationVersionNotFound, domain.ErrApplicationVersionNotFound},
		{"conflict", shared.AuthenticatedUserIdentity{AuthID: "a"}, valid, port.ErrApplicationVersionRevisionConflict, domain.ErrApplicationVersionRevisionConflict},
		{"reviewed", shared.AuthenticatedUserIdentity{AuthID: "a"}, valid, port.ErrApplicationVersionNotAbandonable, domain.ErrApplicationVersionNotAbandonable},
		{"closed", shared.AuthenticatedUserIdentity{AuthID: "a"}, valid, port.ErrVersionAbandonmentNotAllowed, domain.ErrApplicationVersionAbandonmentNotAllowed},
		{"corrupt", shared.AuthenticatedUserIdentity{AuthID: "a"}, valid, port.ErrVersionAbandonmentStateInconsistent, domain.ErrApplicationVersionAbandonmentStateInconsistent},
		{"unavailable", shared.AuthenticatedUserIdentity{AuthID: "a"}, valid, port.ErrVersionAbandonmentUnavailable, domain.NewApplicationVersionAbandonmentUnavailableError(nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &abandonmentRepository{err: test.repositoryErr}
			result, err := NewAbandonApplicationVersionDraftHandler(&abandonmentIDs{}, &abandonmentClock{}, repository).Handle(t.Context(), test.identity, test.command)
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v,%v), want %v", result, err, test.want)
			}
			if (test.want == domain.ErrUserIdentityRequired || test.want == domain.ErrInvalidApplicationVersionAbandonment) && repository.calls != 0 {
				t.Fatal("repository called before validation")
			}
		})
	}
}
