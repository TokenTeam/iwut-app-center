package domain

import (
	"errors"
	"fmt"
	"iwut-app-center/internal/shared"
	"strings"
	"testing"
	"time"
)

const app shared.ApplicationID = "01900000-0000-7000-8000-000000000001"
const first ApplicationTesterJoinLinkID = "01900000-0000-7000-8000-000000000002"
const second ApplicationTesterJoinLinkID = "01900000-0000-7000-8000-000000000003"

var now = time.Date(2026, 9, 22, 1, 0, 0, 0, time.UTC)

func active(t *testing.T) *ApplicationTesterJoinLink {
	t.Helper()
	l, e := NewActiveTesterJoinLink(first, app, NewTesterJoinTokenHash([32]byte{42}), "admin", now)
	if e != nil {
		t.Fatal(e)
	}
	return l
}
func ptr[T any](v T) *T { return &v }
func TestBRTST002005007ImmutableRotationAudit(t *testing.T) {
	old := active(t)
	rotated, err := old.Rotate(second, "next-admin", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if old.Status() != JoinLinkStatusActive || old.RevokedBy() != nil || rotated.Status() != JoinLinkStatusRevoked || *rotated.RevocationReason() != RevocationReasonRotated || *rotated.ReplacedByJoinLinkID() != second || *rotated.RevokedBy() != "next-admin" || !rotated.RevokedAt().Equal(now.Add(time.Hour)) || rotated.CreatedBy() != old.CreatedBy() || !rotated.CreatedAt().Equal(old.CreatedAt()) || rotated.TokenHash().Bytes() != old.TokenHash().Bytes() {
		t.Fatal("rotation changed immutable facts or missed audit")
	}
	if _, err = rotated.Rotate(second, "admin", now); !errors.Is(err, ErrApplicationTesterJoinLinkChanged) {
		t.Fatal("revoked link rotated again")
	}
	revoker := rotated.RevokedBy()
	*revoker = "mutated"
	replacement := rotated.ReplacedByJoinLinkID()
	*replacement = first
	if *rotated.RevokedBy() != "next-admin" || *rotated.ReplacedByJoinLinkID() != second {
		t.Fatal("audit accessors alias state")
	}
}
func TestBRTST007LifecycleFieldCombinations(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      JoinLinkStatus
		by          *shared.AuthID
		at          *time.Time
		reason      *RevocationReason
		replacement *ApplicationTesterJoinLinkID
		valid       bool
	}{
		{"active", JoinLinkStatusActive, nil, nil, nil, nil, true},
		{"active_with_revoker", JoinLinkStatusActive, ptr(shared.AuthID("admin")), nil, nil, nil, false},
		{"active_with_time", JoinLinkStatusActive, nil, &now, nil, nil, false},
		{"active_with_reason", JoinLinkStatusActive, nil, nil, ptr(RevocationReasonRotated), nil, false},
		{"active_with_replacement", JoinLinkStatusActive, nil, nil, nil, ptr(second), false},
		{"rotated", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), &now, ptr(RevocationReasonRotated), ptr(second), true},
		{"missing_replacement", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), &now, ptr(RevocationReasonRotated), nil, false},
		{"self_replacement", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), &now, ptr(RevocationReasonRotated), ptr(first), false},
		{"manual_shape", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), &now, ptr(RevocationReasonManual), nil, true},
		{"manual_with_replacement", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), &now, ptr(RevocationReasonManual), ptr(second), false},
		{"missing_revoker", JoinLinkStatusRevoked, nil, &now, ptr(RevocationReasonRotated), ptr(second), false},
		{"missing_time", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), nil, ptr(RevocationReasonRotated), ptr(second), false},
		{"missing_reason", JoinLinkStatusRevoked, ptr(shared.AuthID("admin")), &now, nil, ptr(second), false},
		{"unknown_status", "UNKNOWN", nil, nil, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RestoreTesterJoinLink(first, app, NewTesterJoinTokenHash([32]byte{1}), tc.status, "admin", now, tc.by, tc.at, tc.reason, tc.replacement)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
func TestBRTST003005006ExpectedCurrentLink(t *testing.T) {
	for _, tc := range []struct {
		name     string
		link     *ApplicationTesterJoinLink
		expected *ApplicationTesterJoinLinkID
		want     error
	}{
		{"first", nil, nil, nil}, {"rotate", active(t), ptr(first), nil}, {"already_exists", active(t), nil, ErrApplicationTesterJoinLinkAlreadyExists}, {"missing", nil, ptr(first), ErrApplicationTesterJoinLinkNotFound}, {"stale", active(t), ptr(second), ErrApplicationTesterJoinLinkChanged}, {"invalid", nil, ptr(ApplicationTesterJoinLinkID("bad")), ErrInvalidTesterJoinLinkId},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewTesterJoinLinkCandidate(app, tc.link)
			if err != nil {
				t.Fatal(err)
			}
			if err = c.EnsureExpected(tc.expected); !errors.Is(err, tc.want) {
				t.Fatalf("%v", err)
			}
		})
	}
}
func TestBRTST004DiagnosticRedaction(t *testing.T) {
	link := active(t)
	result, err := NewCreateOrRotateTesterJoinLinkResult(link, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{link, *link, result, *result, link.TokenHash()} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(format, value); !strings.Contains(got, "redacted") {
				t.Fatal("credential diagnostic not redacted")
			}
		}
	}
}
