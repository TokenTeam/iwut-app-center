package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"iwut-app-center/internal/application/domain"
)

const (
	TesterJoinURLPrefixEnv       = "APP_CENTER_TESTER_JOIN_URL_PREFIX"
	InitialApplicationQuotaEnv   = "APP_CENTER_INITIAL_APPLICATION_QUOTA"
	ScopeCatalogCacheTTLEnv      = "APP_CENTER_SCOPE_CATALOG_CACHE_TTL"
	HTTPAddrEnv                  = "APP_CENTER_HTTP_ADDR"
	GRPCAddrEnv                  = "APP_CENTER_GRPC_ADDR"
	MongoURIEnv                  = "APP_CENTER_MONGO_URI"
	MongoDatabaseEnv             = "APP_CENTER_MONGO_DATABASE"
	IdentityIssuerEnv            = "APP_CENTER_IDENTITY_ISSUER"
	IdentityAudienceEnv          = "APP_CENTER_IDENTITY_AUDIENCE"
	IdentityMaxTTLEnv            = "APP_CENTER_IDENTITY_MAX_TTL"
	IdentityClockSkewEnv         = "APP_CENTER_IDENTITY_CLOCK_SKEW"
	IdentityPublicKeysEnv        = "APP_CENTER_IDENTITY_PUBLIC_KEYS"
	AuthScopeCatalogTargetEnv    = "APP_CENTER_AUTH_SCOPE_CATALOG_GRPC_TARGET"
	ServiceIdentityIDEnv         = "APP_CENTER_SERVICE_IDENTITY_ID"
	ServiceIdentityKIDEnv        = "APP_CENTER_SERVICE_IDENTITY_KID"
	ServiceIdentityAudienceEnv   = "APP_CENTER_SERVICE_IDENTITY_AUDIENCE"
	ServiceIdentityPrivateKeyEnv = "APP_CENTER_SERVICE_IDENTITY_PRIVATE_KEY_PEM_B64"
	ServiceIdentityTTLEnv        = "APP_CENTER_SERVICE_IDENTITY_TTL"

	DefaultTesterJoinURLPrefix     = "https://app.example/tester/join"
	DefaultScopeCatalogCacheTTL    = 5 * time.Minute
	DefaultHTTPAddr                = ":8080"
	DefaultGRPCAddr                = ":9090"
	DefaultMongoDatabase           = "iwut_app_center"
	DefaultIdentityAudience        = "iwut-app-center"
	DefaultIdentityMaxTTL          = 5 * time.Minute
	DefaultIdentityClockSkew       = 30 * time.Second
	DefaultServiceIdentityAudience = "iwut-auth-center"
	DefaultServiceIdentityTTL      = time.Minute
)

var ErrInvalidConfiguration = errors.New("invalid application configuration")

type LookupEnv func(key string) (value string, found bool)

// Config is validated once at the composition boundary. UseCases and adapters
// receive the scalar values they need through constructors and never read the
// environment themselves.
type Config struct {
	TesterJoinURLPrefix     string
	InitialApplicationQuota int32
	ScopeCatalogCacheTTL    time.Duration

	HTTPAddr string
	GRPCAddr string

	MongoURI      string
	MongoDatabase string

	IdentityIssuer         string
	IdentityAudience       string
	IdentityMaxTTL         time.Duration
	IdentityClockSkew      time.Duration
	IdentityPublicKeyFiles map[string]string

	AuthScopeCatalogTarget       string
	ServiceIdentityID            string
	ServiceIdentityKID           string
	ServiceIdentityAudience      string
	ServiceIdentityPrivateKeyPEM []byte
	ServiceIdentityTTL           time.Duration
}

func LoadFromEnvironment() (Config, error) {
	return Load(os.LookupEnv)
}

// MongoConfig is the subset of configuration the standalone migration command
// needs. The server and the migrator both load it through LoadMongo so the
// environment parsing lives in exactly one place.
type MongoConfig struct {
	URI      string
	Database string
}

func LoadMongoFromEnvironment() (MongoConfig, error) {
	return LoadMongo(os.LookupEnv)
}

func LoadMongo(lookup LookupEnv) (MongoConfig, error) {
	if lookup == nil {
		return MongoConfig{}, fmt.Errorf("%w: environment lookup is nil", ErrInvalidConfiguration)
	}

	database, err := optionalNonEmpty(lookup, MongoDatabaseEnv, DefaultMongoDatabase)
	if err != nil {
		return MongoConfig{}, err
	}
	uri, err := requiredValue(lookup, MongoURIEnv)
	if err != nil {
		return MongoConfig{}, err
	}
	return MongoConfig{URI: uri, Database: database}, nil
}

func Load(lookup LookupEnv) (Config, error) {
	if lookup == nil {
		return Config{}, fmt.Errorf("%w: environment lookup is nil", ErrInvalidConfiguration)
	}

	configuration := Config{
		TesterJoinURLPrefix:     DefaultTesterJoinURLPrefix,
		InitialApplicationQuota: domain.InitialDeveloperApplicationQuotaLimit,
		ScopeCatalogCacheTTL:    DefaultScopeCatalogCacheTTL,
		HTTPAddr:                DefaultHTTPAddr,
		GRPCAddr:                DefaultGRPCAddr,
		MongoDatabase:           DefaultMongoDatabase,
		IdentityAudience:        DefaultIdentityAudience,
		IdentityMaxTTL:          DefaultIdentityMaxTTL,
		IdentityClockSkew:       DefaultIdentityClockSkew,
		ServiceIdentityAudience: DefaultServiceIdentityAudience,
		ServiceIdentityTTL:      DefaultServiceIdentityTTL,
	}

	if raw, found := lookup(TesterJoinURLPrefixEnv); found {
		if !validTesterJoinURLPrefix(raw) {
			return Config{}, fmt.Errorf("%w: %s must be an absolute HTTP(S) URL with host and no userinfo, query, fragment or whitespace", ErrInvalidConfiguration, TesterJoinURLPrefixEnv)
		}
		configuration.TesterJoinURLPrefix = raw
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

	var err error
	if configuration.HTTPAddr, err = optionalNonEmpty(lookup, HTTPAddrEnv, DefaultHTTPAddr); err != nil {
		return Config{}, err
	}
	if configuration.GRPCAddr, err = optionalNonEmpty(lookup, GRPCAddrEnv, DefaultGRPCAddr); err != nil {
		return Config{}, err
	}
	mongoConfiguration, mongoErr := LoadMongo(lookup)
	if mongoErr != nil {
		return Config{}, mongoErr
	}
	configuration.MongoURI = mongoConfiguration.URI
	configuration.MongoDatabase = mongoConfiguration.Database
	if configuration.IdentityIssuer, err = requiredValue(lookup, IdentityIssuerEnv); err != nil {
		return Config{}, err
	}
	if configuration.IdentityAudience, err = optionalNonEmpty(lookup, IdentityAudienceEnv, DefaultIdentityAudience); err != nil {
		return Config{}, err
	}
	if configuration.AuthScopeCatalogTarget, err = requiredValue(lookup, AuthScopeCatalogTargetEnv); err != nil {
		return Config{}, err
	}
	if configuration.ServiceIdentityID, err = requiredValue(lookup, ServiceIdentityIDEnv); err != nil {
		return Config{}, err
	}
	if configuration.ServiceIdentityKID, err = requiredValue(lookup, ServiceIdentityKIDEnv); err != nil {
		return Config{}, err
	}
	if configuration.ServiceIdentityAudience, err = optionalNonEmpty(lookup, ServiceIdentityAudienceEnv, DefaultServiceIdentityAudience); err != nil {
		return Config{}, err
	}
	rawPrivateKey, err := requiredValue(lookup, ServiceIdentityPrivateKeyEnv)
	if err != nil {
		return Config{}, err
	}
	configuration.ServiceIdentityPrivateKeyPEM, err = base64.StdEncoding.Strict().DecodeString(rawPrivateKey)
	if err != nil || len(configuration.ServiceIdentityPrivateKeyPEM) == 0 {
		return Config{}, fmt.Errorf("%w: %s must be strict standard Base64 containing PEM", ErrInvalidConfiguration, ServiceIdentityPrivateKeyEnv)
	}
	if raw, found := lookup(ServiceIdentityTTLEnv); found {
		configuration.ServiceIdentityTTL, err = time.ParseDuration(strings.TrimSpace(raw))
		if err != nil || configuration.ServiceIdentityTTL <= 0 {
			return Config{}, fmt.Errorf("%w: %s must be a positive Go duration", ErrInvalidConfiguration, ServiceIdentityTTLEnv)
		}
	}

	if raw, found := lookup(IdentityMaxTTLEnv); found {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return Config{}, fmt.Errorf("%w: %s must be a positive Go duration", ErrInvalidConfiguration, IdentityMaxTTLEnv)
		}
		configuration.IdentityMaxTTL = parsed
	}

	if raw, found := lookup(IdentityClockSkewEnv); found {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed < 0 {
			return Config{}, fmt.Errorf("%w: %s must be a non-negative Go duration", ErrInvalidConfiguration, IdentityClockSkewEnv)
		}
		configuration.IdentityClockSkew = parsed
	}

	rawKeys, err := requiredValue(lookup, IdentityPublicKeysEnv)
	if err != nil {
		return Config{}, err
	}
	publicKeyFiles, err := ParsePublicKeyFiles(rawKeys)
	if err != nil {
		return Config{}, err
	}
	configuration.IdentityPublicKeyFiles = publicKeyFiles

	return configuration, nil
}

func requiredValue(lookup LookupEnv, key string) (string, error) {
	raw, found := lookup(key)
	if !found || strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: %s is required and must not be empty", ErrInvalidConfiguration, key)
	}
	return strings.TrimSpace(raw), nil
}

func optionalNonEmpty(lookup LookupEnv, key string, fallback string) (string, error) {
	raw, found := lookup(key)
	if !found {
		return fallback, nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%w: %s must not be empty when set", ErrInvalidConfiguration, key)
	}
	return trimmed, nil
}

// ParsePublicKeyFiles parses `kid=path` entries separated by commas. Each kid
// must be unique and map to a non-empty PEM file path; the verifier still has
// to parse the file content.
func ParsePublicKeyFiles(raw string) (map[string]string, error) {
	files := make(map[string]string)
	for _, entry := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			return nil, fmt.Errorf("%w: %s contains an empty entry", ErrInvalidConfiguration, IdentityPublicKeysEnv)
		}
		kid, path, ok := strings.Cut(trimmed, "=")
		kid = strings.TrimSpace(kid)
		path = strings.TrimSpace(path)
		if !ok || kid == "" || path == "" {
			return nil, fmt.Errorf("%w: %s entry %q must be `kid=path`", ErrInvalidConfiguration, IdentityPublicKeysEnv, trimmed)
		}
		if _, exists := files[kid]; exists {
			return nil, fmt.Errorf("%w: %s declares kid %q more than once", ErrInvalidConfiguration, IdentityPublicKeysEnv, kid)
		}
		files[kid] = path
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%w: %s must declare at least one kid=path", ErrInvalidConfiguration, IdentityPublicKeysEnv)
	}
	return files, nil
}

func validTesterJoinURLPrefix(prefix string) bool {
	if prefix == "" || strings.TrimSpace(prefix) != prefix || strings.ContainsAny(prefix, "?#") || strings.ContainsFunc(prefix, unicode.IsSpace) {
		return false
	}
	parsed, err := url.Parse(prefix)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Hostname() != "" && parsed.User == nil && parsed.Opaque == "" && parsed.RawQuery == "" && !parsed.ForceQuery && parsed.Fragment == ""
}
