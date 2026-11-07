package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
	"testing"
	"time"
)

type cascadeIDStub struct{}

func (cascadeIDStub) NewUUIDv7() (domain.ReviewID, error) {
	return "018f0000-0000-7000-8000-000000000049", nil
}

type cascadeRepositoryStub struct {
	lease        *port.CascadeLease
	actor        shared.AuthID
	failure      string
	advanced     bool
	advanceError error
}

func (r *cascadeRepositoryStub) ClaimCascade(context.Context, string, time.Time) (*port.CascadeLease, error) {
	return r.lease, nil
}
func (r *cascadeRepositoryStub) AdvanceCascade(_ context.Context, _ port.CascadeLease, actor shared.AuthID, _ time.Time) error {
	r.actor = actor
	r.advanced = true
	return r.advanceError
}
func (r *cascadeRepositoryStub) RecordCascadeFailure(_ context.Context, _ port.CascadeLease, code string, _ time.Time) error {
	r.failure = code
	return nil
}

type cascadeSystemStub struct {
	id    shared.AuthID
	err   error
	calls int
}

func (r *cascadeSystemStub) ResolveReviewAutoRejection(context.Context) (shared.AuthID, error) {
	r.calls++
	return r.id, r.err
}

func TestBRSDP007008CascadeUsesResolvedSystemAndRetainsFailures(t *testing.T) {
	for _, test := range []struct {
		name                        string
		id                          shared.AuthID
		resolverError, advanceError error
		failure                     string
		advanced                    bool
	}{
		{name: "valid", id: "resolved-system", advanced: true},
		{name: "unavailable", resolverError: errors.New("auth unavailable"), failure: "SYSTEM_PRINCIPAL_UNAVAILABLE"},
		{name: "invalid identity", failure: "SYSTEM_PRINCIPAL_UNAVAILABLE"},
		{name: "database failure", id: "resolved-system", advanceError: errors.New("database unavailable"), failure: "CASCADE_TARGET_UNAVAILABLE", advanced: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &cascadeRepositoryStub{lease: &port.CascadeLease{TaskID: "task", Token: "token"}, advanceError: test.advanceError}
			resolver := &cascadeSystemStub{id: test.id, err: test.resolverError}
			handler := NewCascadeHandler(cascadeIDStub{}, resourceClock(time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)), repo, resolver)
			err := handler.Reconcile(t.Context())
			if (err != nil) != (test.failure != "") || repo.failure != test.failure || repo.advanced != test.advanced {
				t.Fatalf("failure %q advanced %v err %v", repo.failure, repo.advanced, err)
			}
			if test.advanced && repo.actor != test.id {
				t.Fatal("did not use resolved identity")
			}
		})
	}
	repo := &cascadeRepositoryStub{}
	resolver := &cascadeSystemStub{err: errors.New("unavailable")}
	handler := NewCascadeHandler(cascadeIDStub{}, resourceClock(time.Now()), repo, resolver)
	if err := handler.Reconcile(t.Context()); err != nil || resolver.calls != 0 {
		t.Fatal("idle worker depends on SYSTEM", err)
	}
}
