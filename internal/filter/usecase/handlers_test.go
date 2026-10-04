package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	filterdomain "iwut-app-center/internal/filter/domain"
	filterport "iwut-app-center/internal/filter/port"
	"iwut-app-center/internal/shared"
)

const testApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"

var testIdentity = shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusApproved}

type fakeRepository struct {
	filter      *filterdomain.ApplicationFilter
	loadErr     error
	commitErr   error
	commitCalls int
}

func (r *fakeRepository) LoadForAdmin(context.Context, shared.ApplicationID, shared.AuthID) (*filterdomain.ApplicationFilter, error) {
	return r.filter, r.loadErr
}
func (r *fakeRepository) Commit(_ context.Context, _ shared.AuthID, _ int64, candidate *filterdomain.ApplicationFilter, _ *filterdomain.ApplicationFilterRevision) (*filterdomain.ApplicationFilter, error) {
	r.commitCalls++
	if r.commitErr != nil {
		return nil, r.commitErr
	}
	r.filter = candidate
	return candidate, nil
}

type fakeIDGenerator struct{ calls int }

func (g *fakeIDGenerator) NewUUIDv7() (filterdomain.FilterRevisionID, error) {
	g.calls++
	return "01890f5a-e810-7cc3-98c8-8c6d5d8b4c22", nil
}

type fakeClock struct{ calls int }

func (c *fakeClock) Now() time.Time {
	c.calls++
	return time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
}

func testRule(t *testing.T) filterdomain.Rule {
	t.Helper()
	value, _ := filterdomain.NewStringScalar("CN")
	rule, err := filterdomain.NewPredicate("profile.country", filterdomain.PredicateOperatorEQ, &value)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func defaultFilter(t *testing.T) *filterdomain.ApplicationFilter {
	t.Helper()
	value, err := filterdomain.NewDefaultApplicationFilter(testApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestHandlers_BR_FLT_003_008_SetClearAndNoopDoNotConsumeDependencies(t *testing.T) {
	t.Parallel()

	repository := &fakeRepository{filter: defaultFilter(t)}
	ids, clock := &fakeIDGenerator{}, &fakeClock{}
	handlers := NewHandlers(ids, clock, repository)

	set, err := handlers.Set(t.Context(), testIdentity, testApplicationID, SetCommand{ExpectedRevision: 0, Rule: testRule(t)})
	if err != nil || !set.Changed() || set.Filter().Revision() != 1 || repository.commitCalls != 1 || ids.calls != 1 || clock.calls != 1 {
		t.Fatalf("first set = %#v err=%v commits=%d ids=%d clocks=%d", set, err, repository.commitCalls, ids.calls, clock.calls)
	}

	noop, err := handlers.Set(t.Context(), testIdentity, testApplicationID, SetCommand{ExpectedRevision: 1, Rule: testRule(t)})
	if err != nil || noop.Changed() || repository.commitCalls != 1 || ids.calls != 1 || clock.calls != 1 {
		t.Fatalf("set noop = %#v err=%v commits=%d ids=%d clocks=%d", noop, err, repository.commitCalls, ids.calls, clock.calls)
	}

	cleared, err := handlers.Clear(t.Context(), testIdentity, testApplicationID, ClearCommand{ExpectedRevision: 1})
	if err != nil || !cleared.Changed() || cleared.Filter().Revision() != 2 || repository.commitCalls != 2 || ids.calls != 2 || clock.calls != 2 {
		t.Fatalf("clear = %#v err=%v commits=%d ids=%d clocks=%d", cleared, err, repository.commitCalls, ids.calls, clock.calls)
	}

	clearNoop, err := handlers.Clear(t.Context(), testIdentity, testApplicationID, ClearCommand{ExpectedRevision: 2})
	if err != nil || clearNoop.Changed() || repository.commitCalls != 2 || ids.calls != 2 || clock.calls != 2 {
		t.Fatalf("clear noop = %#v err=%v commits=%d ids=%d clocks=%d", clearNoop, err, repository.commitCalls, ids.calls, clock.calls)
	}
}

func TestHandlers_BR_FLT_008_009_ValidateIdentityRevisionAndRepositoryErrors(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		identity shared.DeveloperIdentity
		appID    string
		want     error
	}{
		{name: "identity", identity: shared.DeveloperIdentity{}, appID: testApplicationID, want: filterdomain.ErrDeveloperIdentityRequired},
		{name: "approval", identity: shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusPending}, appID: testApplicationID, want: filterdomain.ErrDeveloperApprovalRequired},
		{name: "application", identity: testIdentity, appID: "invalid", want: filterdomain.ErrInvalidApplicationID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := NewHandlers(&fakeIDGenerator{}, &fakeClock{}, &fakeRepository{filter: defaultFilter(t)})
			_, err := h.Get(t.Context(), testCase.identity, testCase.appID)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Get() error = %v, want %v", err, testCase.want)
			}
		})
	}

	h := NewHandlers(&fakeIDGenerator{}, &fakeClock{}, &fakeRepository{filter: defaultFilter(t)})
	if _, err := h.Set(t.Context(), testIdentity, testApplicationID, SetCommand{ExpectedRevision: 2, Rule: testRule(t)}); !errors.Is(err, filterdomain.ErrApplicationFilterRevisionConflict) {
		t.Fatalf("revision conflict error = %v", err)
	}

	for repositoryError, want := range map[error]error{
		filterport.ErrApplicationNotFound:               filterdomain.ErrApplicationNotFound,
		filterport.ErrApplicationAdminRequired:          filterdomain.ErrApplicationAdminRequired,
		filterport.ErrApplicationFilterRevisionConflict: filterdomain.ErrApplicationFilterRevisionConflict,
	} {
		h = NewHandlers(&fakeIDGenerator{}, &fakeClock{}, &fakeRepository{loadErr: repositoryError})
		if _, err := h.Get(t.Context(), testIdentity, testApplicationID); !errors.Is(err, want) {
			t.Fatalf("repository error %v mapped to %v, want %v", repositoryError, err, want)
		}
	}
}
