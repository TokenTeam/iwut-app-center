//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/goforj/wire"

	"iwut-app-center/internal/adapter/generator"
	"iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/config"
)

// wireApp is the composition root injector. Generate wire_gen.go with:
//
//	wire gen ./cmd/app-center
//
// Do not hand-edit the generated file.
func wireApp(configuration config.Config) (*kratos.App, func(), error) {
	panic(wire.Build(
		generator.ProviderSet,
		mongo.ProviderSet,
		transport.ProviderSet,
		usecase.NewCreateApplicationHandler,
		wire.Bind(new(transport.CreateApplicationHandler), new(*usecase.CreateApplicationHandler)),
		provideMongoClient,
		provideMongoDatabase,
		provideInitialApplicationQuota,
		provideIdentityConfig,
		provideServerConfig,
		provideApp,
	))
}
