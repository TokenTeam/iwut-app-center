package usecase

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type CreateResourceCommand struct {
	Name    string
	Purpose string
}

type ReplaceResourceMetadataCommand struct {
	ResourceID       string
	ExpectedRevision int64
	Name             string
	Purpose          string
}

type ResourceHandlers struct {
	ids   port.ResourceIDGenerator
	clock port.Clock
	repo  port.ResourceRepository
}

func NewResourceHandlers(ids port.ResourceIDGenerator, clock port.Clock, repo port.ResourceRepository) *ResourceHandlers {
	return &ResourceHandlers{ids: ids, clock: clock, repo: repo}
}

func requireMaintainer(identity shared.TrustedIdentity) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrIdentityRequired
	}
	if !slices.Contains(identity.Permissions, domain.MaintainPermission) {
		return domain.ErrMaintainForbidden
	}
	return nil
}

func (h *ResourceHandlers) Create(ctx context.Context, identity shared.TrustedIdentity, cmd CreateResourceCommand) (domain.Resource, error) {
	if err := requireMaintainer(identity); err != nil {
		return domain.Resource{}, err
	}
	metadata, err := domain.NewMetadata(cmd.Name, cmd.Purpose)
	if err != nil {
		return domain.Resource{}, err
	}
	id, err := h.ids.NewUUIDv7()
	if err != nil {
		return domain.Resource{}, fmt.Errorf("%w: generate id: %w", domain.ErrInternal, err)
	}
	resource, err := domain.NewResource(id, metadata, identity.AuthID, h.clock.Now())
	if err != nil {
		return domain.Resource{}, err
	}
	stored, err := h.repo.CreateResource(ctx, resource)
	if err != nil {
		return domain.Resource{}, fmt.Errorf("%w: create: %w", domain.ErrInternal, err)
	}
	return stored, nil
}

func (h *ResourceHandlers) ReplaceMetadata(ctx context.Context, identity shared.TrustedIdentity, cmd ReplaceResourceMetadataCommand) (domain.Resource, error) {
	if err := requireMaintainer(identity); err != nil {
		return domain.Resource{}, err
	}
	id, err := domain.ParseResourceID(cmd.ResourceID)
	if err != nil {
		return domain.Resource{}, err
	}
	if cmd.ExpectedRevision < 1 {
		return domain.Resource{}, domain.ErrInvalidRevision
	}
	metadata, err := domain.NewMetadata(cmd.Name, cmd.Purpose)
	if err != nil {
		return domain.Resource{}, err
	}
	stored, err := h.repo.ReplaceResourceMetadata(ctx, port.ReplaceMetadata{
		ID: id, ExpectedRevision: cmd.ExpectedRevision, Metadata: metadata,
		Actor: identity.AuthID, At: h.clock.Now().UTC(),
	})
	switch {
	case err == nil:
		return stored, nil
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrRevisionConflict):
		return domain.Resource{}, err
	default:
		return domain.Resource{}, fmt.Errorf("%w: replace metadata: %w", domain.ErrInternal, err)
	}
}
