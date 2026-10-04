package domain

import (
	"iwut-app-center/internal/shared"
	"math"
	"time"
)

type RevisionMode string
type FilterRevisionID string

const (
	RevisionModeAllowAll RevisionMode = "ALLOW_ALL"
	RevisionModeRule     RevisionMode = "RULE"
)

func (id FilterRevisionID) String() string { return string(id) }
func (id FilterRevisionID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ApplicationFilterRevision struct {
	id            FilterRevisionID
	applicationID shared.ApplicationID
	sequence      int64
	mode          RevisionMode
	rule          *Rule
	publishedBy   shared.AuthID
	publishedAt   time.Time
}

func NewRuleRevision(id FilterRevisionID, applicationID shared.ApplicationID, sequence int64, rule Rule, by shared.AuthID, at time.Time) (*ApplicationFilterRevision, error) {
	if rule.kind == "" {
		return nil, ErrInvalidApplicationFilter
	}
	return newRevision(id, applicationID, sequence, RevisionModeRule, &rule, by, at)
}
func NewAllowAllRevision(id FilterRevisionID, applicationID shared.ApplicationID, sequence int64, by shared.AuthID, at time.Time) (*ApplicationFilterRevision, error) {
	return newRevision(id, applicationID, sequence, RevisionModeAllowAll, nil, by, at)
}
func newRevision(id FilterRevisionID, applicationID shared.ApplicationID, sequence int64, mode RevisionMode, rule *Rule, by shared.AuthID, at time.Time) (*ApplicationFilterRevision, error) {
	if !id.IsValid() || !applicationID.IsValid() || sequence < 1 || !by.IsValid() || at.IsZero() || at.Location() != time.UTC || (mode == RevisionModeRule) != (rule != nil) || (mode != RevisionModeRule && mode != RevisionModeAllowAll) {
		return nil, ErrApplicationFilterStateInconsistent
	}
	var copied *Rule
	if rule != nil {
		v := rule.clone()
		if nodes, depth, ok := v.shape(); !ok || nodes > MaxRuleNodes || depth > MaxRuleDepth {
			return nil, ErrApplicationFilterStateInconsistent
		}
		copied = &v
	}
	return &ApplicationFilterRevision{id: id, applicationID: applicationID, sequence: sequence, mode: mode, rule: copied, publishedBy: by, publishedAt: at}, nil
}
func (r *ApplicationFilterRevision) ID() FilterRevisionID                { return r.id }
func (r *ApplicationFilterRevision) ApplicationID() shared.ApplicationID { return r.applicationID }
func (r *ApplicationFilterRevision) Sequence() int64                     { return r.sequence }
func (r *ApplicationFilterRevision) Mode() RevisionMode                  { return r.mode }
func (r *ApplicationFilterRevision) Rule() *Rule {
	if r == nil || r.rule == nil {
		return nil
	}
	v := r.rule.clone()
	return &v
}
func (r *ApplicationFilterRevision) PublishedBy() shared.AuthID { return r.publishedBy }
func (r *ApplicationFilterRevision) PublishedAt() time.Time     { return r.publishedAt }

type ApplicationFilter struct {
	applicationID shared.ApplicationID
	revision      int64
	nextSequence  int64
	current       *ApplicationFilterRevision
}

func NewDefaultApplicationFilter(applicationID shared.ApplicationID) (*ApplicationFilter, error) {
	if !applicationID.IsValid() {
		return nil, ErrApplicationFilterStateInconsistent
	}
	return &ApplicationFilter{applicationID: applicationID, nextSequence: 1}, nil
}
func RestoreApplicationFilter(applicationID shared.ApplicationID, revision, nextSequence int64, current *ApplicationFilterRevision) (*ApplicationFilter, error) {
	if !applicationID.IsValid() || revision < 1 || revision == math.MaxInt64 || nextSequence != revision+1 || current == nil || current.ApplicationID() != applicationID || current.Sequence() != revision {
		return nil, ErrApplicationFilterStateInconsistent
	}
	return &ApplicationFilter{applicationID: applicationID, revision: revision, nextSequence: nextSequence, current: current}, nil
}
func (f *ApplicationFilter) ApplicationID() shared.ApplicationID { return f.applicationID }
func (f *ApplicationFilter) Revision() int64                     { return f.revision }
func (f *ApplicationFilter) NextSequence() int64                 { return f.nextSequence }
func (f *ApplicationFilter) EffectiveMode() RevisionMode {
	if f == nil || f.current == nil {
		return RevisionModeAllowAll
	}
	return f.current.Mode()
}
func (f *ApplicationFilter) CurrentRevision() *ApplicationFilterRevision { return f.current }
func (f *ApplicationFilter) MatchesRule(rule Rule) bool {
	return f != nil && f.current != nil && f.current.mode == RevisionModeRule && f.current.rule.Equal(rule)
}
func (f *ApplicationFilter) IsAllowAll() bool {
	return f == nil || f.current == nil || f.current.mode == RevisionModeAllowAll
}

func (f *ApplicationFilter) PublishRule(id FilterRevisionID, rule Rule, by shared.AuthID, at time.Time) (*ApplicationFilter, *ApplicationFilterRevision, error) {
	revision, err := NewRuleRevision(id, f.applicationID, f.nextSequence, rule, by, at)
	if err != nil {
		return nil, nil, err
	}
	return f.advance(revision)
}
func (f *ApplicationFilter) PublishAllowAll(id FilterRevisionID, by shared.AuthID, at time.Time) (*ApplicationFilter, *ApplicationFilterRevision, error) {
	revision, err := NewAllowAllRevision(id, f.applicationID, f.nextSequence, by, at)
	if err != nil {
		return nil, nil, err
	}
	return f.advance(revision)
}
func (f *ApplicationFilter) advance(current *ApplicationFilterRevision) (*ApplicationFilter, *ApplicationFilterRevision, error) {
	if f == nil || f.revision == math.MaxInt64 || f.nextSequence == math.MaxInt64 || current == nil || current.sequence != f.nextSequence {
		return nil, nil, ErrApplicationFilterStateInconsistent
	}
	next := &ApplicationFilter{applicationID: f.applicationID, revision: f.revision + 1, nextSequence: f.nextSequence + 1, current: current}
	return next, current, nil
}

type ChangeResult struct {
	filter  *ApplicationFilter
	changed bool
}

func NewChangeResult(filter *ApplicationFilter, changed bool) *ChangeResult {
	return &ChangeResult{filter: filter, changed: changed}
}
func (r *ChangeResult) Filter() *ApplicationFilter { return r.filter }
func (r *ChangeResult) Changed() bool              { return r.changed }
