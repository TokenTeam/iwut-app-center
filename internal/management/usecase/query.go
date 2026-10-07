package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/management/domain"
	"iwut-app-center/internal/management/port"
	"iwut-app-center/internal/shared"
	"slices"
)

const DefaultPageSize int32 = 20
const MaxPageSize int32 = 100
const MaxPageTokenBytes = 4096

type ListQuery struct {
	LifecycleStatuses, PlatformAvailabilityStatuses []string
	PageSize                                        int32
	PageToken                                       string
}
type Query struct{ repository port.Repository }

func NewQuery(repository port.Repository) *Query { return &Query{repository: repository} }

func (q *Query) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, input ListQuery) (*domain.Page, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrAuthenticatedUserRequired
	}
	lifecycle, ok := normalize(input.LifecycleStatuses, []string{"ACTIVE", "CLOSING", "CLOSED"}, []string{"ACTIVE", "CLOSING"})
	if !ok {
		return nil, domain.ErrInvalidRequest
	}
	availability, ok := normalize(input.PlatformAvailabilityStatuses, []string{"AVAILABLE", "SUSPENDED"}, nil)
	if !ok {
		return nil, domain.ErrInvalidRequest
	}
	size := input.PageSize
	if size == 0 {
		size = DefaultPageSize
	}
	if size < 1 || size > MaxPageSize {
		return nil, domain.ErrInvalidRequest
	}
	if len(input.PageToken) > MaxPageTokenBytes {
		return nil, domain.ErrInvalidPageToken
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	page, err := q.repository.List(ctx, identity.AuthID, lifecycle, availability, size, input.PageToken)
	if err != nil {
		return nil, mapError(err)
	}
	if page == nil {
		return nil, domain.NewInternalError(nil)
	}
	return page, nil
}

func (q *Query) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, rawID string) (*domain.Detail, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrAuthenticatedUserRequired
	}
	id, ok := shared.ParseApplicationID(rawID)
	if !ok {
		return nil, domain.ErrNotFound
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	detail, err := q.repository.Get(ctx, identity.AuthID, id)
	if err != nil {
		return nil, mapError(err)
	}
	if detail == nil || detail.Application.ApplicationID != id.String() {
		return nil, domain.ErrStateInconsistent
	}
	return detail, nil
}

func normalize(values, allowed, defaults []string) ([]string, bool) {
	if len(values) == 0 {
		return append([]string(nil), defaults...), true
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !slices.Contains(allowed, value) || slices.Contains(result, value) {
			return nil, false
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return result, true
}
func mapError(err error) error {
	switch {
	case errors.Is(err, port.ErrInvalidPageToken):
		return domain.ErrInvalidPageToken
	case errors.Is(err, port.ErrNotFound):
		return domain.ErrNotFound
	case errors.Is(err, port.ErrStateInconsistent):
		return domain.ErrStateInconsistent
	default:
		return domain.NewInternalError(err)
	}
}
