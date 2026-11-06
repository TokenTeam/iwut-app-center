package shared

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"
)

const MaxScriptReferences = 16

var (
	ErrInvalidScriptReferences  = errors.New("invalid script references")
	ErrScriptDependencyPending  = errors.New("script dependency pending")
	ErrScriptDependencyRejected = errors.New("script dependency rejected")
)

type ScriptReference struct {
	ResourceID string
	VersionID  string
}

func NormalizeScriptReferences(input []ScriptReference) ([]ScriptReference, error) {
	if len(input) > MaxScriptReferences {
		return nil, ErrInvalidScriptReferences
	}
	out := slices.Clone(input)
	resources := make(map[string]bool, len(out))
	versions := make(map[string]bool, len(out))
	for i, item := range out {
		resource, version := strings.ToLower(item.ResourceID), strings.ToLower(item.VersionID)
		if !IsUUIDv7(resource) || !IsUUIDv7(version) || resources[resource] || versions[version] {
			return nil, ErrInvalidScriptReferences
		}
		resources[resource], versions[version] = true, true
		out[i] = ScriptReference{ResourceID: resource, VersionID: version}
	}
	slices.SortFunc(out, func(a, b ScriptReference) int { return strings.Compare(a.VersionID, b.VersionID) })
	return out, nil
}

type FrozenScriptReference struct {
	ScriptReference
	Format, Digest, ReviewID string
}

func (ref FrozenScriptReference) Valid() bool {
	return ref.Format == "json" && ValidScriptDigest(ref.Digest) && IsUUIDv7(ref.ReviewID) && ref.ReviewID == strings.ToLower(ref.ReviewID)
}

func ValidScriptDigest(digest string) bool {
	data, err := hex.DecodeString(digest)
	return err == nil && len(data) == 32 && digest == strings.ToLower(digest)
}

type ScriptApprovalFact struct {
	VersionID, Digest, DecisionID string
}

type ScriptDependencySummary struct {
	ScriptReference
	Format, Digest, State, ReviewID, DecisionID string
}
