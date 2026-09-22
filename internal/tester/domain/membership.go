package domain

import (
	"iwut-app-center/internal/shared"
	"strings"
	"time"
)

const ApplicationTesterLimit int32 = 100

type ApplicationTesterMembershipID string

func (id ApplicationTesterMembershipID) String() string { return string(id) }
func (id ApplicationTesterMembershipID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }
func ParseTesterMembershipID(value string) (ApplicationTesterMembershipID, error) {
	if !shared.IsUUIDv7(value) {
		return "", NewInternalError(nil)
	}
	return ApplicationTesterMembershipID(strings.ToLower(value)), nil
}

type MembershipStatus string

const (
	MembershipStatusActive  MembershipStatus = "ACTIVE"
	MembershipStatusRemoved MembershipStatus = "REMOVED"
)

// ApplicationTesterMembership is an immutable membership episode. A removed
// episode cannot become active again: rejoining creates a separate episode.
type ApplicationTesterMembership struct {
	membershipID        ApplicationTesterMembershipID
	applicationID       shared.ApplicationID
	testerAuthID        shared.AuthID
	status              MembershipStatus
	joinedViaJoinLinkID ApplicationTesterJoinLinkID
	joinedAt            time.Time
	removedBy           *shared.AuthID
	removedAt           *time.Time
}

func NewActiveTesterMembership(id ApplicationTesterMembershipID, app shared.ApplicationID, tester shared.AuthID, link ApplicationTesterJoinLinkID, at time.Time) (*ApplicationTesterMembership, error) {
	return RestoreTesterMembership(id, app, tester, MembershipStatusActive, link, at, nil, nil)
}
func RestoreTesterMembership(id ApplicationTesterMembershipID, app shared.ApplicationID, tester shared.AuthID, status MembershipStatus, link ApplicationTesterJoinLinkID, joinedAt time.Time, removedBy *shared.AuthID, removedAt *time.Time) (*ApplicationTesterMembership, error) {
	if !id.IsValid() || !app.IsValid() || !tester.IsValid() || !link.IsValid() || joinedAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	switch status {
	case MembershipStatusActive:
		if removedBy != nil || removedAt != nil {
			return nil, NewInternalError(nil)
		}
	case MembershipStatusRemoved:
		if removedBy == nil || !removedBy.IsValid() || removedAt == nil || removedAt.IsZero() {
			return nil, NewInternalError(nil)
		}
	default:
		return nil, NewInternalError(nil)
	}
	m := &ApplicationTesterMembership{id, app, tester, status, link, joinedAt.UTC(), copyValue(removedBy), copyValue(removedAt)}
	if m.removedAt != nil {
		at := m.removedAt.UTC()
		m.removedAt = &at
	}
	return m, nil
}
func (m *ApplicationTesterMembership) MembershipID() ApplicationTesterMembershipID {
	return m.membershipID
}
func (m *ApplicationTesterMembership) ApplicationID() shared.ApplicationID { return m.applicationID }
func (m *ApplicationTesterMembership) TesterAuthID() shared.AuthID         { return m.testerAuthID }
func (m *ApplicationTesterMembership) Status() MembershipStatus            { return m.status }
func (m *ApplicationTesterMembership) JoinedViaJoinLinkID() ApplicationTesterJoinLinkID {
	return m.joinedViaJoinLinkID
}
func (m *ApplicationTesterMembership) JoinedAt() time.Time       { return m.joinedAt }
func (m *ApplicationTesterMembership) RemovedBy() *shared.AuthID { return copyValue(m.removedBy) }
func (m *ApplicationTesterMembership) RemovedAt() *time.Time     { return copyValue(m.removedAt) }
func copyMembership(m *ApplicationTesterMembership) *ApplicationTesterMembership {
	if m == nil {
		return nil
	}
	c := *m
	c.removedBy = copyValue(m.removedBy)
	c.removedAt = copyValue(m.removedAt)
	return &c
}

type TesterJoinCandidate struct{ applicationID shared.ApplicationID }

func NewTesterJoinCandidate(app shared.ApplicationID) (*TesterJoinCandidate, error) {
	if !app.IsValid() {
		return nil, NewInternalError(nil)
	}
	return &TesterJoinCandidate{app}, nil
}
func (c *TesterJoinCandidate) ApplicationID() shared.ApplicationID { return c.applicationID }

type JoinApplicationAsTesterResult struct {
	membership        ApplicationTesterMembership
	joined            bool
	activeTesterCount int32
	testerLimit       int32
}

func NewJoinApplicationAsTesterResult(m *ApplicationTesterMembership, joined bool, count, limit int32) (*JoinApplicationAsTesterResult, error) {
	if m == nil || !m.membershipID.IsValid() || m.status != MembershipStatusActive || limit != ApplicationTesterLimit || count < 1 || count > limit {
		return nil, NewInternalError(nil)
	}
	return &JoinApplicationAsTesterResult{*copyMembership(m), joined, count, limit}, nil
}
func (r *JoinApplicationAsTesterResult) Membership() *ApplicationTesterMembership {
	return copyMembership(&r.membership)
}
func (r *JoinApplicationAsTesterResult) Joined() bool             { return r.joined }
func (r *JoinApplicationAsTesterResult) ActiveTesterCount() int32 { return r.activeTesterCount }
func (r *JoinApplicationAsTesterResult) TesterLimit() int32       { return r.testerLimit }
