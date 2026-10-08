package config

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
)

func requiredValues() map[string]string {
	return map[string]string{
		MongoURIEnv:                  "mongodb://localhost:27017",
		IdentityIssuerEnv:            "https://auth.example.test",
		IdentityPublicKeysEnv:        "primary=/etc/iwut/identity-primary.pem",
		AuthScopeCatalogTargetEnv:    "127.0.0.1:9000",
		ServiceIdentityIDEnv:         "iwut-app-center",
		ServiceIdentityKIDEnv:        "app-center-1",
		ServiceIdentityPrivateKeyEnv: base64.StdEncoding.EncodeToString([]byte("PEM")),
		ServiceCallersEnv:            testServiceCallersValue("PUBLIC KEY"),
	}
}

func testServiceCallersValue(publicKey string) string {
	key := base64.StdEncoding.EncodeToString([]byte(publicKey))
	registry := `{"iwut-auth-center":{"status":"ACTIVE","keys":{"auth-1":{"publicKeyPemB64":"` + key + `"}},"permissions":["app.oauth.client.read","app.oauth.client.verify","app.oauth.runtime.resolve","app.oauth.context.resolve","app.oauth.redirects.read"]}}`
	return base64.StdEncoding.EncodeToString([]byte(registry))
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
	if configuration.LogLevel != DefaultLogLevel || configuration.OTLPGRPCEndpoint != "" || configuration.OTLPInsecure || configuration.TraceSampleRatio != DefaultTraceSampleRatio || configuration.MetricExportInterval != DefaultMetricExportInterval {
		t.Fatalf("observability defaults = %#v", configuration)
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
	if len(configuration.ServiceCallers) != 1 || configuration.ServiceCallers[0].ServiceID != "iwut-auth-center" || configuration.ServiceIdentityMaxTTL != DefaultServiceMaxTTL || configuration.ServiceIdentityClockSkew != DefaultServiceClockSkew {
		t.Fatalf("service caller configuration = %#v", configuration)
	}
	if configuration.ApplicationClosureEnabled {
		t.Fatal("ApplicationClosureEnabled = true, want default false")
	}
	if configuration.ApplicationOperationsEnabled {
		t.Fatal("ApplicationOperationsEnabled = true, want default false")
	}
}

func TestLoad_UsesExplicitValues(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		InitialApplicationQuotaEnv:      "27",
		ScopeCatalogCacheTTLEnv:         "90s",
		HTTPAddrEnv:                     "127.0.0.1:18080",
		GRPCAddrEnv:                     "127.0.0.1:19090",
		MongoURIEnv:                     "mongodb://db.internal:27017",
		MongoDatabaseEnv:                "app_center_test",
		IdentityIssuerEnv:               "https://issuer.test",
		IdentityAudienceEnv:             "iwut-app-center",
		IdentityMaxTTLEnv:               "2m",
		IdentityClockSkewEnv:            "15s",
		IdentityPublicKeysEnv:           "k1=/keys/one.pem,k2=/keys/two.pem",
		AuthScopeCatalogTargetEnv:       "dns:///auth-center.internal:9000",
		ServiceIdentityIDEnv:            "iwut-app-center-prod",
		ServiceIdentityKIDEnv:           "app-center-prod-1",
		ServiceIdentityAudienceEnv:      "auth-prod",
		ServiceIdentityPrivateKeyEnv:    base64.StdEncoding.EncodeToString([]byte("PEM-PROD")),
		ServiceIdentityTTLEnv:           "45s",
		ServiceCallersEnv:               testServiceCallersValue("PUBLIC KEY PROD"),
		ServiceMaxTTLEnv:                "50s",
		ServiceClockSkewEnv:             "12s",
		ApplicationClosureEnabledEnv:    "true",
		ApplicationOperationsEnabledEnv: "true",
		LogLevelEnv:                     "DEBUG",
		OTLPGRPCEndpointEnv:             "otel-collector.internal:4317",
		OTLPInsecureEnv:                 "true",
		TraceSampleRatioEnv:             "0.25",
		MetricExportIntervalEnv:         "15s",
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
	if configuration.AuthScopeCatalogTarget != "dns:///auth-center.internal:9000" {
		t.Fatalf("AuthScopeCatalogTarget = %q", configuration.AuthScopeCatalogTarget)
	}
	if configuration.ServiceIdentityID != "iwut-app-center-prod" || configuration.ServiceIdentityKID != "app-center-prod-1" ||
		configuration.ServiceIdentityAudience != "auth-prod" || configuration.ServiceIdentityTTL != 45*time.Second ||
		string(configuration.ServiceIdentityPrivateKeyPEM) != "PEM-PROD" {
		t.Fatalf("service identity = %#v", configuration)
	}
	if configuration.ServiceIdentityMaxTTL != 50*time.Second || configuration.ServiceIdentityClockSkew != 12*time.Second || len(configuration.ServiceCallers) != 1 {
		t.Fatalf("service caller settings = %#v", configuration)
	}
	if !configuration.ApplicationClosureEnabled {
		t.Fatal("ApplicationClosureEnabled = false, want true")
	}
	if !configuration.ApplicationOperationsEnabled {
		t.Fatal("ApplicationOperationsEnabled = false, want true")
	}
	if configuration.LogLevel != "DEBUG" || configuration.OTLPGRPCEndpoint != "otel-collector.internal:4317" || !configuration.OTLPInsecure || configuration.TraceSampleRatio != 0.25 || configuration.MetricExportInterval != 15*time.Second {
		t.Fatalf("observability settings = %#v", configuration)
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
		{name: "empty Auth Scope Catalog target", key: AuthScopeCatalogTargetEnv, value: ""},
		{name: "empty service identity id", key: ServiceIdentityIDEnv, value: ""},
		{name: "empty service identity kid", key: ServiceIdentityKIDEnv, value: ""},
		{name: "empty service identity private key", key: ServiceIdentityPrivateKeyEnv, value: ""},
		{name: "invalid service identity private key encoding", key: ServiceIdentityPrivateKeyEnv, value: "not-base64"},
		{name: "invalid service identity TTL", key: ServiceIdentityTTLEnv, value: "forever"},
		{name: "zero service identity TTL", key: ServiceIdentityTTLEnv, value: "0s"},
		{name: "empty caller registry", key: ServiceCallersEnv, value: ""},
		{name: "invalid caller registry encoding", key: ServiceCallersEnv, value: "not-base64"},
		{name: "zero service max TTL", key: ServiceMaxTTLEnv, value: "0s"},
		{name: "negative service clock skew", key: ServiceClockSkewEnv, value: "-1s"},
		{name: "invalid application closure flag", key: ApplicationClosureEnabledEnv, value: "TRUE"},
		{name: "invalid application operations flag", key: ApplicationOperationsEnabledEnv, value: "TRUE"},
		{name: "invalid log level", key: LogLevelEnv, value: "TRACE"},
		{name: "empty OTLP endpoint", key: OTLPGRPCEndpointEnv, value: ""},
		{name: "OTLP endpoint with scheme", key: OTLPGRPCEndpointEnv, value: "https://collector:4317"},
		{name: "OTLP endpoint without port", key: OTLPGRPCEndpointEnv, value: "collector"},
		{name: "OTLP endpoint with empty host", key: OTLPGRPCEndpointEnv, value: ":4317"},
		{name: "OTLP endpoint with nonnumeric port", key: OTLPGRPCEndpointEnv, value: "collector:otlp"},
		{name: "OTLP endpoint with zero port", key: OTLPGRPCEndpointEnv, value: "collector:0"},
		{name: "OTLP endpoint with out-of-range port", key: OTLPGRPCEndpointEnv, value: "collector:65536"},
		{name: "invalid OTLP insecure", key: OTLPInsecureEnv, value: "TRUE"},
		{name: "negative trace ratio", key: TraceSampleRatioEnv, value: "-0.1"},
		{name: "trace ratio above one", key: TraceSampleRatioEnv, value: "1.1"},
		{name: "short metric interval", key: MetricExportIntervalEnv, value: "500ms"},
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

func TestLoad_RequiresMongoURIIssuerKeysAndAuthTarget(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{MongoURIEnv, IdentityIssuerEnv, IdentityPublicKeysEnv, AuthScopeCatalogTargetEnv, ServiceIdentityIDEnv, ServiceIdentityKIDEnv, ServiceIdentityPrivateKeyEnv, ServiceCallersEnv} {
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

func TestParseServiceCallersRejectsNonCanonicalRegistry(t *testing.T) {
	t.Parallel()
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	key := base64.StdEncoding.EncodeToString([]byte("PUBLIC KEY"))
	for name, value := range map[string]string{
		"empty":                `{}`,
		"unknown field":        `{"iwut-auth-center":{"status":"ACTIVE","keys":{"k":{"publicKeyPemB64":"` + key + `"}},"permissions":[],"unexpected":true}}`,
		"identity audiences":   `{"iwut-auth-center":{"status":"ACTIVE","keys":{"k":{"publicKeyPemB64":"` + key + `"}},"permissions":[],"identityAudiences":["iwut-app-center"]}}`,
		"system purposes":      `{"iwut-auth-center":{"status":"ACTIVE","keys":{"k":{"publicKeyPemB64":"` + key + `"}},"permissions":[],"systemPrincipalPurposes":["purpose"]}}`,
		"duplicate permission": `{"iwut-auth-center":{"status":"ACTIVE","keys":{"k":{"publicKeyPemB64":"` + key + `"}},"permissions":["app.oauth.client.read","app.oauth.client.read"]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseServiceCallers(encode(value)); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("parseServiceCallers() error = %v", err)
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
