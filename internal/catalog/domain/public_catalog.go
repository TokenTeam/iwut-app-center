package domain

import "iwut-app-center/internal/shared"

const PublicFilterSchemaVersion = "profile-filter-v1"

type PublicApplicationProfile struct {
	revisionID        string
	displayName       string
	description, icon *string
}

func NewPublicApplicationProfile(revisionID, displayName string, description, icon *string) (*PublicApplicationProfile, error) {
	if !shared.IsUUIDv7(revisionID) || displayName == "" {
		return nil, ErrApplicationCatalogStateInconsistent
	}
	return &PublicApplicationProfile{revisionID: revisionID, displayName: displayName, description: copyString(description), icon: copyString(icon)}, nil
}
func (p *PublicApplicationProfile) RevisionID() string   { return p.revisionID }
func (p *PublicApplicationProfile) DisplayName() string  { return p.displayName }
func (p *PublicApplicationProfile) Description() *string { return copyString(p.description) }
func (p *PublicApplicationProfile) Icon() *string        { return copyString(p.icon) }

type PublicFilterScalar struct {
	kind         string
	stringValue  string
	integerValue int32
	booleanValue bool
}

func NewPublicFilterScalar(kind, stringValue string, integerValue int32, booleanValue bool) PublicFilterScalar {
	return PublicFilterScalar{kind: kind, stringValue: stringValue, integerValue: integerValue, booleanValue: booleanValue}
}
func (s PublicFilterScalar) Kind() string        { return s.kind }
func (s PublicFilterScalar) StringValue() string { return s.stringValue }
func (s PublicFilterScalar) IntegerValue() int32 { return s.integerValue }
func (s PublicFilterScalar) BooleanValue() bool  { return s.booleanValue }

type PublicFilterRule struct {
	kind, operator, fieldKey string
	children                 []PublicFilterRule
	value                    *PublicFilterScalar
}

func NewPublicFilterGroup(operator string, children []PublicFilterRule) PublicFilterRule {
	return PublicFilterRule{kind: "GROUP", operator: operator, children: clonePublicRules(children)}
}
func NewPublicFilterPredicate(fieldKey, operator string, value *PublicFilterScalar) PublicFilterRule {
	var copied *PublicFilterScalar
	if value != nil {
		v := *value
		copied = &v
	}
	return PublicFilterRule{kind: "PREDICATE", fieldKey: fieldKey, operator: operator, value: copied}
}
func (r PublicFilterRule) Kind() string                 { return r.kind }
func (r PublicFilterRule) Operator() string             { return r.operator }
func (r PublicFilterRule) FieldKey() string             { return r.fieldKey }
func (r PublicFilterRule) Children() []PublicFilterRule { return clonePublicRules(r.children) }
func (r PublicFilterRule) Value() *PublicFilterScalar {
	if r.value == nil {
		return nil
	}
	v := *r.value
	return &v
}
func clonePublicRules(values []PublicFilterRule) []PublicFilterRule {
	result := make([]PublicFilterRule, len(values))
	for i, value := range values {
		result[i] = value
		result[i].children = clonePublicRules(value.children)
		if value.value != nil {
			v := *value.value
			result[i].value = &v
		}
	}
	return result
}

type PublicApplicationFilter struct {
	revision   int64
	revisionID string
	mode       string
	rule       *PublicFilterRule
}

func NewPublicApplicationFilter(revision int64, revisionID, mode string, rule *PublicFilterRule) (*PublicApplicationFilter, error) {
	if revision < 0 || (revision == 0 && (revisionID != "" || mode != "ALLOW_ALL" || rule != nil)) || (revision > 0 && !shared.IsUUIDv7(revisionID)) || (mode == "RULE") != (rule != nil) || (mode != "RULE" && mode != "ALLOW_ALL") {
		return nil, ErrApplicationCatalogStateInconsistent
	}
	var copied *PublicFilterRule
	if rule != nil {
		v := clonePublicRules([]PublicFilterRule{*rule})[0]
		copied = &v
	}
	return &PublicApplicationFilter{revision: revision, revisionID: revisionID, mode: mode, rule: copied}, nil
}
func (f *PublicApplicationFilter) Revision() int64       { return f.revision }
func (f *PublicApplicationFilter) RevisionID() string    { return f.revisionID }
func (f *PublicApplicationFilter) SchemaVersion() string { return PublicFilterSchemaVersion }
func (f *PublicApplicationFilter) Mode() string          { return f.mode }
func (f *PublicApplicationFilter) Rule() *PublicFilterRule {
	if f == nil || f.rule == nil {
		return nil
	}
	v := clonePublicRules([]PublicFilterRule{*f.rule})[0]
	return &v
}

type PublicApplicationCatalogItem struct {
	applicationID shared.ApplicationID
	profile       *PublicApplicationProfile
	launchTarget  *LaunchTargetDescriptor
	filter        *PublicApplicationFilter
}

func NewPublicApplicationCatalogItem(applicationID shared.ApplicationID, profile *PublicApplicationProfile, launchTarget *LaunchTargetDescriptor, filter *PublicApplicationFilter) (*PublicApplicationCatalogItem, error) {
	if !applicationID.IsValid() || profile == nil || launchTarget == nil || launchTarget.ApplicationID() != applicationID || filter == nil {
		return nil, ErrApplicationCatalogStateInconsistent
	}
	return &PublicApplicationCatalogItem{applicationID: applicationID, profile: profile, launchTarget: launchTarget, filter: filter}, nil
}
func (i *PublicApplicationCatalogItem) ApplicationID() shared.ApplicationID   { return i.applicationID }
func (i *PublicApplicationCatalogItem) Profile() *PublicApplicationProfile    { return i.profile }
func (i *PublicApplicationCatalogItem) LaunchTarget() *LaunchTargetDescriptor { return i.launchTarget }
func (i *PublicApplicationCatalogItem) Filter() *PublicApplicationFilter      { return i.filter }

type PublicApplicationCatalogPage struct {
	items     []*PublicApplicationCatalogItem
	nextToken string
}

func NewPublicApplicationCatalogPage(items []*PublicApplicationCatalogItem, nextToken string) (*PublicApplicationCatalogPage, error) {
	copyItems := append([]*PublicApplicationCatalogItem(nil), items...)
	for index, item := range copyItems {
		if item == nil || (index > 0 && copyItems[index-1].ApplicationID().String() >= item.ApplicationID().String()) {
			return nil, ErrApplicationCatalogStateInconsistent
		}
	}
	return &PublicApplicationCatalogPage{items: copyItems, nextToken: nextToken}, nil
}
func (p *PublicApplicationCatalogPage) Items() []*PublicApplicationCatalogItem {
	return append([]*PublicApplicationCatalogItem(nil), p.items...)
}
func (p *PublicApplicationCatalogPage) NextPageToken() string { return p.nextToken }

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
