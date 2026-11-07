package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrInvalidScriptCursor     = errors.New("invalid script cursor")
	ErrScriptReviewNotFound    = errors.New("script review not found")
	ErrScriptQueryInconsistent = errors.New("inconsistent script query state")
)

type ScriptReadScope struct {
	Actor                                       shared.AuthID
	Maintain, Review, Publish, Suspend, Restore bool
}

type ScriptPageRequest struct {
	Limit  int
	Cursor string
}

type ScriptPage[T any] struct {
	Items      []T
	NextCursor string
}

type ScriptVersionSummary struct {
	ResourceID            domain.ResourceID
	ID                    domain.VersionID
	Label, Format, Digest string
	State                 domain.VersionState
	Revision              int64
	CreatedAt, UpdatedAt  time.Time
}

type ScriptVersionDetail struct {
	Version    ScriptVersionSummary
	Text       *string
	ReviewID   domain.ReviewID
	DecisionID string
}

type ScriptReviewSummary struct {
	ResourceID    domain.ResourceID
	VersionID     domain.VersionID
	ID            domain.ReviewID
	Label, Digest string
	SubmittedAt   time.Time
}

type ScriptReviewDetail struct {
	Cascade *domain.CascadeProgress
	Review  domain.Review
	Version ScriptVersionSummary
}

type PublicationHistoryItem struct {
	EventID    domain.PublicationEventID
	ResourceID domain.ResourceID
	Action     string
	Revision   int64
	Version    *domain.PublishedScript
	At         time.Time
}

type ScriptQueryRepository interface {
	GetPublication(context.Context, domain.ResourceID) (domain.PublicationState, error)
	ListPublicationHistory(context.Context, ScriptReadScope, domain.ResourceID, ScriptPageRequest) (ScriptPage[PublicationHistoryItem], error)
	ListResources(context.Context, ScriptReadScope, ScriptPageRequest) (ScriptPage[domain.Resource], error)
	GetResource(context.Context, domain.ResourceID) (domain.Resource, error)
	ListVersions(context.Context, ScriptReadScope, domain.ResourceID, domain.VersionState, ScriptPageRequest) (ScriptPage[ScriptVersionSummary], error)
	GetVersion(context.Context, ScriptReadScope, domain.ResourceID, domain.VersionID) (ScriptVersionDetail, error)
	ListPendingReviews(context.Context, ScriptReadScope, domain.ResourceID, ScriptPageRequest) (ScriptPage[ScriptReviewSummary], error)
	GetReview(context.Context, domain.ResourceID, domain.VersionID, domain.ReviewID) (ScriptReviewDetail, error)
}
