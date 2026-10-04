package domain

import (
	"iwut-app-center/internal/shared"
	"regexp"
	"slices"
	"unicode/utf8"
)

type CapabilityName string

func (n CapabilityName) String() string { return string(n) }

var capabilityNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*\.v[1-9][0-9]*$`)

// NormalizeHostCapabilities interprets the host declaration as a set. It does
// not impose the duplicate rejection rule used for persisted requirements.
func NormalizeHostCapabilities(values []string) ([]CapabilityName, error) {
	result := make([]CapabilityName, 0, len(values))
	for _, value := range values {
		if !capabilityNamePattern.MatchString(value) {
			return nil, ErrInvalidHostCapabilities
		}
		result = append(result, CapabilityName(value))
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}
func MissingCapabilities(required, host []CapabilityName) []CapabilityName {
	available := make(map[CapabilityName]struct{}, len(host))
	for _, v := range host {
		available[v] = struct{}{}
	}
	missing := make([]CapabilityName, 0, len(required))
	for _, v := range required {
		if _, ok := available[v]; !ok {
			missing = append(missing, v)
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing)
}

type LaunchChannel string

const (
	LaunchChannelTest   LaunchChannel = "TEST"
	LaunchChannelGrey   LaunchChannel = "GREY"
	LaunchChannelStable LaunchChannel = "STABLE"
)

func (channel LaunchChannel) IsValid() bool {
	switch channel {
	case LaunchChannelTest, LaunchChannelGrey, LaunchChannelStable:
		return true
	default:
		return false
	}
}

// LaunchTargetDescriptor is the one resolved TEST, GREY or STABLE target for
// a logical read snapshot. It carries neither candidate-set nor cohort facts.
type LaunchTargetDescriptor struct {
	applicationID                               shared.ApplicationID
	publicationID                               string
	publicationRevision                         int64
	channel                                     LaunchChannel
	rpcAPIMajor                                 int32
	versionID, versionLabel, launchURL          string
	rpcAPIMinVersion, rpcAPIMaxVersionExclusive int32
	requiredCapabilities                        []CapabilityName
	requiredScopes, optionalScopes              []string
}

func NewLaunchTargetDescriptor(applicationID shared.ApplicationID, publicationID string, publicationRevision int64, channel LaunchChannel, rpcAPIMajor int32, versionID, versionLabel, launchURL string, rpcAPIMinVersion, rpcAPIMaxVersionExclusive int32, requiredCapabilities []CapabilityName, requiredScopes, optionalScopes []string) (*LaunchTargetDescriptor, error) {
	if !validLaunchDescriptor(applicationID, publicationID, publicationRevision, rpcAPIMajor, versionID, versionLabel, launchURL, rpcAPIMinVersion, rpcAPIMaxVersionExclusive, requiredCapabilities, requiredScopes, optionalScopes) || !channel.IsValid() {
		return nil, ErrApplicationRuntimeStateInconsistent
	}
	return &LaunchTargetDescriptor{applicationID, publicationID, publicationRevision, channel, rpcAPIMajor, versionID, versionLabel, launchURL, rpcAPIMinVersion, rpcAPIMaxVersionExclusive, append([]CapabilityName{}, requiredCapabilities...), append([]string{}, requiredScopes...), append([]string{}, optionalScopes...)}, nil
}

func (d LaunchTargetDescriptor) ApplicationID() shared.ApplicationID { return d.applicationID }
func (d LaunchTargetDescriptor) PublicationID() string               { return d.publicationID }
func (d LaunchTargetDescriptor) PublicationRevision() int64          { return d.publicationRevision }
func (d LaunchTargetDescriptor) Channel() LaunchChannel              { return d.channel }
func (d LaunchTargetDescriptor) RPCAPIMajor() int32                  { return d.rpcAPIMajor }
func (d LaunchTargetDescriptor) VersionID() string                   { return d.versionID }
func (d LaunchTargetDescriptor) VersionLabel() string                { return d.versionLabel }
func (d LaunchTargetDescriptor) LaunchURL() string                   { return d.launchURL }
func (d LaunchTargetDescriptor) RPCAPIMinVersion() int32             { return d.rpcAPIMinVersion }
func (d LaunchTargetDescriptor) RPCAPIMaxVersionExclusive() int32 {
	return d.rpcAPIMaxVersionExclusive
}
func (d LaunchTargetDescriptor) RequiredCapabilities() []CapabilityName {
	return append([]CapabilityName{}, d.requiredCapabilities...)
}
func (d LaunchTargetDescriptor) RequiredScopes() []string {
	return append([]string{}, d.requiredScopes...)
}
func (d LaunchTargetDescriptor) OptionalScopes() []string {
	return append([]string{}, d.optionalScopes...)
}

// TestLaunchDescriptor is an immutable result for one consistent read snapshot.
// It is not a lease and carries no membership, approval audit or credentials.
type TestLaunchDescriptor struct {
	applicationID                               shared.ApplicationID
	publicationID                               string
	publicationRevision                         int64
	rpcAPIMajor                                 int32
	versionID, versionLabel, launchURL          string
	rpcAPIMinVersion, rpcAPIMaxVersionExclusive int32
	requiredCapabilities                        []CapabilityName
	requiredScopes, optionalScopes              []string
}

func NewTestLaunchDescriptor(applicationID shared.ApplicationID, publicationID string, publicationRevision int64, rpcAPIMajor int32, versionID, versionLabel, launchURL string, rpcAPIMinVersion, rpcAPIMaxVersionExclusive int32, requiredCapabilities []CapabilityName, requiredScopes, optionalScopes []string) (*TestLaunchDescriptor, error) {
	if !validLaunchDescriptor(applicationID, publicationID, publicationRevision, rpcAPIMajor, versionID, versionLabel, launchURL, rpcAPIMinVersion, rpcAPIMaxVersionExclusive, requiredCapabilities, requiredScopes, optionalScopes) {
		return nil, ErrApplicationTestPublicationInconsistent
	}
	return &TestLaunchDescriptor{applicationID, publicationID, publicationRevision, rpcAPIMajor, versionID, versionLabel, launchURL, rpcAPIMinVersion, rpcAPIMaxVersionExclusive, append([]CapabilityName{}, requiredCapabilities...), append([]string{}, requiredScopes...), append([]string{}, optionalScopes...)}, nil
}

func validLaunchDescriptor(applicationID shared.ApplicationID, publicationID string, publicationRevision int64, rpcAPIMajor int32, versionID, versionLabel, launchURL string, rpcAPIMinVersion, rpcAPIMaxVersionExclusive int32, requiredCapabilities []CapabilityName, requiredScopes, optionalScopes []string) bool {
	if !applicationID.IsValid() || !shared.IsUUIDv7(publicationID) || !shared.IsUUIDv7(versionID) || publicationRevision < 1 || rpcAPIMinVersion < 1 || rpcAPIMaxVersionExclusive <= rpcAPIMinVersion || rpcAPIMajor < rpcAPIMinVersion || rpcAPIMajor >= rpcAPIMaxVersionExclusive || versionLabel == "" || !utf8.ValidString(versionLabel) || launchURL == "" || !strictlySortedUnique(requiredCapabilities) || !strictlySortedUnique(requiredScopes) || !strictlySortedUnique(optionalScopes) {
		return false
	}
	for _, capability := range requiredCapabilities {
		if !capabilityNamePattern.MatchString(string(capability)) {
			return false
		}
	}
	for _, required := range requiredScopes {
		if slices.Contains(optionalScopes, required) {
			return false
		}
	}
	return true
}
func strictlySortedUnique[T ~string](values []T) bool {
	if !slices.IsSorted(values) {
		return false
	}
	for i, v := range values {
		if v == "" || (i > 0 && values[i-1] == v) {
			return false
		}
	}
	return true
}
func (d TestLaunchDescriptor) ApplicationID() shared.ApplicationID { return d.applicationID }
func (d TestLaunchDescriptor) PublicationID() string               { return d.publicationID }
func (d TestLaunchDescriptor) PublicationRevision() int64          { return d.publicationRevision }
func (d TestLaunchDescriptor) RPCAPIMajor() int32                  { return d.rpcAPIMajor }
func (d TestLaunchDescriptor) VersionID() string                   { return d.versionID }
func (d TestLaunchDescriptor) VersionLabel() string                { return d.versionLabel }
func (d TestLaunchDescriptor) LaunchURL() string                   { return d.launchURL }
func (d TestLaunchDescriptor) RPCAPIMinVersion() int32             { return d.rpcAPIMinVersion }
func (d TestLaunchDescriptor) RPCAPIMaxVersionExclusive() int32    { return d.rpcAPIMaxVersionExclusive }
func (d TestLaunchDescriptor) RequiredCapabilities() []CapabilityName {
	return append([]CapabilityName{}, d.requiredCapabilities...)
}
func (d TestLaunchDescriptor) RequiredScopes() []string {
	return append([]string{}, d.requiredScopes...)
}
func (d TestLaunchDescriptor) OptionalScopes() []string {
	return append([]string{}, d.optionalScopes...)
}
