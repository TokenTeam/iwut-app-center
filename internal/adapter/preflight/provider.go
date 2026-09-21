package preflight

import (
	"github.com/goforj/wire"

	reviewport "iwut-app-center/internal/review/port"
)

var ProviderSet = wire.NewSet(
	NewLaunchURLSubmissionPolicy,
	wire.Bind(new(reviewport.LaunchURLSubmissionPolicy), new(*LaunchURLSubmissionPolicy)),
)
