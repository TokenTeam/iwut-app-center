package domain

import (
	"iwut-app-center/internal/shared"
	"time"
)

// Revoke preserves all creation facts and never rewrites a terminal audit.
func (l *ApplicationTesterJoinLink) Revoke(admin shared.AuthID, at time.Time) (*ApplicationTesterJoinLink, error) {
	if err := validateRevocationLink(l); err != nil {
		return nil, err
	}
	if l.status == JoinLinkStatusRevoked {
		return copyLink(l), nil
	}
	if !admin.IsValid() || at.IsZero() {
		return nil, NewInternalError(nil)
	}
	reason := RevocationReasonManual
	return RestoreTesterJoinLink(l.joinLinkID, l.applicationID, l.tokenHash, JoinLinkStatusRevoked, l.createdBy, l.createdAt, &admin, &at, &reason, nil)
}

// A revoked candidate is a final authorized snapshot. An active candidate must
// be checked again by Revoke under the same Application write fence.
type TesterJoinLinkRevocationCandidate struct{ joinLink ApplicationTesterJoinLink }

func NewTesterJoinLinkRevocationCandidate(link *ApplicationTesterJoinLink) (*TesterJoinLinkRevocationCandidate, error) {
	if err := validateRevocationLink(link); err != nil {
		return nil, err
	}
	return &TesterJoinLinkRevocationCandidate{*copyLink(link)}, nil
}
func (c *TesterJoinLinkRevocationCandidate) JoinLink() *ApplicationTesterJoinLink {
	return copyLink(&c.joinLink)
}
func (c TesterJoinLinkRevocationCandidate) String() string {
	return "TesterJoinLinkRevocationCandidate{credential: [redacted]}"
}
func (c TesterJoinLinkRevocationCandidate) GoString() string { return c.String() }

type RevokeTesterJoinLinkResult struct {
	joinLink ApplicationTesterJoinLink
	revoked  bool
}

func NewRevokeTesterJoinLinkResult(link *ApplicationTesterJoinLink, revoked bool) (*RevokeTesterJoinLinkResult, error) {
	if err := validateRevocationLink(link); err != nil {
		return nil, err
	}
	if link.status != JoinLinkStatusRevoked || revoked && *link.revocationReason != RevocationReasonManual {
		return nil, ErrApplicationTesterJoinLinkStateInconsistent
	}
	return &RevokeTesterJoinLinkResult{*copyLink(link), revoked}, nil
}
func (r *RevokeTesterJoinLinkResult) JoinLink() *ApplicationTesterJoinLink {
	return copyLink(&r.joinLink)
}
func (r *RevokeTesterJoinLinkResult) Revoked() bool { return r.revoked }
func (r RevokeTesterJoinLinkResult) String() string {
	return "RevokeTesterJoinLinkResult{credential: [redacted]}"
}
func (r RevokeTesterJoinLinkResult) GoString() string { return r.String() }

func validateRevocationLink(l *ApplicationTesterJoinLink) error {
	if l == nil {
		return ErrApplicationTesterJoinLinkStateInconsistent
	}
	if _, err := RestoreTesterJoinLink(l.joinLinkID, l.applicationID, l.tokenHash, l.status, l.createdBy, l.createdAt, l.revokedBy, l.revokedAt, l.revocationReason, l.replacedByJoinLinkID); err != nil {
		return ErrApplicationTesterJoinLinkStateInconsistent
	}
	return nil
}
