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
