package auth

import (
	"github.com/goforj/wire"

	"iwut-app-center/internal/version/port"
)

var ProviderSet = wire.NewSet(
	NewGRPCScopeCatalogSnapshotSource,
	wire.Bind(new(ScopeCatalogSnapshotSource), new(*GRPCScopeCatalogSnapshotSource)),
	NewScopeCatalogCache,
	wire.Bind(new(port.ScopeCatalog), new(*ScopeCatalogCache)),
)
