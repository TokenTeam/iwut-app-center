package domain

import (
	"sort"
	"strings"
)

type CapabilityName string

type CapabilitySet struct {
	values []CapabilityName
}

func NewCapabilitySet(values []string) (CapabilitySet, error) {
	normalized := make([]CapabilityName, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isCapabilityName(value) {
			return CapabilitySet{}, ErrInvalidRequiredCapability
		}
		if _, exists := seen[value]; exists {
			return CapabilitySet{}, ErrInvalidRequiredCapability
		}
		seen[value] = struct{}{}
		normalized = append(normalized, CapabilityName(value))
	}
	sort.Slice(normalized, func(left, right int) bool {
		return unicodeCodePointLess(string(normalized[left]), string(normalized[right]))
	})
	return CapabilitySet{values: normalized}, nil
}

func (set CapabilitySet) Values() []CapabilityName {
	return append([]CapabilityName{}, set.values...)
}

func (set CapabilitySet) Strings() []string {
	values := make([]string, len(set.values))
	for index, value := range set.values {
		values[index] = string(value)
	}
	return values
}

func (set CapabilitySet) valid() bool {
	values := set.Strings()
	validated, err := NewCapabilitySet(values)
	return err == nil && equalCapabilityNames(validated.values, set.values)
}

func isCapabilityName(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		if len(part) == 0 || part[0] < 'a' || part[0] > 'z' {
			return false
		}
		for index := 1; index < len(part); index++ {
			char := part[index]
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') {
				return false
			}
		}
	}

	version := parts[len(parts)-1]
	if len(version) < 2 || version[0] != 'v' || version[1] < '1' || version[1] > '9' {
		return false
	}
	for index := 1; index < len(version); index++ {
		char := version[index]
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func equalCapabilityNames(left, right []CapabilityName) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
