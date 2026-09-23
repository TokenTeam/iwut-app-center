package domain

import (
	"errors"
	"fmt"
	"iwut-app-center/internal/shared"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBRTST031032036ManualRevocationIsImmutableTerminalAudit(t *testing.T) {
	original := active(t)
	at := now.Add(time.Hour).In(time.FixedZone("local", 3600))
	revoked, err := original.Revoke("current-admin", at)
	if err != nil {
		t.Fatal(err)
	}
	if original.Status() != JoinLinkStatusActive || original.RevokedBy() != nil || revoked.Status() != JoinLinkStatusRevoked || *revoked.RevocationReason() != RevocationReasonManual || revoked.ReplacedByJoinLinkID() != nil || *revoked.RevokedBy() != "current-admin" || !revoked.RevokedAt().Equal(at) || revoked.RevokedAt().Location() != time.UTC || revoked.JoinLinkID() != original.JoinLinkID() || revoked.ApplicationID() != original.ApplicationID() || revoked.TokenHash() != original.TokenHash() || revoked.CreatedBy() != original.CreatedBy() || !revoked.CreatedAt().Equal(original.CreatedAt()) {
		t.Fatal("manual transition altered immutable facts")
	}
	repeated, err := revoked.Revoke("another-admin", time.Time{})
	if err != nil || !reflect.DeepEqual(repeated, revoked) {
		t.Fatal("rewrote terminal audit", err)
	}
	*repeated.RevokedBy() = "mutated"
	*repeated.RevokedAt() = time.Time{}
	*repeated.RevocationReason() = RevocationReasonRotated
	if !reflect.DeepEqual(repeated, revoked) {
		t.Fatal("audit aliases caller memory")
	}
	if _, err := revoked.Rotate(second, "admin", at); !errors.Is(err, ErrApplicationTesterJoinLinkChanged) {
		t.Fatal("reactivated revoked link")
	}
}

func TestBRTST030032RotatedLinkCannotBeRewrittenAsManual(t *testing.T) {
	rotated, err := active(t).Rotate(second, "rotating-admin", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := rotated.Revoke("later-admin", now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(repeated, rotated) || *repeated.ReplacedByJoinLinkID() != second {
		t.Fatal("changed rotated audit", err)
	}
	if r, err := NewRevokeTesterJoinLinkResult(repeated, true); r != nil || !errors.Is(err, ErrApplicationTesterJoinLinkStateInconsistent) {
		t.Fatal("reported rotated as newly manually revoked")
	}
}

func TestBRTST031036RevocationRejectsCorruptStateAndInvalidServerAudit(t *testing.T) {
	for _, tc := range []struct {
		admin shared.AuthID
		at    time.Time
	}{{"", now}, {"admin", time.Time{}}} {
		l := active(t)
		r, err := l.Revoke(tc.admin, tc.at)
		if r != nil || !errors.Is(err, ErrInternal) || l.Status() != JoinLinkStatusActive {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*ApplicationTesterJoinLink){
		func(l *ApplicationTesterJoinLink) { l.status = "UNKNOWN" },
		func(l *ApplicationTesterJoinLink) { l.revokedBy = ptr(shared.AuthID("admin")) },
		func(l *ApplicationTesterJoinLink) { l.status = JoinLinkStatusRevoked },
		func(l *ApplicationTesterJoinLink) { l.createdAt = time.Time{} },
	} {
		l := active(t)
		mutate(l)
		if r, err := l.Revoke("admin", now); r != nil || !errors.Is(err, ErrApplicationTesterJoinLinkStateInconsistent) {
			t.Fatal(err)
		}
		if c, err := NewTesterJoinLinkRevocationCandidate(l); c != nil || !errors.Is(err, ErrApplicationTesterJoinLinkStateInconsistent) {
			t.Fatal(err)
		}
	}
	var absent *ApplicationTesterJoinLink
	if _, err := absent.Revoke("admin", now); !errors.Is(err, ErrApplicationTesterJoinLinkStateInconsistent) {
		t.Fatal(err)
	}
}

func TestBRTST032036RevocationSnapshotsCopyAndRedactCredentials(t *testing.T) {
	l, _ := active(t).Revoke("admin", now.Add(time.Hour))
	c, err := NewTesterJoinLinkRevocationCandidate(l)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRevokeTesterJoinLinkResult(l, true)
	if err != nil {
		t.Fatal(err)
	}
	*c.JoinLink().RevokedBy() = "mutated"
	*r.JoinLink().RevokedBy() = "mutated"
	if !reflect.DeepEqual(c.JoinLink(), l) || !reflect.DeepEqual(r.JoinLink(), l) || !r.Revoked() {
		t.Fatal("mutable snapshot")
	}
	for _, value := range []any{c, *c, r, *r} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(format, value); !strings.Contains(got, "redacted") || strings.Contains(got, "value:") {
				t.Fatal("credential details in diagnostics")
			}
		}
	}
	if r, err := NewRevokeTesterJoinLinkResult(active(t), false); r != nil || !errors.Is(err, ErrApplicationTesterJoinLinkStateInconsistent) {
		t.Fatal("active terminal result")
	}
	if c, err := NewTesterJoinLinkRevocationCandidate(nil); c != nil || !errors.Is(err, ErrApplicationTesterJoinLinkStateInconsistent) {
		t.Fatal("nil snapshot")
	}
}
