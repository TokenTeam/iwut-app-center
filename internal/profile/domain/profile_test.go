package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"strings"
	"testing"
	"time"
)

func TestBRPRF003004005TextBoundaries(t *testing.T) {
	constructors := []struct {
		name  string
		max   int
		error error
		new   func(string) (string, error)
	}{
		{"displayName", 80, ErrInvalidApplicationDisplayName, func(s string) (string, error) { v, e := NewApplicationDisplayName(s); return v.String(), e }},
		{"description", 1000, ErrInvalidApplicationDescription, func(s string) (string, error) { v, e := NewApplicationDescription(s); return v.String(), e }},
		{"icon", 512, ErrInvalidApplicationIcon, func(s string) (string, error) { v, e := NewApplicationIcon(s); return v.String(), e }},
	}
	for _, ctor := range constructors {
		t.Run(ctor.name, func(t *testing.T) {
			for _, text := range []string{"x", strings.Repeat("😀", ctor.max), strings.Repeat("e\u0301", ctor.max), "a b", "<tag>literal</tag>", "https://127.0.0.1/opaque", "\u034f", "e" + strings.Repeat("\u0301", ctor.max-1)} {
				got, err := ctor.new(text)
				if err != nil {
					t.Fatalf("valid text rejected length=%d: %v", len([]rune(text)), err)
				}
				if canonicalNFC(got) != got {
					t.Fatal("not NFC")
				}
			}
			for _, text := range []string{"", strings.Repeat("x", ctor.max+1), " x", "x ", "\u00a0x", "x\u3000", "x\ny", "x\ty", "x\u200by", "x\u2028y", "x\u2029y", string([]byte{0xff}), string([]byte{0xed, 0xa0, 0x80})} {
				if _, err := ctor.new(text); !errors.Is(err, ctor.error) {
					t.Fatalf("invalid text accepted %q: %v", text, err)
				}
			}
			got, err := ctor.new("Cafe\u0301")
			if err != nil || got != "Café" {
				t.Fatalf("NFC=%q %v", got, err)
			}
		})
	}
}

func TestBRPRF003NFCWithoutStreamSafeMutation(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"e" + strings.Repeat("\u0301", 79), "é" + strings.Repeat("\u0301", 78)},
		{"a" + strings.Repeat("\u0315", 35) + "\u0300", "à" + strings.Repeat("\u0315", 35)},
		{"e\u034f\u0301", "e\u034f\u0301"},
		{"\u1100\u1161\u11a8", "각"},
		{"\u0301\u0327", "\u0327\u0301"},
		{"A\u030a\u0301", "Ǻ"},
	} {
		got, err := NewApplicationDisplayName(tc.input)
		if err != nil || got.String() != tc.want {
			t.Fatalf("got=%q want=%q err=%v", got.String(), tc.want, err)
		}
		if canonicalNFC(got.String()) != got.String() {
			t.Fatal("non-idempotent")
		}
	}
	// 80 NFC code points must be accepted even when the decomposed input is 81.
	input := "e" + strings.Repeat("\u0301", 80)
	got, err := NewApplicationDisplayName(input)
	if err != nil || len([]rune(got.String())) != 80 {
		t.Fatalf("NFC length boundary: %v", err)
	}
}

func TestBRPRF001006007DraftIdentityAuditAndIsolation(t *testing.T) {
	name, _ := NewApplicationDisplayName("课程表")
	description, _ := NewApplicationDescription("original")
	icon, _ := NewApplicationIcon("opaque")
	at := time.Date(2026, 9, 27, 8, 0, 0, 0, time.FixedZone("test", 8*3600))
	draft, err := NewDraftApplicationProfileRevision("01995000-0000-7000-8000-000000000002", "01995000-0000-7000-8000-000000000001", name, &description, &icon, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	description.value = "changed"
	icon.value = "changed"
	revision, err := draft.AssignSequence(2)
	if err != nil {
		t.Fatal(err)
	}
	if revision.Sequence() != 2 || revision.ReviewStatus() != ReviewStatusDraft || revision.Revision() != 1 || revision.CreatedBy() != "admin" || revision.UpdatedBy() != revision.CreatedBy() || !revision.UpdatedAt().Equal(at) || revision.CreatedAt().Location() != time.UTC {
		t.Fatal("initial facts invalid")
	}
	if revision.Description().String() != "original" || revision.Icon().String() != "opaque" {
		t.Fatal("mutable constructor alias")
	}
	revision.Description().value = "mutated"
	revision.Icon().value = "mutated"
	if revision.Description().String() != "original" || revision.Icon().String() != "opaque" {
		t.Fatal("mutable getter alias")
	}
	if _, err := draft.AssignSequence(0); err == nil {
		t.Fatal("zero sequence accepted")
	}
	if _, err := NewDraftApplicationProfileRevision("bad", draft.ApplicationID(), name, nil, nil, "admin", at); err == nil {
		t.Fatal("bad ID")
	}
}

func TestBRPRF005007RestoreRejectsCorruption(t *testing.T) {
	at := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	valid := ApplicationProfileRevisionState{ProfileRevisionID: "01995000-0000-7000-8000-000000000002", ApplicationID: shared.ApplicationID("01995000-0000-7000-8000-000000000001"), Sequence: 1, DisplayName: "Café", ReviewStatus: ReviewStatusDraft, CreatedBy: "a", CreatedAt: at, Revision: 1, UpdatedBy: "a", UpdatedAt: at}
	if _, err := RestoreApplicationProfileRevision(valid); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*ApplicationProfileRevisionState){
		func(s *ApplicationProfileRevisionState) { s.DisplayName = "Cafe\u0301" },
		func(s *ApplicationProfileRevisionState) { v := ""; s.Description = &v },
		func(s *ApplicationProfileRevisionState) { v := "e\u0301"; s.Icon = &v },
		func(s *ApplicationProfileRevisionState) { s.UpdatedBy = "other" },
		func(s *ApplicationProfileRevisionState) { s.UpdatedAt = at.Add(time.Second) },
		func(s *ApplicationProfileRevisionState) { s.ReviewStatus = ReviewStatusSubmitted },
		func(s *ApplicationProfileRevisionState) { s.Revision = 0 },
		func(s *ApplicationProfileRevisionState) { s.Sequence = 0 },
		func(s *ApplicationProfileRevisionState) { s.CreatedBy = "" },
		func(s *ApplicationProfileRevisionState) { s.CreatedAt = time.Time{} },
	} {
		s := valid
		change(&s)
		if _, err := RestoreApplicationProfileRevision(s); !errors.Is(err, ErrApplicationProfileStateInconsistent) {
			t.Fatalf("corruption accepted: %v", err)
		}
	}
	for _, status := range []ReviewStatus{ReviewStatusSubmitted, ReviewStatusApproved, ReviewStatusRejected} {
		s := valid
		s.Revision = 2
		s.ReviewStatus = status
		if _, err := RestoreApplicationProfileRevision(s); err != nil {
			t.Fatalf("valid historical state rejected %s: %v", status, err)
		}
	}
}
