package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

type ListProfileRevisionsQuery struct {
	ApplicationID string
	ReviewStatus  *string
	PageSize      int32
	PageToken     string
}
type GetProfileRevisionQuery struct {
	ApplicationID, ProfileRevisionID string
	ReviewPageSize                   int32
	ReviewPageToken                  string
}
type ManagementQuery struct {
	repository port.ManagementQueryRepository
}

func NewManagementQuery(repository port.ManagementQueryRepository) *ManagementQuery {
	return &ManagementQuery{repository: repository}
}

func profileManagementPageSize(size int32) (int32, error) {
	if size == 0 {
		size = 20
	}
	if size < 1 || size > 100 {
		return 0, domain.ErrInvalidProfileManagementQuery
	}
	return size, nil
}
func (q *ManagementQuery) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, in ListProfileRevisionsQuery) (*domain.ManagedProfilePage, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrUserIdentityRequired
	}
	app, ok := shared.ParseApplicationID(in.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidProfileManagementQuery
	}
	size, err := profileManagementPageSize(in.PageSize)
	if err != nil {
		return nil, err
	}
	var status *domain.ReviewStatus
	if in.ReviewStatus != nil {
		v := domain.ReviewStatus(*in.ReviewStatus)
		if v != domain.ReviewStatusDraft && v != domain.ReviewStatusSubmitted && v != domain.ReviewStatusApproved && v != domain.ReviewStatusRejected {
			return nil, domain.ErrInvalidProfileManagementQuery
		}
		status = &v
	}
	if q == nil || q.repository == nil {
		return nil, domain.ErrProfileManagementUnavailable
	}
	// Tokens are checked by the repository after snapshot authorization, so
	// malformed or oversized tokens do not distinguish another owner's data.
	page, err := q.repository.List(ctx, identity.AuthID, app, status, size, in.PageToken)
	if err != nil {
		return nil, mapProfileManagementError(err)
	}
	if page == nil {
		return nil, domain.ErrProfileManagementStateInconsistent
	}
	return page, nil
}
func (q *ManagementQuery) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, in GetProfileRevisionQuery) (*domain.ManagedProfileDetail, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrUserIdentityRequired
	}
	app, ok := shared.ParseApplicationID(in.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidProfileManagementQuery
	}
	id, err := domain.ParseApplicationProfileRevisionID(in.ProfileRevisionID)
	if err != nil {
		return nil, domain.ErrProfileManagementNotFound
	}
	size, err := profileManagementPageSize(in.ReviewPageSize)
	if err != nil {
		return nil, err
	}
	if q == nil || q.repository == nil {
		return nil, domain.ErrProfileManagementUnavailable
	}
	detail, err := q.repository.Get(ctx, identity.AuthID, app, id, size, in.ReviewPageToken)
	if err != nil {
		return nil, mapProfileManagementError(err)
	}
	if detail == nil || detail.Profile.ApplicationID != app.String() || detail.Profile.ProfileRevisionID != id.String() {
		return nil, domain.ErrProfileManagementStateInconsistent
	}
	return detail, nil
}
func mapProfileManagementError(err error) error {
	switch {
	case errors.Is(err, port.ErrProfileManagementNotFound):
		return domain.ErrProfileManagementNotFound
	case errors.Is(err, port.ErrInvalidProfileManagementPageToken):
		return domain.ErrInvalidProfileManagementPageToken
	case errors.Is(err, port.ErrProfileManagementStateInconsistent):
		return domain.ErrProfileManagementStateInconsistent
	case errors.Is(err, port.ErrProfileManagementResponseTooLarge):
		return domain.ErrProfileManagementResponseTooLarge
	default:
		return domain.NewProfileManagementUnavailableError(err)
	}
}
