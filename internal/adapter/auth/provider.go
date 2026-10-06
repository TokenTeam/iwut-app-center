package auth

import (
	"github.com/goforj/wire"
	applicationport "iwut-app-center/internal/application/port"
	publicationport "iwut-app-center/internal/publication/port"

	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/version/port"
)

var ProviderSet = wire.NewSet(
	NewGRPCApplicationClosure,
	wire.Bind(new(applicationport.AuthApplicationClosure), new(*GRPCApplicationClosure)),
	NewGRPCDeveloperLifecycleDirectory,
	wire.Bind(new(applicationport.DeveloperLifecycleDirectory), new(*GRPCDeveloperLifecycleDirectory)),
	NewPublicationScopeCatalog,
	wire.Bind(new(publicationport.ScopeCatalog), new(*PublicationScopeCatalog)),
	NewGRPCScopeCatalogSnapshotSource,
	wire.Bind(new(ScopeCatalogSnapshotSource), new(*GRPCScopeCatalogSnapshotSource)),
	NewScopeCatalogCache,
	wire.Bind(new(port.ScopeCatalog), new(*ScopeCatalogCache)),
	NewReviewScopeCatalog,
	wire.Bind(new(reviewport.ScopeCatalog), new(*ReviewScopeCatalog)),
	NewGRPCDeveloperApprovalChecker,
	wire.Bind(new(reviewport.DeveloperApprovalChecker), new(*GRPCDeveloperApprovalChecker)),
	NewGRPCSystemPrincipalResolver,
	wire.Bind(new(reviewport.SystemPrincipalResolver), new(*GRPCSystemPrincipalResolver)),
)
