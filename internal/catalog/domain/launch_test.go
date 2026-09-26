package domain

import (
	"errors"
	"iwut-app-center/internal/shared"
	"math"
	"reflect"
	"testing"
)

const appID shared.ApplicationID = "018f0000-0000-7000-8000-000000000001"
const publicationID = "018f0000-0000-7000-8000-000000000002"
const versionID = "018f0000-0000-7000-8000-000000000003"

func TestBR_RUN_005_HostCapabilitiesSet(t *testing.T) {
	got, err := NormalizeHostCapabilities([]string{"user.profile.v1", "camera.read.v1", "user.profile.v1", "a.v99999999999999999999999999"})
	want := []CapabilityName{"a.v99999999999999999999999999", "camera.read.v1", "user.profile.v1"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"", "camera.read.v0", "camera.read.v01", "Camera.read.v1", "camera-read.v1", "camera..v1", "camera.read.v1\n", " camera.read.v1", "camera.read", ".v1", "v1", "camera.read.v-1", "用户.v1"} {
		if _, err := NormalizeHostCapabilities([]string{bad}); !errors.Is(err, ErrInvalidHostCapabilities) {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, empty := range [][]string{nil, {}} {
		got, err := NormalizeHostCapabilities(empty)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatal("empty host set is valid and non-nil")
		}
	}
	required := []CapabilityName{"z.v1", "camera.read.v1", "z.v1", "a.v1"}
	host := []CapabilityName{"camera.read.v1", "extra.v1"}
	missing := MissingCapabilities(required, host)
	if !reflect.DeepEqual(missing, []CapabilityName{"a.v1", "z.v1"}) {
		t.Fatal(missing)
	}
	err = NewHostCapabilitiesInsufficientError(missing)
	var business *Error
	if !errors.Is(err, ErrHostCapabilitiesInsufficient) || !errors.As(err, &business) {
		t.Fatal(err)
	}
	missing[0] = "forged.v1"
	first := business.MissingCapabilities()
	first[0] = "forged.v1"
	if !reflect.DeepEqual(business.MissingCapabilities(), []CapabilityName{"a.v1", "z.v1"}) {
		t.Fatal("error details mutable")
	}
	if !errors.Is(NewHostCapabilitiesInsufficientError([]CapabilityName{"private-invalid"}), ErrInternal) {
		t.Fatal("invalid safe details accepted")
	}
}
func TestBR_RUN_002_004_006_DescriptorRangeAndImmutability(t *testing.T) {
	caps := []CapabilityName{"camera.read.v1"}
	required := []string{"profile.basic"}
	optional := []string{"schedule.read"}
	descriptor, err := NewTestLaunchDescriptor(appID, publicationID, math.MaxInt64, 3, versionID, "v1", "https://example.edu/test", 3, 4, caps, required, optional)
	if err != nil {
		t.Fatal(err)
	}
	caps[0] = "forged.v1"
	required[0] = "forged"
	optional[0] = "forged"
	descriptor.RequiredCapabilities()[0] = "forged.v1"
	descriptor.RequiredScopes()[0] = "forged"
	descriptor.OptionalScopes()[0] = "forged"
	if descriptor.RequiredCapabilities()[0] != "camera.read.v1" || descriptor.RequiredScopes()[0] != "profile.basic" || descriptor.OptionalScopes()[0] != "schedule.read" {
		t.Fatal("descriptor mutable")
	}
	if descriptor.ApplicationID() != appID || descriptor.PublicationID() != publicationID || descriptor.VersionID() != versionID || descriptor.PublicationRevision() != math.MaxInt64 || descriptor.RPCAPIMajor() != 3 || descriptor.RPCAPIMinVersion() != 3 || descriptor.RPCAPIMaxVersionExclusive() != 4 || descriptor.VersionLabel() != "v1" || descriptor.LaunchURL() != "https://example.edu/test" {
		t.Fatal("descriptor projection differs")
	}
	for _, major := range []int32{0, 2, 4, math.MaxInt32} {
		if _, err := NewTestLaunchDescriptor(appID, publicationID, 1, major, versionID, "v1", "https://example.edu", 3, 4, nil, nil, nil); !errors.Is(err, ErrApplicationTestPublicationInconsistent) {
			t.Fatalf("accepted major%d", major)
		}
	}
	for _, bad := range []struct {
		caps               []CapabilityName
		required, optional []string
	}{
		{[]CapabilityName{"z.v1", "a.v1"}, nil, nil}, {[]CapabilityName{"a.v1", "a.v1"}, nil, nil}, {[]CapabilityName{"invalid"}, nil, nil}, {nil, []string{"profile.basic"}, []string{"profile.basic"}}, {nil, []string{"z", "a"}, nil},
	} {
		if _, err := NewTestLaunchDescriptor(appID, publicationID, 1, 3, versionID, "v1", "https://example.edu", 3, 4, bad.caps, bad.required, bad.optional); !errors.Is(err, ErrApplicationTestPublicationInconsistent) {
			t.Fatal("invalid persisted facts accepted")
		}
	}
}
