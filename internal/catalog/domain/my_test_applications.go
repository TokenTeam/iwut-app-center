package domain

import (
	"iwut-app-center/internal/shared"
	"time"
)

type MyTestAvailability string

const (
	MyTestReady                    MyTestAvailability = "READY"
	MyTestProfileRequired          MyTestAvailability = "PROFILE_REQUIRED"
	MyTestTargetUnavailable        MyTestAvailability = "TEST_TARGET_UNAVAILABLE"
	MyTestCapabilitiesInsufficient MyTestAvailability = "HOST_CAPABILITIES_INSUFFICIENT"
)

const (
	ErrorCodeInvalidMyTestApplicationQuery      ErrorCode = "InvalidMyTestApplicationQuery"
	ErrorCodeInvalidMyTestApplicationPageToken  ErrorCode = "InvalidMyTestApplicationPageToken"
	ErrorCodeMyTestApplicationNotFound          ErrorCode = "MyTestApplicationNotFound"
	ErrorCodeMyTestApplicationStateInconsistent ErrorCode = "MyTestApplicationStateInconsistent"
	ErrorCodeMyTestApplicationUnavailable       ErrorCode = "MyTestApplicationUnavailable"
)

var (
	ErrInvalidMyTestApplicationQuery      = newError(ErrorCategoryValidation, ErrorCodeInvalidMyTestApplicationQuery, "test participation query is invalid")
	ErrInvalidMyTestApplicationPageToken  = newError(ErrorCategoryValidation, ErrorCodeInvalidMyTestApplicationPageToken, "test participation page token is invalid")
	ErrMyTestApplicationNotFound          = newError(ErrorCategoryNotFound, ErrorCodeMyTestApplicationNotFound, "test participation not found")
	ErrMyTestApplicationStateInconsistent = newError(ErrorCategoryInternal, ErrorCodeMyTestApplicationStateInconsistent, "test participation state is inconsistent")
	ErrMyTestApplicationUnavailable       = newError(ErrorCategoryDependencyUnavailable, ErrorCodeMyTestApplicationUnavailable, "test participation query is unavailable")
)

// OwnTestMembership deliberately contains no identity, source link or actor.
type MyTestMembershipID string

func (id MyTestMembershipID) String() string { return string(id) }
func (id MyTestMembershipID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type OwnTestMembership struct {
	MembershipID MyTestMembershipID
	JoinedAt     time.Time
}
type MyTestApplicationItem struct {
	ApplicationID       shared.ApplicationID
	Membership          OwnTestMembership
	Profile             *PublicApplicationProfile
	Availability        MyTestAvailability
	LaunchTarget        *TestLaunchDescriptor
	MissingCapabilities []CapabilityName
}

func NewMyTestApplicationItem(app shared.ApplicationID, membership OwnTestMembership, profile *PublicApplicationProfile, state MyTestAvailability, target *TestLaunchDescriptor, missing []CapabilityName) (*MyTestApplicationItem, error) {
	if !app.IsValid() || !membership.MembershipID.IsValid() || membership.JoinedAt.IsZero() || membership.JoinedAt.Year() < 1 || membership.JoinedAt.Year() > 9999 {
		return nil, ErrMyTestApplicationStateInconsistent
	}
	switch state {
	case MyTestProfileRequired:
		if profile != nil || target != nil || len(missing) != 0 {
			return nil, ErrMyTestApplicationStateInconsistent
		}
	case MyTestTargetUnavailable:
		if profile == nil || target != nil || len(missing) != 0 {
			return nil, ErrMyTestApplicationStateInconsistent
		}
	case MyTestCapabilitiesInsufficient:
		if profile == nil || target != nil || len(missing) == 0 || !strictlySortedUnique(missing) {
			return nil, ErrMyTestApplicationStateInconsistent
		}
	case MyTestReady:
		if profile == nil || target == nil || target.ApplicationID() != app || len(missing) != 0 {
			return nil, ErrMyTestApplicationStateInconsistent
		}
	default:
		return nil, ErrMyTestApplicationStateInconsistent
	}
	return &MyTestApplicationItem{app, membership, profile, state, target, append([]CapabilityName{}, missing...)}, nil
}

type MyTestApplicationPage struct {
	Items         []*MyTestApplicationItem
	NextPageToken string
	AsOf          time.Time
}
type MyTestApplicationDetail struct {
	Item *MyTestApplicationItem
	AsOf time.Time
}
