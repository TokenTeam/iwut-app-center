package domain

import (
	"iwut-app-center/internal/shared"
	"math"
	"time"
)

type DraftApplicationProfileReplacement struct {
	displayName ApplicationDisplayName
	description *ApplicationDescription
	icon        *ApplicationIcon
}

func NewDraftApplicationProfileReplacement(name string, description, icon *string) (DraftApplicationProfileReplacement, error) {
	var r DraftApplicationProfileReplacement
	var err error
	r.displayName, err = NewApplicationDisplayName(name)
	if err != nil {
		return r, err
	}
	if description != nil {
		v, e := NewApplicationDescription(*description)
		if e != nil {
			return r, e
		}
		r.description = &v
	}
	if icon != nil {
		v, e := NewApplicationIcon(*icon)
		if e != nil {
			return r, e
		}
		r.icon = &v
	}
	return r, nil
}
func (r *ApplicationProfileRevision) ReplaceDraft(expected int64, replacement DraftApplicationProfileReplacement, by shared.AuthID, at time.Time) (*ApplicationProfileRevision, error) {
	if r == nil {
		return nil, ErrApplicationProfileStateInconsistent
	}
	if expected < 1 {
		return nil, ErrApplicationProfileExpectedRevisionRequired
	}
	if r.reviewStatus != ReviewStatusDraft {
		return nil, ErrApplicationProfileRevisionNotDraft
	}
	if r.revision != expected {
		return nil, ErrApplicationProfileRevisionConflict
	}
	if !canonicalText(replacement.displayName.value, 80) || (replacement.description != nil && !canonicalText(replacement.description.value, 1000)) || (replacement.icon != nil && !canonicalText(replacement.icon.value, 512)) || !by.IsValid() || at.IsZero() {
		return nil, NewInternalError(nil)
	}
	if replacement.displayName == r.draft.displayName && equalDescription(replacement.description, r.draft.description) && equalIcon(replacement.icon, r.draft.icon) {
		return r, nil
	}
	if r.revision == math.MaxInt64 {
		return nil, ErrApplicationProfileStateInconsistent
	}
	result := *r
	result.draft.displayName = replacement.displayName
	result.draft.description = cloneDescription(replacement.description)
	result.draft.icon = cloneIcon(replacement.icon)
	result.revision++
	result.updatedBy, result.updatedAt = by, at.UTC()
	return &result, nil
}
func equalDescription(a, b *ApplicationDescription) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func equalIcon(a, b *ApplicationIcon) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
