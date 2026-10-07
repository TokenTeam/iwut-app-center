package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/management/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrInvalidPageToken  = errors.New("invalid application management page token")
	ErrNotFound          = errors.New("application management resource not found")
	ErrStateInconsistent = errors.New("application management state inconsistent")
)

type Repository interface {
	List(context.Context, shared.AuthID, []string, []string, int32, string) (*domain.Page, error)
	Get(context.Context, shared.AuthID, shared.ApplicationID) (*domain.Detail, error)
}
