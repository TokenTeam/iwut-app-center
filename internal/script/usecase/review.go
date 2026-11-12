package usecase

import (
	"context"
	"errors"
	"fmt"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type SubmitDraftCommand struct {
	ResourceID       string
	VersionID        string
	ExpectedRevision int64
}

type SubmitDraftHandler struct {
	ids   port.ReviewIDGenerator
	clock port.Clock
	repo  port.ReviewSubmissionRepository
}

func NewSubmitDraftHandler(ids port.ReviewIDGenerator, clock port.Clock, repo port.ReviewSubmissionRepository) *SubmitDraftHandler {
	return &SubmitDraftHandler{ids: ids, clock: clock, repo: repo}
}

func (h *SubmitDraftHandler) Submit(ctx context.Context, identity shared.TrustedIdentity, cmd SubmitDraftCommand) (domain.Submission, error) {
	if err := RequireScriptAuthor(identity); err != nil {
		return domain.Submission{}, err
	}
	resourceID, err := domain.ParseResourceID(cmd.ResourceID)
	if err != nil {
		return domain.Submission{}, err
	}
	versionID, err := domain.ParseVersionID(cmd.VersionID)
	if err != nil {
		return domain.Submission{}, err
	}
	if cmd.ExpectedRevision < 1 {
		return domain.Submission{}, domain.ErrInvalidRevision
	}
	reviewID, err := h.ids.NewUUIDv7()
	if err != nil {
		return domain.Submission{}, fmt.Errorf("%w: generate script review id: %w", domain.ErrInternal, err)
	}
	stored, err := h.repo.SubmitDraft(ctx, port.SubmitDraft{
		ResourceID: resourceID, VersionID: versionID, ReviewID: reviewID, ExpectedRevision: cmd.ExpectedRevision,
		Actor: identity.AuthID, At: h.clock.Now(),
	})
	if err == nil || errors.Is(err, domain.ErrReviewPolicyUnavailable) {
		return stored, err
	}
	return domain.Submission{}, draftWriteError(err)
}
