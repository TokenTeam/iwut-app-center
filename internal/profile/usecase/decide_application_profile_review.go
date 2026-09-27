package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"slices"
)

type ReviewerIdentity struct {
	AuthID      shared.AuthID
	Permissions []string
}
type DecideApplicationProfileRevisionReviewCommand struct {
	ApplicationID, ProfileRevisionID, ProfileReviewID string
	ExpectedProfileRevisionRevision                   int64
	ExpectedCurrentPublishedProfileRevisionID         *string
	PublicationPreconditionPresent                    bool
	ExpectedPolicyVersion, Outcome                    string
	ConfirmedCheckIDs                                 []string
	Reason                                            *string
}
type DecideApplicationProfileRevisionReviewHandler struct {
	clock      port.Clock
	repository port.ApplicationProfileReviewDecisionRepository
}

func NewDecideApplicationProfileRevisionReviewHandler(clock port.Clock, repository port.ApplicationProfileReviewDecisionRepository) *DecideApplicationProfileRevisionReviewHandler {
	return &DecideApplicationProfileRevisionReviewHandler{clock, repository}
}
func (h *DecideApplicationProfileRevisionReviewHandler) Handle(ctx context.Context, identity ReviewerIdentity, c DecideApplicationProfileRevisionReviewCommand) (*domain.ApplicationProfileDecisionResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrReviewerIdentityRequired
	}
	if !slices.Contains(identity.Permissions, domain.ProfileReviewPermission) {
		return nil, domain.ErrApplicationProfileReviewPermissionRequired
	}
	app, ok := shared.ParseApplicationID(c.ApplicationID)
	revision, err := domain.ParseApplicationProfileRevisionID(c.ProfileRevisionID)
	review, e := domain.ParseApplicationProfileReviewID(c.ProfileReviewID)
	if !ok || err != nil || e != nil || c.ExpectedProfileRevisionRevision < 1 || !domain.ValidProfileReviewPolicyVersion(c.ExpectedPolicyVersion) || (c.Outcome != "APPROVE" && c.Outcome != "REJECT") || c.Outcome == "APPROVE" && !c.PublicationPreconditionPresent || c.Outcome == "REJECT" && c.PublicationPreconditionPresent {
		return nil, domain.ErrInvalidApplicationProfileReviewDecision
	}
	var published *domain.ApplicationProfileRevisionID
	if c.ExpectedCurrentPublishedProfileRevisionID != nil {
		id, err := domain.ParseApplicationProfileRevisionID(*c.ExpectedCurrentPublishedProfileRevisionID)
		if err != nil {
			return nil, domain.ErrInvalidApplicationProfileReviewDecision
		}
		published = &id
	}
	if err := domain.ValidateProfileReviewReason(c.Reason, c.Outcome == "REJECT"); err != nil {
		return nil, err
	}
	if c.Outcome == "REJECT" && len(c.ConfirmedCheckIDs) > 0 {
		return nil, domain.ErrProfileReviewChecksIncomplete
	}
	if h == nil || h.clock == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.DecideReview(ctx, port.ProfileReviewDecisionInput{ApplicationID: app, ProfileRevisionID: revision, ProfileReviewID: review, ReviewerID: identity.AuthID, Permissions: slices.Clone(identity.Permissions), ExpectedRevision: c.ExpectedProfileRevisionRevision, ExpectedPublishedID: published, PolicyVersion: c.ExpectedPolicyVersion, Outcome: c.Outcome, ConfirmedCheckIDs: slices.Clone(c.ConfirmedCheckIDs), Reason: c.Reason, DecidedAt: at})
	if err != nil {
		var business *domain.Error
		if errors.As(err, &business) {
			return nil, err
		}
		return nil, domain.NewInternalError(err)
	}
	if result == nil || result.ProfileRevision == nil || result.Review == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}
