package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"math"
	"strings"
	"testing"
	"time"
)

func decisionFixture(t *testing.T) (*ApplicationProfileRevision, *ApplicationProfileReview) {
	t.Helper()
	s, e := replacementFixture(t).SubmitDraft(1, reviewTestID, 1, "submitter", time.Unix(200, 0))
	if e != nil {
		t.Fatal(e)
	}
	return s.ProfileRevision, s.Review
}
func TestBRPRF023032ProfileReviewDecision(t *testing.T) {
	for _, outcome := range []string{"APPROVE", "REJECT"} {
		t.Run(outcome, func(t *testing.T) {
			r, v := decisionFixture(t)
			reason := "Cafe\u0301 reason"
			checks := InitialProfileReviewChecks()
			if outcome == "REJECT" {
				checks = nil
			}
			got, e := r.DecideReview(v, "reviewer", "admin", []string{ProfileReviewPermission}, 2, outcome, ProfileReviewPolicy{InitialProfileReviewPolicyVersion, InitialProfileReviewChecks(), "ACTIVE"}, checks, &reason, time.Unix(300, 0))
			if e != nil {
				t.Fatal(e)
			}
			if got.ProfileRevision.Revision() != 3 || got.ProfileRevision.UpdatedBy() != "reviewer" || got.Review.Decision() == nil || *got.Review.Decision().Reason != reason || v.Decision() != nil || r.Revision() != 2 {
				t.Fatal("transition or immutable audit")
			}
			d := got.Review.Decision()
			d.Reason = nil
			d.ConfirmedCheckIDs = append(d.ConfirmedCheckIDs, "mutated")
			if got.Review.Decision().Reason == nil || len(got.Review.Decision().ConfirmedCheckIDs) > 2 {
				t.Fatal("decision aliases")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ApplicationProfileRevision, *ApplicationProfileReview, *ProfileReviewPolicy, *shared.AuthID, *[]string, *[]string, *string)
		want   error
	}{
		{"permission", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, p *[]string, _ *[]string, _ *string) {
			*p = []string{"app.version.review"}
		}, ErrApplicationProfileReviewPermissionRequired},
		{"creator", func(r *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, a *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			*a = r.CreatedBy()
		}, ErrApplicationProfileReviewConflictOfInterest},
		{"submitter", func(_ *ApplicationProfileRevision, v *ApplicationProfileReview, _ *ProfileReviewPolicy, a *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			*a = v.SubmittedBy()
		}, ErrApplicationProfileReviewConflictOfInterest},
		{"admin", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, a *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			*a = "admin"
		}, ErrApplicationProfileReviewConflictOfInterest},
		{"state", func(r *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			r.reviewStatus = ReviewStatusDraft
		}, ErrApplicationProfileReviewStateConflict},
		{"snapshot", func(r *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			r.draft.displayName.value = "drift"
		}, ErrApplicationProfileReviewStateInconsistent},
		{"source", func(_ *ApplicationProfileRevision, v *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			v.sourceRevision = 2
		}, ErrApplicationProfileReviewStateInconsistent},
		{"retired", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, p *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			p.Status = "RETIRED"
		}, ErrProfileReviewPolicyUnavailable},
		{"missing check", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, c *[]string, _ *string) {
			*c = (*c)[:1]
		}, ErrProfileReviewChecksIncomplete},
		{"duplicate", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, c *[]string, _ *string) {
			*c = append(*c, (*c)[0])
		}, ErrProfileReviewChecksIncomplete},
		{"unknown", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, c *[]string, _ *string) {
			(*c)[0] = "unknown"
		}, ErrProfileReviewChecksIncomplete},
		{"invalid content", func(r *ApplicationProfileRevision, v *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, _ *[]string, _ *string) {
			r.draft.displayName.value = " bad "
			v.snapshot.displayName.value = " bad "
		}, ErrInvalidApplicationProfileContent},
		{"reason", func(_ *ApplicationProfileRevision, _ *ApplicationProfileReview, _ *ProfileReviewPolicy, _ *shared.AuthID, _ *[]string, _ *[]string, r *string) {
			*r = "\n"
		}, ErrInvalidProfileReviewReason},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, v := decisionFixture(t)
			p := ProfileReviewPolicy{InitialProfileReviewPolicyVersion, InitialProfileReviewChecks(), "ACTIVE"}
			actor := shared.AuthID("reviewer")
			permissions := []string{ProfileReviewPermission}
			checks := InitialProfileReviewChecks()
			reason := "reason"
			tc.mutate(r, v, &p, &actor, &permissions, &checks, &reason)
			_, e := r.DecideReview(v, actor, "admin", permissions, 2, "APPROVE", p, checks, &reason, time.Unix(300, 0))
			if !errors.Is(e, tc.want) {
				t.Fatalf("%v want %v", e, tc.want)
			}
		})
	}
	r, v := decisionFixture(t)
	r.draft.displayName.value = " bad \x00"
	v.snapshot.displayName.value = r.DisplayName().String()
	reason := "Reject"
	got, e := r.DecideReview(v, "reviewer", "admin", []string{ProfileReviewPermission}, 2, "REJECT", ProfileReviewPolicy{InitialProfileReviewPolicyVersion, InitialProfileReviewChecks(), "ACTIVE"}, nil, &reason, time.Now())
	if e != nil || got.ProfileRevision.DisplayName() != r.DisplayName() {
		t.Fatal("rejection must preserve invalid content", e)
	}
	r.revision = math.MaxInt64
	v.sourceRevision = math.MaxInt64 - 1
	if _, e = r.DecideReview(v, "reviewer", "admin", []string{ProfileReviewPermission}, math.MaxInt64, "REJECT", ProfileReviewPolicy{InitialProfileReviewPolicyVersion, InitialProfileReviewChecks(), "ACTIVE"}, nil, &reason, time.Now()); !errors.Is(e, ErrApplicationProfileReviewStateInconsistent) {
		t.Fatal("overflow", e)
	}
}
func TestBRPRF027ReasonUnicode(t *testing.T) {
	for _, s := range []string{"", " reason", "reason\u0085", "bad\nreason", strings.Repeat("界", 2001), string([]byte{0xff})} {
		if ValidateProfileReviewReason(&s, false) == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	for _, s := range []string{"e\u0301", "a\u200db", "a\u2028b", strings.Repeat("界", 2000)} {
		if e := ValidateProfileReviewReason(&s, true); e != nil {
			t.Fatalf("rejected valid reason %v", e)
		}
	}
}
