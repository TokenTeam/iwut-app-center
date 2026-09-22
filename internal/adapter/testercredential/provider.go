package testercredential

import (
	"github.com/goforj/wire"
	testerport "iwut-app-center/internal/tester/port"
)

var ProviderSet = wire.NewSet(NewSecureTesterJoinTokenFactory, NewTesterJoinURLBuilder, NewTokenHasher, wire.Bind(new(testerport.SecureTesterJoinTokenFactory), new(*SecureTesterJoinTokenFactory)), wire.Bind(new(testerport.TesterJoinTokenHasher), new(*TokenHasher)), wire.Bind(new(testerport.TesterJoinURLBuilder), new(*TesterJoinURLBuilder)))
