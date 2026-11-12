package usecase

import (
	"context"
	"errors"
	"fmt"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type CreateDraftCommand struct {
	ResourceID string
	Label      string
	Format     string
	Text       string
}

type ReplaceDraftCommand struct {
	ResourceID       string
	VersionID        string
	ExpectedRevision int64
	Format           string
	Text             string
}

type DraftHandlers struct {
	ids   port.VersionIDGenerator
	clock port.Clock
	repo  port.DraftRepository
}

func NewDraftHandlers(ids port.VersionIDGenerator, clock port.Clock, repo port.DraftRepository) *DraftHandlers {
	return &DraftHandlers{ids: ids, clock: clock, repo: repo}
}

func (h *DraftHandlers) Create(ctx context.Context, identity shared.TrustedIdentity, cmd CreateDraftCommand) (domain.Version, error) {
	if err := RequireScriptAuthor(identity); err != nil {
		return domain.Version{}, err
	}
	resourceID, err := domain.ParseResourceID(cmd.ResourceID)
	if err != nil {
		return domain.Version{}, err
	}
	if err := domain.ValidateVersionLabel(cmd.Label); err != nil {
		return domain.Version{}, err
	}
	content, err := domain.NewContent(cmd.Format, cmd.Text)
	if err != nil {
		return domain.Version{}, err
	}
	id, err := h.ids.NewUUIDv7()
	if err != nil {
		return domain.Version{}, fmt.Errorf("%w: generate script version id: %w", domain.ErrInternal, err)
	}
	draft, err := domain.NewDraft(id, resourceID, cmd.Label, content, identity.AuthID, h.clock.Now())
	if err != nil {
		return domain.Version{}, err
	}
	stored, err := h.repo.CreateDraft(ctx, draft)
	if err != nil {
		return domain.Version{}, draftWriteError(err)
	}
	return stored, nil
}

func (h *DraftHandlers) Replace(ctx context.Context, identity shared.TrustedIdentity, cmd ReplaceDraftCommand) (domain.Version, error) {
	if err := RequireScriptAuthor(identity); err != nil {
		return domain.Version{}, err
	}
	resourceID, err := domain.ParseResourceID(cmd.ResourceID)
	if err != nil {
		return domain.Version{}, err
	}
	versionID, err := domain.ParseVersionID(cmd.VersionID)
	if err != nil {
		return domain.Version{}, err
	}
	if cmd.ExpectedRevision < 1 {
		return domain.Version{}, domain.ErrInvalidRevision
	}
	content, err := domain.NewContent(cmd.Format, cmd.Text)
	if err != nil {
		return domain.Version{}, err
	}
	stored, err := h.repo.ReplaceDraft(ctx, port.ReplaceDraft{
		ResourceID: resourceID, VersionID: versionID, ExpectedRevision: cmd.ExpectedRevision,
		Content: content, Actor: identity.AuthID, At: h.clock.Now(),
	})
	if err != nil {
		return domain.Version{}, draftWriteError(err)
	}
	return stored, nil
}

func draftWriteError(err error) error {
	if errors.Is(err, shared.ErrAccountExitBlocked) || errors.Is(err, domain.ErrNotFound) || errors.Is(err, domain.ErrVersionNotFound) ||
		errors.Is(err, domain.ErrRevisionConflict) || errors.Is(err, domain.ErrVersionLabelConflict) ||
		errors.Is(err, domain.ErrVersionNotDraft) {
		return err
	}
	return fmt.Errorf("%w: write script draft: %w", domain.ErrInternal, err)
}
