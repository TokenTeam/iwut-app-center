package domain

import (
	"iwut-app-center/internal/shared"
	"strings"
	"time"
)

type ApplicationTesterJoinLinkID string

func (id ApplicationTesterJoinLinkID) String() string { return string(id) }
func (id ApplicationTesterJoinLinkID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }
func ParseTesterJoinLinkID(value string) (ApplicationTesterJoinLinkID, error) {
	if !shared.IsUUIDv7(value) {
		return "", ErrInvalidTesterJoinLinkId
	}
	return ApplicationTesterJoinLinkID(strings.ToLower(value)), nil
}

// TesterJoinTokenHash holds only the hash of a CSPRNG-generated secret. Its
// diagnostic representations are redacted; Bytes is for persistence only.
type TesterJoinTokenHash struct{ value [32]byte }

func NewTesterJoinTokenHash(value [32]byte) TesterJoinTokenHash { return TesterJoinTokenHash{value} }
func (h TesterJoinTokenHash) Bytes() [32]byte                   { return h.value }
func (h TesterJoinTokenHash) String() string                    { return "[redacted tester token hash]" }
func (h TesterJoinTokenHash) GoString() string                  { return h.String() }

type JoinLinkStatus string

const (
	JoinLinkStatusActive  JoinLinkStatus = "ACTIVE"
	JoinLinkStatusRevoked JoinLinkStatus = "REVOKED"
)

type RevocationReason string

const (
	RevocationReasonRotated            RevocationReason = "ROTATED"
	RevocationReasonManual             RevocationReason = "MANUAL"
	RevocationReasonAdminTransfer      RevocationReason = "ADMIN_TRANSFER"
	RevocationReasonApplicationClosure RevocationReason = "APPLICATION_CLOSURE"
)

type ApplicationTesterJoinLink struct {
	joinLinkID           ApplicationTesterJoinLinkID
	applicationID        shared.ApplicationID
	tokenHash            TesterJoinTokenHash
	status               JoinLinkStatus
	createdBy            shared.AuthID
	createdAt            time.Time
	revokedBy            *shared.AuthID
	revokedAt            *time.Time
	revocationReason     *RevocationReason
	replacedByJoinLinkID *ApplicationTesterJoinLinkID
}

func NewActiveTesterJoinLink(id ApplicationTesterJoinLinkID, app shared.ApplicationID, hash TesterJoinTokenHash, by shared.AuthID, at time.Time) (*ApplicationTesterJoinLink, error) {
	return RestoreTesterJoinLink(id, app, hash, JoinLinkStatusActive, by, at, nil, nil, nil, nil)
}
func RestoreTesterJoinLink(id ApplicationTesterJoinLinkID, app shared.ApplicationID, hash TesterJoinTokenHash, status JoinLinkStatus, createdBy shared.AuthID, createdAt time.Time, revokedBy *shared.AuthID, revokedAt *time.Time, reason *RevocationReason, replacedBy *ApplicationTesterJoinLinkID) (*ApplicationTesterJoinLink, error) {
	if !id.IsValid() || !app.IsValid() || !createdBy.IsValid() || createdAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	switch status {
	case JoinLinkStatusActive:
		if revokedBy != nil || revokedAt != nil || reason != nil || replacedBy != nil {
			return nil, NewInternalError(nil)
		}
	case JoinLinkStatusRevoked:
		if revokedBy == nil || !revokedBy.IsValid() || revokedAt == nil || revokedAt.IsZero() || reason == nil {
			return nil, NewInternalError(nil)
		}
		switch *reason {
		case RevocationReasonRotated:
			if replacedBy == nil || !replacedBy.IsValid() || *replacedBy == id {
				return nil, NewInternalError(nil)
			}
		case RevocationReasonManual, RevocationReasonAdminTransfer, RevocationReasonApplicationClosure:
			if replacedBy != nil {
				return nil, NewInternalError(nil)
			}
		default:
			return nil, NewInternalError(nil)
		}
	default:
		return nil, NewInternalError(nil)
	}
	link := &ApplicationTesterJoinLink{id, app, hash, status, createdBy, createdAt.UTC(), copyValue(revokedBy), copyValue(revokedAt), copyValue(reason), copyValue(replacedBy)}
	if link.revokedAt != nil {
		v := link.revokedAt.UTC()
		link.revokedAt = &v
	}
	return link, nil
}
func (l *ApplicationTesterJoinLink) JoinLinkID() ApplicationTesterJoinLinkID { return l.joinLinkID }
func (l *ApplicationTesterJoinLink) ApplicationID() shared.ApplicationID     { return l.applicationID }
func (l *ApplicationTesterJoinLink) TokenHash() TesterJoinTokenHash          { return l.tokenHash }
func (l *ApplicationTesterJoinLink) Status() JoinLinkStatus                  { return l.status }
func (l *ApplicationTesterJoinLink) CreatedBy() shared.AuthID                { return l.createdBy }
func (l *ApplicationTesterJoinLink) CreatedAt() time.Time                    { return l.createdAt }
func (l *ApplicationTesterJoinLink) RevokedBy() *shared.AuthID               { return copyValue(l.revokedBy) }
func (l *ApplicationTesterJoinLink) RevokedAt() *time.Time                   { return copyValue(l.revokedAt) }
func (l *ApplicationTesterJoinLink) RevocationReason() *RevocationReason {
	return copyValue(l.revocationReason)
}
func (l *ApplicationTesterJoinLink) ReplacedByJoinLinkID() *ApplicationTesterJoinLinkID {
	return copyValue(l.replacedByJoinLinkID)
}
func (l ApplicationTesterJoinLink) String() string {
	return "ApplicationTesterJoinLink{credential: [redacted]}"
}
func (l ApplicationTesterJoinLink) GoString() string { return l.String() }
func (l *ApplicationTesterJoinLink) Rotate(replacement ApplicationTesterJoinLinkID, by shared.AuthID, at time.Time) (*ApplicationTesterJoinLink, error) {
	if l == nil || l.status != JoinLinkStatusActive {
		return nil, ErrApplicationTesterJoinLinkChanged
	}
	reason := RevocationReasonRotated
	return RestoreTesterJoinLink(l.joinLinkID, l.applicationID, l.tokenHash, JoinLinkStatusRevoked, l.createdBy, l.createdAt, &by, &at, &reason, &replacement)
}
func copyValue[T any](v *T) *T {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
func copyLink(link *ApplicationTesterJoinLink) *ApplicationTesterJoinLink {
	if link == nil {
		return nil
	}
	v := *link
	v.revokedBy = copyValue(link.revokedBy)
	v.revokedAt = copyValue(link.revokedAt)
	v.revocationReason = copyValue(link.revocationReason)
	v.replacedByJoinLinkID = copyValue(link.replacedByJoinLinkID)
	return &v
}

type TesterJoinLinkCandidate struct {
	applicationID shared.ApplicationID
	activeLink    *ApplicationTesterJoinLink
}

func NewTesterJoinLinkCandidate(app shared.ApplicationID, active *ApplicationTesterJoinLink) (*TesterJoinLinkCandidate, error) {
	if !app.IsValid() || active != nil && (active.ApplicationID() != app || active.Status() != JoinLinkStatusActive) {
		return nil, NewInternalError(nil)
	}
	return &TesterJoinLinkCandidate{app, copyLink(active)}, nil
}
func (c *TesterJoinLinkCandidate) ApplicationID() shared.ApplicationID { return c.applicationID }
func (c *TesterJoinLinkCandidate) ActiveLink() *ApplicationTesterJoinLink {
	return copyLink(c.activeLink)
}
func (c *TesterJoinLinkCandidate) EnsureExpected(expected *ApplicationTesterJoinLinkID) error {
	if c == nil {
		return NewInternalError(nil)
	}
	if expected != nil && !expected.IsValid() {
		return ErrInvalidTesterJoinLinkId
	}
	if expected == nil && c.activeLink != nil {
		return ErrApplicationTesterJoinLinkAlreadyExists
	}
	if expected != nil && c.activeLink == nil {
		return ErrApplicationTesterJoinLinkNotFound
	}
	if expected != nil && *expected != c.activeLink.JoinLinkID() {
		return ErrApplicationTesterJoinLinkChanged
	}
	return nil
}

type CreateOrRotateTesterJoinLinkResult struct {
	joinLink           ApplicationTesterJoinLink
	replacedJoinLinkID *ApplicationTesterJoinLinkID
}

func NewCreateOrRotateTesterJoinLinkResult(link *ApplicationTesterJoinLink, replaced *ApplicationTesterJoinLinkID) (*CreateOrRotateTesterJoinLinkResult, error) {
	if link == nil || link.Status() != JoinLinkStatusActive || replaced != nil && (!replaced.IsValid() || *replaced == link.JoinLinkID()) {
		return nil, NewInternalError(nil)
	}
	return &CreateOrRotateTesterJoinLinkResult{*copyLink(link), copyValue(replaced)}, nil
}
func (r *CreateOrRotateTesterJoinLinkResult) JoinLink() *ApplicationTesterJoinLink {
	return copyLink(&r.joinLink)
}
func (r *CreateOrRotateTesterJoinLinkResult) ReplacedJoinLinkID() *ApplicationTesterJoinLinkID {
	return copyValue(r.replacedJoinLinkID)
}
func (r CreateOrRotateTesterJoinLinkResult) String() string {
	return "CreateOrRotateTesterJoinLinkResult{credential: [redacted]}"
}
func (r CreateOrRotateTesterJoinLinkResult) GoString() string { return r.String() }
