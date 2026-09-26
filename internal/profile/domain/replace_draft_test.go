package domain

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func replacementFixture(t *testing.T) *ApplicationProfileRevision {
	t.Helper()
	name, _ := NewApplicationDisplayName("Café")
	d, e := NewDraftApplicationProfileRevision("01995000-0000-7000-8000-000000000002", "01995000-0000-7000-8000-000000000001", name, nil, nil, "creator", time.Unix(100, 0))
	if e != nil {
		t.Fatal(e)
	}
	r, e := d.AssignSequence(3)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestBRPRF008To014ReplaceDraft(t *testing.T) {
	original := replacementFixture(t)
	at := time.Unix(200, 0)
	desc, icon := "de\u0301tail", "opaque://127.0.0.1"
	change, e := NewDraftApplicationProfileReplacement("New", &desc, &icon)
	if e != nil {
		t.Fatal(e)
	}
	updated, e := original.ReplaceDraft(1, change, "new-admin", at)
	if e != nil {
		t.Fatal(e)
	}
	if updated.ProfileRevisionID() != original.ProfileRevisionID() || updated.ApplicationID() != original.ApplicationID() || updated.Sequence() != 3 || updated.CreatedBy() != original.CreatedBy() || !updated.CreatedAt().Equal(original.CreatedAt()) || updated.ReviewStatus() != ReviewStatusDraft || updated.Revision() != 2 || updated.UpdatedBy() != "new-admin" || !updated.UpdatedAt().Equal(at) || updated.Description().String() != "détail" || updated.Icon().String() != icon {
		t.Fatal("replacement/audit mismatch")
	}
	if original.Revision() != 1 || original.DisplayName().String() != "Café" || original.Description() != nil {
		t.Fatal("original mutated")
	}
	clear, _ := NewDraftApplicationProfileReplacement("New", nil, nil)
	cleared, e := updated.ReplaceDraft(2, clear, "new-admin", at)
	if e != nil || cleared.Description() != nil || cleared.Icon() != nil || cleared.Revision() != 3 {
		t.Fatal("clear failed")
	}
	noop, _ := NewDraftApplicationProfileReplacement("Cafe\u0301", nil, nil)
	same, e := original.ReplaceDraft(1, noop, "another", at)
	if e != nil || !reflect.DeepEqual(same, original) {
		t.Fatal("normalized no-op changed audit")
	}
	if _, e = original.ReplaceDraft(2, noop, "another", at); !errors.Is(e, ErrApplicationProfileRevisionConflict) {
		t.Fatal(e)
	}
	for _, status := range []ReviewStatus{ReviewStatusSubmitted, ReviewStatusApproved, ReviewStatusRejected} {
		r := *original
		r.reviewStatus = status
		r.revision = 2
		if _, e = r.ReplaceDraft(2, change, "admin", at); !errors.Is(e, ErrApplicationProfileRevisionNotDraft) {
			t.Fatal(e)
		}
	}
	r := *original
	r.revision = math.MaxInt64
	if _, e = r.ReplaceDraft(math.MaxInt64, change, "admin", at); !errors.Is(e, ErrApplicationProfileStateInconsistent) {
		t.Fatal("overflow", e)
	}
	if got, e := r.ReplaceDraft(math.MaxInt64, noop, "admin", at); e != nil || got.Revision() != math.MaxInt64 {
		t.Fatal("max no-op", e)
	}
}
func TestBRPRF012ReplacementReusesValidation(t *testing.T) {
	empty := ""
	for _, c := range []struct {
		name              string
		description, icon *string
		want              error
	}{{" ", nil, nil, ErrInvalidApplicationDisplayName}, {"ok", &empty, nil, ErrInvalidApplicationDescription}, {"ok", nil, &empty, ErrInvalidApplicationIcon}} {
		if _, e := NewDraftApplicationProfileReplacement(c.name, c.description, c.icon); !errors.Is(e, c.want) {
			t.Fatal(e)
		}
	}
}
