package generator

import (
	"github.com/goforj/wire"

	"iwut-app-center/internal/application/port"
)

// ProviderSet binds the concrete generators to the narrow ports the
// Application use case consumes.
var ProviderSet = wire.NewSet(
	NewUUIDv7Generator,
	NewSystemClock,
	wire.Bind(new(port.ApplicationIDGenerator), new(*UUIDv7Generator)),
	wire.Bind(new(port.Clock), new(*SystemClock)),
)
