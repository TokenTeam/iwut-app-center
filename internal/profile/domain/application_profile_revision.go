package domain

import (
	"iwut-app-center/internal/shared"
	"strings"
	"time"
)

type ApplicationProfileRevisionID string

func ParseApplicationProfileRevisionID(value string) (ApplicationProfileRevisionID, error) {
	if !shared.IsUUIDv7(value) {
		return "", NewInternalError(nil)
	}
	return ApplicationProfileRevisionID(strings.ToLower(value)), nil
}
func (id ApplicationProfileRevisionID) String() string { return string(id) }
func (id ApplicationProfileRevisionID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ProfileSequence int32

func NewProfileSequence(value int32) (ProfileSequence, error) {
	if value < 1 {
		return 0, NewInternalError(nil)
	}
	return ProfileSequence(value), nil
}
func (s ProfileSequence) Int32() int32 { return int32(s) }

type ReviewStatus string

const (
	ReviewStatusDraft     ReviewStatus = "DRAFT"
	ReviewStatusSubmitted ReviewStatus = "SUBMITTED"
	ReviewStatusApproved  ReviewStatus = "APPROVED"
	ReviewStatusRejected  ReviewStatus = "REJECTED"
)

func (s ReviewStatus) valid() bool {
	return s == ReviewStatusDraft || s == ReviewStatusSubmitted || s == ReviewStatusApproved || s == ReviewStatusRejected
}

type DraftApplicationProfileRevision struct {
	id            ApplicationProfileRevisionID
	applicationID shared.ApplicationID
	displayName   ApplicationDisplayName
	description   *ApplicationDescription
	icon          *ApplicationIcon
	createdBy     shared.AuthID
	createdAt     time.Time
}

func NewDraftApplicationProfileRevision(id ApplicationProfileRevisionID, applicationID shared.ApplicationID, displayName ApplicationDisplayName, description *ApplicationDescription, icon *ApplicationIcon, createdBy shared.AuthID, createdAt time.Time) (*DraftApplicationProfileRevision, error) {
	if !id.IsValid() || !applicationID.IsValid() || !canonicalText(displayName.value, 80) || (description != nil && !canonicalText(description.value, 1000)) || (icon != nil && !canonicalText(icon.value, 512)) || !createdBy.IsValid() || createdAt.IsZero() {
		return nil, NewInternalError(nil)
	}
	return &DraftApplicationProfileRevision{id, applicationID, displayName, cloneDescription(description), cloneIcon(icon), createdBy, createdAt.UTC()}, nil
}
func (d *DraftApplicationProfileRevision) ProfileRevisionID() ApplicationProfileRevisionID {
	return d.id
}
func (d *DraftApplicationProfileRevision) ApplicationID() shared.ApplicationID {
	return d.applicationID
}
func (d *DraftApplicationProfileRevision) DisplayName() ApplicationDisplayName { return d.displayName }
func (d *DraftApplicationProfileRevision) Description() *ApplicationDescription {
	return cloneDescription(d.description)
}
func (d *DraftApplicationProfileRevision) Icon() *ApplicationIcon   { return cloneIcon(d.icon) }
func (d *DraftApplicationProfileRevision) CreatedBy() shared.AuthID { return d.createdBy }
func (d *DraftApplicationProfileRevision) CreatedAt() time.Time     { return d.createdAt }
func (d *DraftApplicationProfileRevision) AssignSequence(sequence ProfileSequence) (*ApplicationProfileRevision, error) {
	if d == nil {
		return nil, NewInternalError(nil)
	}
	var description, icon *string
	if d.description != nil {
		v := d.description.String()
		description = &v
	}
	if d.icon != nil {
		v := d.icon.String()
		icon = &v
	}
	return RestoreApplicationProfileRevision(ApplicationProfileRevisionState{d.id, d.applicationID, sequence, d.displayName.String(), description, icon, ReviewStatusDraft, d.createdBy, d.createdAt, 1, d.createdBy, d.createdAt})
}

// ApplicationProfileRevisionState is the explicit persistence reconstitution
// boundary. It validates stored text without silently normalizing it.
type ApplicationProfileRevisionState struct {
	ProfileRevisionID ApplicationProfileRevisionID
	ApplicationID     shared.ApplicationID
	Sequence          ProfileSequence
	DisplayName       string
	Description       *string
	Icon              *string
	ReviewStatus      ReviewStatus
	CreatedBy         shared.AuthID
	CreatedAt         time.Time
	Revision          int64
	UpdatedBy         shared.AuthID
	UpdatedAt         time.Time
}
type ApplicationProfileRevision struct {
	draft        DraftApplicationProfileRevision
	sequence     ProfileSequence
	reviewStatus ReviewStatus
	revision     int64
	updatedBy    shared.AuthID
	updatedAt    time.Time
}

func RestoreApplicationProfileRevision(s ApplicationProfileRevisionState) (*ApplicationProfileRevision, error) {
	return restoreProfileRevision(s, s.ReviewStatus == ReviewStatusRejected)
}

// RestoreProfileRevisionDecisionCandidate preserves original content so rejection
// is possible even when approval content validation fails.
func RestoreProfileRevisionDecisionCandidate(s ApplicationProfileRevisionState) (*ApplicationProfileRevision, error) {
	return restoreProfileRevision(s, true)
}
func restoreProfileRevision(s ApplicationProfileRevisionState, rawContent bool) (*ApplicationProfileRevision, error) {
	if !s.ProfileRevisionID.IsValid() || !s.ApplicationID.IsValid() || s.Sequence < 1 || (!rawContent && (!canonicalText(s.DisplayName, 80) || (s.Description != nil && !canonicalText(*s.Description, 1000)) || (s.Icon != nil && !canonicalText(*s.Icon, 512)))) || !s.ReviewStatus.valid() || !s.CreatedBy.IsValid() || s.CreatedAt.IsZero() || s.Revision < 1 || !s.UpdatedBy.IsValid() || s.UpdatedAt.IsZero() {
		return nil, ErrApplicationProfileStateInconsistent
	}
	if s.Revision == 1 && (s.ReviewStatus != ReviewStatusDraft || s.CreatedBy != s.UpdatedBy || !s.CreatedAt.Equal(s.UpdatedAt)) {
		return nil, ErrApplicationProfileStateInconsistent
	}
	var description *ApplicationDescription
	var icon *ApplicationIcon
	if s.Description != nil {
		description = &ApplicationDescription{*s.Description}
	}
	if s.Icon != nil {
		icon = &ApplicationIcon{*s.Icon}
	}
	d := DraftApplicationProfileRevision{s.ProfileRevisionID, s.ApplicationID, ApplicationDisplayName{s.DisplayName}, description, icon, s.CreatedBy, s.CreatedAt.UTC()}
	return &ApplicationProfileRevision{d, s.Sequence, s.ReviewStatus, s.Revision, s.UpdatedBy, s.UpdatedAt.UTC()}, nil
}
func (r *ApplicationProfileRevision) ProfileRevisionID() ApplicationProfileRevisionID {
	return r.draft.id
}
func (r *ApplicationProfileRevision) ApplicationID() shared.ApplicationID {
	return r.draft.applicationID
}
func (r *ApplicationProfileRevision) Sequence() ProfileSequence           { return r.sequence }
func (r *ApplicationProfileRevision) DisplayName() ApplicationDisplayName { return r.draft.displayName }
func (r *ApplicationProfileRevision) Description() *ApplicationDescription {
	return cloneDescription(r.draft.description)
}
func (r *ApplicationProfileRevision) Icon() *ApplicationIcon     { return cloneIcon(r.draft.icon) }
func (r *ApplicationProfileRevision) ReviewStatus() ReviewStatus { return r.reviewStatus }
func (r *ApplicationProfileRevision) CreatedBy() shared.AuthID   { return r.draft.createdBy }
func (r *ApplicationProfileRevision) CreatedAt() time.Time       { return r.draft.createdAt }
func (r *ApplicationProfileRevision) Revision() int64            { return r.revision }
func (r *ApplicationProfileRevision) UpdatedBy() shared.AuthID   { return r.updatedBy }
func (r *ApplicationProfileRevision) UpdatedAt() time.Time       { return r.updatedAt }
