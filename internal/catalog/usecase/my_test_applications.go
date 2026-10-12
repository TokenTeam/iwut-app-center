package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

type ListMyTestApplicationsQuery struct {
	Runtime   CatalogRuntimeQuery
	PageSize  int32
	PageToken string
}
type GetMyTestApplicationQuery struct {
	ApplicationID string
	Runtime       CatalogRuntimeQuery
}
type MyTestApplications struct {
	repository port.MyTestApplicationRepository
}

func NewMyTestApplications(repository port.MyTestApplicationRepository) *MyTestApplications {
	return &MyTestApplications{repository}
}
func myTestRuntime(identity shared.AuthenticatedUserIdentity, query CatalogRuntimeQuery) ([]domain.CapabilityName, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrAuthenticatedUserRequired
	}
	if query.HostRPCAPIMajor < 1 {
		return nil, domain.ErrInvalidMyTestApplicationQuery
	}
	host, err := domain.NormalizeHostCapabilities(query.HostCapabilities)
	if err != nil {
		return nil, domain.ErrInvalidMyTestApplicationQuery
	}
	return host, nil
}
func (h *MyTestApplications) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, q ListMyTestApplicationsQuery) (*domain.MyTestApplicationPage, error) {
	host, err := myTestRuntime(identity, q.Runtime)
	if err != nil {
		return nil, err
	}
	if q.PageSize == 0 {
		q.PageSize = 20
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		return nil, domain.ErrInvalidMyTestApplicationQuery
	}
	if len(q.PageToken) > 4096 {
		return nil, domain.ErrInvalidMyTestApplicationPageToken
	}
	if h == nil || h.repository == nil {
		return nil, domain.ErrMyTestApplicationUnavailable
	}
	page, err := h.repository.ListMyTest(ctx, identity.AuthID, q.Runtime.HostRPCAPIMajor, host, q.PageSize, q.PageToken)
	if err != nil {
		return nil, myTestError(err)
	}
	if page == nil {
		return nil, domain.ErrMyTestApplicationStateInconsistent
	}
	return page, nil
}
func (h *MyTestApplications) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, q GetMyTestApplicationQuery) (*domain.MyTestApplicationDetail, error) {
	host, err := myTestRuntime(identity, q.Runtime)
	if err != nil {
		return nil, err
	}
	app, ok := shared.ParseApplicationID(q.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidMyTestApplicationQuery
	}
	if h == nil || h.repository == nil {
		return nil, domain.ErrMyTestApplicationUnavailable
	}
	detail, err := h.repository.GetMyTest(ctx, app, identity.AuthID, q.Runtime.HostRPCAPIMajor, host)
	if err != nil {
		return nil, myTestError(err)
	}
	if detail == nil || detail.Item == nil || detail.Item.ApplicationID != app {
		return nil, domain.ErrMyTestApplicationStateInconsistent
	}
	return detail, nil
}
func myTestError(err error) error {
	for _, known := range []error{domain.ErrInvalidMyTestApplicationPageToken, domain.ErrMyTestApplicationNotFound, domain.ErrMyTestApplicationStateInconsistent, domain.ErrMyTestApplicationUnavailable} {
		if errors.Is(err, known) {
			return known
		}
	}
	return domain.ErrMyTestApplicationUnavailable
}
