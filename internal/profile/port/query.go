package port

import (
	"context"
	"errors"
	profiledomain "iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrInvalidProfileReviewPageToken       = errors.New("invalid profile review page token")
	ErrProfileReviewQueryNotFound          = errors.New("profile review query not found")
	ErrProfileReviewQueryStateInconsistent = errors.New("profile review query state inconsistent")
)

type ProfileReviewQueryRepository interface {
	ListPending(context.Context, shared.AuthID, *shared.ApplicationID, int32, string) (*profiledomain.PendingProfileReviewPage, error)
	Get(context.Context, shared.AuthID, shared.ApplicationID, profiledomain.ApplicationProfileRevisionID, profiledomain.ApplicationProfileReviewID) (*profiledomain.ProfileReviewDetail, error)
}
