package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

type operationIDs struct {
	id    domain.ApplicationOperationEventID
	calls int
}

func (g *operationIDs) NewUUIDv7() (domain.ApplicationOperationEventID, error) {
	g.calls++
	return g.id, nil
}

type operationRepository struct {
	result domain.ApplicationPlatformAvailability
	calls  int
	target domain.PlatformAvailabilityStatus
	reason string
}

func (r *operationRepository) Get(context.Context, shared.ApplicationID) (domain.ApplicationPlatformAvailability, error) {
	r.calls++
	return r.result, nil
}
func (r *operationRepository) Set(_ context.Context, _ shared.ApplicationID, _ shared.AuthID, target domain.PlatformAvailabilityStatus, _, _ int64, reason string, _ domain.ApplicationOperationEventID, _ time.Time) (domain.ApplicationPlatformAvailability, error) {
	r.calls++
	r.target, r.reason = target, reason
	return r.result, nil
}

func TestApplicationOperations_ExactPermissionsAndValidation(t *testing.T) {
	appID, _ := shared.ParseApplicationID(generatedApplicationID.String())
	eventID := domain.ApplicationOperationEventID("0199b33c-d030-7abc-8abc-123456789012")
	repository := &operationRepository{result: domain.ApplicationPlatformAvailability{ApplicationID: appID, LifecycleStatus: domain.ApplicationLifecycleActive, LifecycleRevision: 1, Status: domain.PlatformAvailabilitySuspended, Revision: 2}}
	ids := &operationIDs{id: eventID}
	handler := NewApplicationOperationsHandlers(repository, ids, &fakeClock{now: time.Now()})
	command := ApplicationOperationCommand{ApplicationID: appID, ExpectedLifecycleRevision: 1, ExpectedPlatformAvailabilityRevision: 1, Reason: "policy violation"}

	if _, err := handler.Suspend(t.Context(), shared.TrustedIdentity{AuthID: "operator", Permissions: []string{PermissionApplicationRestore}}, command); !errors.Is(err, domain.ErrApplicationOperationForbidden) {
		t.Fatalf("wrong permission error=%v", err)
	}
	if repository.calls != 0 || ids.calls != 0 {
		t.Fatal("forbidden command reached dependencies")
	}
	if _, err := handler.Suspend(t.Context(), shared.TrustedIdentity{AuthID: "operator", Permissions: []string{PermissionApplicationSuspend}}, command); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 1 || ids.calls != 1 || repository.target != domain.PlatformAvailabilitySuspended || repository.reason != command.Reason {
		t.Fatalf("unexpected invocation %#v", repository)
	}
	command.Reason = " padded "
	if _, err := handler.Restore(t.Context(), shared.TrustedIdentity{AuthID: "operator", Permissions: []string{PermissionApplicationRestore}}, command); !errors.Is(err, domain.ErrInvalidApplicationOperationReason) {
		t.Fatalf("invalid reason error=%v", err)
	}
}

func TestApplicationOperations_GetAcceptsEitherPermission(t *testing.T) {
	appID, _ := shared.ParseApplicationID(generatedApplicationID.String())
	repository := &operationRepository{result: domain.ApplicationPlatformAvailability{ApplicationID: appID}}
	handler := NewApplicationOperationsHandlers(repository, nil, nil)
	for _, permission := range []string{PermissionApplicationSuspend, PermissionApplicationRestore} {
		if _, err := handler.Get(t.Context(), shared.TrustedIdentity{AuthID: "operator", Permissions: []string{permission}}, appID); err != nil {
			t.Fatalf("permission %s: %v", permission, err)
		}
	}
	if _, err := handler.Get(t.Context(), shared.TrustedIdentity{AuthID: "operator"}, appID); !errors.Is(err, domain.ErrApplicationOperationForbidden) {
		t.Fatalf("missing permission error=%v", err)
	}
}
