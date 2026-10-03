package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

type fakeProviderRepository struct {
	configuration *domain.ClientConfiguration
	runtime       *domain.RuntimeConfiguration
	context       *domain.AuthorizationContext
	redirects     *domain.PublishedRedirectSnapshot
	verified      bool
	revision      int64
	err           error
	calls         int
}

func (f *fakeProviderRepository) GetClientConfiguration(context.Context, domain.ClientID) (*domain.ClientConfiguration, error) {
	f.calls++
	return f.configuration, f.err
}
func (f *fakeProviderRepository) VerifyClientSecret(context.Context, domain.ClientID, string, int64) (bool, int64, error) {
	f.calls++
	return f.verified, f.revision, f.err
}
func (f *fakeProviderRepository) ResolveRuntime(context.Context, domain.ClientID, domain.Channel, int32, int64, time.Time) (*domain.RuntimeConfiguration, error) {
	f.calls++
	return f.runtime, f.err
}
func (f *fakeProviderRepository) ResolveAuthorizationContext(context.Context, domain.ClientID, shared.AuthID, domain.Channel, int32, int64, domain.RuntimeVersion, time.Time) (*domain.AuthorizationContext, error) {
	f.calls++
	return f.context, f.err
}
func (f *fakeProviderRepository) GetPublishedRedirects(context.Context, shared.ApplicationID, time.Time) (*domain.PublishedRedirectSnapshot, error) {
	f.calls++
	return f.redirects, f.err
}

func providerFixtures(t *testing.T) (*domain.ClientConfiguration, *domain.RuntimeConfiguration, *domain.AuthorizationContext, *domain.PublishedRedirectSnapshot) {
	t.Helper()
	registration, _ := oauthFixtures(t, domain.ClientTypePublicPKCE)
	clientID, _ := domain.ParseClientID(testClientID)
	configuration, err := domain.NewClientConfiguration(registration, clientID, nil)
	if err != nil {
		t.Fatal(err)
	}
	display, err := domain.NewApplicationDisplay("01890f47-0000-7000-8000-000000000020", "Calendar", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := domain.NewRuntimeConfiguration(configuration, 1, "admin", "01890f47-0000-7000-8000-000000000021", 2, []string{"https://example.test/callback"}, []string{"profile.basic"}, []string{"schedule.read"}, display, testNow)
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := domain.NewAuthorizationContext(runtime, "tester", "01890f47-0000-7000-8000-000000000022")
	if err != nil {
		t.Fatal(err)
	}
	redirects, err := domain.NewPublishedRedirectSnapshot(configuration.ApplicationID, []domain.PublishedRedirectEntry{{Channel: domain.ChannelTest, RPCAPIMajor: 1, VersionID: runtime.VersionID, PublicationRevision: 2}}, runtime.RedirectURIs, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return configuration, runtime, authorization, redirects
}

func TestProviderHandlersReturnValidatedFiveSecondSnapshots(t *testing.T) {
	configuration, runtime, authorization, redirects := providerFixtures(t)
	repository := &fakeProviderRepository{configuration: configuration, runtime: runtime, context: authorization, redirects: redirects, verified: true, revision: 3}
	handler := NewProviderHandlers(fakeClock{at: testNow}, repository)
	clientID, _ := domain.ParseClientID(testClientID)

	if got, err := handler.GetClientConfiguration(t.Context(), clientID); err != nil || got != configuration {
		t.Fatalf("GetClientConfiguration() = (%v, %v)", got, err)
	}
	if verified, revision, err := handler.VerifyClientSecret(t.Context(), clientID, "secret", 3); err != nil || !verified || revision != 3 {
		t.Fatalf("VerifyClientSecret() = (%t, %d, %v)", verified, revision, err)
	}
	if got, err := handler.ResolveRuntime(t.Context(), clientID, domain.ChannelTest, 1, 1); err != nil || got != runtime || got.ValidUntil.Sub(got.ObservedAt) != 5*time.Second {
		t.Fatalf("ResolveRuntime() = (%v, %v)", got, err)
	}
	if got, err := handler.ResolveAuthorizationContext(t.Context(), clientID, "tester", domain.ChannelTest, 1, 1, runtime.Version()); err != nil || got != authorization {
		t.Fatalf("ResolveAuthorizationContext() = (%v, %v)", got, err)
	}
	if got, err := handler.GetPublishedRedirects(t.Context(), configuration.ApplicationID); err != nil || got != redirects {
		t.Fatalf("GetPublishedRedirects() = (%v, %v)", got, err)
	}
}

func TestProviderHandlersRejectUntrustedInputBeforeRepository(t *testing.T) {
	repository := &fakeProviderRepository{}
	handler := NewProviderHandlers(fakeClock{at: testNow}, repository)
	clientID, _ := domain.ParseClientID(testClientID)
	version := domain.RuntimeVersion{VersionID: "01890f47-0000-7000-8000-000000000021", PublicationRevision: 1, ProfileRevisionID: "01890f47-0000-7000-8000-000000000020", AdminAuthID: "admin"}

	if _, err := handler.GetClientConfiguration(t.Context(), "invalid"); !errors.Is(err, domain.ErrInvalidOAuthClientID) {
		t.Fatalf("invalid client error = %v", err)
	}
	if _, _, err := handler.VerifyClientSecret(t.Context(), clientID, "secret", 0); !errors.Is(err, domain.ErrInvalidOAuthProviderRequest) {
		t.Fatalf("invalid verify error = %v", err)
	}
	if _, err := handler.ResolveRuntime(t.Context(), clientID, domain.Channel("OTHER"), 1, 1); !errors.Is(err, domain.ErrInvalidOAuthProviderRequest) {
		t.Fatalf("invalid runtime error = %v", err)
	}
	if _, err := handler.ResolveAuthorizationContext(t.Context(), clientID, "", domain.ChannelTest, 1, 1, version); !errors.Is(err, domain.ErrInvalidOAuthProviderRequest) {
		t.Fatalf("invalid context error = %v", err)
	}
	if _, err := handler.GetPublishedRedirects(t.Context(), "invalid"); !errors.Is(err, domain.ErrInvalidApplicationID) {
		t.Fatalf("invalid redirects error = %v", err)
	}
	if repository.calls != 0 {
		t.Fatalf("invalid inputs reached repository %d times", repository.calls)
	}
}

func TestProviderHandlersMapRepositoryFailures(t *testing.T) {
	clientID, _ := domain.ParseClientID(testClientID)
	for _, test := range []struct {
		source error
		want   error
	}{
		{port.ErrRuntimeUnavailable, domain.ErrOAuthClientRuntimeUnavailable},
		{port.ErrRuntimeVersionChanged, domain.ErrOAuthRuntimeVersionChanged},
		{port.ErrProfileStateInconsistent, domain.ErrApplicationProfileStateInconsistent},
		{errors.New("private storage detail"), domain.ErrOAuthProviderUnavailable},
	} {
		t.Run(test.source.Error(), func(t *testing.T) {
			handler := NewProviderHandlers(fakeClock{at: testNow}, &fakeProviderRepository{err: test.source})
			if _, err := handler.ResolveRuntime(t.Context(), clientID, domain.ChannelTest, 1, 1); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
