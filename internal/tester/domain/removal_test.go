package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"reflect"
	"testing"
	"time"
)

func removalMembership(t *testing.T) *ApplicationTesterMembership {
	t.Helper()
	m, err := NewActiveTesterMembership("01900000-0000-7000-8000-000000000007", "01900000-0000-7000-8000-000000000001", "tester", "01900000-0000-7000-8000-000000000002", time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBRTST021022023028RemovalRetainsEpisodeAndImmutableAudit(t *testing.T) {
	m := removalMembership(t)
	at := m.JoinedAt().Add(time.Hour).In(time.FixedZone("local", 3600))
	r, err := m.Remove("admin", at)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status() != MembershipStatusActive || m.RemovedBy() != nil || m.RemovedAt() != nil {
		t.Fatal("mutated original episode")
	}
	if r.Status() != MembershipStatusRemoved || r.MembershipID() != m.MembershipID() || r.ApplicationID() != m.ApplicationID() || r.TesterAuthID() != m.TesterAuthID() || r.JoinedViaJoinLinkID() != m.JoinedViaJoinLinkID() || !r.JoinedAt().Equal(m.JoinedAt()) || *r.RemovedBy() != "admin" || !r.RemovedAt().Equal(at) || r.RemovedAt().Location() != time.UTC {
		t.Fatal("lost episode audit")
	}
	repeated, err := r.Remove("another-admin", at.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(r, repeated) {
		t.Fatalf("rewrote terminal audit: %v", err)
	}
	*repeated.RemovedBy() = "mutated"
	*repeated.RemovedAt() = time.Time{}
	if *r.RemovedBy() != "admin" || !r.RemovedAt().Equal(at) {
		t.Fatal("audit aliases caller memory")
	}
	for _, method := range []string{"Activate", "Restore", "Ban", "Blacklist", "SetRemovalReason"} {
		if _, ok := reflect.TypeOf(r).MethodByName(method); ok {
			t.Fatalf("unexpected command %s", method)
		}
	}
}

func TestBRTST023RemovalRequiresValidServerAudit(t *testing.T) {
	at := time.Date(2026, 9, 22, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		admin shared.AuthID
		at    time.Time
	}{{"", at}, {"admin", time.Time{}}} {
		m := removalMembership(t)
		r, err := m.Remove(tc.admin, tc.at)
		if r != nil || !errors.Is(err, ErrInternal) || m.Status() != MembershipStatusActive {
			t.Fatalf("invalid audit accepted: %v", err)
		}
	}
	var absent *ApplicationTesterMembership
	if _, err := absent.Remove("admin", at); !errors.Is(err, ErrApplicationTesterStateInconsistent) {
		t.Fatal(err)
	}
}

func TestBRTST022024026RemovalSnapshotCapacityInvariants(t *testing.T) {
	active := removalMembership(t)
	removed, _ := active.Remove("admin", active.JoinedAt().Add(time.Hour))
	if result, err := NewRemoveApplicationTesterResult(removed, true, 100, 100, true); result != nil || !errors.Is(err, ErrApplicationTesterStateInconsistent) {
		t.Fatal("a newly released slot cannot leave the count at 100")
	}
	for _, tc := range []struct {
		name  string
		m     *ApplicationTesterMembership
		count int32
		valid bool
	}{
		{"active_one", active, 1, true}, {"active_full", active, 100, true}, {"active_zero", active, 0, false},
		{"removed_zero", removed, 0, true}, {"removed_full", removed, 100, true}, {"negative", removed, -1, false}, {"over_limit", active, 101, false}, {"missing", nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, link := range []bool{false, true} {
				c, err := NewTesterRemovalCandidate(tc.m, tc.count, link)
				if (err == nil) != tc.valid {
					t.Fatalf("candidate: %v", err)
				}
				if !tc.valid {
					if c != nil || !errors.Is(err, ErrApplicationTesterStateInconsistent) {
						t.Fatal(err)
					}
					continue
				}
				if c.ActiveTesterCount() != tc.count || c.ActiveJoinLinkExists() != link || !reflect.DeepEqual(c.Membership(), tc.m) {
					t.Fatal("bad snapshot")
				}
			}
		})
	}
	for _, tc := range []struct {
		m            *ApplicationTesterMembership
		count, limit int32
		valid        bool
	}{{removed, 0, 100, true}, {removed, 100, 100, true}, {removed, -1, 100, false}, {removed, 101, 100, false}, {removed, 1, 99, false}, {active, 1, 100, false}, {nil, 0, 100, false}} {
		r, err := NewRemoveApplicationTesterResult(tc.m, false, tc.count, tc.limit, true)
		if (err == nil) != tc.valid {
			t.Fatalf("result: %v", err)
		}
		if tc.valid {
			if r.Removed() || r.TesterLimit() != 100 || r.ActiveTesterCount() != tc.count || !r.ActiveJoinLinkExists() {
				t.Fatal("bad result")
			}
			*r.Membership().RemovedBy() = "mutated"
			if *r.Membership().RemovedBy() != "admin" {
				t.Fatal("mutable result")
			}
		} else if r != nil || !errors.Is(err, ErrApplicationTesterStateInconsistent) {
			t.Fatal(err)
		}
	}
}

func TestBRTST021MembershipPathIDValidation(t *testing.T) {
	if _, err := ParseTesterMembershipID("bad"); !errors.Is(err, ErrInvalidTesterMembershipId) {
		t.Fatal(err)
	}
	value := "01900000-0000-7000-8000-00000000ABCD"
	id, err := ParseTesterMembershipID(value)
	if err != nil || id.String() != "01900000-0000-7000-8000-00000000abcd" {
		t.Fatalf("%s: %v", id, err)
	}
}
