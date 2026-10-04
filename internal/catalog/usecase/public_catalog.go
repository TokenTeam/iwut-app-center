package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

const DefaultCatalogPageSize int32 = 20
const MaxCatalogPageSize int32 = 100
const MaxCatalogPageTokenBytes = 4096

type CatalogRuntimeQuery struct {
	HostRPCAPIMajor  int32
	HostCapabilities []string
}
type ListPublicApplicationsQuery struct {
	Runtime   CatalogRuntimeQuery
	PageSize  int32
	PageToken string
}
type GetPublicApplicationQuery struct {
	ApplicationID string
	Runtime       CatalogRuntimeQuery
}
type PublicCatalog struct {
	repository port.PublicApplicationCatalogRepository
}

func NewPublicCatalog(repository port.PublicApplicationCatalogRepository) *PublicCatalog {
	return &PublicCatalog{repository: repository}
}

func (handler *PublicCatalog) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, query ListPublicApplicationsQuery) (*domain.PublicApplicationCatalogPage, error) {
	major, capabilities, err := validateCatalogRuntime(query.Runtime)
	if err != nil {
		return nil, err
	}
	pageSize := query.PageSize
	if pageSize == 0 {
		pageSize = DefaultCatalogPageSize
	}
	if pageSize < 1 || pageSize > MaxCatalogPageSize {
		return nil, domain.ErrInvalidPageSize
	}
	if len(query.PageToken) > MaxCatalogPageTokenBytes {
		return nil, domain.ErrInvalidPageToken
	}
	if handler == nil || handler.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	result, err := handler.repository.ListPublic(ctx, identity.AuthID, major, capabilities, pageSize, query.PageToken)
	if err != nil {
		return nil, mapPublicCatalogError(err)
	}
	if result == nil {
		return nil, domain.NewInternalError(nil)
	}
	return result, nil
}

func (handler *PublicCatalog) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, query GetPublicApplicationQuery) (*domain.PublicApplicationCatalogItem, error) {
	applicationID, ok := shared.ParseApplicationID(query.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidApplicationID
	}
	major, capabilities, err := validateCatalogRuntime(query.Runtime)
	if err != nil {
		return nil, err
	}
	if handler == nil || handler.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	result, err := handler.repository.GetPublic(ctx, applicationID, identity.AuthID, major, capabilities)
	if err != nil {
		return nil, mapPublicCatalogError(err)
	}
	if result == nil || result.ApplicationID() != applicationID {
		return nil, domain.ErrApplicationCatalogStateInconsistent
	}
	return result, nil
}

func validateCatalogRuntime(query CatalogRuntimeQuery) (int32, []domain.CapabilityName, error) {
	if query.HostRPCAPIMajor < 1 {
		return 0, nil, domain.ErrInvalidHostRPCAPIMajor
	}
	capabilities, err := domain.NormalizeHostCapabilities(query.HostCapabilities)
	if err != nil {
		return 0, nil, err
	}
	return query.HostRPCAPIMajor, capabilities, nil
}

func mapPublicCatalogError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrInvalidPageToken, domain.ErrInvalidPageToken},
		{port.ErrPublicApplicationNotFound, domain.ErrPublicApplicationNotFound},
		{port.ErrApplicationCatalogStateInconsistent, domain.ErrApplicationCatalogStateInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	return domain.NewInternalError(nil)
}
