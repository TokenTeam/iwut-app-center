package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
	"time"
)

var ErrInvalidApplicationProfileContent = errors.New("invalid application profile content")

type ApplicationProfileReviewIDGenerator interface {
	NewUUIDv7() (domain.ApplicationProfileReviewID, error)
}
type ApplicationProfileReviewRepository interface {
	SubmitDraft(context.Context, shared.ApplicationID, domain.ApplicationProfileRevisionID, shared.AuthID, int64, domain.ApplicationProfileReviewID, time.Time) (*domain.ApplicationProfileSubmission, error)
}
