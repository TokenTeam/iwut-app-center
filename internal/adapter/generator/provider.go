package generator

import (
	"github.com/goforj/wire"

	"iwut-app-center/internal/application/port"
	reviewport "iwut-app-center/internal/review/port"
	versionport "iwut-app-center/internal/version/port"
)

// ProviderSet binds the concrete generators to the narrow ports the
// Application use case consumes.
var ProviderSet = wire.NewSet(
	NewUUIDv7Generator,
	NewApplicationVersionUUIDv7Generator,
	NewApplicationReviewUUIDv7Generator,
	NewSystemClock,
	wire.Bind(new(port.ApplicationIDGenerator), new(*UUIDv7Generator)),
	wire.Bind(new(port.Clock), new(*SystemClock)),
	wire.Bind(new(versionport.ApplicationVersionIDGenerator), new(*ApplicationVersionUUIDv7Generator)),
	wire.Bind(new(versionport.Clock), new(*SystemClock)),
	wire.Bind(new(reviewport.ApplicationReviewIDGenerator), new(*ApplicationReviewUUIDv7Generator)),
	wire.Bind(new(reviewport.Clock), new(*SystemClock)),
)
