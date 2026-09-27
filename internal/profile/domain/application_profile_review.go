package domain

import (
	"iwut-app-center/internal/shared"
	"math"
	"strings"
	"time"
)

type ApplicationProfileReviewID string

func ParseApplicationProfileReviewID(value string) (ApplicationProfileReviewID, error) {
	if !shared.IsUUIDv7(value) {
		return "", NewInternalError(nil)
	}
	return ApplicationProfileReviewID(strings.ToLower(value)), nil
}
func (id ApplicationProfileReviewID) String() string { return string(id) }
func (id ApplicationProfileReviewID) IsValid() bool  { return shared.IsUUIDv7(string(id)) }

type ProfileReviewStatus string

const (
	ProfileReviewStatusPending  ProfileReviewStatus = "PENDING"
	ProfileReviewStatusApproved ProfileReviewStatus = "APPROVED"
	ProfileReviewStatusRejected ProfileReviewStatus = "REJECTED"
)

type ApplicationProfileReviewSnapshot struct {
	displayName ApplicationDisplayName
	description *ApplicationDescription
	icon        *ApplicationIcon
}

func (s ApplicationProfileReviewSnapshot) DisplayName() ApplicationDisplayName { return s.displayName }
func (s ApplicationProfileReviewSnapshot) Description() *ApplicationDescription {
	return cloneDescription(s.description)
}
func (s ApplicationProfileReviewSnapshot) Icon() *ApplicationIcon { return cloneIcon(s.icon) }

type ApplicationProfileReview struct {
	id                ApplicationProfileReviewID
	applicationID     shared.ApplicationID
	profileRevisionID ApplicationProfileRevisionID
	attempt           int32
	sourceRevision    int64
	snapshot          ApplicationProfileReviewSnapshot
	submittedBy       shared.AuthID
	submittedAt       time.Time
	status            ProfileReviewStatus
	decision          *ApplicationProfileReviewDecision
}

func (r *ApplicationProfileReview) ProfileReviewID() ApplicationProfileReviewID { return r.id }
func (r *ApplicationProfileReview) ApplicationID() shared.ApplicationID         { return r.applicationID }
func (r *ApplicationProfileReview) ProfileRevisionID() ApplicationProfileRevisionID {
	return r.profileRevisionID
}
func (r *ApplicationProfileReview) Attempt() int32              { return r.attempt }
func (r *ApplicationProfileReview) SourceRevision() int64       { return r.sourceRevision }
func (r *ApplicationProfileReview) Status() ProfileReviewStatus { return r.status }
func (r *ApplicationProfileReview) Snapshot() ApplicationProfileReviewSnapshot {
	return ApplicationProfileReviewSnapshot{r.snapshot.displayName, cloneDescription(r.snapshot.description), cloneIcon(r.snapshot.icon)}
}
func (r *ApplicationProfileReview) SubmittedBy() shared.AuthID { return r.submittedBy }
func (r *ApplicationProfileReview) SubmittedAt() time.Time     { return r.submittedAt }

type ApplicationProfileSubmission struct {
	ProfileRevision *ApplicationProfileRevision
	Review          *ApplicationProfileReview
}

// ValidateStoredApplicationProfileContent rejects noncanonical storage rather than repairing it.
func ValidateStoredApplicationProfileContent(name string, description, icon *string) error {
	if !canonicalText(name, 80) || description != nil && !canonicalText(*description, 1000) || icon != nil && !canonicalText(*icon, 512) {
		return ErrInvalidApplicationProfileContent
	}
	return nil
}
func (r *ApplicationProfileRevision) SubmitDraft(expected int64, id ApplicationProfileReviewID, attempt int32, by shared.AuthID, at time.Time) (*ApplicationProfileSubmission, error) {
	if r == nil {
		return nil, ErrApplicationProfileStateInconsistent
	}
	if expected < 1 {
		return nil, ErrInvalidApplicationProfileReviewSubmission
	}
	if r.reviewStatus != ReviewStatusDraft {
		return nil, ErrApplicationProfileRevisionNotDraft
	}
	if r.revision != expected {
		return nil, ErrApplicationProfileRevisionConflict
	}
	var description, icon *string
	if r.draft.description != nil {
		v := r.draft.description.String()
		description = &v
	}
	if r.draft.icon != nil {
		v := r.draft.icon.String()
		icon = &v
	}
	if err := ValidateStoredApplicationProfileContent(r.draft.displayName.String(), description, icon); err != nil {
		return nil, err
	}
	if r.revision == math.MaxInt64 || attempt < 1 {
		return nil, ErrApplicationProfileStateInconsistent
	}
	if !id.IsValid() || !by.IsValid() || at.IsZero() {
		return nil, NewInternalError(nil)
	}
	updated := *r
	updated.draft.description = cloneDescription(r.draft.description)
	updated.draft.icon = cloneIcon(r.draft.icon)
	updated.reviewStatus = ReviewStatusSubmitted
	updated.revision++
	updated.updatedBy = by
	updated.updatedAt = at.UTC()
	review := &ApplicationProfileReview{id, r.ApplicationID(), r.ProfileRevisionID(), attempt, expected, ApplicationProfileReviewSnapshot{r.draft.displayName, cloneDescription(r.draft.description), cloneIcon(r.draft.icon)}, by, at.UTC(), ProfileReviewStatusPending, nil}
	return &ApplicationProfileSubmission{&updated, review}, nil
}
