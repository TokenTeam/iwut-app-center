package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/application/domain"
)

var (
	ErrApplicationNameAlreadyExists = errors.New("application name already exists")
	ErrApplicationQuotaExceeded     = errors.New("application creation quota exceeded")
)

type ApplicationIDGenerator interface {
	NewUUIDv7() (domain.ApplicationID, error)
}

type Clock interface {
	Now() time.Time
}

type ApplicationQuotaPolicy interface {
	LimitFor(ctx context.Context, adminID domain.AuthID) (int32, error)
}

// ApplicationRepository owns the complete atomic persistence operation. Its
// implementation must check name availability and quota, insert the
// Application, and consume quota in one transaction.
type ApplicationRepository interface {
	CreateWithinQuota(ctx context.Context, application *domain.Application, limit int32) error
}
