package main

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authadapter "iwut-app-center/internal/adapter/auth"
	"iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/preflight"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/config"
	oauthclientusecase "iwut-app-center/internal/oauthclient/usecase"
	reviewport "iwut-app-center/internal/review/port"
	reviewusecase "iwut-app-center/internal/review/usecase"
	versionport "iwut-app-center/internal/version/port"
	versionusecase "iwut-app-center/internal/version/usecase"
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

func provideScopeCatalogCacheTTL(configuration config.Config) time.Duration {
	return configuration.ScopeCatalogCacheTTL
}

func provideServiceIdentitySigner(configuration config.Config, clock port.Clock) (*authadapter.ServiceIdentitySigner, error) {
	return authadapter.NewServiceIdentitySigner(authadapter.ServiceIdentitySignerConfig{
		ServiceID:     configuration.ServiceIdentityID,
		KID:           configuration.ServiceIdentityKID,
		Audience:      configuration.ServiceIdentityAudience,
		TTL:           configuration.ServiceIdentityTTL,
		PrivateKeyPEM: configuration.ServiceIdentityPrivateKeyPEM,
		Clock:         clock,
	})
}

func provideAuthScopeCatalogConnection(configuration config.Config, signer *authadapter.ServiceIdentitySigner) (*grpc.ClientConn, func(), error) {
	connection, err := grpc.NewClient(
		configuration.AuthScopeCatalogTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(signer.UnaryClientInterceptor),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create Auth Scope Catalog gRPC client: %w", err)
	}
	cleanup := func() { _ = connection.Close() }
	return connection, cleanup, nil
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

func provideServiceIdentityConfig(configuration config.Config) transport.ServiceIdentityConfig {
	callers := make([]transport.ServiceCallerConfig, len(configuration.ServiceCallers))
	for index, caller := range configuration.ServiceCallers {
		callers[index] = transport.ServiceCallerConfig{
			ServiceID:         caller.ServiceID,
			Status:            caller.Status,
			PublicKeyPEMByKID: caller.PublicKeyPEMByKID,
			Permissions:       caller.Permissions,
		}
	}
	return transport.ServiceIdentityConfig{
		Callers:   callers,
		MaxTTL:    configuration.ServiceIdentityMaxTTL,
		ClockSkew: configuration.ServiceIdentityClockSkew,
	}
}

func provideServiceIdentityVerifier(configuration transport.ServiceIdentityConfig, clock port.Clock) (*transport.ServiceIdentityVerifier, error) {
	return transport.NewServiceIdentityVerifier(configuration, clock)
}

func provideServerConfig(configuration config.Config) transport.ServerConfig {
	return transport.ServerConfig{
		HTTPAddr:                     configuration.HTTPAddr,
		GRPCAddr:                     configuration.GRPCAddr,
		ApplicationClosureEnabled:    configuration.ApplicationClosureEnabled,
		ApplicationOperationsEnabled: configuration.ApplicationOperationsEnabled,
	}
}

func provideApp(servers *transport.Servers) *kratos.App {
	return kratos.New(
		kratos.Name("iwut-app-center"),
		kratos.Server(servers.HTTP, servers.GRPC),
	)
}

func wireApp(configuration config.Config) (*kratos.App, func(), error) {
	return wireAppWithResolver(configuration, preflight.NewNetResolver())
}

// ensure the concrete handler still satisfies the transport's narrow port.
var _ transport.CreateApplicationHandler = (*usecase.CreateApplicationHandler)(nil)
var _ transport.CreateApplicationVersionHandler = (*versionusecase.CreateApplicationVersionHandler)(nil)
var _ transport.UpdateDraftApplicationVersionHandler = (*versionusecase.UpdateDraftApplicationVersionHandler)(nil)
var _ transport.SubmitApplicationVersionReviewHandler = (*reviewusecase.SubmitApplicationVersionReviewHandler)(nil)
var _ transport.DecideApplicationVersionReviewHandler = (*reviewusecase.DecideApplicationVersionReviewHandler)(nil)
var _ transport.RestoreRejectedApplicationVersionHandler = (*reviewusecase.RestoreRejectedApplicationVersionHandler)(nil)
var _ authadapter.ScopeCatalogSnapshotSource = (*authadapter.GRPCScopeCatalogSnapshotSource)(nil)
var _ versionport.ScopeCatalog = (*authadapter.ScopeCatalogCache)(nil)
var _ reviewport.ScopeCatalog = (*authadapter.ReviewScopeCatalog)(nil)
var _ transport.OAuthClientHandlers = (*oauthclientusecase.Handlers)(nil)

func provideTesterJoinURLPrefix(configuration config.Config) string {
	return configuration.TesterJoinURLPrefix
}
