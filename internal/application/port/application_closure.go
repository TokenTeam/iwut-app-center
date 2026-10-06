package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrAuthApplicationClosureNotFound = errors.New("Auth application closure not found")
	ErrAuthApplicationClosureConflict = errors.New("Auth application closure binding conflict")
)

type ApplicationClosureIDGenerator interface {
	NewUUIDv7() (domain.ApplicationClosureID, error)
}

type ApplicationClosureRepository interface {
	Preview(context.Context, shared.ApplicationID, shared.AuthID, time.Time) (domain.ApplicationClosurePreview, error)
	Get(context.Context, shared.ApplicationID, shared.AuthID) (domain.ApplicationClosure, error)
	Start(context.Context, shared.ApplicationID, shared.AuthID, int64, int64, domain.ApplicationCloseProof, domain.ApplicationClosureID, time.Time) (domain.ApplicationClosure, error)
	NextPending(context.Context, time.Time) (domain.ApplicationClosure, error)
	ScheduleRetry(context.Context, domain.ApplicationClosureID, time.Time) error
	Complete(context.Context, domain.ApplicationClosureID, string, time.Time) (domain.ApplicationClosure, error)
}

type AuthApplicationClosureReceipt struct {
	ReceiptID string
	AppliedAt time.Time
}
type AuthApplicationClosure interface {
	Apply(context.Context, domain.ApplicationClosure) (AuthApplicationClosureReceipt, error)
	Get(context.Context, domain.ApplicationClosure) (AuthApplicationClosureReceipt, error)
}
