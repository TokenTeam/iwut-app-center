package port

import (
	"context"
	"errors"
	filterdomain "iwut-app-center/internal/filter/domain"
	"iwut-app-center/internal/shared"
	"time"
)

var (
	ErrApplicationNotFound                = errors.New("application not found")
	ErrApplicationAdminRequired           = errors.New("application administrator required")
	ErrApplicationFilterRevisionConflict  = errors.New("application filter revision conflict")
	ErrApplicationFilterStateInconsistent = errors.New("application filter state inconsistent")
)

type RevisionIDGenerator interface {
	NewUUIDv7() (filterdomain.FilterRevisionID, error)
}
type Clock interface{ Now() time.Time }

type Repository interface {
	LoadForAdmin(context.Context, shared.ApplicationID, shared.AuthID) (*filterdomain.ApplicationFilter, error)
	Commit(context.Context, shared.AuthID, int64, *filterdomain.ApplicationFilter, *filterdomain.ApplicationFilterRevision) (*filterdomain.ApplicationFilter, error)
}
