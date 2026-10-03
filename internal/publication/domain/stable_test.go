package domain

import (
	"testing"
	"time"
)

func TestBRPUB014015StableSetClearAndEmptyPublication(t *testing.T) {
	candidate, err := NewStablePlacementCandidate(appID, 3, versionID, reviewID, 3, snapshot(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	set, err := candidate.SetStable(pointer(publicationID), historyID, "admin", PublicationValidation{19, "submit-v1"}, at)
	if err != nil {
		t.Fatal(err)
	}
	if set.Publication().TestVersionIDPtr() != nil || set.Publication().StableVersionID() != versionID || set.History().Action() != PublicationActionSetStableVersion || set.History().PreviousVersionID() != nil {
		t.Fatalf("stable set=%#v history=%#v", set.Publication(), set.History())
	}
	clearCandidate, err := NewStableClearCandidate(set.Publication(), 1)
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := clearCandidate.Clear(ApplicationPublicationHistoryID("01900000-0000-7000-8000-000000000007"), "admin", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Publication().TestVersionIDPtr() != nil || cleared.Publication().StableVersionIDPtr() != nil || cleared.Publication().Revision() != 2 || cleared.History().Action() != PublicationActionClearStableVersion || cleared.History().PreviousVersionID() == nil || cleared.History().NewVersionIDPtr() != nil || cleared.History().ApprovedReviewIDPtr() != nil || cleared.History().ScopeCatalogRevisionPtr() != nil || cleared.History().PreflightPolicyVersionPtr() != nil {
		t.Fatalf("stable clear=%#v history=%#v", cleared.Publication(), cleared.History())
	}
	noopCandidate, err := NewStableClearCandidate(cleared.Publication(), 2)
	if err != nil {
		t.Fatal(err)
	}
	noop, err := noopCandidate.Clear("", "", time.Time{})
	if err != nil || noop.Changed() || noop.History() != nil || noop.Publication().Revision() != 2 {
		t.Fatalf("empty no-op=%#v error=%v", noop, err)
	}
}
