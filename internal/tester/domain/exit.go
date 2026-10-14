package domain

import "time"

// ExitTimeValid guarantees both stored audit chronology and public timestamp encoding.
func ExitTimeValid(at, joinedAt time.Time) bool {
	return !at.IsZero() && at.Year() >= 1 && at.Year() <= 9999 && !at.Before(joinedAt)
}

type LeaveApplicationAsTesterResult struct {
	membership ApplicationTesterMembership
	left       bool
}

func NewLeaveApplicationAsTesterResult(m *ApplicationTesterMembership, left bool) (*LeaveApplicationAsTesterResult, error) {
	if m == nil || !ExitTimeValid(m.JoinedAt(), m.JoinedAt()) || m.Status() != MembershipStatusRemoved || m.RemovedAt() == nil || m.RemovedBy() == nil || !ExitTimeValid(*m.RemovedAt(), m.JoinedAt()) {
		return nil, ErrApplicationTesterStateInconsistent
	}
	return &LeaveApplicationAsTesterResult{*copyMembership(m), left}, nil
}
func (r *LeaveApplicationAsTesterResult) Membership() *ApplicationTesterMembership {
	return copyMembership(&r.membership)
}
func (r *LeaveApplicationAsTesterResult) Left() bool { return r.left }
