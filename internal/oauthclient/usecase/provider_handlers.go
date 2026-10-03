package usecase

import (
	"context"
	"errors"
	"math"
	"time"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

type ProviderHandlers struct {
	clock      port.Clock
	repository port.ProviderRepository
}

func NewProviderHandlers(clock port.Clock, repository port.ProviderRepository) *ProviderHandlers {
	return &ProviderHandlers{clock: clock, repository: repository}
}

func (h *ProviderHandlers) GetClientConfiguration(ctx context.Context, clientID domain.ClientID) (*domain.ClientConfiguration, error) {
	if !clientID.IsValid() {
		return nil, domain.ErrInvalidOAuthClientID
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.GetClientConfiguration(ctx, clientID)
	if err != nil {
		return nil, mapProviderRepositoryError(err)
	}
	if result == nil || result.ClientID != clientID {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return result, nil
}

func (h *ProviderHandlers) VerifyClientSecret(ctx context.Context, clientID domain.ClientID, secret string, expected int64) (bool, int64, error) {
	if !clientID.IsValid() {
		return false, 0, domain.ErrInvalidOAuthClientID
	}
	if expected < 1 || expected == math.MaxInt64 || len(secret) > 256 {
		return false, 0, domain.ErrInvalidOAuthProviderRequest
	}
	if h == nil || h.repository == nil {
		return false, 0, domain.NewInternalError(nil)
	}
	verified, revision, err := h.repository.VerifyClientSecret(ctx, clientID, secret, expected)
	if err != nil {
		return false, 0, mapProviderRepositoryError(err)
	}
	if revision < 0 {
		return false, 0, domain.ErrOAuthClientStateInconsistent
	}
	return verified, revision, nil
}

func (h *ProviderHandlers) ResolveRuntime(ctx context.Context, clientID domain.ClientID, channel domain.Channel, major int32, expected int64) (*domain.RuntimeConfiguration, error) {
	if !clientID.IsValid() || channel != domain.ChannelTest || major < 1 || expected < 1 {
		return nil, domain.ErrInvalidOAuthProviderRequest
	}
	at, err := h.observedAt()
	if err != nil {
		return nil, err
	}
	result, err := h.repository.ResolveRuntime(ctx, clientID, channel, major, expected, at)
	if err != nil {
		return nil, mapProviderRepositoryError(err)
	}
	if result == nil || result.ClientID != clientID || result.Channel != channel || result.RPCAPIMajor != major || result.RegistrationRevision != expected || !result.ObservedAt.Equal(at) || result.ValidUntil.Sub(result.ObservedAt) > domain.ProviderSnapshotValidity {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return result, nil
}

func (h *ProviderHandlers) ResolveAuthorizationContext(ctx context.Context, clientID domain.ClientID, authID shared.AuthID, channel domain.Channel, major int32, expected int64, version domain.RuntimeVersion) (*domain.AuthorizationContext, error) {
	if !clientID.IsValid() || !authID.IsValid() || channel != domain.ChannelTest || major < 1 || expected < 1 || !version.IsValid() {
		return nil, domain.ErrInvalidOAuthProviderRequest
	}
	at, err := h.observedAt()
	if err != nil {
		return nil, err
	}
	result, err := h.repository.ResolveAuthorizationContext(ctx, clientID, authID, channel, major, expected, version, at)
	if err != nil {
		return nil, mapProviderRepositoryError(err)
	}
	if result == nil || result.Runtime == nil || result.AuthID != authID || result.Runtime.ClientID != clientID || !result.Runtime.Version().Equal(version) || !result.Runtime.ObservedAt.Equal(at) {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return result, nil
}

func (h *ProviderHandlers) GetPublishedRedirects(ctx context.Context, applicationID shared.ApplicationID) (*domain.PublishedRedirectSnapshot, error) {
	if !applicationID.IsValid() {
		return nil, domain.ErrInvalidApplicationID
	}
	at, err := h.observedAt()
	if err != nil {
		return nil, err
	}
	result, err := h.repository.GetPublishedRedirects(ctx, applicationID, at)
	if err != nil {
		return nil, mapProviderRepositoryError(err)
	}
	if result == nil || result.ApplicationID != applicationID || !result.ObservedAt.Equal(at) || result.ValidUntil.Sub(result.ObservedAt) > domain.ProviderSnapshotValidity {
		return nil, domain.ErrOAuthClientStateInconsistent
	}
	return result, nil
}

func (h *ProviderHandlers) observedAt() (time.Time, error) {
	if h == nil || h.clock == nil || h.repository == nil {
		return time.Time{}, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return time.Time{}, domain.NewInternalError(nil)
	}
	return at, nil
}

func mapProviderRepositoryError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{port.ErrClientNotFound, domain.ErrOAuthClientNotFound},
		{port.ErrStateInconsistent, domain.ErrOAuthClientStateInconsistent},
		{port.ErrRuntimeUnavailable, domain.ErrOAuthClientRuntimeUnavailable},
		{port.ErrRuntimeVersionChanged, domain.ErrOAuthRuntimeVersionChanged},
		{port.ErrProfileStateInconsistent, domain.ErrApplicationProfileStateInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	return domain.ErrOAuthProviderUnavailable
}
