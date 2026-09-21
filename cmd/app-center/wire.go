//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/goforj/wire"

	"iwut-app-center/internal/adapter/auth"
	"iwut-app-center/internal/adapter/generator"
	"iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/config"
	versionusecase "iwut-app-center/internal/version/usecase"
)

// wireApp is the composition root injector. Generate wire_gen.go with:
//
//	wire gen ./cmd/app-center
//
// Do not hand-edit the generated file.
func wireApp(configuration config.Config) (*kratos.App, func(), error) {
	panic(wire.Build(
		generator.ProviderSet,
		auth.ProviderSet,
		mongo.ProviderSet,
		transport.ProviderSet,
		usecase.NewCreateApplicationHandler,
		versionusecase.NewCreateApplicationVersionHandler,
		versionusecase.NewUpdateDraftApplicationVersionHandler,
		wire.Bind(new(transport.CreateApplicationHandler), new(*usecase.CreateApplicationHandler)),
		wire.Bind(new(transport.CreateApplicationVersionHandler), new(*versionusecase.CreateApplicationVersionHandler)),
		wire.Bind(new(transport.UpdateDraftApplicationVersionHandler), new(*versionusecase.UpdateDraftApplicationVersionHandler)),
		provideMongoClient,
		provideMongoDatabase,
		provideInitialApplicationQuota,
		provideScopeCatalogCacheTTL,
		provideAuthScopeCatalogConnection,
		provideIdentityConfig,
		provideServerConfig,
		provideApp,
	))
}
