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

// ApplicationRepository owns the complete atomic persistence operation. Its
// implementation must initialize a missing quota with initialLimit, preserve
// the limit of an existing quota, check name availability and that quota's
// capacity, insert the Application, and consume quota in one transaction.
type ApplicationRepository interface {
	CreateWithinQuota(ctx context.Context, application *domain.Application, initialLimit int32) error
}
