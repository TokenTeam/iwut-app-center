package port

import (
	"context"
	"errors"
	"time"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/shared"
)

var (
	ErrApplicationNotFound      = errors.New("application not found")
	ErrApplicationAdminRequired = errors.New("application administrator required")
	ErrRegistrationNotFound     = errors.New("oauth registration not found")
	ErrClientAlreadyExists      = errors.New("oauth client already exists")
	ErrClientNotFound           = errors.New("oauth client not found")
	ErrRegistrationChanged      = errors.New("oauth registration changed")
	ErrCredentialNotFound       = errors.New("oauth credential not found")
	ErrCredentialChanged        = errors.New("oauth credential changed")
	ErrStateInconsistent        = errors.New("oauth client state inconsistent")
)

type ClientIDGenerator interface {
	NewUUIDv4() (domain.ClientID, error)
}
type Clock interface{ Now() time.Time }
type SecretFactory interface {
	NewSecret(domain.ClientID) (plain string, digest domain.SecretDigest, err error)
}

type Repository interface {
	Register(context.Context, shared.ApplicationID, domain.Channel, domain.ClientType, *int64, domain.ClientID, *domain.SecretDigest, shared.AuthID, time.Time) (*domain.RegisterResult, error)
	GetRegistration(context.Context, shared.ApplicationID, domain.Channel, shared.AuthID) (*domain.Registration, error)
	SetStatus(context.Context, domain.ClientID, int64, domain.ClientStatus, shared.AuthID, time.Time) (*domain.StatusResult, error)
	GetCredential(context.Context, domain.ClientID, shared.AuthID) (*domain.Credential, error)
	RotateSecret(context.Context, domain.ClientID, int64, domain.SecretDigest, shared.AuthID, time.Time) (*domain.Credential, error)
}
