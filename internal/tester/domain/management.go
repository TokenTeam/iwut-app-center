package domain

import (
	"iwut-app-center/internal/shared"
	"time"
)

type MembershipQueryStatus string
type JoinLinkQueryStatus string

const (
	MembershipQueryActive  MembershipQueryStatus = "ACTIVE"
	MembershipQueryRemoved MembershipQueryStatus = "REMOVED"
	MembershipQueryAll     MembershipQueryStatus = "ALL"
	JoinLinkQueryActive    JoinLinkQueryStatus   = "ACTIVE"
	JoinLinkQueryRevoked   JoinLinkQueryStatus   = "REVOKED"
	JoinLinkQueryAll       JoinLinkQueryStatus   = "ALL"
)

// Management projections intentionally exclude credentials and historical actors.
type ManagedTesterMembership struct {
	MembershipID        ApplicationTesterMembershipID
	ApplicationID       shared.ApplicationID
	TesterAuthID        shared.AuthID
	Status              MembershipStatus
	JoinedViaJoinLinkID ApplicationTesterJoinLinkID
	JoinedAt            time.Time
	RemovedAt           *time.Time
}
type ManagedTesterJoinLink struct {
	JoinLinkID           ApplicationTesterJoinLinkID
	ApplicationID        shared.ApplicationID
	Status               JoinLinkStatus
	CreatedAt            time.Time
	RevokedAt            *time.Time
	RevocationReason     *RevocationReason
	ReplacedByJoinLinkID *ApplicationTesterJoinLinkID
}
type TesterManagement struct {
	ApplicationID     shared.ApplicationID
	ActiveTesterCount int32
	TesterLimit       int32
	ActiveJoinLink    *ManagedTesterJoinLink
	AsOf              time.Time
}
type TesterMembershipPage struct {
	Items         []ManagedTesterMembership
	NextPageToken string
	AsOf          time.Time
}
type TesterJoinLinkPage struct {
	Items         []ManagedTesterJoinLink
	NextPageToken string
	AsOf          time.Time
}

const (
	ErrorCodeInvalidApplicationTesterManagementQuery      ErrorCode = "InvalidApplicationTesterManagementQuery"
	ErrorCodeInvalidApplicationTesterManagementPageToken  ErrorCode = "InvalidApplicationTesterManagementPageToken"
	ErrorCodeApplicationTesterManagementNotFound          ErrorCode = "ApplicationTesterManagementNotFound"
	ErrorCodeApplicationTesterManagementStateInconsistent ErrorCode = "ApplicationTesterManagementStateInconsistent"
	ErrorCodeApplicationTesterManagementUnavailable       ErrorCode = "ApplicationTesterManagementUnavailable"
)

var (
	ErrInvalidApplicationTesterManagementQuery      = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationTesterManagementQuery, "invalid application tester management query", nil)
	ErrInvalidApplicationTesterManagementPageToken  = newError(ErrorCategoryValidation, ErrorCodeInvalidApplicationTesterManagementPageToken, "invalid application tester management page token", nil)
	ErrApplicationTesterManagementNotFound          = newError(ErrorCategoryNotFound, ErrorCodeApplicationTesterManagementNotFound, "application tester management resource not found", nil)
	ErrApplicationTesterManagementStateInconsistent = newError(ErrorCategoryInternal, ErrorCodeApplicationTesterManagementStateInconsistent, "application tester management state inconsistent", nil)
	ErrApplicationTesterManagementUnavailable       = newError(ErrorCategoryDependencyUnavailable, ErrorCodeApplicationTesterManagementUnavailable, "application tester management unavailable", nil)
)
