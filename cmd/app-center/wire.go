//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/goforj/wire"

	"iwut-app-center/internal/adapter/auth"
	"iwut-app-center/internal/adapter/generator"
	"iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/oauthcredential"
	"iwut-app-center/internal/adapter/preflight"
	"iwut-app-center/internal/adapter/testercredential"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/application/usecase"
	catalogusecase "iwut-app-center/internal/catalog/usecase"
	"iwut-app-center/internal/config"
	filterusecase "iwut-app-center/internal/filter/usecase"
	oauthclientusecase "iwut-app-center/internal/oauthclient/usecase"
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
		usecase.NewApplicationAdminTransferHandlers,
		wire.Bind(new(transport.ApplicationAdminTransferHandlers), new(*usecase.ApplicationAdminTransferHandlers)),
		filterusecase.NewHandlers,
		wire.Bind(new(transport.ApplicationFilterHandlers), new(*filterusecase.Handlers)),
		oauthcredential.ProviderSet,
		oauthclientusecase.NewHandlers,
		wire.Bind(new(transport.OAuthClientHandlers), new(*oauthclientusecase.Handlers)),
		oauthclientusecase.NewProviderHandlers,
		wire.Bind(new(transport.OAuthClientProviderHandlers), new(*oauthclientusecase.ProviderHandlers)),
		profileusecase.NewCreateApplicationProfileRevisionHandler,
		profileusecase.NewDecideApplicationProfileRevisionReviewHandler,
		wire.Bind(new(transport.DecideApplicationProfileRevisionReviewHandler), new(*profileusecase.DecideApplicationProfileRevisionReviewHandler)),
		profileusecase.NewSubmitApplicationProfileRevisionReviewHandler,
		wire.Bind(new(transport.SubmitApplicationProfileRevisionReviewHandler), new(*profileusecase.SubmitApplicationProfileRevisionReviewHandler)),
		profileusecase.NewUpdateDraftApplicationProfileRevisionHandler,
		wire.Bind(new(transport.UpdateDraftApplicationProfileRevisionHandler), new(*profileusecase.UpdateDraftApplicationProfileRevisionHandler)),
		wire.Bind(new(transport.CreateApplicationProfileRevisionHandler), new(*profileusecase.CreateApplicationProfileRevisionHandler)),
		catalogusecase.NewResolveTestLaunchTarget,
		wire.Bind(new(transport.ResolveTestLaunchTargetHandler), new(*catalogusecase.ResolveTestLaunchTarget)),
		catalogusecase.NewResolveLaunchTarget,
		wire.Bind(new(transport.ResolveLaunchTargetHandler), new(*catalogusecase.ResolveLaunchTarget)),
		catalogusecase.NewPublicCatalog,
		wire.Bind(new(transport.PublicCatalogHandler), new(*catalogusecase.PublicCatalog)),
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
		publicationusecase.NewSetApprovedVersionInStableSlotHandler,
		wire.Bind(new(transport.SetApprovedVersionInStableSlotHandler), new(*publicationusecase.SetApprovedVersionInStableSlotHandler)),
		publicationusecase.NewClearStableSlotHandler,
		wire.Bind(new(transport.ClearStableSlotHandler), new(*publicationusecase.ClearStableSlotHandler)),
		publicationusecase.NewSetGreyRolloutHandler,
		wire.Bind(new(transport.SetGreyRolloutHandler), new(*publicationusecase.SetGreyRolloutHandler)),
		publicationusecase.NewClearGreyRolloutHandler,
		wire.Bind(new(transport.ClearGreyRolloutHandler), new(*publicationusecase.ClearGreyRolloutHandler)),
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
		provideServiceIdentityConfig,
		provideServiceIdentityVerifier,
		provideServerConfig,
		provideAppWithOwnerExit,
		provideOwnerExitHandlers,
		provideOwnerExitWorker,
	))
}
