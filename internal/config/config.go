package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"iwut-app-center/internal/application/domain"
)

const (
	InitialApplicationQuotaEnv = "APP_CENTER_INITIAL_APPLICATION_QUOTA"
	ScopeCatalogCacheTTLEnv    = "APP_CENTER_SCOPE_CATALOG_CACHE_TTL"

	DefaultScopeCatalogCacheTTL = 5 * time.Minute
)

var ErrInvalidConfiguration = errors.New("invalid application configuration")

type LookupEnv func(key string) (value string, found bool)

type Config struct {
	InitialApplicationQuota int32
	ScopeCatalogCacheTTL    time.Duration
}

func LoadFromEnvironment() (Config, error) {
	return Load(os.LookupEnv)
}

func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("%w: environment lookup is nil", ErrInvalidConfiguration)
	}

	configuration := Config{
		InitialApplicationQuota: domain.InitialDeveloperApplicationQuotaLimit,
		ScopeCatalogCacheTTL:    DefaultScopeCatalogCacheTTL,
	}

	if raw, found := lookup(InitialApplicationQuotaEnv); found {
		parsed, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || parsed < 0 {
			return Config{}, fmt.Errorf("%w: %s must be a decimal int32 greater than or equal to zero", ErrInvalidConfiguration, InitialApplicationQuotaEnv)
		}
		configuration.InitialApplicationQuota = int32(parsed)
	}

	if raw, found := lookup(ScopeCatalogCacheTTLEnv); found {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%w: %s must be a positive Go duration", ErrInvalidConfiguration, ScopeCatalogCacheTTLEnv)
		}
		configuration.ScopeCatalogCacheTTL = parsed
	}

	return configuration, nil
}
