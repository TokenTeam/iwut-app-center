package domain

import (
	"iwut-app-center/internal/shared"
	"time"
)

// Remove returns a new immutable terminal episode. It never changes the joined
// facts or replaces the audit facts of a removal that already happened.
func (m *ApplicationTesterMembership) Remove(admin shared.AuthID, at time.Time) (*ApplicationTesterMembership, error) {
	if m == nil || !m.membershipID.IsValid() {
		return nil, ErrApplicationTesterStateInconsistent
	}
	if m.status == MembershipStatusRemoved {
		return copyMembership(m), nil
	}
	if m.status != MembershipStatusActive {
		return nil, ErrApplicationTesterStateInconsistent
	}
	if !admin.IsValid() || at.IsZero() {
		return nil, NewInternalError(nil)
	}
	return RestoreTesterMembership(m.membershipID, m.applicationID, m.testerAuthID, MembershipStatusRemoved, m.joinedViaJoinLinkID, m.joinedAt, &admin, &at)
}

// TesterRemovalCandidate contains a consistent, authorized repository snapshot.
// An ACTIVE candidate is preliminary; Remove must recheck it in its transaction.
type TesterRemovalCandidate struct {
	membership           ApplicationTesterMembership
	activeTesterCount    int32
	activeJoinLinkExists bool
}

func NewTesterRemovalCandidate(m *ApplicationTesterMembership, count int32, activeLink bool) (*TesterRemovalCandidate, error) {
	if err := validateRemovalSnapshot(m, count); err != nil {
		return nil, err
	}
	return &TesterRemovalCandidate{*copyMembership(m), count, activeLink}, nil
}

func (c *TesterRemovalCandidate) Membership() *ApplicationTesterMembership {
	return copyMembership(&c.membership)
}
func (c *TesterRemovalCandidate) ActiveTesterCount() int32   { return c.activeTesterCount }
func (c *TesterRemovalCandidate) ActiveJoinLinkExists() bool { return c.activeJoinLinkExists }

type RemoveApplicationTesterResult struct {
	membership           ApplicationTesterMembership
	removed              bool
	activeTesterCount    int32
	testerLimit          int32
	activeJoinLinkExists bool
}

func NewRemoveApplicationTesterResult(m *ApplicationTesterMembership, removed bool, count, limit int32, activeLink bool) (*RemoveApplicationTesterResult, error) {
	if err := validateRemovalSnapshot(m, count); err != nil {
		return nil, err
	}
	if m.status != MembershipStatusRemoved || limit != ApplicationTesterLimit || (removed && count >= limit) {
		return nil, ErrApplicationTesterStateInconsistent
	}
	return &RemoveApplicationTesterResult{*copyMembership(m), removed, count, limit, activeLink}, nil
}

func (r *RemoveApplicationTesterResult) Membership() *ApplicationTesterMembership {
	return copyMembership(&r.membership)
}
func (r *RemoveApplicationTesterResult) Removed() bool              { return r.removed }
func (r *RemoveApplicationTesterResult) ActiveTesterCount() int32   { return r.activeTesterCount }
func (r *RemoveApplicationTesterResult) TesterLimit() int32         { return r.testerLimit }
func (r *RemoveApplicationTesterResult) ActiveJoinLinkExists() bool { return r.activeJoinLinkExists }

func validateRemovalSnapshot(m *ApplicationTesterMembership, count int32) error {
	if m == nil || count < 0 || count > ApplicationTesterLimit || (m.status == MembershipStatusActive && count == 0) {
		return ErrApplicationTesterStateInconsistent
	}
	if _, err := RestoreTesterMembership(m.membershipID, m.applicationID, m.testerAuthID, m.status, m.joinedViaJoinLinkID, m.joinedAt, m.removedBy, m.removedAt); err != nil {
		return ErrApplicationTesterStateInconsistent
	}
	return nil
}
