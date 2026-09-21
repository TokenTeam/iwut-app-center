package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrApplicationReviewNotLatest       = errors.New("application review is not the latest attempt")
	ErrApplicationReviewAlreadyRestored = errors.New("application review is already restored")
	ErrApplicationVersionNotRejected    = errors.New("application version is not rejected")
)

type RejectedApplicationVersionRepository interface {
	RestoreDraft(
		ctx context.Context,
		applicationID shared.ApplicationID,
		versionID domain.ApplicationVersionID,
		reviewID domain.ApplicationReviewID,
		expectedAdminID shared.AuthID,
		expectedVersionRevision int64,
		restoredAt time.Time,
	) (*domain.RestoreRejectedVersionResult, error)
}
