package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
)

type TesterManagementListQuery struct {
	ApplicationID string
	Status        string
	PageSize      int32
	PageToken     string
}
type ManagementQuery struct {
	repository port.ManagementQueryRepository
}

func NewManagementQuery(repository port.ManagementQueryRepository) *ManagementQuery {
	return &ManagementQuery{repository}
}
func managementApplication(identity shared.AuthenticatedUserIdentity, value string) (shared.ApplicationID, error) {
	if !identity.AuthID.IsValid() {
		return "", domain.ErrAuthenticatedUserRequired
	}
	id, ok := shared.ParseApplicationID(value)
	if !ok {
		return "", domain.ErrInvalidApplicationTesterManagementQuery
	}
	return id, nil
}
func normalizeManagementQuery(input TesterManagementListQuery, kind string) (TesterManagementListQuery, error) {
	if input.Status == "" {
		if kind == "membership" {
			input.Status = "ACTIVE"
		} else {
			input.Status = "ALL"
		}
	}
	if input.Status != "ALL" && input.Status != "ACTIVE" && !(kind == "membership" && input.Status == "REMOVED") && !(kind == "link" && input.Status == "REVOKED") {
		return input, domain.ErrInvalidApplicationTesterManagementQuery
	}
	if input.PageSize == 0 {
		input.PageSize = 20
	}
	if input.PageSize < 1 || input.PageSize > 100 {
		return input, domain.ErrInvalidApplicationTesterManagementQuery
	}
	if len(input.PageToken) > 4096 {
		return input, domain.ErrInvalidApplicationTesterManagementPageToken
	}
	return input, nil
}
func (q *ManagementQuery) Get(ctx context.Context, identity shared.AuthenticatedUserIdentity, value string) (*domain.TesterManagement, error) {
	id, err := managementApplication(identity, value)
	if err != nil {
		return nil, err
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	r, err := q.repository.GetManagement(ctx, identity.AuthID, id)
	if err != nil {
		return nil, mapTesterManagementError(err)
	}
	if r == nil || r.ApplicationID != id {
		return nil, domain.ErrApplicationTesterManagementStateInconsistent
	}
	return r, nil
}
func (q *ManagementQuery) ListMemberships(ctx context.Context, identity shared.AuthenticatedUserIdentity, input TesterManagementListQuery) (*domain.TesterMembershipPage, error) {
	id, err := managementApplication(identity, input.ApplicationID)
	if err != nil {
		return nil, err
	}
	input, err = normalizeManagementQuery(input, "membership")
	if err != nil {
		return nil, err
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	r, err := q.repository.ListMemberships(ctx, identity.AuthID, id, domain.MembershipQueryStatus(input.Status), input.PageSize, input.PageToken)
	if err != nil {
		return nil, mapTesterManagementError(err)
	}
	if r == nil {
		return nil, domain.ErrApplicationTesterManagementStateInconsistent
	}
	return r, nil
}
func (q *ManagementQuery) ListJoinLinks(ctx context.Context, identity shared.AuthenticatedUserIdentity, input TesterManagementListQuery) (*domain.TesterJoinLinkPage, error) {
	id, err := managementApplication(identity, input.ApplicationID)
	if err != nil {
		return nil, err
	}
	input, err = normalizeManagementQuery(input, "link")
	if err != nil {
		return nil, err
	}
	if q == nil || q.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	r, err := q.repository.ListJoinLinks(ctx, identity.AuthID, id, domain.JoinLinkQueryStatus(input.Status), input.PageSize, input.PageToken)
	if err != nil {
		return nil, mapTesterManagementError(err)
	}
	if r == nil {
		return nil, domain.ErrApplicationTesterManagementStateInconsistent
	}
	return r, nil
}
func mapTesterManagementError(err error) error {
	switch {
	case errors.Is(err, port.ErrInvalidManagementPageToken):
		return domain.ErrInvalidApplicationTesterManagementPageToken
	case errors.Is(err, port.ErrManagementNotFound):
		return domain.ErrApplicationTesterManagementNotFound
	case errors.Is(err, port.ErrManagementStateInconsistent):
		return domain.ErrApplicationTesterManagementStateInconsistent
	case errors.Is(err, port.ErrManagementUnavailable):
		return domain.ErrApplicationTesterManagementUnavailable
	default:
		return domain.NewInternalError(err)
	}
}
