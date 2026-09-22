package domain

import (
	"iwut-app-center/internal/shared"
	"reflect"
	"testing"
	"time"
)

func TestBRTST012015016MembershipEpisodeInvariants(t *testing.T) {
	id := ApplicationTesterMembershipID("01900000-0000-7000-8000-000000000007")
	app := shared.ApplicationID("01900000-0000-7000-8000-000000000001")
	link := ApplicationTesterJoinLinkID("01900000-0000-7000-8000-000000000002")
	at := time.Date(2026, 9, 22, 1, 0, 0, 0, time.FixedZone("local", 3600))
	by := shared.AuthID("admin")
	removedAt := at.Add(time.Hour)
	for _, tc := range []struct {
		name    string
		id      ApplicationTesterMembershipID
		app     shared.ApplicationID
		user    shared.AuthID
		link    ApplicationTesterJoinLinkID
		at      time.Time
		status  MembershipStatus
		by      *shared.AuthID
		removed *time.Time
		valid   bool
	}{
		{"active", id, app, "user", link, at, MembershipStatusActive, nil, nil, true},
		{"removed", id, app, "user", link, at, MembershipStatusRemoved, &by, &removedAt, true},
		{"missing_id", "", app, "user", link, at, MembershipStatusActive, nil, nil, false},
		{"missing_app", id, "", "user", link, at, MembershipStatusActive, nil, nil, false},
		{"missing_user", id, app, "", link, at, MembershipStatusActive, nil, nil, false},
		{"missing_link", id, app, "user", "", at, MembershipStatusActive, nil, nil, false},
		{"missing_time", id, app, "user", link, time.Time{}, MembershipStatusActive, nil, nil, false},
		{"active_removal_actor", id, app, "user", link, at, MembershipStatusActive, &by, nil, false},
		{"active_removal_time", id, app, "user", link, at, MembershipStatusActive, nil, &removedAt, false},
		{"removed_missing_actor", id, app, "user", link, at, MembershipStatusRemoved, nil, &removedAt, false},
		{"removed_missing_time", id, app, "user", link, at, MembershipStatusRemoved, &by, nil, false},
		{"invalid_status", id, app, "user", link, at, "UNKNOWN", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := RestoreTesterMembership(tc.id, tc.app, tc.user, tc.status, tc.link, tc.at, tc.by, tc.removed)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid {
				if m != nil {
					t.Fatal("partial membership")
				}
				return
			}
			if m.JoinedAt().Location() != time.UTC || m.MembershipID() != id || m.ApplicationID() != app || m.TesterAuthID() != "user" || m.JoinedViaJoinLinkID() != link {
				t.Fatal("incorrect membership projection")
			}
			if tc.status == MembershipStatusRemoved {
				*m.RemovedBy() = "other"
				*m.RemovedAt() = time.Time{}
				if *m.RemovedBy() != by || !m.RemovedAt().Equal(removedAt) {
					t.Fatal("mutable history")
				}
			}
		})
	}
	// UC010 adds the terminal Remove transition; no transition can restore an
	// existing episode to ACTIVE.
	typ := reflect.TypeOf(&ApplicationTesterMembership{})
	for _, method := range []string{"Activate", "Restore", "SetStatus"} {
		if _, ok := typ.MethodByName(method); ok {
			t.Fatalf("unexpected transition %s", method)
		}
	}
}

func TestBRTST013014MembershipResultCapacity(t *testing.T) {
	m, _ := NewActiveTesterMembership("01900000-0000-7000-8000-000000000007", "01900000-0000-7000-8000-000000000001", "user", "01900000-0000-7000-8000-000000000002", time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC))
	for _, tc := range []struct {
		count, limit int32
		valid        bool
	}{{1, 100, true}, {100, 100, true}, {0, 100, false}, {101, 100, false}, {1, 99, false}} {
		r, err := NewJoinApplicationAsTesterResult(m, false, tc.count, tc.limit)
		if (err == nil) != tc.valid {
			t.Fatalf("count=%d limit=%d err=%v", tc.count, tc.limit, err)
		}
		if tc.valid && (r.Joined() || r.Membership().MembershipID() != m.MembershipID() || r.ActiveTesterCount() != tc.count) {
			t.Fatal("incorrect idempotent result")
		}
	}
}
