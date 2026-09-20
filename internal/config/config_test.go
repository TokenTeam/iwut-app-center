package config

import (
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
)

func TestLoad_DefaultsWhenVariablesAreMissing(t *testing.T) {
	t.Parallel()

	configuration, err := Load(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.InitialApplicationQuota != domain.InitialDeveloperApplicationQuotaLimit {
		t.Fatalf("InitialApplicationQuota = %d, want %d", configuration.InitialApplicationQuota, domain.InitialDeveloperApplicationQuotaLimit)
	}
	if configuration.ScopeCatalogCacheTTL != 5*time.Minute {
		t.Fatalf("ScopeCatalogCacheTTL = %v, want 5m", configuration.ScopeCatalogCacheTTL)
	}
}

func TestLoad_UsesExplicitValues(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		InitialApplicationQuotaEnv: "27",
		ScopeCatalogCacheTTLEnv:    "90s",
	}
	configuration, err := Load(func(key string) (string, bool) {
		value, found := values[key]
		return value, found
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.InitialApplicationQuota != 27 || configuration.ScopeCatalogCacheTTL != 90*time.Second {
		t.Fatalf("Load() = %#v, want quota 27 and TTL 90s", configuration)
	}
}

func TestLoad_AllowsZeroInitialApplicationQuota(t *testing.T) {
	t.Parallel()

	configuration, err := Load(func(key string) (string, bool) {
		if key == InitialApplicationQuotaEnv {
			return "0", true
		}
		return "", false
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.InitialApplicationQuota != 0 {
		t.Fatalf("InitialApplicationQuota = %d, want 0", configuration.InitialApplicationQuota)
	}
}

func TestLoad_RejectsExplicitInvalidValues(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		key   string
		value string
	}{
		{name: "empty quota", key: InitialApplicationQuotaEnv, value: ""},
		{name: "non-decimal quota", key: InitialApplicationQuotaEnv, value: "ten"},
		{name: "negative quota", key: InitialApplicationQuotaEnv, value: "-1"},
		{name: "quota int32 overflow", key: InitialApplicationQuotaEnv, value: "2147483648"},
		{name: "empty TTL", key: ScopeCatalogCacheTTLEnv, value: ""},
		{name: "invalid TTL", key: ScopeCatalogCacheTTLEnv, value: "five minutes"},
		{name: "zero TTL", key: ScopeCatalogCacheTTLEnv, value: "0s"},
		{name: "negative TTL", key: ScopeCatalogCacheTTLEnv, value: "-1s"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(func(key string) (string, bool) {
				if key == testCase.key {
					return testCase.value, true
				}
				return "", false
			})
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("Load() error = %v, want InvalidConfiguration", err)
			}
		})
	}
}

func TestLoad_RejectsNilLookup(t *testing.T) {
	t.Parallel()

	_, err := Load(nil)
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Load(nil) error = %v, want InvalidConfiguration", err)
	}
}
