//go:build wireinject
// +build wireinject

// The build tag makes sure the stub is not built in the final build.

package main

import (
	"iwut-app-center/internal/biz"
	"iwut-app-center/internal/conf"
	"iwut-app-center/internal/data"
	"iwut-app-center/internal/middleware"
	"iwut-app-center/internal/server"
	"iwut-app-center/internal/service"
	"iwut-app-center/internal/util"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

// wireApp init kratos application.
func wireApp(*conf.Server, *conf.Data, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, data.ProviderSet, biz.ProviderSet, service.ProviderSet, middleware.ProviderSet, util.ProviderSet, newApp))
}
