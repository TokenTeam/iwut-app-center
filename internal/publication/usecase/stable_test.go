package usecase

import (
	"context"
	"testing"
	"time"

	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
)

type stableFixture struct {
	*fixture
	placement *domain.StablePlacementCandidate
	clear     *domain.StableClearCandidate
}

func (f *stableFixture) LoadStablePlacementCandidate(context.Context, shared.ApplicationID, int32, domain.ApplicationVersionID, shared.AuthID, *int64) (*domain.StablePlacementCandidate, error) {
	f.events = append(f.events, "load-stable")
	return f.placement, f.loadErr
}
func (f *stableFixture) SetStable(_ context.Context, c *domain.StablePlacementCandidate, p *domain.ApplicationPublicationID, h domain.ApplicationPublicationHistoryID, admin shared.AuthID, v domain.PublicationValidation, at time.Time) (*domain.PlaceInTestResult, error) {
	f.events = append(f.events, "set-stable")
	if f.placeErr != nil {
		return nil, f.placeErr
	}
	return c.SetStable(p, h, admin, v, at)
}
func (f *stableFixture) LoadStableClearCandidate(context.Context, shared.ApplicationID, int32, shared.AuthID, int64) (*domain.StableClearCandidate, error) {
	f.events = append(f.events, "load-clear")
	return f.clear, f.loadErr
}
func (f *stableFixture) ClearStable(_ context.Context, c *domain.StableClearCandidate, h domain.ApplicationPublicationHistoryID, admin shared.AuthID, at time.Time) (*domain.PlaceInTestResult, error) {
	f.events = append(f.events, "clear-stable")
	if f.placeErr != nil {
		return nil, f.placeErr
	}
	return c.Clear(h, admin, at)
}

func newStableFixture(t *testing.T, stableVersion *domain.ApplicationVersionID) *stableFixture {
	t.Helper()
	base := setup(t, "")
	var publicationState *domain.ApplicationPublication
	var expected *int64
	if stableVersion != nil {
		publicationState, _ = domain.RestoreApplicationPublicationSlots(publication, app, 3, nil, stableVersion, 1, "admin", now, "admin", now)
		value := int64(1)
		expected = &value
	}
	placement, err := domain.NewStablePlacementCandidate(app, 3, version, review, 3, base.candidate.Snapshot(), publicationState, expected)
	if err != nil {
		t.Fatal(err)
	}
	var clear *domain.StableClearCandidate
	if publicationState != nil {
		clear, err = domain.NewStableClearCandidate(publicationState, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	return &stableFixture{fixture: base, placement: placement, clear: clear}
}

func TestBRPUB014StableSetAndNoOpDependencyOrder(t *testing.T) {
	f := newStableFixture(t, nil)
	result, err := NewSetApprovedVersionInStableSlotHandler(f, f, f, f, f).Handle(context.Background(), identity, app, 3, SetApprovedVersionInStableSlotCommand{VersionID: version})
	if err != nil || !result.Changed() || result.Publication().StableVersionID() != version {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	want := []string{"load-stable", "scope", "url", "id", "id", "clock", "set-stable"}
	if len(f.events) != len(want) {
		t.Fatalf("events=%v", f.events)
	}
	for i := range want {
		if f.events[i] != want[i] {
			t.Fatalf("events=%v", f.events)
		}
	}

	stableVersion := version
	f = newStableFixture(t, &stableVersion)
	result, err = NewSetApprovedVersionInStableSlotHandler(nil, nil, nil, nil, f).Handle(context.Background(), identity, app, 3, SetApprovedVersionInStableSlotCommand{VersionID: version, ExpectedPublicationRevision: f.placement.ExpectedPublicationRevision()})
	if err != nil || result.Changed() || len(f.events) != 1 || f.events[0] != "load-stable" {
		t.Fatalf("no-op result=%#v error=%v events=%v", result, err, f.events)
	}
}

func TestBRPUB014015ClearAndEmptyNoOp(t *testing.T) {
	stableVersion := version
	f := newStableFixture(t, &stableVersion)
	result, err := NewClearStableSlotHandler(f, f, f).Handle(context.Background(), identity, app, 3, ClearStableSlotCommand{ExpectedPublicationRevision: 1})
	if err != nil || !result.Changed() || result.Publication().StableVersionIDPtr() != nil || result.History().NewVersionIDPtr() != nil {
		t.Fatalf("clear result=%#v error=%v", result, err)
	}
	empty := result.Publication()
	f = newStableFixture(t, &stableVersion)
	f.clear, err = domain.NewStableClearCandidate(empty, 2)
	if err != nil {
		t.Fatal(err)
	}
	result, err = NewClearStableSlotHandler(nil, nil, f).Handle(context.Background(), identity, app, 3, ClearStableSlotCommand{ExpectedPublicationRevision: 2})
	if err != nil || result.Changed() || len(f.events) != 1 || f.events[0] != "load-clear" {
		t.Fatalf("empty no-op result=%#v error=%v events=%v", result, err, f.events)
	}
}
