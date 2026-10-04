package domain

import (
	"regexp"
	"time"
	"unicode/utf8"
)

const (
	SchemaVersion    = "profile-filter-v1"
	MaxRuleNodes     = 128
	MaxRuleDepth     = 8
	MaxGroupChildren = 32
	MaxStringRunes   = 4096
)

type RuleKind string
type GroupOperator string
type PredicateOperator string
type ScalarKind string

const (
	RuleKindGroup     RuleKind = "GROUP"
	RuleKindPredicate RuleKind = "PREDICATE"

	GroupOperatorAll GroupOperator = "ALL"
	GroupOperatorAny GroupOperator = "ANY"
	GroupOperatorNot GroupOperator = "NOT"

	PredicateOperatorEQ        PredicateOperator = "EQ"
	PredicateOperatorNE        PredicateOperator = "NE"
	PredicateOperatorLT        PredicateOperator = "LT"
	PredicateOperatorLTE       PredicateOperator = "LTE"
	PredicateOperatorGT        PredicateOperator = "GT"
	PredicateOperatorGTE       PredicateOperator = "GTE"
	PredicateOperatorExists    PredicateOperator = "EXISTS"
	PredicateOperatorNotExists PredicateOperator = "NOT_EXISTS"

	ScalarKindString  ScalarKind = "STRING"
	ScalarKindInteger ScalarKind = "INTEGER"
	ScalarKindBoolean ScalarKind = "BOOLEAN"
	ScalarKindDate    ScalarKind = "DATE"
)

var fieldKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

type Scalar struct {
	kind         ScalarKind
	stringValue  string
	integerValue int32
	booleanValue bool
}

func NewStringScalar(value string) (Scalar, error) {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > MaxStringRunes {
		return Scalar{}, ErrInvalidApplicationFilter
	}
	return Scalar{kind: ScalarKindString, stringValue: value}, nil
}
func NewIntegerScalar(value int32) Scalar {
	return Scalar{kind: ScalarKindInteger, integerValue: value}
}
func NewBooleanScalar(value bool) Scalar {
	return Scalar{kind: ScalarKindBoolean, booleanValue: value}
}
func NewDateScalar(value string) (Scalar, error) {
	if len(value) != len("2006-01-02") || value[:4] == "0000" {
		return Scalar{}, ErrInvalidApplicationFilter
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil || parsed.Format("2006-01-02") != value {
		return Scalar{}, ErrInvalidApplicationFilter
	}
	return Scalar{kind: ScalarKindDate, stringValue: value}, nil
}
func (s Scalar) Kind() ScalarKind    { return s.kind }
func (s Scalar) StringValue() string { return s.stringValue }
func (s Scalar) IntegerValue() int32 { return s.integerValue }
func (s Scalar) BooleanValue() bool  { return s.booleanValue }

type Predicate struct {
	fieldKey string
	operator PredicateOperator
	value    *Scalar
}

type Rule struct {
	kind          RuleKind
	groupOperator GroupOperator
	children      []Rule
	predicate     Predicate
}

func NewGroup(operator GroupOperator, children []Rule) (Rule, error) {
	validCount := len(children) >= 1 && len(children) <= MaxGroupChildren
	if operator == GroupOperatorNot {
		validCount = len(children) == 1
	}
	if (operator != GroupOperatorAll && operator != GroupOperatorAny && operator != GroupOperatorNot) || !validCount {
		return Rule{}, ErrInvalidApplicationFilter
	}
	result := Rule{kind: RuleKindGroup, groupOperator: operator, children: cloneRules(children)}
	if nodes, depth, ok := result.shape(); !ok || nodes > MaxRuleNodes || depth > MaxRuleDepth {
		return Rule{}, ErrInvalidApplicationFilter
	}
	return result, nil
}

func NewPredicate(fieldKey string, operator PredicateOperator, value *Scalar) (Rule, error) {
	if len(fieldKey) < 1 || len(fieldKey) > 128 || !fieldKeyPattern.MatchString(fieldKey) {
		return Rule{}, ErrInvalidApplicationFilter
	}
	switch operator {
	case PredicateOperatorExists, PredicateOperatorNotExists:
		if value != nil {
			return Rule{}, ErrInvalidApplicationFilter
		}
	case PredicateOperatorEQ, PredicateOperatorNE:
		if value == nil || !validScalar(*value) {
			return Rule{}, ErrInvalidApplicationFilter
		}
	case PredicateOperatorLT, PredicateOperatorLTE, PredicateOperatorGT, PredicateOperatorGTE:
		if value == nil || (value.kind != ScalarKindInteger && value.kind != ScalarKindDate) || !validScalar(*value) {
			return Rule{}, ErrInvalidApplicationFilter
		}
	default:
		return Rule{}, ErrInvalidApplicationFilter
	}
	var copyValue *Scalar
	if value != nil {
		v := *value
		copyValue = &v
	}
	return Rule{kind: RuleKindPredicate, predicate: Predicate{fieldKey: fieldKey, operator: operator, value: copyValue}}, nil
}

func validScalar(value Scalar) bool {
	switch value.kind {
	case ScalarKindString:
		_, err := NewStringScalar(value.stringValue)
		return err == nil
	case ScalarKindInteger, ScalarKindBoolean:
		return true
	case ScalarKindDate:
		_, err := NewDateScalar(value.stringValue)
		return err == nil
	default:
		return false
	}
}

func (r Rule) shape() (nodes, depth int, ok bool) {
	if r.kind == RuleKindPredicate {
		return 1, 1, true
	}
	if r.kind != RuleKindGroup || len(r.children) == 0 {
		return 0, 0, false
	}
	nodes, depth = 1, 1
	for _, child := range r.children {
		childNodes, childDepth, childOK := child.shape()
		if !childOK {
			return 0, 0, false
		}
		nodes += childNodes
		if childDepth+1 > depth {
			depth = childDepth + 1
		}
	}
	return nodes, depth, true
}

func (r Rule) Kind() RuleKind { return r.kind }
func (r Rule) Group() (GroupOperator, []Rule, bool) {
	if r.kind != RuleKindGroup {
		return "", nil, false
	}
	return r.groupOperator, cloneRules(r.children), true
}
func (r Rule) Predicate() (string, PredicateOperator, *Scalar, bool) {
	if r.kind != RuleKindPredicate {
		return "", "", nil, false
	}
	var value *Scalar
	if r.predicate.value != nil {
		v := *r.predicate.value
		value = &v
	}
	return r.predicate.fieldKey, r.predicate.operator, value, true
}
func (r Rule) Equal(other Rule) bool {
	if r.kind != other.kind {
		return false
	}
	if r.kind == RuleKindGroup {
		if r.groupOperator != other.groupOperator || len(r.children) != len(other.children) {
			return false
		}
		for i := range r.children {
			if !r.children[i].Equal(other.children[i]) {
				return false
			}
		}
		return true
	}
	if r.predicate.fieldKey != other.predicate.fieldKey || r.predicate.operator != other.predicate.operator {
		return false
	}
	if r.predicate.value == nil || other.predicate.value == nil {
		return r.predicate.value == nil && other.predicate.value == nil
	}
	return *r.predicate.value == *other.predicate.value
}
func cloneRules(values []Rule) []Rule {
	result := make([]Rule, len(values))
	for i, value := range values {
		result[i] = value.clone()
	}
	return result
}
func (r Rule) clone() Rule {
	r.children = cloneRules(r.children)
	if r.predicate.value != nil {
		v := *r.predicate.value
		r.predicate.value = &v
	}
	return r
}
