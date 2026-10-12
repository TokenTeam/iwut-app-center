package usecase

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
)

type testClearFixture struct {
	*fixture
	publication *domain.ApplicationPublication
	err         error
	retry       bool
	called      bool
}

func (f *testClearFixture) ClearTest(_ context.Context, app shared.ApplicationID, major int32, admin shared.AuthID, expected int64, factory port.TestClearAuditFactory) (*domain.PlaceInTestResult, error) {
	f.called = true
	if f.err != nil {
		return nil, f.err
	}
	c, err := domain.NewTestClearCandidate(f.publication, expected)
	if err != nil {
		return nil, err
	}
	if c.IsNoOp() {
		return domain.NewNoOpResult(c.Publication())
	}
	id, at, err := factory()
	if err != nil {
		return nil, err
	}
	if f.retry {
		againID, againAt, err := factory()
		if err != nil || againID != id || againAt != at {
			panic("unstable audit factory")
		}
	}
	return c.Clear(id, admin, at)
}

func TestBRPUB031033034ClearTestLazyAuditAndRetries(t *testing.T) {
	base := setup(t, "")
	v := version
	p, _ := domain.RestoreApplicationPublicationSlots(publication, app, 3, &v, nil, 1, "admin", now, "admin", now)
	f := &testClearFixture{fixture: base, publication: p, retry: true}
	r, err := NewClearTestSlotHandler(f, f, f).Handle(context.Background(), identity, app, 3, ClearTestSlotCommand{1})
	if err != nil || !r.Changed() || !reflect.DeepEqual(f.events, []string{"id", "clock"}) {
		t.Fatalf("change=%v error=%v events=%v", r, err, f.events)
	}
	f.publication = r.Publication()
	f.events = nil
	r, err = NewClearTestSlotHandler(nil, nil, f).Handle(context.Background(), identity, app, 3, ClearTestSlotCommand{2})
	if err != nil || r.Changed() || len(f.events) != 0 {
		t.Fatalf("no-op=%v error=%v events=%v", r, err, f.events)
	}
	for _, tc := range []struct {
		name     string
		identity shared.DeveloperIdentity
		major    int32
		expected int64
		want     error
	}{
		{"ordinary user", shared.DeveloperIdentity{AuthID: "admin"}, 3, 2, domain.ErrDeveloperApprovalRequired},
		{"suspended developer", shared.DeveloperIdentity{AuthID: "admin", DeveloperStatus: shared.DeveloperStatusSuspended}, 3, 2, domain.ErrDeveloperApprovalRequired},
		{"missing identity", shared.DeveloperIdentity{}, 3, 2, domain.ErrDeveloperIdentityRequired},
		{"major", identity, 0, 2, domain.ErrInvalidRpcApiMajor},
		{"revision", identity, 3, 0, domain.ErrInvalidApplicationPublicationRevision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.called = false
			_, err := NewClearTestSlotHandler(f, f, f).Handle(context.Background(), tc.identity, app, tc.major, ClearTestSlotCommand{tc.expected})
			if !errors.Is(err, tc.want) || f.called {
				t.Fatalf("error=%v called=%v", err, f.called)
			}
		})
	}
	if _, err := NewClearTestSlotHandler(f, f, f).Handle(context.Background(), identity, "invalid", 3, ClearTestSlotCommand{2}); !errors.Is(err, domain.ErrInvalidApplicationID) {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, want error }{{port.ErrApplicationPublicationNotFound, domain.ErrApplicationPublicationNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired}, {port.ErrApplicationPublicationNotAllowed, domain.ErrApplicationPublicationNotAllowed}, {port.ErrApplicationPublicationStateInconsistent, domain.ErrApplicationPublicationStateInconsistent}, {port.ErrApplicationPublicationRevisionConflict, domain.ErrApplicationPublicationRevisionConflict}, {errors.New("storage"), domain.ErrInternal}} {
		f.err = tc.source
		_, err := NewClearTestSlotHandler(f, f, f).Handle(context.Background(), identity, app, 3, ClearTestSlotCommand{2})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%v mapped to %v", tc.source, err)
		}
	}
}
