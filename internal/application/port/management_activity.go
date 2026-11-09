package port

import (
	"context"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"time"
)

type ManagementActivityRepository interface {
	ListManagementActivity(context.Context, shared.AuthID, shared.ApplicationID, domain.ManagementActivityQuery, time.Time) (domain.ManagementActivityPage, error)
	GetManagementActivity(context.Context, shared.AuthID, shared.ApplicationID, domain.ManagementActivityLocator, time.Time) (domain.ManagementActivityDetail, error)
}
