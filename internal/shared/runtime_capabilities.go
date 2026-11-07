package shared

import (
	"errors"
	"regexp"
	"slices"
)

var (
	runtimeCapabilityPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*\.v[1-9][0-9]*$`)
	ErrInvalidRuntimeCapabilities = errors.New("invalid runtime capabilities")
)

func ValidRuntimeCapabilityName(value string) bool {
	return runtimeCapabilityPattern.MatchString(value)
}

// NormalizeRuntimeCapabilities shares the established host-set grammar without
// coupling the script and catalog capabilities to each other's domain packages.
func NormalizeRuntimeCapabilities(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !ValidRuntimeCapabilityName(value) {
			return nil, ErrInvalidRuntimeCapabilities
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}
