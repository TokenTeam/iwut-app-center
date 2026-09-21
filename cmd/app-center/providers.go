package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	"iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/config"
)

const (
	mongoShutdownTimeout  = 5 * time.Second
	mongoReadinessTimeout = 10 * time.Second
)

func provideMongoClient(configuration config.Config) (*drivermongo.Client, func(), error) {
	client, err := mongo.NewClient(configuration.MongoURI)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), mongoShutdownTimeout)
		defer cancel()
		_ = client.Disconnect(ctx)
	}
	return client, cleanup, nil
}

// provideMongoDatabase selects the database and fail-fast verifies the
// deployment is ready to serve. Verification is read-only: it requires a
// transaction-capable topology and the latest recorded migration, but never
// runs migrations itself.
func provideMongoDatabase(client *drivermongo.Client, configuration config.Config) (*drivermongo.Database, error) {
	database, err := mongo.NewDatabase(client, configuration.MongoDatabase)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), mongoReadinessTimeout)
	defer cancel()
	if err := mongo.VerifyDeploymentReadiness(ctx, database); err != nil {
		return nil, fmt.Errorf("mongo deployment not ready: %w", err)
	}
	return database, nil
}

func provideInitialApplicationQuota(configuration config.Config) int32 {
	return configuration.InitialApplicationQuota
}

func provideIdentityConfig(configuration config.Config, clock port.Clock) (transport.IdentityConfig, error) {
	publicKeys, err := transport.LoadRSAPublicKeys(configuration.IdentityPublicKeyFiles)
	if err != nil {
		return transport.IdentityConfig{}, err
	}
	return transport.IdentityConfig{
		Issuer:     configuration.IdentityIssuer,
		Audience:   configuration.IdentityAudience,
		MaxTTL:     configuration.IdentityMaxTTL,
		ClockSkew:  configuration.IdentityClockSkew,
		PublicKeys: publicKeys,
		Clock:      clock,
	}, nil
}

func provideServerConfig(configuration config.Config) transport.ServerConfig {
	return transport.ServerConfig{
		HTTPAddr: configuration.HTTPAddr,
		GRPCAddr: configuration.GRPCAddr,
	}
}

func provideApp(servers *transport.Servers) *kratos.App {
	return kratos.New(
		kratos.Name("iwut-app-center"),
		kratos.Server(servers.HTTP, servers.GRPC),
	)
}

// ensure the concrete handler still satisfies the transport's narrow port.
var _ transport.CreateApplicationHandler = (*usecase.CreateApplicationHandler)(nil)
