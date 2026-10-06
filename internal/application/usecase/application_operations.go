package usecase

import (
	"context"
	"strings"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

const (
	PermissionApplicationSuspend = "app.application.suspend"
	PermissionApplicationRestore = "app.application.restore"
)

type ApplicationOperationCommand struct {
	ApplicationID                        shared.ApplicationID
	ExpectedLifecycleRevision            int64
	ExpectedPlatformAvailabilityRevision int64
	Reason                               string
}

type ApplicationOperationsHandlers struct {
	repository port.ApplicationOperationsRepository
	ids        port.ApplicationOperationEventIDGenerator
	clock      port.Clock
}

func NewApplicationOperationsHandlers(repository port.ApplicationOperationsRepository, ids port.ApplicationOperationEventIDGenerator, clock port.Clock) *ApplicationOperationsHandlers {
	return &ApplicationOperationsHandlers{repository: repository, ids: ids, clock: clock}
}

func hasPermission(identity shared.TrustedIdentity, permission string) bool {
	for _, candidate := range identity.Permissions {
		if candidate == permission {
			return true
		}
	}
	return false
}

func (h *ApplicationOperationsHandlers) Get(ctx context.Context, identity shared.TrustedIdentity, applicationID shared.ApplicationID) (domain.ApplicationPlatformAvailability, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationPlatformAvailability{}, domain.ErrUserIdentityRequired
	}
	if !hasPermission(identity, PermissionApplicationSuspend) && !hasPermission(identity, PermissionApplicationRestore) {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationForbidden
	}
	if !applicationID.IsValid() {
		return domain.ApplicationPlatformAvailability{}, domain.ErrInvalidApplicationID
	}
	if h == nil || h.repository == nil {
		return domain.ApplicationPlatformAvailability{}, domain.NewInternalError(nil)
	}
	return h.repository.Get(ctx, applicationID)
}

func (h *ApplicationOperationsHandlers) Suspend(ctx context.Context, identity shared.TrustedIdentity, command ApplicationOperationCommand) (domain.ApplicationPlatformAvailability, error) {
	return h.set(ctx, identity, command, domain.PlatformAvailabilitySuspended, PermissionApplicationSuspend)
}

func (h *ApplicationOperationsHandlers) Restore(ctx context.Context, identity shared.TrustedIdentity, command ApplicationOperationCommand) (domain.ApplicationPlatformAvailability, error) {
	return h.set(ctx, identity, command, domain.PlatformAvailabilityAvailable, PermissionApplicationRestore)
}

func (h *ApplicationOperationsHandlers) set(ctx context.Context, identity shared.TrustedIdentity, command ApplicationOperationCommand, target domain.PlatformAvailabilityStatus, permission string) (domain.ApplicationPlatformAvailability, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationPlatformAvailability{}, domain.ErrUserIdentityRequired
	}
	if !hasPermission(identity, permission) {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationForbidden
	}
	if !command.ApplicationID.IsValid() {
		return domain.ApplicationPlatformAvailability{}, domain.ErrInvalidApplicationID
	}
	if command.ExpectedLifecycleRevision < 1 {
		return domain.ApplicationPlatformAvailability{}, domain.ErrInvalidLifecycleRevision
	}
	if command.ExpectedPlatformAvailabilityRevision < 1 {
		return domain.ApplicationPlatformAvailability{}, domain.ErrInvalidPlatformAvailabilityRevision
	}
	reason := strings.TrimSpace(command.Reason)
	if reason == "" || reason != command.Reason || len([]byte(reason)) > 1024 {
		return domain.ApplicationPlatformAvailability{}, domain.ErrInvalidApplicationOperationReason
	}
	if h == nil || h.repository == nil || h.ids == nil || h.clock == nil {
		return domain.ApplicationPlatformAvailability{}, domain.NewInternalError(nil)
	}
	id, err := h.ids.NewUUIDv7()
	if err != nil || !id.IsValid() {
		return domain.ApplicationPlatformAvailability{}, domain.NewInternalError(err)
	}
	return h.repository.Set(ctx, command.ApplicationID, identity.AuthID, target, command.ExpectedLifecycleRevision, command.ExpectedPlatformAvailabilityRevision, reason, id, h.clock.Now().UTC())
}
