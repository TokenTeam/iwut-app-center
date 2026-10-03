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
	ErrRuntimeUnavailable       = errors.New("oauth client runtime unavailable")
	ErrRuntimeVersionChanged    = errors.New("oauth runtime version changed")
	ErrProfileStateInconsistent = errors.New("application profile state inconsistent")
)

type ClientIDGenerator interface {
	NewUUIDv4() (domain.ClientID, error)
}
type Clock interface{ Now() time.Time }
type SecretFactory interface {
	NewSecret(domain.ClientID) (plain string, digest domain.SecretDigest, err error)
}
type SecretVerifier interface {
	Verify(domain.ClientID, string, domain.SecretDigest) bool
}

type Repository interface {
	Register(context.Context, shared.ApplicationID, domain.Channel, domain.ClientType, *int64, domain.ClientID, *domain.SecretDigest, shared.AuthID, time.Time) (*domain.RegisterResult, error)
	GetRegistration(context.Context, shared.ApplicationID, domain.Channel, shared.AuthID) (*domain.Registration, error)
	SetStatus(context.Context, domain.ClientID, int64, domain.ClientStatus, shared.AuthID, time.Time) (*domain.StatusResult, error)
	GetCredential(context.Context, domain.ClientID, shared.AuthID) (*domain.Credential, error)
	RotateSecret(context.Context, domain.ClientID, int64, domain.SecretDigest, shared.AuthID, time.Time) (*domain.Credential, error)
}

type ProviderRepository interface {
	GetClientConfiguration(context.Context, domain.ClientID) (*domain.ClientConfiguration, error)
	VerifyClientSecret(context.Context, domain.ClientID, string, int64) (bool, int64, error)
	ResolveRuntime(context.Context, domain.ClientID, domain.Channel, int32, int64, time.Time) (*domain.RuntimeConfiguration, error)
	ResolveAuthorizationContext(context.Context, domain.ClientID, shared.AuthID, domain.Channel, int32, int64, domain.RuntimeVersion, time.Time) (*domain.AuthorizationContext, error)
	GetPublishedRedirects(context.Context, shared.ApplicationID, time.Time) (*domain.PublishedRedirectSnapshot, error)
}
