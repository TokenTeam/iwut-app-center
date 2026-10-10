package domain

import (
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

func TestApplicationVersionAbandonment_BR_VER_028_033_MinimalImmutableFact(t *testing.T) {
	applicationID, _ := shared.ParseApplicationID("018f7777-7777-7777-8777-777777777771")
	sequence, _ := NewVersionSequence(9)
	label, _ := NewVersionLabel("release candidate")
	at := time.Date(2026, 10, 10, 8, 0, 0, 123456789, time.FixedZone("test", 8*60*60))
	fact, err := NewApplicationVersionAbandonment(
		"018f7777-7777-7777-8777-777777777772", applicationID,
		"018f7777-7777-7777-8777-777777777773", sequence, label, 4, "auth-admin", at,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fact.Sequence() != 9 || fact.VersionLabel().String() != "release candidate" || fact.SourceRevision() != 4 ||
		fact.AbandonedBy() != "auth-admin" || !fact.AbandonedAt().Equal(at.UTC().Truncate(time.Millisecond)) {
		t.Fatalf("fact = %#v", fact)
	}
}

func TestApplicationVersionAbandonment_BR_VER_028_RejectsInvalidAuditFact(t *testing.T) {
	applicationID, _ := shared.ParseApplicationID("018f7777-7777-7777-8777-777777777771")
	sequence, _ := NewVersionSequence(1)
	label, _ := NewVersionLabel("v1")
	valid := func() (ApplicationVersionAbandonmentID, shared.ApplicationID, ApplicationVersionID, VersionSequence, VersionLabel, int64, shared.AuthID, time.Time) {
		return "018f7777-7777-7777-8777-777777777772", applicationID, "018f7777-7777-7777-8777-777777777773", sequence, label, 1, "auth-admin", time.Now()
	}
	id, app, version, seq, versionLabel, revision, actor, at := valid()
	for _, test := range []struct {
		name   string
		mutate func()
	}{
		{"invalid id", func() { id = "invalid" }},
		{"zero revision", func() { revision = 0 }},
		{"missing actor", func() { actor = "" }},
		{"zero time", func() { at = time.Time{} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, app, version, seq, versionLabel, revision, actor, at = valid()
			test.mutate()
			if fact, err := NewApplicationVersionAbandonment(id, app, version, seq, versionLabel, revision, actor, at); fact != nil || err == nil {
				t.Fatalf("NewApplicationVersionAbandonment() = (%v, %v)", fact, err)
			}
		})
	}
}
