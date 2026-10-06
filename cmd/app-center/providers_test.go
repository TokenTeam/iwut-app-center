package main

import (
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/config"
)

func validConfig() config.Config {
	return config.Config{
		InitialApplicationQuota: 10,
		TesterJoinURLPrefix:     config.DefaultTesterJoinURLPrefix,
		ScopeCatalogCacheTTL:    5 * time.Minute,
		HTTPAddr:                "127.0.0.1:0",
		GRPCAddr:                "127.0.0.1:0",
		MongoURI:                "mongodb://localhost:27017",
		MongoDatabase:           "app_center_test",
		IdentityIssuer:          "https://issuer.test",
		IdentityAudience:        "iwut-app-center",
		IdentityMaxTTL:          5 * time.Minute,
		IdentityClockSkew:       30 * time.Second,
		IdentityPublicKeyFiles:  map[string]string{"primary": "/nonexistent/identity.pem"},
		AuthScopeCatalogTarget:  "127.0.0.1:9000",
	}
}

func TestWireApp_FailsClosedOnInvalidIdentityKeyFile(t *testing.T) {
	t.Parallel()

	app, cleanup, err := wireApp(validConfig())
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatal("wireApp() error = nil, want startup failure for a missing public key file")
	}
	if app != nil {
		t.Fatalf("wireApp() app = %v, want nil on failure", app)
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wireApp() error = %v, want an identity configuration failure", err)
	}
}

func TestProvideServerConfigMapsAddresses(t *testing.T) {
	t.Parallel()

	configuration := validConfig()
	configuration.ApplicationClosureEnabled = true
	configuration.ApplicationOperationsEnabled = true
	serverConfig := provideServerConfig(configuration)
	if serverConfig.HTTPAddr != "127.0.0.1:0" || serverConfig.GRPCAddr != "127.0.0.1:0" {
		t.Fatalf("server config = %#v", serverConfig)
	}
	if !serverConfig.ApplicationClosureEnabled {
		t.Fatalf("server config = %#v, want application closure enabled", serverConfig)
	}
	if !serverConfig.ApplicationOperationsEnabled {
		t.Fatalf("server config = %#v, want application operations enabled", serverConfig)
	}
}

func TestProvideInitialApplicationQuota(t *testing.T) {
	t.Parallel()

	if quota := provideInitialApplicationQuota(validConfig()); quota != 10 {
		t.Fatalf("quota = %d, want 10", quota)
	}
}

// TestRunMigrate_RequiresOnlyMongoConfiguration proves the migrate command
// reaches Mongo configuration without any identity/server settings. It fails
// at configuration load, so it never opens a connection.
func TestRunMigrate_RequiresOnlyMongoConfiguration(t *testing.T) {
	t.Setenv(config.MongoURIEnv, "")
	t.Setenv(config.IdentityIssuerEnv, "")
	t.Setenv(config.IdentityPublicKeysEnv, "")

	err := runMigrate()
	if err == nil {
		t.Fatal("runMigrate() error = nil, want a missing-Mongo-URI configuration error")
	}
	if !strings.Contains(err.Error(), config.MongoURIEnv) {
		t.Fatalf("runMigrate() error = %v, want it to name %s", err, config.MongoURIEnv)
	}
	if strings.Contains(err.Error(), config.IdentityIssuerEnv) {
		t.Fatalf("runMigrate() error = %v, must not require identity configuration", err)
	}
}
