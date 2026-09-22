package preflight

import (
	"github.com/goforj/wire"
	publicationport "iwut-app-center/internal/publication/port"

	reviewport "iwut-app-center/internal/review/port"
)

var ProviderSet = wire.NewSet(
	NewPublicationLaunchURLSubmissionPolicy,
	wire.Bind(new(publicationport.LaunchURLSubmissionPolicy), new(*PublicationLaunchURLSubmissionPolicy)),
	NewLaunchURLSubmissionPolicy,
	wire.Bind(new(reviewport.LaunchURLSubmissionPolicy), new(*LaunchURLSubmissionPolicy)),
)
