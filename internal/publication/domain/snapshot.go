package domain

import (
	"slices"
	"unicode/utf8"
)

type ApplicationVersionReviewSnapshot struct {
	versionLabel              string
	launchURL                 LaunchURL
	rpcAPIMinVersion          int32
	rpcAPIMaxVersionExclusive int32
	requiredCapabilities      []string
	requiredScopes            []ScopeName
	optionalScopes            []ScopeName
}

func NewApplicationVersionReviewSnapshot(
	versionLabel string,
	launchURL LaunchURL,
	rpcAPIMinVersion int32,
	rpcAPIMaxVersionExclusive int32,
	requiredCapabilities []string,
	requiredScopes []ScopeName,
	optionalScopes []ScopeName,
) (*ApplicationVersionReviewSnapshot, error) {
	if versionLabel == "" || !utf8.ValidString(versionLabel) || launchURL == "" ||
		rpcAPIMinVersion < 1 || rpcAPIMaxVersionExclusive <= rpcAPIMinVersion ||
		!strictlySortedUnique(requiredCapabilities) || !strictlySortedUnique(requiredScopes) ||
		!strictlySortedUnique(optionalScopes) || hasOverlap(requiredScopes, optionalScopes) {
		return nil, NewInternalError(nil)
	}
	return &ApplicationVersionReviewSnapshot{
		versionLabel:              versionLabel,
		launchURL:                 launchURL,
		rpcAPIMinVersion:          rpcAPIMinVersion,
		rpcAPIMaxVersionExclusive: rpcAPIMaxVersionExclusive,
		requiredCapabilities:      append([]string{}, requiredCapabilities...),
		requiredScopes:            append([]ScopeName{}, requiredScopes...),
		optionalScopes:            append([]ScopeName{}, optionalScopes...),
	}, nil
}

func (snapshot ApplicationVersionReviewSnapshot) VersionLabel() string { return snapshot.versionLabel }
func (snapshot ApplicationVersionReviewSnapshot) LaunchURL() LaunchURL { return snapshot.launchURL }
func (snapshot ApplicationVersionReviewSnapshot) RPCAPIMinVersion() int32 {
	return snapshot.rpcAPIMinVersion
}
func (snapshot ApplicationVersionReviewSnapshot) RPCAPIMaxVersionExclusive() int32 {
	return snapshot.rpcAPIMaxVersionExclusive
}
func (snapshot ApplicationVersionReviewSnapshot) RequiredCapabilities() []string {
	return append([]string{}, snapshot.requiredCapabilities...)
}
func (snapshot ApplicationVersionReviewSnapshot) RequiredScopes() []ScopeName {
	return append([]ScopeName{}, snapshot.requiredScopes...)
}
func (snapshot ApplicationVersionReviewSnapshot) OptionalScopes() []ScopeName {
	return append([]ScopeName{}, snapshot.optionalScopes...)
}

// Equal reports whether two snapshots carry exactly the same reviewed content.
func (snapshot ApplicationVersionReviewSnapshot) Equal(other ApplicationVersionReviewSnapshot) bool {
	return snapshot.versionLabel == other.versionLabel &&
		snapshot.launchURL == other.launchURL &&
		snapshot.rpcAPIMinVersion == other.rpcAPIMinVersion &&
		snapshot.rpcAPIMaxVersionExclusive == other.rpcAPIMaxVersionExclusive &&
		slices.Equal(snapshot.requiredCapabilities, other.requiredCapabilities) &&
		slices.Equal(snapshot.requiredScopes, other.requiredScopes) &&
		slices.Equal(snapshot.optionalScopes, other.optionalScopes)
}

func strictlySortedUnique[T ~string](values []T) bool {
	return values != nil && slices.IsSorted(values) && len(slices.Compact(append([]T{}, values...))) == len(values)
}

func hasOverlap[T ~string](left, right []T) bool {
	seen := make(map[T]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := seen[value]; ok {
			return true
		}
	}
	return false
}
