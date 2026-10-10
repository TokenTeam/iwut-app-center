package port

import (
	"context"
	"errors"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
)

var (
	ErrInvalidManagementPageToken  = errors.New("invalid application version management page token")
	ErrManagementNotFound          = errors.New("application version management resource not found")
	ErrManagementStateInconsistent = errors.New("application version management state inconsistent")
	ErrManagementUnavailable       = errors.New("application version management unavailable")
)

type ManagementQueryRepository interface {
	List(context.Context, shared.AuthID, shared.ApplicationID, *domain.ReviewStatus, int32, string) (*domain.OwnedVersionPage, error)
	Get(context.Context, shared.AuthID, shared.ApplicationID, domain.ApplicationVersionID) (*domain.OwnedVersionDetail, error)
}
