package port

import (
	"context"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"time"
)

type PlatformApplicationQueryRepository interface {
	SearchPlatformApplications(context.Context, shared.AuthID, domain.PlatformApplicationQuery, time.Time) (domain.PlatformApplicationPage, error)
	GetPlatformApplicationSummary(context.Context, shared.ApplicationID) (domain.PlatformApplicationSummary, error)
}
