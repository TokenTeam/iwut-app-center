package generator

import (
	"github.com/goforj/wire"
	profileport "iwut-app-center/internal/profile/port"
	publicationport "iwut-app-center/internal/publication/port"
	testerport "iwut-app-center/internal/tester/port"

	"iwut-app-center/internal/application/port"
	reviewport "iwut-app-center/internal/review/port"
	versionport "iwut-app-center/internal/version/port"
)

// ProviderSet binds the concrete generators to the narrow ports the
// Application use case consumes.
var ProviderSet = wire.NewSet(
	NewApplicationProfileRevisionUUIDv7Generator,
	NewApplicationProfileReviewUUIDv7Generator,
	wire.Bind(new(profileport.ApplicationProfileReviewIDGenerator), new(*ApplicationProfileReviewUUIDv7Generator)),
	wire.Bind(new(profileport.ApplicationProfileRevisionIDGenerator), new(*ApplicationProfileRevisionUUIDv7Generator)),
	wire.Bind(new(profileport.Clock), new(*SystemClock)),
	wire.Bind(new(testerport.UUIDv7Generator), new(*PublicationUUIDv7Generator)),
	wire.Bind(new(testerport.Clock), new(*SystemClock)),
	NewPublicationUUIDv7Generator,
	wire.Bind(new(publicationport.UUIDv7Generator), new(*PublicationUUIDv7Generator)),
	wire.Bind(new(publicationport.Clock), new(*SystemClock)),
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
