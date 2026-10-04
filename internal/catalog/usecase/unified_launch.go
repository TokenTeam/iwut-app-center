package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

type ResolveLaunchTargetQuery struct {
	ApplicationID    string
	HostRPCAPIMajor  int32
	HostCapabilities []string
}

type ResolveLaunchTarget struct{ resolver port.LaunchTargetResolver }

func NewResolveLaunchTarget(resolver port.LaunchTargetResolver) *ResolveLaunchTarget {
	return &ResolveLaunchTarget{resolver: resolver}
}

// Execute accepts a zero AuthID as the explicit anonymous identity projection.
// Present-but-invalid credentials are rejected by transport before this call.
func (handler *ResolveLaunchTarget) Execute(ctx context.Context, identity shared.AuthenticatedUserIdentity, query ResolveLaunchTargetQuery) (*domain.LaunchTargetDescriptor, error) {
	applicationID, ok := shared.ParseApplicationID(query.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidApplicationID
	}
	if query.HostRPCAPIMajor < 1 {
		return nil, domain.ErrInvalidHostRPCAPIMajor
	}
	capabilities, err := domain.NormalizeHostCapabilities(query.HostCapabilities)
	if err != nil {
		return nil, err
	}
	if handler == nil || handler.resolver == nil {
		return nil, domain.NewInternalError(nil)
	}
	result, err := handler.resolver.Resolve(ctx, applicationID, identity.AuthID, query.HostRPCAPIMajor, capabilities)
	if err != nil {
		return nil, mapUnifiedResolverError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	if result.ApplicationID() != applicationID || result.RPCAPIMajor() != query.HostRPCAPIMajor || !result.Channel().IsValid() || len(domain.MissingCapabilities(result.RequiredCapabilities(), capabilities)) != 0 {
		return nil, domain.ErrApplicationRuntimeStateInconsistent
	}
	return result, nil
}

func mapUnifiedResolverError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{port.ErrApplicationLaunchTargetUnavailable, domain.ErrApplicationLaunchTargetUnavailable},
		{port.ErrApplicationRuntimeStateInconsistent, domain.ErrApplicationRuntimeStateInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	return domain.NewInternalError(nil)
}
