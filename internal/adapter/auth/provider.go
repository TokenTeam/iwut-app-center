package auth

import (
	"github.com/goforj/wire"

	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/version/port"
)

var ProviderSet = wire.NewSet(
	NewGRPCScopeCatalogSnapshotSource,
	wire.Bind(new(ScopeCatalogSnapshotSource), new(*GRPCScopeCatalogSnapshotSource)),
	NewScopeCatalogCache,
	wire.Bind(new(port.ScopeCatalog), new(*ScopeCatalogCache)),
	NewReviewScopeCatalog,
	wire.Bind(new(reviewport.ScopeCatalog), new(*ReviewScopeCatalog)),
	NewGRPCDeveloperSuspensionChecker,
	wire.Bind(new(reviewport.DeveloperSuspensionChecker), new(*GRPCDeveloperSuspensionChecker)),
)
