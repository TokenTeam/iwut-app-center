package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestManagementActivity_BRAPP068069071_IdentityOrderAndValidation(t *testing.T) {
	app := shared.ApplicationID("019a0000-0000-7000-8000-000000000099")
	at := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	label := "old label"
	rows := []ManagementActivity{}
	for source := 0; source < 2; source++ {
		for phase := 0; phase < 2; phase++ {
			for _, id := range []string{"019a0000-0000-7000-8000-000000000001", "019a0000-0000-7000-8000-000000000002"} {
				l := ManagementActivityLocator{source, phase, id}
				parsed, err := ParseManagementActivityID(l.String())
				if err != nil || parsed != l {
					t.Fatal("stable ID", err)
				}
				a := ManagementActivity{ActivityID: l.String(), ApplicationID: app, OccurredAt: at}
				if source == 0 {
					a.VersionReview = &VersionReviewActivityTarget{VersionID: id, ReviewID: id, Attempt: 2}
					a.VersionLabelAtSubmission = &label
					a.Kind = VersionReviewRejected
					if phase == 1 {
						a.Kind = VersionReviewSubmitted
					}
				} else {
					a.ProfileReview = &ProfileReviewActivityTarget{ProfileRevisionID: id, ProfileReviewID: id, Attempt: 1}
					a.ProfileRevisionSequence = 2
					a.Kind = ProfileReviewApproved
					if phase == 1 {
						a.Kind = ProfileReviewSubmitted
					}
				}
				if a.Validate() != nil {
					t.Fatal(a)
				}
				rows = append(rows, a)
			}
		}
	}
	slices.SortFunc(rows, CompareManagementActivity)
	for i, a := range rows {
		l := a.Locator()
		if l.Source != i/4 || l.Phase != (i%4)/2 || !strings.HasSuffix(l.ReviewID, []string{"2", "1"}[i%2]) {
			t.Fatal("complete tie order", i, l)
		}
	}
	q, _ := NewManagementActivityQuery(ManagementActivityQuery{PageSize: 100})
	p := ManagementActivityPage{Items: rows, AsOf: at}
	if p.Validate(app, q, at) != nil {
		t.Fatal("valid page")
	}
	p.Items = append(rows, rows[0])
	if p.Validate(app, q, at) == nil {
		t.Fatal("duplicate/out-of-order accepted")
	}
	p.Items = []ManagementActivity{}
	p.NextPageToken = "x"
	if p.Validate(app, q, at) == nil {
		t.Fatal("empty continuation")
	}
	d := ManagementActivityDetail{Activity: rows[0], AsOf: at}
	if d.Validate(app, rows[0].Locator(), at) == nil {
		t.Fatal("missing reject reason")
	}
	reason := "  original\nreason  "
	d.RejectionReason = &reason
	if d.Validate(app, rows[0].Locator(), at) != nil || *d.RejectionReason != reason {
		t.Fatal("original reason")
	}
}
func TestManagementActivity_BRAPP071_QueryCanonicalization(t *testing.T) {
	q, err := NewManagementActivityQuery(ManagementActivityQuery{Kinds: []ManagementActivityKind{ProfileReviewRejected, VersionReviewSubmitted, ProfileReviewRejected}})
	if err != nil || q.PageSize != 20 || !slices.Equal(q.Kinds, []ManagementActivityKind{VersionReviewSubmitted, ProfileReviewRejected}) {
		t.Fatal(q, err)
	}
	for _, q := range []ManagementActivityQuery{{PageSize: -1}, {PageSize: 101}, {PageToken: strings.Repeat("x", 4097)}, {PageToken: string([]byte{255})}, {Kinds: []ManagementActivityKind{"UNKNOWN"}}} {
		if _, err := NewManagementActivityQuery(q); !errors.Is(err, ErrInvalidManagementActivityQuery) {
			t.Fatal(q, err)
		}
	}
	for _, id := range []string{"", "v1.version.submission.bad", "v1.foo.submission.019a0000-0000-7000-8000-000000000001", "v1.version.unknown.019a0000-0000-7000-8000-000000000001"} {
		if _, err := ParseManagementActivityID(id); !errors.Is(err, ErrInvalidManagementActivityQuery) {
			t.Fatal(id, err)
		}
	}
}

func TestManagementActivity_BRAPP068070_DuplicateStableIDAtDifferentTimes(t *testing.T) {
	app := shared.ApplicationID("019a0000-0000-7000-8000-000000000099")
	at := time.Date(2026, 11, 9, 1, 0, 0, 0, time.UTC)
	label := "historical label"
	id := ManagementActivityLocator{Source: 0, Phase: 1, ReviewID: app.String()}
	a := ManagementActivity{ActivityID: id.String(), ApplicationID: app, Kind: VersionReviewSubmitted, OccurredAt: at, VersionReview: &VersionReviewActivityTarget{VersionID: app.String(), ReviewID: app.String(), Attempt: 1}, VersionLabelAtSubmission: &label}
	b := a
	b.OccurredAt = at.Add(-time.Millisecond)
	q, _ := NewManagementActivityQuery(ManagementActivityQuery{})
	if err := (ManagementActivityPage{Items: []ManagementActivity{a, b}, AsOf: at}).Validate(app, q, at); !errors.Is(err, ErrManagementActivityStateInconsistent) {
		t.Fatal("duplicate stable activity ID passed validation", err)
	}
}
