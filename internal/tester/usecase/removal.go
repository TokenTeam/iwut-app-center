package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"strings"
)

type RemoveApplicationTesterHandler struct {
	clock      port.Clock
	repository port.ApplicationTesterRemovalRepository
}

func NewRemoveApplicationTesterHandler(clock port.Clock, repository port.ApplicationTesterRemovalRepository) *RemoveApplicationTesterHandler {
	return &RemoveApplicationTesterHandler{clock, repository}
}

func (h *RemoveApplicationTesterHandler) Handle(ctx context.Context, identity shared.DeveloperIdentity, appID shared.ApplicationID, membershipID domain.ApplicationTesterMembershipID) (*domain.RemoveApplicationTesterResult, error) {
	if !identity.AuthID.IsValid() {
		return nil, domain.ErrDeveloperIdentityRequired
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return nil, domain.ErrDeveloperApprovalRequired
	}
	if !appID.IsValid() {
		return nil, domain.ErrInvalidApplicationId
	}
	appID = shared.ApplicationID(strings.ToLower(appID.String()))
	membershipID, err := domain.ParseTesterMembershipID(membershipID.String())
	if err != nil {
		return nil, err
	}
	if h == nil || h.repository == nil {
		return nil, domain.NewInternalError(nil)
	}
	candidate, err := h.repository.LoadRemovalCandidate(ctx, appID, membershipID, identity.AuthID)
	if err != nil {
		return nil, mapRemovalRepositoryError(err)
	}
	if candidate == nil {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	membership := candidate.Membership()
	if membership == nil || membership.ApplicationID() != appID || membership.MembershipID() != membershipID {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	if membership.Status() == domain.MembershipStatusRemoved {
		return domain.NewRemoveApplicationTesterResult(membership, false, candidate.ActiveTesterCount(), domain.ApplicationTesterLimit, candidate.ActiveJoinLinkExists())
	}
	if h.clock == nil {
		return nil, domain.NewInternalError(nil)
	}
	at := h.clock.Now().UTC()
	if at.IsZero() {
		return nil, domain.NewInternalError(nil)
	}
	result, err := h.repository.Remove(ctx, appID, membershipID, identity.AuthID, at)
	if err != nil {
		return nil, mapRemovalRepositoryError(err)
	}
	if result == nil {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	stored := result.Membership()
	if stored == nil || stored.ApplicationID() != appID || stored.MembershipID() != membershipID || stored.TesterAuthID() != membership.TesterAuthID() || stored.JoinedViaJoinLinkID() != membership.JoinedViaJoinLinkID() || !stored.JoinedAt().Equal(membership.JoinedAt()) || stored.Status() != domain.MembershipStatusRemoved || result.TesterLimit() != domain.ApplicationTesterLimit || result.ActiveTesterCount() < 0 || result.ActiveTesterCount() > domain.ApplicationTesterLimit || stored.RemovedBy() == nil || stored.RemovedAt() == nil {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	if result.Removed() && *stored.RemovedBy() != identity.AuthID {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	return result, nil
}

func mapRemovalRepositoryError(err error) error {
	for _, pair := range []struct{ source, target error }{
		{port.ErrApplicationTesterMembershipNotFound, domain.ErrApplicationTesterMembershipNotFound},
		{port.ErrApplicationAdminRequired, domain.ErrApplicationAdminRequired},
		{port.ErrApplicationTesterStateInconsistent, domain.ErrApplicationTesterStateInconsistent},
	} {
		if errors.Is(err, pair.source) || errors.Is(err, pair.target) {
			return pair.target
		}
	}
	// Storage causes can contain other testers' identity or indexed credentials.
	return domain.NewInternalError(nil)
}
