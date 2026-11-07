package port

import (
	"context"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

type DecideReview struct {
	ResourceID         domain.ResourceID
	VersionID          domain.VersionID
	ReviewID           domain.ReviewID
	Input              domain.DecisionInput
	DecisionID, TaskID string
	Actor              shared.AuthID
	At                 time.Time
}

type ReviewDecisionRepository interface {
	DecideReview(context.Context, DecideReview) (domain.DecisionResult, error)
}

type SystemPrincipalResolver interface {
	ResolveReviewAutoRejection(context.Context) (shared.AuthID, error)
}

type CascadeLease struct {
	TaskID, Token string
}

type CascadeRepository interface {
	ClaimCascade(context.Context, string, time.Time) (*CascadeLease, error)
	AdvanceCascade(context.Context, CascadeLease, shared.AuthID, time.Time) error
	RecordCascadeFailure(context.Context, CascadeLease, string, time.Time) error
}
