package usecase

import (
	"context"
	"errors"
	"math"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

type Handlers struct {
	ids        port.ClientIDGenerator
	secrets    port.SecretFactory
	clock      port.Clock
	repository port.Repository
}

func NewHandlers(ids port.ClientIDGenerator, secrets port.SecretFactory, clock port.Clock, repository port.Repository) *Handlers {
	return &Handlers{ids: ids, secrets: secrets, clock: clock, repository: repository}
}

func validateIdentity(identity shared.DeveloperIdentity) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return domain.ErrDeveloperApprovalRequired
	}
	return nil
}

func (h *Handlers) Register(ctx context.Context, identity shared.DeveloperIdentity, appID shared.ApplicationID, channel domain.Channel, typ domain.ClientType, expected *int64) (*domain.RegisterResult, string, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, "", err
	}
	if !appID.IsValid() {
		return nil, "", domain.ErrInvalidApplicationID
	}
	if !channel.Enabled() {
		return nil, "", domain.ErrOAuthChannelNotEnabled
	}
	if typ != domain.ClientTypePublicPKCE && typ != domain.ClientTypeConfidentialSecret {
		return nil, "", domain.ErrInvalidOAuthClientType
	}
	if expected != nil && *expected < 1 {
		return nil, "", domain.ErrInvalidRegistrationRevision
	}
	if h == nil || h.ids == nil || h.secrets == nil || h.clock == nil || h.repository == nil {
		return nil, "", domain.NewInternalError(nil)
	}
	id, err := h.ids.NewUUIDv4()
	if err != nil || !id.IsValid() {
		return nil, "", domain.NewInternalError(nil)
	}
	var plain string
	var digest *domain.SecretDigest
	if typ == domain.ClientTypeConfidentialSecret {
		value, hashed, secretErr := h.secrets.NewSecret(id)
		if secretErr != nil || value == "" {
			return nil, "", domain.NewInternalError(nil)
		}
		plain, digest = value, &hashed
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, "", domain.NewInternalError(nil)
	}
	result, err := h.repository.Register(ctx, appID, channel, typ, expected, id, digest, identity.AuthID, at)
	if err != nil {
		return nil, "", mapRepositoryError(err)
	}
	if result == nil || result.Registration == nil || result.Registration.ApplicationID() != appID || result.Registration.Channel() != channel || result.Registration.Client(typ) == nil || result.Registration.Client(typ).ClientID() != id || (typ == domain.ClientTypeConfidentialSecret) != (result.Credential != nil) {
		return nil, "", domain.ErrOAuthClientStateInconsistent
	}
	return result, plain, nil
}

func (h *Handlers) GetRegistration(ctx context.Context, identity shared.DeveloperIdentity, appID shared.ApplicationID, channel domain.Channel) (*domain.Registration, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if !appID.IsValid() {
		return nil, domain.ErrInvalidApplicationID
	}
	if !channel.Enabled() {
		return nil, domain.ErrOAuthChannelNotEnabled
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	registration, err := h.repository.GetRegistration(ctx, appID, channel, identity.AuthID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if registration == nil || registration.ApplicationID() != appID || registration.Channel() != channel {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return registration, nil
}

func (h *Handlers) SetStatus(ctx context.Context, identity shared.DeveloperIdentity, clientID domain.ClientID, expected int64, status domain.ClientStatus) (*domain.StatusResult, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if !clientID.IsValid() {
		return nil, domain.ErrInvalidOAuthClientID
	}
	if expected < 1 {
		return nil, domain.ErrInvalidRegistrationRevision
	}
	if status != domain.ClientStatusActive && status != domain.ClientStatusDisabled {
		return nil, domain.ErrInvalidOAuthClientStatus
	}
	if h == nil || h.clock == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.SetStatus(ctx, clientID, expected, status, identity.AuthID, at)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if result == nil || result.Registration == nil || result.Registration.ClientByID(clientID) == nil {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return result, nil
}

func (h *Handlers) GetCredential(ctx context.Context, identity shared.DeveloperIdentity, clientID domain.ClientID) (*domain.Credential, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, err
	}
	if !clientID.IsValid() {
		return nil, domain.ErrInvalidOAuthClientID
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	credential, err := h.repository.GetCredential(ctx, clientID, identity.AuthID)
	if err != nil {
		return nil, mapRepositoryError(err)
	}
	if credential == nil || credential.ClientID() != clientID {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return credential, nil
}

func (h *Handlers) RotateSecret(ctx context.Context, identity shared.DeveloperIdentity, clientID domain.ClientID, expected int64) (*domain.Credential, string, error) {
	if err := validateIdentity(identity); err != nil {
		return nil, "", err
	}
	if !clientID.IsValid() {
		return nil, "", domain.ErrInvalidOAuthClientID
	}
	if expected < 1 || expected == math.MaxInt64 {
		return nil, "", domain.ErrInvalidCredentialRevision
	}
	if h == nil || h.secrets == nil || h.clock == nil || h.repository == nil {
		return nil, "", domain.NewInternalError(nil)
	}
	plain, digest, err := h.secrets.NewSecret(clientID)
	if err != nil || plain == "" {
		return nil, "", domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, "", domain.NewInternalError(nil)
	}
	credential, err := h.repository.RotateSecret(ctx, clientID, expected, digest, identity.AuthID, at)
	if err != nil {
		return nil, "", mapRepositoryError(err)
	}
	if credential == nil || credential.ClientID() != clientID || credential.Revision() != expected+1 {
		return nil, "", domain.ErrOAuthClientStateInconsistent
	}
	return credential, plain, nil
}

func mapRepositoryError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound}, {port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrRegistrationNotFound, domain.ErrOAuthRegistrationNotFound}, {port.ErrClientAlreadyExists, domain.ErrOAuthClientAlreadyExists},
		{port.ErrClientNotFound, domain.ErrOAuthClientNotFound}, {port.ErrRegistrationChanged, domain.ErrOAuthRegistrationChanged},
		{port.ErrCredentialNotFound, domain.ErrOAuthCredentialNotFound}, {port.ErrCredentialChanged, domain.ErrOAuthCredentialChanged},
		{port.ErrStateInconsistent, domain.ErrOAuthClientStateInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	return domain.NewInternalError(nil)
}
