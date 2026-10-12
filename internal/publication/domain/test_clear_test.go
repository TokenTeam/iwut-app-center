package domain

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func TestBRPUB032034TestClearPreservesSlotsAndImmutableHistory(t *testing.T) {
	for _, slots := range []string{"test-only", "test-stable", "test-grey-stable"} {
		t.Run(slots, func(t *testing.T) {
			var stable *ApplicationVersionID
			var grey *GreyRollout
			if slots != "test-only" {
				stable = pointer(versionID)
			}
			if slots == "test-grey-stable" {
				seed, _ := NewCohortSeed(make([]byte, 32))
				grey, _ = RestoreGreyRollout(GreyRolloutID(historyID), versionID, 5000, seed)
			}
			original, err := RestoreApplicationPublicationSlotsWithGrey(publicationID, appID, 3, pointer(versionID), stable, grey, 2, "admin", at, "admin", at)
			if err != nil {
				t.Fatal(err)
			}
			candidate, err := NewTestClearCandidate(original, 2)
			if err != nil {
				t.Fatal(err)
			}
			result, err := candidate.Clear(historyID, "admin", at.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			p, h := result.Publication(), result.History()
			if !result.Changed() || p.TestVersionIDPtr() != nil || p.Revision() != 3 || p.PublicationID() != original.PublicationID() || !reflect.DeepEqual(p.StableVersionIDPtr(), stable) || !reflect.DeepEqual(p.GreyRollout(), grey) || original.TestVersionID() != versionID {
				t.Fatalf("clear modified unexpected state: %v", p)
			}
			if h.Action() != PublicationActionClearTestVersion || h.PreviousVersionID() == nil || *h.PreviousVersionID() != versionID || h.NewVersionIDPtr() != nil || h.ApprovedReviewIDPtr() != nil || h.ScopeCatalogRevisionPtr() != nil || h.PreflightPolicyVersionPtr() != nil || h.GreyRolloutIDPtr() != nil || h.PreviousExposureBasisPointsPtr() != nil || h.NewExposureBasisPointsPtr() != nil || h.CohortSeedPtr() != nil {
				t.Fatalf("invalid clear history: %v", h)
			}
		})
	}
}

func TestBRPUB033TestClearRevisionBeforeNoOpAndOverflow(t *testing.T) {
	p, _ := RestoreApplicationPublicationSlots(publicationID, appID, 3, nil, nil, 2, "admin", at, "admin", at)
	for _, tc := range []struct {
		revision int64
		want     error
	}{{0, ErrInvalidApplicationPublicationRevision}, {1, ErrApplicationPublicationRevisionConflict}, {3, ErrApplicationPublicationRevisionConflict}} {
		if _, err := NewTestClearCandidate(p, tc.revision); !errors.Is(err, tc.want) {
			t.Fatalf("revision %d: %v", tc.revision, err)
		}
	}
	c, _ := NewTestClearCandidate(p, 2)
	result, err := c.Clear("", "", time.Time{})
	if err != nil || result.Changed() || !reflect.DeepEqual(result.Publication(), p) {
		t.Fatalf("no-op: %v %v", result, err)
	}
	if _, err := NewTestClearCandidate(nil, 1); !errors.Is(err, ErrApplicationPublicationNotFound) {
		t.Fatal(err)
	}
	p, _ = RestoreApplicationPublicationSlots(publicationID, appID, 3, pointer(versionID), nil, math.MaxInt64, "admin", at, "admin", at)
	c, _ = NewTestClearCandidate(p, math.MaxInt64)
	if _, err := c.Clear(historyID, "admin", at); !errors.Is(err, ErrInternal) {
		t.Fatal(err)
	}
}
