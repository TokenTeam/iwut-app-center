package auth

import (
	"github.com/goforj/wire"
	publicationport "iwut-app-center/internal/publication/port"

	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/version/port"
)

var ProviderSet = wire.NewSet(
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
