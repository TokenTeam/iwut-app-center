package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrProfileManagementNotFound          = errors.New("profile management resource not found")
	ErrInvalidProfileManagementPageToken  = errors.New("invalid profile management page token")
	ErrProfileManagementStateInconsistent = errors.New("profile management state inconsistent")
	ErrProfileManagementUnavailable       = errors.New("profile management unavailable")
	ErrProfileManagementResponseTooLarge  = errors.New("profile management response too large")
)

type ManagementQueryRepository interface {
	List(context.Context, shared.AuthID, shared.ApplicationID, *domain.ReviewStatus, int32, string) (*domain.ManagedProfilePage, error)
	Get(context.Context, shared.AuthID, shared.ApplicationID, domain.ApplicationProfileRevisionID, int32, string) (*domain.ManagedProfileDetail, error)
}

// ManagementResponseSizer measures both public wire representations. Its
// implementation is a pure in-process conversion, with no external calls.
type ManagementResponseSizer interface {
	ListSize(*domain.ManagedProfilePage) (int, error)
	DetailSize(*domain.ManagedProfileDetail) (int, error)
}
