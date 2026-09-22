package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"math"
	"reflect"
	"testing"
	"time"
)

const appID shared.ApplicationID = "01900000-0000-7000-8000-000000000001"
const versionID ApplicationVersionID = "01900000-0000-7000-8000-000000000002"
const reviewID ApplicationReviewID = "01900000-0000-7000-8000-000000000003"
const publicationID ApplicationPublicationID = "01900000-0000-7000-8000-000000000004"
const historyID ApplicationPublicationHistoryID = "01900000-0000-7000-8000-000000000005"
const oldVersionID ApplicationVersionID = "01900000-0000-7000-8000-000000000006"

var at = time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)

func snapshot(t *testing.T) ApplicationVersionReviewSnapshot {
	t.Helper()
	s, e := NewApplicationVersionReviewSnapshot("1.0", "https://iwut.net/app", 3, 5, []string{"camera"}, []ScopeName{"a"}, []ScopeName{"b"})
	if e != nil {
		t.Fatal(e)
	}
	return *s
}
func publication(t *testing.T, revision int64, version ApplicationVersionID) *ApplicationPublication {
	t.Helper()
	p, e := RestoreApplicationPublication(publicationID, appID, 3, version, revision, "old-admin", at, "old-admin", at)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func candidate(t *testing.T, p *ApplicationPublication) *TestPlacementCandidate {
	t.Helper()
	var expected *int64
	if p != nil {
		v := p.Revision()
		expected = &v
	}
	c, e := NewTestPlacementCandidate(appID, 3, versionID, reviewID, 3, snapshot(t), p, expected)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestBRPUB002006PublicationExpectationAndRange(t *testing.T) {
	one, two, zero := int64(1), int64(2), int64(0)
	for _, tc := range []struct {
		name     string
		major    int32
		p        *ApplicationPublication
		expected *int64
		want     error
	}{
		{"first", 3, nil, nil, nil}, {"replace", 3, publication(t, 1, oldVersionID), &one, nil}, {"lower_boundary", 2, nil, nil, ErrApplicationVersionRpcApiIncompatible}, {"upper_boundary", 5, nil, nil, ErrApplicationVersionRpcApiIncompatible}, {"create_existing", 3, publication(t, 1, oldVersionID), nil, ErrApplicationPublicationAlreadyExists}, {"update_missing", 3, nil, &one, ErrApplicationPublicationNotFound}, {"stale", 3, publication(t, 1, oldVersionID), &two, ErrApplicationPublicationRevisionConflict}, {"zero", 3, nil, &zero, ErrInvalidApplicationPublicationRevision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, e := NewTestPlacementCandidate(appID, tc.major, versionID, reviewID, 3, snapshot(t), tc.p, tc.expected)
			if !errors.Is(e, tc.want) {
				t.Fatalf("error %v want %v", e, tc.want)
			}
		})
	}
}
func TestBRPUB004006007010CreateReplaceNoOpImmutable(t *testing.T) {
	validation := PublicationValidation{19, "submit-v1"}
	c := candidate(t, nil)
	r, e := c.PlaceInTest(pointer(publicationID), historyID, "admin", validation, at)
	if e != nil {
		t.Fatal(e)
	}
	p, h := r.Publication(), r.History()
	if !r.Changed() || p.Revision() != 1 || p.RPCAPIMajor() != 3 || p.TestVersionID() != versionID || h.PreviousVersionID() != nil || h.ApprovedReviewID() != reviewID || h.ScopeCatalogRevision() != 19 || h.PublicationRevision() != 1 || h.Action() != PublicationActionSetTestVersion {
		t.Fatalf("bad create %#v %#v", p, h)
	}
	if c.Publication() != nil {
		t.Fatal("candidate mutated")
	}
	old := publication(t, 7, oldVersionID)
	c = candidate(t, old)
	r, e = c.PlaceInTest(nil, historyID, "new-admin", validation, at.Add(time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	p, h = r.Publication(), r.History()
	if p.Revision() != 8 || *h.PreviousVersionID() != oldVersionID || p.CreatedBy() != "old-admin" || p.UpdatedBy() != "new-admin" || old.Revision() != 7 || old.TestVersionID() != oldVersionID {
		t.Fatal("replacement changed old state or audit")
	}
	h.previousVersionID = pointer(versionID)
	if *r.History().PreviousVersionID() != oldVersionID {
		t.Fatal("history copy aliases result")
	}
	p.revision = 99
	if r.Publication().Revision() != 8 {
		t.Fatal("publication copy aliases result")
	}
	c = candidate(t, publication(t, 3, versionID))
	r, e = c.PlaceInTest(nil, "", "", PublicationValidation{}, time.Time{})
	if e != nil || r.Changed() || r.History() != nil || !reflect.DeepEqual(r.Publication(), c.Publication()) {
		t.Fatal("no-op changed state")
	}
}
func pointer[T any](v T) *T { return &v }
func TestBRPUB007008InvalidAuditCannotCreateResult(t *testing.T) {
	for _, tc := range []struct {
		name       string
		id         *ApplicationPublicationID
		history    ApplicationPublicationHistoryID
		admin      shared.AuthID
		validation PublicationValidation
		at         time.Time
	}{
		{"missing_publication", nil, historyID, "admin", PublicationValidation{1, "v1"}, at},
		{"bad_history", pointer(publicationID), "invalid", "admin", PublicationValidation{1, "v1"}, at},
		{"bad_scope", pointer(publicationID), historyID, "admin", PublicationValidation{0, "v1"}, at},
		{"bad_policy", pointer(publicationID), historyID, "admin", PublicationValidation{1, "bad policy"}, at},
		{"bad_admin", pointer(publicationID), historyID, "", PublicationValidation{1, "v1"}, at},
		{"bad_time", pointer(publicationID), historyID, "admin", PublicationValidation{1, "v1"}, time.Time{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := candidate(t, nil).PlaceInTest(tc.id, tc.history, tc.admin, tc.validation, tc.at)
			if r != nil || !errors.Is(e, ErrInternal) {
				t.Fatalf("%v %v", r, e)
			}
		})
	}
	if _, e := candidate(t, publication(t, math.MaxInt64, oldVersionID)).PlaceInTest(nil, historyID, "admin", PublicationValidation{1, "v1"}, at); !errors.Is(e, ErrInternal) {
		t.Fatal("revision overflow accepted")
	}
}
func TestBRREV004SnapshotDefensiveCopies(t *testing.T) {
	s := snapshot(t)
	caps := s.RequiredCapabilities()
	caps[0] = "mutated"
	scopes := s.RequiredScopes()
	scopes[0] = "mutated"
	if !s.Equal(snapshot(t)) {
		t.Fatal("snapshot mutated")
	}
	c := candidate(t, nil)
	scopes = c.AllScopes()
	scopes[0] = "mutated"
	if !reflect.DeepEqual(c.AllScopes(), []ScopeName{"a", "b"}) {
		t.Fatal("candidate scopes mutated")
	}
}
