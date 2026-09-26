package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

type ResolveTestLaunchTargetQuery struct {
	ApplicationID    string
	HostRPCAPIMajor  int32
	HostCapabilities []string
}
type ResolveTestLaunchTarget struct{ resolver port.TestLaunchResolver }

func NewResolveTestLaunchTarget(resolver port.TestLaunchResolver) *ResolveTestLaunchTarget {
	return &ResolveTestLaunchTarget{resolver}
}
func (h *ResolveTestLaunchTarget) Execute(ctx context.Context, identity shared.AuthenticatedUserIdentity, query ResolveTestLaunchTargetQuery) (*domain.TestLaunchDescriptor, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrAuthenticatedUserRequired
	}
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
	if h == nil || h.resolver == nil {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.resolver.ResolveForTester(ctx, applicationID, identity.AuthID, query.HostRPCAPIMajor, capabilities)
	if err != nil {
		return nil, mapResolverError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	if result.ApplicationID() != applicationID || result.RPCAPIMajor() != query.HostRPCAPIMajor || len(domain.MissingCapabilities(result.RequiredCapabilities(), capabilities)) != 0 {
		return nil, domain.ErrApplicationTestPublicationInconsistent
	}
	return result, nil
}
func mapResolverError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{port.ErrApplicationTesterRequired, domain.ErrApplicationTesterRequired},
		{port.ErrApplicationTestTargetUnavailable, domain.ErrApplicationTestTargetUnavailable},
		{port.ErrApplicationTestPublicationInconsistent, domain.ErrApplicationTestPublicationInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	var failure *domain.Error
	if errors.As(err, &failure) && errors.Is(err, domain.ErrHostCapabilitiesInsufficient) {
		return domain.NewHostCapabilitiesInsufficientError(failure.MissingCapabilities())
	}
	// A storage decoder error can embed arbitrary persisted content. Preserve no
	// driver cause in an externally visible failure or ordinary transport log.
	return domain.NewInternalError(nil)
}
