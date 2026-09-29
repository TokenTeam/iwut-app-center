package preflight

import (
	"github.com/goforj/wire"
	publicationport "iwut-app-center/internal/publication/port"
	versionport "iwut-app-center/internal/version/port"

	reviewport "iwut-app-center/internal/review/port"
)

var ProviderSet = wire.NewSet(
	NewOAuthRedirectPolicy,
	wire.Bind(new(versionport.OAuthRedirectPolicy), new(*OAuthRedirectPolicy)),
	wire.Bind(new(reviewport.OAuthRedirectPolicy), new(*OAuthRedirectPolicy)),
	NewPublicationLaunchURLSubmissionPolicy,
	wire.Bind(new(publicationport.LaunchURLSubmissionPolicy), new(*PublicationLaunchURLSubmissionPolicy)),
	NewLaunchURLSubmissionPolicy,
	wire.Bind(new(reviewport.LaunchURLSubmissionPolicy), new(*LaunchURLSubmissionPolicy)),
)
