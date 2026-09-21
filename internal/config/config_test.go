package config

import (
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
)

func requiredValues() map[string]string {
	return map[string]string{
		MongoURIEnv:           "mongodb://localhost:27017",
		IdentityIssuerEnv:     "https://auth.example.test",
		IdentityPublicKeysEnv: "primary=/etc/iwut/identity-primary.pem",
	}
}

func lookupFrom(values map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		value, found := values[key]
		return value, found
	}
}

func TestLoad_DefaultsWhenOptionalVariablesAreMissing(t *testing.T) {
	t.Parallel()

	configuration, err := Load(lookupFrom(requiredValues()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.InitialApplicationQuota != domain.InitialDeveloperApplicationQuotaLimit {
		t.Fatalf("InitialApplicationQuota = %d, want %d", configuration.InitialApplicationQuota, domain.InitialDeveloperApplicationQuotaLimit)
	}
	if configuration.ScopeCatalogCacheTTL != DefaultScopeCatalogCacheTTL {
		t.Fatalf("ScopeCatalogCacheTTL = %v, want %v", configuration.ScopeCatalogCacheTTL, DefaultScopeCatalogCacheTTL)
	}
	if configuration.HTTPAddr != DefaultHTTPAddr || configuration.GRPCAddr != DefaultGRPCAddr {
		t.Fatalf("addresses = %q/%q, want %q/%q", configuration.HTTPAddr, configuration.GRPCAddr, DefaultHTTPAddr, DefaultGRPCAddr)
	}
	if configuration.MongoDatabase != DefaultMongoDatabase {
		t.Fatalf("MongoDatabase = %q, want %q", configuration.MongoDatabase, DefaultMongoDatabase)
	}
	if configuration.IdentityAudience != DefaultIdentityAudience {
		t.Fatalf("IdentityAudience = %q, want %q", configuration.IdentityAudience, DefaultIdentityAudience)
	}
	if configuration.IdentityMaxTTL != DefaultIdentityMaxTTL {
		t.Fatalf("IdentityMaxTTL = %v, want %v", configuration.IdentityMaxTTL, DefaultIdentityMaxTTL)
	}
	if configuration.IdentityClockSkew != DefaultIdentityClockSkew {
		t.Fatalf("IdentityClockSkew = %v, want %v", configuration.IdentityClockSkew, DefaultIdentityClockSkew)
	}
	if got := configuration.IdentityPublicKeyFiles["primary"]; got != "/etc/iwut/identity-primary.pem" {
		t.Fatalf("IdentityPublicKeyFiles[primary] = %q", got)
	}
}

func TestLoad_UsesExplicitValues(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		InitialApplicationQuotaEnv: "27",
		ScopeCatalogCacheTTLEnv:    "90s",
		HTTPAddrEnv:                "127.0.0.1:18080",
		GRPCAddrEnv:                "127.0.0.1:19090",
		MongoURIEnv:                "mongodb://db.internal:27017",
		MongoDatabaseEnv:           "app_center_test",
		IdentityIssuerEnv:          "https://issuer.test",
		IdentityAudienceEnv:        "iwut-app-center",
		IdentityMaxTTLEnv:          "2m",
		IdentityClockSkewEnv:       "15s",
		IdentityPublicKeysEnv:      "k1=/keys/one.pem,k2=/keys/two.pem",
	}
	configuration, err := Load(lookupFrom(values))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.InitialApplicationQuota != 27 || configuration.ScopeCatalogCacheTTL != 90*time.Second {
		t.Fatalf("Load() = %#v, want quota 27 and TTL 90s", configuration)
	}
	if configuration.HTTPAddr != "127.0.0.1:18080" || configuration.GRPCAddr != "127.0.0.1:19090" {
		t.Fatalf("addresses = %q/%q", configuration.HTTPAddr, configuration.GRPCAddr)
	}
	if configuration.MongoURI != "mongodb://db.internal:27017" || configuration.MongoDatabase != "app_center_test" {
		t.Fatalf("mongo = %q/%q", configuration.MongoURI, configuration.MongoDatabase)
	}
	if configuration.IdentityIssuer != "https://issuer.test" ||
		configuration.IdentityMaxTTL != 2*time.Minute ||
		configuration.IdentityClockSkew != 15*time.Second {
		t.Fatalf("identity = %#v", configuration)
	}
	if len(configuration.IdentityPublicKeyFiles) != 2 {
		t.Fatalf("IdentityPublicKeyFiles = %#v", configuration.IdentityPublicKeyFiles)
	}
}

func TestLoad_AllowsZeroInitialApplicationQuota(t *testing.T) {
	t.Parallel()

	values := requiredValues()
	values[InitialApplicationQuotaEnv] = "0"
	configuration, err := Load(lookupFrom(values))
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
		{name: "empty HTTP address", key: HTTPAddrEnv, value: ""},
		{name: "empty gRPC address", key: GRPCAddrEnv, value: "  "},
		{name: "empty mongo URI", key: MongoURIEnv, value: ""},
		{name: "empty mongo database", key: MongoDatabaseEnv, value: ""},
		{name: "empty issuer", key: IdentityIssuerEnv, value: ""},
		{name: "empty audience", key: IdentityAudienceEnv, value: ""},
		{name: "zero identity max TTL", key: IdentityMaxTTLEnv, value: "0s"},
		{name: "negative identity max TTL", key: IdentityMaxTTLEnv, value: "-1s"},
		{name: "invalid identity max TTL", key: IdentityMaxTTLEnv, value: "forever"},
		{name: "negative clock skew", key: IdentityClockSkewEnv, value: "-1s"},
		{name: "empty clock skew", key: IdentityClockSkewEnv, value: ""},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			values := requiredValues()
			values[testCase.key] = testCase.value
			_, err := Load(lookupFrom(values))
			if !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("Load() error = %v, want InvalidConfiguration", err)
			}
		})
	}
}

func TestLoad_RequiresMongoURIAndIssuerAndKeys(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{MongoURIEnv, IdentityIssuerEnv, IdentityPublicKeysEnv} {
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			values := requiredValues()
			delete(values, missing)
			if _, err := Load(lookupFrom(values)); !errors.Is(err, ErrInvalidConfiguration) {
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

func TestParsePublicKeyFiles(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		got, err := ParsePublicKeyFiles("a=/a.pem, b=/b.pem")
		if err != nil {
			t.Fatalf("ParsePublicKeyFiles() error = %v", err)
		}
		if got["a"] != "/a.pem" || got["b"] != "/b.pem" {
			t.Fatalf("got %#v", got)
		}
	})

	for _, invalid := range []string{"", "nope", "=path", "kid=", "a=/a,a=/b", "a=/a,,b=/b"} {
		t.Run("invalid:"+invalid, func(t *testing.T) {
			if _, err := ParsePublicKeyFiles(invalid); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("ParsePublicKeyFiles(%q) error = %v, want InvalidConfiguration", invalid, err)
			}
		})
	}
}

func TestLoadMongo_OnlyNeedsMongoVariables(t *testing.T) {
	t.Parallel()

	t.Run("defaults database", func(t *testing.T) {
		t.Parallel()
		configuration, err := LoadMongo(lookupFrom(map[string]string{MongoURIEnv: "mongodb://localhost:27017"}))
		if err != nil {
			t.Fatalf("LoadMongo() error = %v", err)
		}
		if configuration.URI != "mongodb://localhost:27017" || configuration.Database != DefaultMongoDatabase {
			t.Fatalf("LoadMongo() = %#v", configuration)
		}
	})

	t.Run("uses explicit database", func(t *testing.T) {
		t.Parallel()
		configuration, err := LoadMongo(lookupFrom(map[string]string{
			MongoURIEnv:      "mongodb://db.internal:27017",
			MongoDatabaseEnv: "app_center_test",
		}))
		if err != nil {
			t.Fatalf("LoadMongo() error = %v", err)
		}
		if configuration.URI != "mongodb://db.internal:27017" || configuration.Database != "app_center_test" {
			t.Fatalf("LoadMongo() = %#v", configuration)
		}
	})

	t.Run("requires URI and rejects empty database", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadMongo(lookupFrom(map[string]string{})); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("LoadMongo() error = %v, want InvalidConfiguration", err)
		}
		if _, err := LoadMongo(lookupFrom(map[string]string{MongoURIEnv: "mongodb://x", MongoDatabaseEnv: " "})); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("LoadMongo() error = %v, want InvalidConfiguration", err)
		}
	})

	t.Run("rejects nil lookup", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadMongo(nil); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("LoadMongo(nil) error = %v, want InvalidConfiguration", err)
		}
	})
}
