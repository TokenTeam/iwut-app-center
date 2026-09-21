package mongo

import (
	"github.com/goforj/wire"

	"iwut-app-center/internal/application/port"
)

// ProviderSet binds the transactional Application repository to its port. The
// database and client providers live in the composition root because they need
// validated configuration.
var ProviderSet = wire.NewSet(
	NewApplicationRepository,
	wire.Bind(new(port.ApplicationRepository), new(*ApplicationRepository)),
)
