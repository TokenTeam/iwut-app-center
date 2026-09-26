package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
	"time"
)

var (
	ErrApplicationProfileRevisionNotFound = errors.New("application profile revision not found")
	ErrApplicationProfileRevisionNotDraft = errors.New("application profile revision is not draft")
	ErrApplicationProfileRevisionConflict = errors.New("application profile revision conflict")
)

// ReplaceDraft atomically protects admin, DRAFT, revision and working pointer,
// including normalized no-op requests.
type DraftApplicationProfileRevisionRepository interface {
	ReplaceDraft(context.Context, shared.ApplicationID, domain.ApplicationProfileRevisionID, shared.AuthID, int64, domain.DraftApplicationProfileReplacement, time.Time) (*domain.ApplicationProfileRevision, error)
}
