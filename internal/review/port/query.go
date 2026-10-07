package port

import (
	"context"
	"errors"
	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrInvalidReviewPageToken       = errors.New("invalid review page token")
	ErrReviewQueryNotFound          = errors.New("review query not found")
	ErrReviewQueryStateInconsistent = errors.New("review query state inconsistent")
)

type ReviewQueryRepository interface {
	ListPending(context.Context, shared.AuthID, *shared.ApplicationID, int32, string) (*reviewdomain.PendingReviewPage, error)
	Get(context.Context, shared.AuthID, shared.ApplicationID, reviewdomain.ApplicationVersionID, reviewdomain.ApplicationReviewID) (*reviewdomain.ReviewDetail, error)
}
