package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type archivalIDs struct{ calls int }

func (g *archivalIDs) NewUUIDv7() (domain.ApplicationArchiveEventID, error) {
	g.calls++
	return "0199b33c-d040-7abc-8abc-123456789012", nil
}

type archivalRepository struct {
	state        domain.ApplicationArchiveState
	err          error
	target       domain.ApplicationArchiveStatus
	expected     int64
	factoryCalls int
	invokeTwice  bool
}

func (r *archivalRepository) GetArchiveState(context.Context, shared.ApplicationID, shared.AuthID) (domain.ApplicationArchiveState, error) {
	return r.state, r.err
}

func (r *archivalRepository) SetArchiveState(_ context.Context, _ shared.ApplicationID, _ shared.AuthID, target domain.ApplicationArchiveStatus, expected int64, factory port.ApplicationArchiveEventFactory) (domain.ApplicationArchiveState, error) {
	r.target, r.expected = target, expected
	if r.err != nil {
		return domain.ApplicationArchiveState{}, r.err
	}
	if r.state.ArchiveStatus == target {
		return r.state, nil
	}
	r.factoryCalls++
	firstID, firstAt, err := factory()
	if err != nil {
		return domain.ApplicationArchiveState{}, err
	}
	if r.invokeTwice {
		r.factoryCalls++
		secondID, secondAt, secondErr := factory()
		if secondErr != nil || firstID != secondID || !firstAt.Equal(secondAt) {
			return domain.ApplicationArchiveState{}, errors.New("event factory is not retry stable")
		}
	}
	r.state.ArchiveStatus = target
	r.state.ArchiveRevision++
	return r.state, nil
}

func TestApplicationArchival_BR_APP_052_056_AuthorizationValidationAndRetryStableEvent(t *testing.T) {
	appID, _ := shared.ParseApplicationID(generatedApplicationID.String())
	repo := &archivalRepository{state: domain.ApplicationArchiveState{ApplicationID: appID, LifecycleStatus: domain.ApplicationLifecycleActive, PlatformAvailabilityStatus: domain.PlatformAvailabilityAvailable, ArchiveStatus: domain.ApplicationArchiveUnarchived, ArchiveRevision: 1}, invokeTwice: true}
	ids := &archivalIDs{}
	h := NewApplicationArchivalHandlers(repo, ids, &fakeClock{now: time.Date(2026, 10, 10, 1, 2, 3, 0, time.FixedZone("CST", 8*60*60))})
	command := ApplicationArchiveCommand{ApplicationID: appID, ExpectedArchiveRevision: 1}
	if _, err := h.Archive(t.Context(), shared.AuthenticatedUserIdentity{}, command); !errors.Is(err, domain.ErrUserIdentityRequired) {
		t.Fatalf("missing identity error=%v", err)
	}
	invalid := command
	invalid.ExpectedArchiveRevision = 0
	if _, err := h.Archive(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, invalid); !errors.Is(err, domain.ErrInvalidApplicationArchiveRequest) {
		t.Fatalf("invalid revision error=%v", err)
	}
	got, err := h.Archive(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, command)
	if err != nil || got.ArchiveStatus != domain.ApplicationArchiveArchived || got.ArchiveRevision != 2 || ids.calls != 1 || repo.factoryCalls != 2 {
		t.Fatalf("state=%#v ids=%d factory=%d err=%v", got, ids.calls, repo.factoryCalls, err)
	}
}

func TestApplicationArchival_BR_APP_056_NoOpDoesNotUseClockOrID(t *testing.T) {
	appID, _ := shared.ParseApplicationID(generatedApplicationID.String())
	repo := &archivalRepository{state: domain.ApplicationArchiveState{ApplicationID: appID, LifecycleStatus: domain.ApplicationLifecycleActive, PlatformAvailabilityStatus: domain.PlatformAvailabilityAvailable, ArchiveStatus: domain.ApplicationArchiveArchived, ArchiveRevision: 2}}
	ids := &archivalIDs{}
	h := NewApplicationArchivalHandlers(repo, ids, &fakeClock{now: time.Now()})
	got, err := h.Archive(t.Context(), shared.AuthenticatedUserIdentity{AuthID: "admin"}, ApplicationArchiveCommand{ApplicationID: appID, ExpectedArchiveRevision: 2})
	if err != nil || got.ArchiveRevision != 2 || ids.calls != 0 || repo.factoryCalls != 0 {
		t.Fatalf("state=%#v ids=%d factory=%d err=%v", got, ids.calls, repo.factoryCalls, err)
	}
}
