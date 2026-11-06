package shared

import (
	"errors"
	"fmt"
	"testing"
)

func TestNormalizeScriptReferences_BRSDP001(t *testing.T) {
	input := make([]ScriptReference, MaxScriptReferences)
	for i := range input {
		input[i] = ScriptReference{
			ResourceID: fmt.Sprintf("01890f47-0000-7000-8000-%012x", i+1),
			VersionID:  fmt.Sprintf("01890f48-0000-7000-8000-%012x", MaxScriptReferences-i),
		}
	}
	normalized, err := NormalizeScriptReferences(input)
	if err != nil || len(normalized) != MaxScriptReferences || normalized[0] != input[len(input)-1] {
		t.Fatalf("the 16-item boundary did not normalize in version order: %v", err)
	}
	if empty, err := NormalizeScriptReferences([]ScriptReference{}); err != nil || len(empty) != 0 {
		t.Fatalf("empty references are invalid: %v", err)
	}
	for _, invalid := range [][]ScriptReference{
		append(input, ScriptReference{ResourceID: "01890f47-0000-7000-8000-000000000099", VersionID: "01890f48-0000-7000-8000-000000000099"}),
		{input[0], input[0]},
		{input[0], {ResourceID: input[0].ResourceID, VersionID: input[1].VersionID}},
		{input[0], {ResourceID: input[1].ResourceID, VersionID: input[0].VersionID}},
	} {
		if _, err := NormalizeScriptReferences(invalid); !errors.Is(err, ErrInvalidScriptReferences) {
			t.Fatalf("invalid references accepted: %v", err)
		}
	}
}
