//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/goforj/wire"

	"iwut-app-center/internal/adapter/auth"
	"iwut-app-center/internal/adapter/generator"
	"iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/preflight"
	"iwut-app-center/internal/adapter/testercredential"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/application/usecase"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/config"
	profileusecase "iwut-app-center/internal/profile/usecase"
	publicationusecase "iwut-app-center/internal/publication/usecase"
	reviewusecase "iwut-app-center/internal/review/usecase"
	testerusecase "iwut-app-center/internal/tester/usecase"
	versionusecase "iwut-app-center/internal/version/usecase"
)

// wireApp is the composition root injector. Generate wire_gen.go with:
//
//	wire gen ./cmd/app-center
//
// Do not hand-edit the generated file.
func wireAppWithResolver(configuration config.Config, resolver preflight.Resolver) (*kratos.App, func(), error) {
	panic(wire.Build(
		generator.ProviderSet,
		profileusecase.NewCreateApplicationProfileRevisionHandler,
		profileusecase.NewSubmitApplicationProfileRevisionReviewHandler,
		wire.Bind(new(transport.SubmitApplicationProfileRevisionReviewHandler), new(*profileusecase.SubmitApplicationProfileRevisionReviewHandler)),
		profileusecase.NewUpdateDraftApplicationProfileRevisionHandler,
		wire.Bind(new(transport.UpdateDraftApplicationProfileRevisionHandler), new(*profileusecase.UpdateDraftApplicationProfileRevisionHandler)),
		wire.Bind(new(transport.CreateApplicationProfileRevisionHandler), new(*profileusecase.CreateApplicationProfileRevisionHandler)),
		catalogusecase.NewResolveTestLaunchTarget,
		wire.Bind(new(transport.ResolveTestLaunchTargetHandler), new(*catalogusecase.ResolveTestLaunchTarget)),
		testercredential.ProviderSet,
		provideTesterJoinURLPrefix,
		testerusecase.NewCreateOrRotateTesterJoinLinkHandler,
		testerusecase.NewJoinApplicationAsTesterHandler,
		testerusecase.NewRemoveApplicationTesterHandler,
		testerusecase.NewRevokeTesterJoinLinkHandler,
		wire.Bind(new(transport.RevokeTesterJoinLinkHandler), new(*testerusecase.RevokeTesterJoinLinkHandler)),
		wire.Bind(new(transport.RemoveApplicationTesterHandler), new(*testerusecase.RemoveApplicationTesterHandler)),
		wire.Bind(new(transport.JoinApplicationAsTesterHandler), new(*testerusecase.JoinApplicationAsTesterHandler)),
		wire.Bind(new(transport.CreateOrRotateTesterJoinLinkHandler), new(*testerusecase.CreateOrRotateTesterJoinLinkHandler)),
		auth.ProviderSet,
		mongo.ProviderSet,
		preflight.ProviderSet,
		transport.ProviderSet,
		usecase.NewCreateApplicationHandler,
		versionusecase.NewCreateApplicationVersionHandler,
		versionusecase.NewUpdateDraftApplicationVersionHandler,
		reviewusecase.NewSubmitApplicationVersionReviewHandler,
		publicationusecase.NewPlaceApprovedVersionInTestSlotHandler,
		wire.Bind(new(transport.PlaceApprovedVersionInTestSlotHandler), new(*publicationusecase.PlaceApprovedVersionInTestSlotHandler)),
		reviewusecase.NewDecideApplicationVersionReviewHandler,
		reviewusecase.NewRestoreRejectedApplicationVersionHandler,
		wire.Bind(new(transport.CreateApplicationHandler), new(*usecase.CreateApplicationHandler)),
		wire.Bind(new(transport.CreateApplicationVersionHandler), new(*versionusecase.CreateApplicationVersionHandler)),
		wire.Bind(new(transport.UpdateDraftApplicationVersionHandler), new(*versionusecase.UpdateDraftApplicationVersionHandler)),
		wire.Bind(new(transport.SubmitApplicationVersionReviewHandler), new(*reviewusecase.SubmitApplicationVersionReviewHandler)),
		wire.Bind(new(transport.DecideApplicationVersionReviewHandler), new(*reviewusecase.DecideApplicationVersionReviewHandler)),
		wire.Bind(new(transport.RestoreRejectedApplicationVersionHandler), new(*reviewusecase.RestoreRejectedApplicationVersionHandler)),
		provideMongoClient,
		provideMongoDatabase,
		provideInitialApplicationQuota,
		provideScopeCatalogCacheTTL,
		provideServiceIdentitySigner,
		provideAuthScopeCatalogConnection,
		provideIdentityConfig,
		provideServerConfig,
		provideApp,
	))
}
