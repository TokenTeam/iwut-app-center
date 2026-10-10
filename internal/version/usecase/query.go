package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

const (
	ManagementQueryDefaultPageSize int32 = 20
	ManagementQueryMaxPageSize     int32 = 100
	ManagementQueryMaxTokenBytes         = 4096
)

type ListApplicationVersionsQuery struct {
	ApplicationID string
	ReviewStatus  *string
	PageSize      int32
	PageToken     string
}

type ManagementQuery struct {
	repository port.ManagementQueryRepository
}

func NewManagementQuery(repository port.ManagementQueryRepository) *ManagementQuery {
	return &ManagementQuery{repository: repository}
}

func (q *ManagementQuery) List(ctx context.Context, identity shared.AuthenticatedUserIdentity, input ListApplicationVersionsQuery) (*domain.OwnedVersionPage, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrUserIdentityRequired
	}
	applicationID, ok := shared.ParseApplicationID(input.ApplicationID)
	if !ok {
		return nil, domain.ErrInvalidApplicationVersionQuery
	}
	var status *domain.ReviewStatus
	if input.ReviewStatus != nil {
		value := domain.ReviewStatus(*input.ReviewStatus)
		switch value {
		case domain.ReviewStatusDraft, domain.ReviewStatusSubmitted, domain.ReviewStatusApproved, domain.ReviewStatusRejected:
			status = &value
		default:
			return nil, domain.ErrInvalidApplicationVersionQuery
		}
	}
	pageSize := input.PageSize
	if pageSize == 0 {
		pageSize = ManagementQueryDefaultPageSize
	}
	if pageSize < 1 || pageSize > ManagementQueryMaxPageSize {
		return nil, domain.ErrInvalidApplicationVersionQuery
	}
	if len(input.PageToken) > ManagementQueryMaxTokenBytes {
		return nil, domain.ErrInvalidApplicationVersionPageToken
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	page, err := q.repository.List(ctx, identity.AuthID, applicationID, status, pageSize, input.PageToken)
	if err != nil {
		return nil, mapManagementQueryError(err)
	}
	if page == nil {
		return nil, domain.ErrApplicationVersionManagementStateInconsistent
	}
	return page, nil
}

func (q *ManagementQuery) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, applicationIDValue, versionIDValue string) (*domain.OwnedVersionDetail, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrUserIdentityRequired
	}
	applicationID, ok := shared.ParseApplicationID(applicationIDValue)
	versionID := domain.ApplicationVersionID(versionIDValue)
	if !ok || !versionID.IsValid() {
		return nil, domain.ErrInvalidApplicationVersionQuery
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	detail, err := q.repository.Get(ctx, identity.AuthID, applicationID, versionID)
	if err != nil {
		return nil, mapManagementQueryError(err)
	}
	if detail == nil || detail.Version.ApplicationID != applicationID.String() || detail.Version.VersionID != versionID.String() {
		return nil, domain.ErrApplicationVersionManagementStateInconsistent
	}
	return detail, nil
}

func mapManagementQueryError(err error) error {
	switch {
	case errors.Is(err, port.ErrInvalidManagementPageToken):
		return domain.ErrInvalidApplicationVersionPageToken
	case errors.Is(err, port.ErrManagementNotFound):
		return domain.ErrApplicationVersionManagementNotFound
	case errors.Is(err, port.ErrManagementStateInconsistent):
		return domain.ErrApplicationVersionManagementStateInconsistent
	case errors.Is(err, port.ErrManagementUnavailable):
		return domain.NewApplicationVersionManagementUnavailableError(err)
	default:
		return domain.NewInternalError(err)
	}
}
