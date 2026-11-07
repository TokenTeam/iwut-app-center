package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func RequireScriptReviewer(identity shared.TrustedIdentity) error {
	scope, err := ScriptReadAccess(identity, ScriptPendingRead)
	if err != nil || !scope.Review {
		if errors.Is(err, domain.ErrIdentityRequired) {
			return err
		}
		return domain.ErrReviewForbidden
	}
	return nil
}

type DecideReviewCommand struct {
	ResourceID, VersionID, ReviewID string
	Input                           domain.DecisionInput
}

type DecideReviewHandler struct {
	ids   port.ReviewIDGenerator
	clock port.Clock
	repo  port.ReviewDecisionRepository
}

func NewDecideReviewHandler(ids port.ReviewIDGenerator, clock port.Clock, repo port.ReviewDecisionRepository) *DecideReviewHandler {
	return &DecideReviewHandler{ids: ids, clock: clock, repo: repo}
}

func (h *DecideReviewHandler) Decide(ctx context.Context, identity shared.TrustedIdentity, command DecideReviewCommand) (domain.DecisionResult, error) {
	var empty domain.DecisionResult
	if err := RequireScriptReviewer(identity); err != nil {
		return empty, err
	}
	resource, err := domain.ParseResourceID(command.ResourceID)
	if err != nil {
		return empty, err
	}
	version, err := domain.ParseVersionID(command.VersionID)
	if err != nil {
		return empty, err
	}
	review, ok := domain.ParseReviewID(command.ReviewID)
	if !ok || command.Input.ExpectedRevision < 1 {
		return empty, domain.ErrInvalidDecision
	}
	decision, err := h.ids.NewUUIDv7()
	if err != nil {
		return empty, domain.ErrInternal
	}
	task, err := h.ids.NewUUIDv7()
	if err != nil {
		return empty, domain.ErrInternal
	}
	return h.repo.DecideReview(ctx, port.DecideReview{ResourceID: resource, VersionID: version, ReviewID: review,
		Input: command.Input, DecisionID: string(decision), TaskID: string(task), Actor: identity.AuthID, At: h.clock.Now()})
}

type CascadeHandler struct {
	ids      port.ReviewIDGenerator
	clock    port.Clock
	repo     port.CascadeRepository
	resolver port.SystemPrincipalResolver
}

func NewCascadeHandler(ids port.ReviewIDGenerator, clock port.Clock, repo port.CascadeRepository, resolver port.SystemPrincipalResolver) *CascadeHandler {
	return &CascadeHandler{ids: ids, clock: clock, repo: repo, resolver: resolver}
}

func (h *CascadeHandler) Reconcile(ctx context.Context) error {
	token, err := h.ids.NewUUIDv7()
	if err != nil {
		return domain.ErrInternal
	}
	lease, err := h.repo.ClaimCascade(ctx, string(token), h.clock.Now())
	if err != nil || lease == nil {
		return err
	}
	actor, err := h.resolver.ResolveReviewAutoRejection(ctx)
	failure := "SYSTEM_PRINCIPAL_UNAVAILABLE"
	if err == nil && actor.IsValid() {
		err = h.repo.AdvanceCascade(ctx, *lease, actor, h.clock.Now())
		failure = "CASCADE_TARGET_UNAVAILABLE"
	}
	if err == nil && actor.IsValid() {
		return nil
	}
	if recordErr := h.repo.RecordCascadeFailure(ctx, *lease, failure, h.clock.Now()); recordErr != nil {
		return recordErr
	}
	if err != nil {
		return err
	}
	return domain.ErrInternal
}
