package usecase

import (
	"context"
	"slices"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type PublicationCommand struct {
	ResourceID, VersionID, Action string
	ExpectedRevision              int64
	Reason                        string
}

type PublicationHandler struct {
	ids   port.PublicationEventIDGenerator
	clock port.Clock
	repo  port.PublicationRepository
}

func NewPublicationHandler(ids port.PublicationEventIDGenerator, clock port.Clock, repo port.PublicationRepository) *PublicationHandler {
	return &PublicationHandler{ids: ids, clock: clock, repo: repo}
}

func RequirePublicationPermission(identity shared.TrustedIdentity, action string) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrIdentityRequired
	}
	permission := domain.PublicationPermission(action)
	if permission == "" || !slices.Contains(identity.Permissions, permission) {
		return domain.ErrPublicationForbidden
	}
	return nil
}

func (h *PublicationHandler) Apply(ctx context.Context, identity shared.TrustedIdentity, command PublicationCommand) (domain.PublicationResult, error) {
	var empty domain.PublicationResult
	if err := RequirePublicationPermission(identity, command.Action); err != nil {
		return empty, err
	}
	id, err := domain.ParseResourceID(command.ResourceID)
	if err != nil {
		return empty, err
	}
	var version domain.VersionID
	if command.Action == domain.PublishScript {
		version, err = domain.ParseVersionID(command.VersionID)
		if err != nil {
			return empty, err
		}
	}
	if command.ExpectedRevision < 1 {
		return empty, domain.ErrInvalidPublicationRequest
	}
	reason := ""
	if command.Action == domain.SuspendScript || command.Action == domain.RestoreScript {
		reason, err = domain.NormalizeDistributionReason(command.Reason)
		if err != nil {
			return empty, err
		}
	}
	eventID, err := h.ids.NewUUIDv7()
	if err != nil {
		return empty, domain.ErrInternal
	}
	return h.repo.ApplyPublication(ctx, port.PublicationCommand{ResourceID: id, VersionID: version, Action: command.Action,
		ExpectedRevision: command.ExpectedRevision, Reason: reason, EventID: eventID, Actor: identity.AuthID, At: h.clock.Now()})
}
