package port

import (
	"context"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
	"time"
)

type ProfileReviewDecisionInput struct {
	ApplicationID          shared.ApplicationID
	ProfileRevisionID      domain.ApplicationProfileRevisionID
	ProfileReviewID        domain.ApplicationProfileReviewID
	ReviewerID             shared.AuthID
	Permissions            []string
	ExpectedRevision       int64
	ExpectedPublishedID    *domain.ApplicationProfileRevisionID
	PolicyVersion, Outcome string
	ConfirmedCheckIDs      []string
	Reason                 *string
	DecidedAt              time.Time
}

// Stable domain errors cross this capability-owned atomic boundary; storage
// failures are sanitized by the adapter and wrapped by the use case.
type ApplicationProfileReviewDecisionRepository interface {
	DecideReview(context.Context, ProfileReviewDecisionInput) (*domain.ApplicationProfileDecisionResult, error)
}
