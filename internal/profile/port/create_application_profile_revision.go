package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
	"time"
)

var (
	ErrApplicationNotFound                         = errors.New("application not found")
	ErrApplicationAdminRequired                    = errors.New("application administrator required")
	ErrApplicationProfileWorkRevisionAlreadyExists = errors.New("application profile work revision already exists")
	ErrApplicationProfileStateInconsistent         = errors.New("application profile state is inconsistent")
)

type ApplicationProfileRevisionIDGenerator interface {
	NewUUIDv7() (domain.ApplicationProfileRevisionID, error)
}
type Clock interface{ Now() time.Time }

// CreateDraft owns the atomic admin check, working slot, sequence allocation,
// draft insertion, and working profile pointer update.
type ApplicationProfileRevisionRepository interface {
	CreateDraft(context.Context, shared.AuthID, *domain.DraftApplicationProfileRevision) (*domain.ApplicationProfileRevision, error)
}
