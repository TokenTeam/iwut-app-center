package domain

import (
	"errors"
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

const (
	testApplicationID shared.ApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"
	testRevisionID    FilterRevisionID     = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c22"
)

func testPredicate(t *testing.T) Rule {
	t.Helper()
	value, err := NewStringScalar("CN")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := NewPredicate("profile.country", PredicateOperatorEQ, &value)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func TestRuleValidation_BR_FLT_004_CoversTypesOperatorsAndBounds(t *testing.T) {
	t.Parallel()

	date, err := NewDateScalar("2024-02-29")
	if err != nil {
		t.Fatalf("valid date: %v", err)
	}
	integer := NewIntegerScalar(18)
	boolean := NewBooleanScalar(true)
	text, _ := NewStringScalar("student")
	for _, value := range []Scalar{date, integer, boolean, text} {
		if _, err := NewPredicate("profile.value", PredicateOperatorEQ, &value); err != nil {
			t.Fatalf("EQ scalar %s: %v", value.Kind(), err)
		}
	}
	for _, operator := range []PredicateOperator{PredicateOperatorLT, PredicateOperatorLTE, PredicateOperatorGT, PredicateOperatorGTE} {
		if _, err := NewPredicate("profile.age", operator, &integer); err != nil {
			t.Fatalf("ordered integer %s: %v", operator, err)
		}
	}
	if _, err := NewPredicate("profile.name", PredicateOperatorLT, &text); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("ordered string error = %v", err)
	}
	if _, err := NewPredicate("Profile.country", PredicateOperatorEQ, &text); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("invalid key error = %v", err)
	}
	if _, err := NewPredicate("profile.country", PredicateOperatorExists, &text); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("EXISTS with value error = %v", err)
	}
	if _, err := NewPredicate("profile.country", PredicateOperatorExists, nil); err != nil {
		t.Fatalf("EXISTS without value: %v", err)
	}
	if _, err := NewDateScalar("2023-02-29"); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("invalid date error = %v", err)
	}
	if _, err := NewStringScalar(strings.Repeat("界", MaxStringRunes+1)); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("oversize string error = %v", err)
	}

	rule := testPredicate(t)
	for i := 0; i < MaxRuleDepth-1; i++ {
		rule, err = NewGroup(GroupOperatorNot, []Rule{rule})
		if err != nil {
			t.Fatalf("depth %d: %v", i+2, err)
		}
	}
	if _, err := NewGroup(GroupOperatorNot, []Rule{rule}); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("depth overflow error = %v", err)
	}
	if _, err := NewGroup(GroupOperatorNot, []Rule{testPredicate(t), testPredicate(t)}); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("non-unary NOT error = %v", err)
	}
	if _, err := NewGroup(GroupOperatorAll, make([]Rule, MaxGroupChildren+1)); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("child overflow error = %v", err)
	}
	groups := make([]Rule, 4)
	for i := range groups {
		leaves := make([]Rule, 31)
		for j := range leaves {
			leaves[j] = testPredicate(t)
		}
		groups[i], err = NewGroup(GroupOperatorAll, leaves)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewGroup(GroupOperatorAll, groups); !errors.Is(err, ErrInvalidApplicationFilter) {
		t.Fatalf("node overflow error = %v", err)
	}
}

func TestApplicationFilterLifecycle_BR_FLT_002_003_008_ImmutableRuleCopies(t *testing.T) {
	t.Parallel()

	filter, err := NewDefaultApplicationFilter(testApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Revision() != 0 || filter.NextSequence() != 1 || !filter.IsAllowAll() || filter.CurrentRevision() != nil {
		t.Fatalf("default filter = revision:%d next:%d mode:%s", filter.Revision(), filter.NextSequence(), filter.EffectiveMode())
	}

	child := testPredicate(t)
	rule, err := NewGroup(GroupOperatorAll, []Rule{child})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	next, published, err := filter.PublishRule(testRevisionID, rule, "admin", now)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Revision() != 0 || next.Revision() != 1 || next.NextSequence() != 2 || published.Sequence() != 1 || !next.MatchesRule(rule) {
		t.Fatalf("published lifecycle mismatch")
	}
	_, copyChildren, _ := rule.Group()
	copyChildren[0] = Rule{}
	if !next.MatchesRule(rule) {
		t.Fatal("mutating an accessor copy changed the stored rule")
	}

	cleared, allowAll, err := next.PublishAllowAll("01890f5a-e810-7cc3-98c8-8c6d5d8b4c23", "admin", now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Revision() != 2 || cleared.NextSequence() != 3 || !cleared.IsAllowAll() || allowAll.Mode() != RevisionModeAllowAll || allowAll.Rule() != nil {
		t.Fatalf("clear lifecycle mismatch")
	}
}
