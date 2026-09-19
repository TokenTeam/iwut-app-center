package domain

import "sort"

type ScopeName string

type ScopeRequest struct {
	required []ScopeName
	optional []ScopeName
}

func NewScopeRequest(requiredValues, optionalValues []string) (ScopeRequest, error) {
	required, requiredSet, ok := normalizeScopes(requiredValues)
	if !ok {
		return ScopeRequest{}, ErrInvalidApplicationScope
	}
	optional, optionalSet, ok := normalizeScopes(optionalValues)
	if !ok {
		return ScopeRequest{}, ErrInvalidApplicationScope
	}
	for value := range requiredSet {
		if _, exists := optionalSet[value]; exists {
			return ScopeRequest{}, ErrInvalidApplicationScope
		}
	}
	return ScopeRequest{required: required, optional: optional}, nil
}

func (request ScopeRequest) Required() []ScopeName {
	return append([]ScopeName{}, request.required...)
}

func (request ScopeRequest) Optional() []ScopeName {
	return append([]ScopeName{}, request.optional...)
}

func (request ScopeRequest) All() []ScopeName {
	values := append(request.Required(), request.optional...)
	sort.Slice(values, func(left, right int) bool {
		return unicodeCodePointLess(string(values[left]), string(values[right]))
	})
	return values
}

func (request ScopeRequest) valid() bool {
	required := scopeStrings(request.required)
	optional := scopeStrings(request.optional)
	validated, err := NewScopeRequest(required, optional)
	return err == nil && equalScopeNames(validated.required, request.required) && equalScopeNames(validated.optional, request.optional)
}

func normalizeScopes(values []string) ([]ScopeName, map[string]struct{}, bool) {
	normalized := make([]ScopeName, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return nil, nil, false
		}
		seen[value] = struct{}{}
		normalized = append(normalized, ScopeName(value))
	}
	sort.Slice(normalized, func(left, right int) bool {
		return unicodeCodePointLess(string(normalized[left]), string(normalized[right]))
	})
	return normalized, seen, true
}

func scopeStrings(values []ScopeName) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func equalScopeNames(left, right []ScopeName) bool {
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
