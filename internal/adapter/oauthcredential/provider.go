package oauthcredential

import (
	"github.com/goforj/wire"
	"iwut-app-center/internal/oauthclient/port"
)

var ProviderSet = wire.NewSet(
	NewSecretFactory,
	wire.Bind(new(port.SecretFactory), new(*SecretFactory)),
	wire.Bind(new(port.SecretVerifier), new(*SecretFactory)),
)
