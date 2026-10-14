package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/tester/domain"
	"iwut-app-center/internal/tester/port"
	"time"
	"unicode/utf8"
)

type LeaveApplicationAsTesterHandler struct {
	clock      port.Clock
	repository port.ApplicationTesterExitRepository
}

func NewLeaveApplicationAsTesterHandler(clock port.Clock, repository port.ApplicationTesterExitRepository) *LeaveApplicationAsTesterHandler {
	return &LeaveApplicationAsTesterHandler{clock, repository}
}
func (h *LeaveApplicationAsTesterHandler) Handle(ctx context.Context, identity shared.AuthenticatedUserIdentity, appID shared.ApplicationID, id domain.ApplicationTesterMembershipID) (*domain.LeaveApplicationAsTesterResult, error) {
	if !identity.AuthID.IsValid() || !utf8.ValidString(identity.AuthID.String()) {
		return nil, domain.ErrUserIdentityRequired
	}
	appID, ok := shared.ParseApplicationID(appID.String())
	if !ok {
		return nil, domain.ErrInvalidApplicationId
	}
	id, err := domain.ParseTesterMembershipID(id.String())
	if err != nil {
		return nil, err
	}
	if h == nil || h.repository == nil {
		return nil, domain.ErrApplicationTesterExitUnavailable
	}
	m, err := h.repository.LoadExitCandidate(ctx, appID, id, identity.AuthID)
	if err != nil {
		return nil, mapExitError(err)
	}
	if !exitTargetValid(m, appID, id, identity.AuthID) {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	if m.Status() == domain.MembershipStatusRemoved {
		return domain.NewLeaveApplicationAsTesterResult(m, false)
	}
	if m.Status() != domain.MembershipStatusActive || h.clock == nil {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	at := h.clock.Now().UTC()
	if !domain.ExitTimeValid(at, m.JoinedAt()) {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	result, err := h.repository.Leave(ctx, appID, id, identity.AuthID, at)
	if err != nil {
		return nil, mapExitError(err)
	}
	if result == nil {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	stored := result.Membership()
	if !exitTargetValid(stored, appID, id, identity.AuthID) || stored.Status() != domain.MembershipStatusRemoved || stored.JoinedViaJoinLinkID() != m.JoinedViaJoinLinkID() || !stored.JoinedAt().Equal(m.JoinedAt()) || stored.RemovedAt() == nil || stored.RemovedBy() == nil || !domain.ExitTimeValid(*stored.RemovedAt(), stored.JoinedAt()) || result.Left() && (*stored.RemovedBy() != identity.AuthID || !stored.RemovedAt().Equal(at.Truncate(time.Millisecond))) {
		return nil, domain.ErrApplicationTesterStateInconsistent
	}
	return result, nil
}
func exitTargetValid(m *domain.ApplicationTesterMembership, app shared.ApplicationID, id domain.ApplicationTesterMembershipID, caller shared.AuthID) bool {
	return m != nil && m.ApplicationID() == app && m.MembershipID() == id && m.TesterAuthID() == caller && !m.JoinedAt().IsZero() && m.JoinedAt().Year() >= 1 && m.JoinedAt().Year() <= 9999
}
func mapExitError(err error) error {
	if errors.Is(err, port.ErrApplicationTesterMembershipNotFound) || errors.Is(err, domain.ErrApplicationTesterMembershipNotFound) {
		return domain.ErrApplicationTesterMembershipNotFound
	}
	if errors.Is(err, port.ErrApplicationTesterStateInconsistent) || errors.Is(err, domain.ErrApplicationTesterStateInconsistent) {
		return domain.ErrApplicationTesterStateInconsistent
	}
	return domain.ErrApplicationTesterExitUnavailable
}
