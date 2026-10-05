package port

import (
	"context"
	"iwut-app-center/internal/ownerexit/domain"
	"time"
)

type Repository interface {
	Prepare(context.Context, domain.Prepare, string, time.Time) (domain.Preparation, error)
	Finish(context.Context, domain.Finish, time.Time) (domain.Status, error)
	Get(context.Context, domain.Key) (domain.Status, error)
	Due(context.Context, time.Time, int) ([]domain.Work, error)
	CleanupBatch(context.Context, domain.Key, int) error
	Retry(context.Context, domain.Key, time.Time, int) error
}
type Decisions interface {
	Get(context.Context, domain.Key) (domain.Status, error)
}
type Clock interface{ Now() time.Time }
type IDs interface{ NewReceiptID() (string, error) }
