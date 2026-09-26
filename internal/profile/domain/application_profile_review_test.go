package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"math"
	"testing"
	"time"
)

const reviewTestID ApplicationProfileReviewID = "01995000-0000-7000-8000-000000000015"

func TestBRPRF018020SubmissionSnapshotAndAudit(t *testing.T) {
	r := replacementFixture(t)
	r.draft.description = &ApplicationDescription{"description"}
	r.draft.icon = &ApplicationIcon{"opaque"}
	at := time.Unix(200, 0).In(time.FixedZone("offset", 3600))
	got, err := r.SubmitDraft(1, reviewTestID, 1, "new-admin", at)
	if err != nil {
		t.Fatal(err)
	}
	v, review := got.ProfileRevision, got.Review
	if r.ReviewStatus() != ReviewStatusDraft || r.Revision() != 1 || v.ReviewStatus() != ReviewStatusSubmitted || v.Revision() != 2 || v.UpdatedBy() != "new-admin" || !v.UpdatedAt().Equal(at) || v.CreatedBy() != r.CreatedBy() || !v.CreatedAt().Equal(r.CreatedAt()) || v.Sequence() != r.Sequence() {
		t.Fatal("audit or lifecycle mutation")
	}
	if review.ProfileReviewID() != reviewTestID || review.ApplicationID() != r.ApplicationID() || review.ProfileRevisionID() != r.ProfileRevisionID() || review.Attempt() != 1 || review.SourceRevision() != 1 || review.Status() != ProfileReviewStatusPending || review.SubmittedBy() != "new-admin" || !review.SubmittedAt().Equal(at) || review.SubmittedAt().Location() != time.UTC {
		t.Fatal("review facts")
	}
	// Mutating input storage and every returned pointer must not mutate snapshot.
	r.draft.description.value = "changed"
	r.draft.icon.value = "changed"
	snapshot := review.Snapshot()
	snapshot.description.value = "changed"
	snapshot.icon.value = "changed"
	description, icon := review.Snapshot().Description(), review.Snapshot().Icon()
	description.value = "changed"
	icon.value = "changed"
	if review.Snapshot().Description().String() != "description" || review.Snapshot().Icon().String() != "opaque" || v.Description().String() != "description" || v.Icon().String() != "opaque" {
		t.Fatal("snapshot aliases mutable pointers")
	}
	if _, err = v.SubmitDraft(2, reviewTestID, 2, "new-admin", at); !errors.Is(err, ErrApplicationProfileRevisionNotDraft) {
		t.Fatal("duplicate", err)
	}
}
func TestBRPRF016017019021SubmissionGuards(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mutate   func(*ApplicationProfileRevision)
		expected int64
		attempt  int32
		want     error
	}{
		{"stale", func(*ApplicationProfileRevision) {}, 2, 1, ErrApplicationProfileRevisionConflict},
		{"zero expected", func(*ApplicationProfileRevision) {}, 0, 1, ErrInvalidApplicationProfileReviewSubmission},
		{"submitted", func(r *ApplicationProfileRevision) { r.reviewStatus = ReviewStatusSubmitted }, 1, 1, ErrApplicationProfileRevisionNotDraft},
		{"approved", func(r *ApplicationProfileRevision) { r.reviewStatus = ReviewStatusApproved }, 1, 1, ErrApplicationProfileRevisionNotDraft},
		{"rejected", func(r *ApplicationProfileRevision) { r.reviewStatus = ReviewStatusRejected }, 1, 1, ErrApplicationProfileRevisionNotDraft},
		{"revision overflow", func(r *ApplicationProfileRevision) { r.revision = math.MaxInt64 }, math.MaxInt64, 1, ErrApplicationProfileStateInconsistent},
		{"attempt overflow", func(*ApplicationProfileRevision) {}, 1, math.MinInt32, ErrApplicationProfileStateInconsistent},
		{"name", func(r *ApplicationProfileRevision) { r.draft.displayName.value = "Cafe\u0301" }, 1, 1, ErrInvalidApplicationProfileContent},
		{"description", func(r *ApplicationProfileRevision) { r.draft.description = &ApplicationDescription{""} }, 1, 1, ErrInvalidApplicationProfileContent},
		{"icon", func(r *ApplicationProfileRevision) { r.draft.icon = &ApplicationIcon{"\u200b"} }, 1, 1, ErrInvalidApplicationProfileContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := replacementFixture(t)
			tc.mutate(r)
			_, err := r.SubmitDraft(tc.expected, reviewTestID, tc.attempt, "admin", time.Unix(200, 0))
			if !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		id ApplicationProfileReviewID
		by string
		at time.Time
	}{{"bad", "admin", time.Unix(200, 0)}, {reviewTestID, "", time.Unix(200, 0)}, {reviewTestID, "admin", time.Time{}}} {
		r := replacementFixture(t)
		_, err := r.SubmitDraft(1, tc.id, 1, shared.AuthID(tc.by), tc.at)
		if !errors.Is(err, ErrInternal) {
			t.Fatal(err)
		}
	}
}
