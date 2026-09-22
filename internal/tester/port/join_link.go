package port

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"time"
)

var (
	ErrApplicationNotFound                    = errors.New("application not found")
	ErrApplicationAdminRequired               = errors.New("application administrator required")
	ErrApplicationTesterJoinLinkAlreadyExists = errors.New("active tester join link already exists")
	ErrApplicationTesterJoinLinkNotFound      = errors.New("active tester join link not found")
	ErrApplicationTesterJoinLinkChanged       = errors.New("active tester join link changed")
)

type UUIDv7Generator interface{ NewUUIDv7() (string, error) }
type Clock interface{ Now() time.Time }
type SecureTesterJoinTokenFactory interface {
	NewToken() (rawToken string, tokenHash [32]byte, err error)
}
type TesterJoinURLBuilder interface {
	Build(domain.ApplicationTesterJoinLinkID, string) (string, error)
}
type ApplicationTesterJoinLinkRepository interface {
	LoadCurrent(context.Context, shared.ApplicationID, shared.AuthID) (*domain.TesterJoinLinkCandidate, error)
	CreateOrRotate(context.Context, shared.ApplicationID, shared.AuthID, *domain.ApplicationTesterJoinLinkID, *domain.ApplicationTesterJoinLink) (*domain.CreateOrRotateTesterJoinLinkResult, error)
}
