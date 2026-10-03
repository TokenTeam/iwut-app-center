package domain

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

const greyRolloutID GreyRolloutID = "01900000-0000-7000-8000-000000000020"

func stablePublication(t *testing.T, revision int64, grey *GreyRollout) *ApplicationPublication {
	t.Helper()
	stable := oldVersionID
	publication, err := RestoreApplicationPublicationSlotsWithGrey(publicationID, appID, 3, nil, &stable, grey, revision, "admin", at, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	return publication
}

func approvedGreyCandidate(t *testing.T, publication *ApplicationPublication, version ApplicationVersionID, exposure int32) *GreyPlacementCandidate {
	t.Helper()
	expected := publication.Revision()
	base, err := NewTestPlacementCandidate(appID, 3, version, reviewID, 3, snapshot(t), publication, &expected)
	if err != nil {
		t.Fatal(err)
	}
	points, err := NewExposureBasisPoints(exposure)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := NewGreyPlacementCandidate(base, points)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestBRPUB021025026GreyLifecyclePreservesCohort(t *testing.T) {
	seedBytes := bytes.Repeat([]byte{7}, 32)
	seed, _ := NewCohortSeed(seedBytes)
	validation := PublicationValidation{ScopeCatalogRevision: 19, PreflightPolicyVersion: "submit-v1"}
	start := approvedGreyCandidate(t, stablePublication(t, 4, nil), versionID, 1000)
	if start.ChangeKind() != GreyChangeStart || !start.RequiresValidation() {
		t.Fatalf("start=%#v", start)
	}
	started, err := start.SetGrey(pointer(greyRolloutID), &seed, historyID, "admin", &validation, at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	rollout := started.Publication().GreyRollout()
	if rollout == nil || rollout.RolloutID() != greyRolloutID || rollout.ExposureBasisPoints() != 1000 || started.History().Action() != PublicationActionSetGreyRollout || started.History().CohortSeedPtr() == nil {
		t.Fatalf("started=%#v history=%#v", rollout, started.History())
	}

	increase := approvedGreyCandidate(t, started.Publication(), versionID, 2500)
	increased, err := increase.SetGrey(nil, nil, "01900000-0000-7000-8000-000000000021", "admin", &validation, at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if increased.Publication().GreyRollout().RolloutID() != greyRolloutID || increased.Publication().GreyRollout().CohortSeed().Bytes() != seed.Bytes() || increased.History().Action() != PublicationActionIncreaseGrey || increased.History().CohortSeedPtr() != nil {
		t.Fatal("increase replaced cohort or audit shape")
	}

	points, _ := NewExposureBasisPoints(500)
	reduction, err := NewGreyReductionCandidate(appID, 3, versionID, points, increased.Publication(), increased.Publication().Revision())
	if err != nil || reduction.RequiresValidation() {
		t.Fatalf("reduction=%#v error=%v", reduction, err)
	}
	decreased, err := reduction.SetGrey(nil, nil, "01900000-0000-7000-8000-000000000022", "admin", nil, at.Add(3*time.Minute))
	if err != nil || decreased.History().Action() != PublicationActionDecreaseGrey || decreased.History().ApprovedReviewIDPtr() != nil {
		t.Fatalf("decrease=%#v error=%v", decreased, err)
	}

	replacementVersion := ApplicationVersionID("01900000-0000-7000-8000-000000000023")
	replace := approvedGreyCandidate(t, decreased.Publication(), replacementVersion, 500)
	replaced, err := replace.SetGrey(nil, nil, "01900000-0000-7000-8000-000000000024", "admin", &validation, at.Add(4*time.Minute))
	if err != nil || replaced.History().Action() != PublicationActionReplaceGreyVersion || replaced.Publication().GreyRollout().RolloutID() != greyRolloutID {
		t.Fatalf("replace=%#v error=%v", replaced, err)
	}

	clear, err := NewGreyClearCandidate(replaced.Publication(), replaced.Publication().Revision())
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := clear.Clear("01900000-0000-7000-8000-000000000025", "admin", at.Add(5*time.Minute))
	if err != nil || cleared.Publication().GreyRollout() != nil || cleared.Publication().StableVersionIDPtr() == nil || cleared.History().Action() != PublicationActionClearGreyRollout || cleared.History().NewExposureBasisPointsPtr() != nil {
		t.Fatalf("clear=%#v error=%v", cleared, err)
	}
}

func TestBRPUB023GreyBucketFixedVectorAndBoundary(t *testing.T) {
	seedBytes := make([]byte, 32)
	for i := range seedBytes {
		seedBytes[i] = byte(i)
	}
	seed, _ := NewCohortSeed(seedBytes)
	for exposure, want := range map[int32]bool{664: false, 665: true, 10_000: true} {
		points, _ := NewExposureBasisPoints(exposure)
		rollout, err := RestoreGreyRollout(greyRolloutID, versionID, points, seed)
		if err != nil {
			t.Fatal(err)
		}
		if got := rollout.Matches("user-123"); got != want {
			t.Fatalf("bucket vector exposure=%d got=%t want=%t", exposure, got, want)
		}
	}
	if fmt.Sprintf("%v %#v", seed, seed) != "[redacted grey cohort seed] [redacted grey cohort seed]" {
		t.Fatal("seed formatting disclosed bytes")
	}
}

func TestBRPUB020028GreyRequiresStableAndValidState(t *testing.T) {
	points, _ := NewExposureBasisPoints(100)
	withoutStable, err := RestoreApplicationPublicationSlots(publicationID, appID, 3, nil, nil, 1, "admin", at, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ClassifyGreyChange(withoutStable, versionID, points, 1); err != ErrGreyStableBaselineRequired {
		t.Fatalf("error=%v", err)
	}
	seed, _ := NewCohortSeed(make([]byte, 32))
	rollout, _ := RestoreGreyRollout(greyRolloutID, versionID, points, seed)
	if _, err = RestoreApplicationPublicationSlotsWithGrey(publicationID, appID, 3, nil, nil, rollout, 1, "admin", at, "admin", at); err != ErrApplicationPublicationStateInconsistent {
		t.Fatalf("error=%v", err)
	}
}
