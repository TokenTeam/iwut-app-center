package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

const (
	validVersionID     ApplicationVersionID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c22"
	validApplicationID shared.ApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"
)

func TestApplicationVersion_BR_VER_001_008_009_InitialServerFields(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.September, 19, 12, 0, 0, 7, time.FixedZone("CST", 8*60*60))
	draft := validDraft(t, createdAt)
	sequence, err := NewVersionSequence(1)
	if err != nil {
		t.Fatalf("NewVersionSequence() error = %v", err)
	}
	version, err := NewApplicationVersion(draft, sequence)
	if err != nil {
		t.Fatalf("NewApplicationVersion() error = %v", err)
	}

	if version.ID() != validVersionID || version.ApplicationID() != validApplicationID || version.Sequence() != 1 {
		t.Fatalf("identity = (%s, %s, %d), want generated IDs and sequence 1", version.ID(), version.ApplicationID(), version.Sequence())
	}
	if version.ReviewStatus() != ReviewStatusDraft || version.Revision() != 1 {
		t.Fatalf("lifecycle = (%s, %d), want DRAFT revision 1", version.ReviewStatus(), version.Revision())
	}
	if version.CreatedBy() != "auth-1" || version.UpdatedBy() != version.CreatedBy() {
		t.Fatalf("audit actors = (%s, %s), want auth-1", version.CreatedBy(), version.UpdatedBy())
	}
	if !version.CreatedAt().Equal(createdAt) || !version.UpdatedAt().Equal(version.CreatedAt()) || version.CreatedAt().Location() != time.UTC {
		t.Fatalf("audit times = (%v, %v), want same UTC instant", version.CreatedAt(), version.UpdatedAt())
	}
	if want := []CapabilityName{"camera.read.v1", "user.profile.v1"}; !reflect.DeepEqual(version.RequiredCapabilities(), want) {
		t.Fatalf("capabilities = %v, want %v", version.RequiredCapabilities(), want)
	}
	if want := []ScopeName{"profile.basic"}; !reflect.DeepEqual(version.RequiredScopes(), want) {
		t.Fatalf("required scopes = %v, want %v", version.RequiredScopes(), want)
	}
}

func TestApplicationVersion_BR_VER_001_SequenceMustStartAtOne(t *testing.T) {
	t.Parallel()
	if _, err := NewVersionSequence(0); !errors.Is(err, ErrInternal) {
		t.Fatalf("NewVersionSequence(0) error = %v, want Internal", err)
	}
	if version, err := NewApplicationVersion(validDraft(t, time.Now()), 0); version != nil || !errors.Is(err, ErrInternal) {
		t.Fatalf("NewApplicationVersion(sequence=0) = (%v, %v), want nil Internal", version, err)
	}
}

func TestApplicationVersion_BR_VER_006_007_ReturnsDefensiveCollectionCopies(t *testing.T) {
	t.Parallel()
	version, err := NewApplicationVersion(validDraft(t, time.Now()), 1)
	if err != nil {
		t.Fatalf("NewApplicationVersion() error = %v", err)
	}
	caps := version.RequiredCapabilities()
	required := version.RequiredScopes()
	optional := version.OptionalScopes()
	caps[0] = "changed.v1"
	required[0] = "changed"
	optional[0] = "changed"
	if version.RequiredCapabilities()[0] != "camera.read.v1" || version.RequiredScopes()[0] != "profile.basic" || version.OptionalScopes()[0] != "schedule.read" {
		t.Fatal("collection getter exposed mutable domain state")
	}
}

func TestApplicationVersion_BR_VER_010_011_012_013_014_ReplaceDraft(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, time.September, 20, 13, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	version, err := NewApplicationVersion(validDraft(t, createdAt), 7)
	if err != nil {
		t.Fatalf("NewApplicationVersion() error = %v", err)
	}
	replacement := validReplacement(t, "v2.0.0", []string{"user.profile.v2", "camera.read.v1"}, []string{"schedule.read", "profile.basic"}, nil)

	updated, err := version.ReplaceDraft(1, replacement, "auth-2", updatedAt)
	if err != nil {
		t.Fatalf("ReplaceDraft() error = %v", err)
	}
	if updated.ID() != version.ID() || updated.ApplicationID() != version.ApplicationID() || updated.Sequence() != version.Sequence() ||
		updated.CreatedBy() != version.CreatedBy() || !updated.CreatedAt().Equal(version.CreatedAt()) || updated.ReviewStatus() != version.ReviewStatus() {
		t.Fatal("immutable identity, creation audit, or review status changed")
	}
	if updated.VersionLabel().String() != "v2.0.0" || updated.LaunchURL().String() != "https://example.edu/v2.0.0" ||
		updated.RPCApiRange().Minimum() != 2 || updated.RPCApiRange().MaximumExclusive() != 5 {
		t.Fatalf("replacement fields were not applied: %#v", updated)
	}
	if want := []CapabilityName{"camera.read.v1", "user.profile.v2"}; !reflect.DeepEqual(updated.RequiredCapabilities(), want) {
		t.Fatalf("capabilities = %v, want %v", updated.RequiredCapabilities(), want)
	}
	if want := []ScopeName{"profile.basic", "schedule.read"}; !reflect.DeepEqual(updated.RequiredScopes(), want) {
		t.Fatalf("required scopes = %v, want %v", updated.RequiredScopes(), want)
	}
	if updated.OptionalScopes() == nil || len(updated.OptionalScopes()) != 0 {
		t.Fatalf("optional scopes = %#v, want non-nil empty set", updated.OptionalScopes())
	}
	if updated.Revision() != 2 || updated.UpdatedBy() != "auth-2" || !updated.UpdatedAt().Equal(updatedAt) || updated.UpdatedAt().Location() != time.UTC {
		t.Fatalf("updated audit = revision %d by %q at %v", updated.Revision(), updated.UpdatedBy(), updated.UpdatedAt())
	}
	if version.Revision() != 1 || version.VersionLabel().String() != "v1.0.0" {
		t.Fatal("ReplaceDraft mutated the source version")
	}
}

func TestApplicationVersion_BR_VER_010_013_RejectsStateAndRevisionWithoutChange(t *testing.T) {
	t.Parallel()
	base, err := NewApplicationVersion(validDraft(t, time.Now()), 1)
	if err != nil {
		t.Fatalf("NewApplicationVersion() error = %v", err)
	}
	replacement := validReplacement(t, "v2", nil, nil, nil)
	nonDraft := restoreVersionForTest(t, base, ReviewStatusSubmitted, 1, base.UpdatedBy(), base.UpdatedAt())

	tests := []struct {
		name             string
		version          *ApplicationVersion
		expectedRevision int64
		want             error
	}{
		{name: "BR-VER-010 submitted", version: nonDraft, expectedRevision: 1, want: ErrApplicationVersionNotDraft},
		{name: "BR-VER-013 stale", version: base, expectedRevision: 2, want: ErrApplicationVersionRevisionConflict},
		{name: "BR-VER-013 missing", version: base, expectedRevision: 0, want: ErrApplicationVersionRevisionRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated, err := test.version.ReplaceDraft(test.expectedRevision, replacement, "auth-2", time.Now())
			if updated != nil || !errors.Is(err, test.want) {
				t.Fatalf("ReplaceDraft() = (%v, %v), want nil and %v", updated, err, test.want)
			}
			if test.version.VersionLabel().String() != "v1.0.0" || test.version.Revision() != 1 {
				t.Fatal("failed replacement changed source version")
			}
		})
	}
}

func TestApplicationVersion_BR_VER_013_014_NormalizedEquivalentReplacementIsNoOp(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	version, err := NewApplicationVersion(validDraft(t, createdAt), 1)
	if err != nil {
		t.Fatalf("NewApplicationVersion() error = %v", err)
	}
	label, _ := NewVersionLabel("v1.0.0")
	launchURL, _ := NewLaunchURL("https://example.edu/app")
	rpcRange, _ := NewRPCApiRange(1, 3)
	capabilities, _ := NewCapabilitySet([]string{"user.profile.v1", "camera.read.v1"})
	scopes, _ := NewScopeRequest([]string{"profile.basic"}, []string{"schedule.read"})
	replacement, err := NewDraftApplicationVersionReplacement(label, launchURL, rpcRange, capabilities, scopes)
	if err != nil {
		t.Fatalf("NewDraftApplicationVersionReplacement() error = %v", err)
	}

	updated, err := version.ReplaceDraft(1, replacement, "auth-other", createdAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("ReplaceDraft() error = %v", err)
	}
	if updated.Revision() != 1 || updated.UpdatedBy() != version.UpdatedBy() || !updated.UpdatedAt().Equal(version.UpdatedAt()) {
		t.Fatalf("no-op changed audit: revision=%d by=%s at=%v", updated.Revision(), updated.UpdatedBy(), updated.UpdatedAt())
	}
}

func validReplacement(
	t *testing.T,
	labelValue string,
	capabilityValues, requiredScopeValues, optionalScopeValues []string,
) DraftApplicationVersionReplacement {
	t.Helper()
	label, err := NewVersionLabel(labelValue)
	if err != nil {
		t.Fatalf("NewVersionLabel() error = %v", err)
	}
	launchURL, err := NewLaunchURL("https://example.edu/" + labelValue)
	if err != nil {
		t.Fatalf("NewLaunchURL() error = %v", err)
	}
	rpcRange, err := NewRPCApiRange(2, 5)
	if err != nil {
		t.Fatalf("NewRPCApiRange() error = %v", err)
	}
	capabilities, err := NewCapabilitySet(capabilityValues)
	if err != nil {
		t.Fatalf("NewCapabilitySet() error = %v", err)
	}
	scopes, err := NewScopeRequest(requiredScopeValues, optionalScopeValues)
	if err != nil {
		t.Fatalf("NewScopeRequest() error = %v", err)
	}
	replacement, err := NewDraftApplicationVersionReplacement(label, launchURL, rpcRange, capabilities, scopes)
	if err != nil {
		t.Fatalf("NewDraftApplicationVersionReplacement() error = %v", err)
	}
	return replacement
}

func restoreVersionForTest(
	t *testing.T,
	version *ApplicationVersion,
	status ReviewStatus,
	revision int64,
	updatedBy shared.AuthID,
	updatedAt time.Time,
) *ApplicationVersion {
	t.Helper()
	capabilities, _ := NewCapabilitySet([]string{"camera.read.v1", "user.profile.v1"})
	scopes, _ := NewScopeRequest([]string{"profile.basic"}, []string{"schedule.read"})
	restored, err := RestoreApplicationVersion(
		version.ID(), version.ApplicationID(), version.Sequence(), version.VersionLabel(), version.LaunchURL(), version.RPCApiRange(),
		capabilities, scopes, status, version.CreatedBy(), version.CreatedAt(), revision, updatedBy, updatedAt,
	)
	if err != nil {
		t.Fatalf("RestoreApplicationVersion() error = %v", err)
	}
	return restored
}

func validDraft(t *testing.T, createdAt time.Time) *DraftApplicationVersion {
	t.Helper()
	label, _ := NewVersionLabel("v1.0.0")
	launchURL, _ := NewLaunchURL("https://example.edu/app")
	apiRange, _ := NewRPCApiRange(1, 3)
	capabilities, _ := NewCapabilitySet([]string{"user.profile.v1", "camera.read.v1"})
	scopes, _ := NewScopeRequest([]string{"profile.basic"}, []string{"schedule.read"})
	draft, err := NewDraftApplicationVersion(
		validVersionID,
		validApplicationID,
		label,
		launchURL,
		apiRange,
		capabilities,
		scopes,
		"auth-1",
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewDraftApplicationVersion() error = %v", err)
	}
	return draft
}
