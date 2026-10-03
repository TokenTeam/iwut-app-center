package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
)

func TestClientIDRequiresCanonicalUUIDv4(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{"123e4567-e89b-42d3-a456-426614174000", true},
		{"123E4567-E89B-42D3-A456-426614174000", false},
		{"123e4567-e89b-72d3-a456-426614174000", false},
		{"123e4567-e89b-42d3-7456-426614174000", false},
	} {
		id, err := ParseClientID(test.value)
		if test.valid != (err == nil) || test.valid != id.IsValid() {
			t.Fatalf("value=%q id=%q err=%v", test.value, id, err)
		}
	}
}

func TestRegistrationEnforcesTypedSlotsAndReturnsCopies(t *testing.T) {
	applicationID, ok := shared.ParseApplicationID("01890f47-0000-7000-8000-000000000018")
	if !ok {
		t.Fatal("invalid fixture application ID")
	}
	clientID, _ := ParseClientID("123e4567-e89b-42d3-a456-426614174000")
	at := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	public, err := NewClientIdentity(clientID, ClientTypePublicPKCE, "admin", at)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := RestoreRegistration(applicationID, ChannelTest, public, nil, 1, at, at)
	if err != nil {
		t.Fatal(err)
	}
	copyValue := registration.PublicClient()
	copyValue.status = ClientStatusDisabled
	if registration.PublicClient().Status() != ClientStatusActive {
		t.Fatal("caller mutated registration identity")
	}
	if _, err := RestoreRegistration(applicationID, ChannelTest, nil, public, 1, at, at); !IsCode(err, ErrorCodeOAuthClientStateInconsistent) {
		t.Fatalf("wrong slot error=%v", err)
	}
	if _, err := RestoreRegistration(applicationID, ChannelGrey, public, nil, 1, at, at); !IsCode(err, ErrorCodeOAuthClientStateInconsistent) {
		t.Fatalf("disabled channel restore error=%v", err)
	}
	stable, err := RestoreRegistration(applicationID, ChannelStable, public, nil, 1, at, at)
	if err != nil || stable.Channel() != ChannelStable {
		t.Fatalf("stable registration=%#v error=%v", stable, err)
	}
}

func TestStableProviderContextDoesNotRequireTesterMembership(t *testing.T) {
	applicationID, _ := shared.ParseApplicationID("01890f47-0000-7000-8000-000000000018")
	clientID, _ := ParseClientID("123e4567-e89b-42d3-a456-426614174000")
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	identity, _ := NewClientIdentity(clientID, ClientTypePublicPKCE, "admin", at)
	registration, err := RestoreRegistration(applicationID, ChannelStable, identity, nil, 1, at, at)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := NewClientConfiguration(registration, clientID, nil)
	if err != nil {
		t.Fatal(err)
	}
	display, err := NewApplicationDisplay("01890f47-0000-7000-8000-000000000019", "Stable App", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeConfiguration(configuration, 1, "admin", "01890f47-0000-7000-8000-000000000020", 2, []string{"https://example.edu/callback"}, []string{}, []string{}, display, at)
	if err != nil {
		t.Fatal(err)
	}
	context, err := NewAuthorizationContext(runtime, "ordinary-user", "")
	if err != nil || context.TesterMembershipID != "" {
		t.Fatalf("context=%#v error=%v", context, err)
	}
	if _, err := NewAuthorizationContext(runtime, "ordinary-user", "01890f47-0000-7000-8000-000000000021"); !IsCode(err, ErrorCodeOAuthClientStateInconsistent) {
		t.Fatalf("stable tester membership accepted: %v", err)
	}
	snapshot, err := NewPublishedRedirectSnapshot(applicationID, []PublishedRedirectEntry{
		{Channel: ChannelStable, RPCAPIMajor: 1, VersionID: runtime.VersionID, PublicationRevision: 2},
		{Channel: ChannelTest, RPCAPIMajor: 1, VersionID: runtime.VersionID, PublicationRevision: 2},
	}, []string{"https://example.edu/callback"}, at)
	if err != nil || len(snapshot.Entries) != 2 {
		t.Fatalf("snapshot=%#v error=%v", snapshot, err)
	}
}

func TestSecretDigestFormattingIsAlwaysRedacted(t *testing.T) {
	var bytes [32]byte
	copy(bytes[:], []byte("private digest sentinel"))
	digest := NewSecretDigest(bytes)
	for _, rendered := range []string{digest.String(), fmt.Sprintf("%v", digest), fmt.Sprintf("%+v", digest), fmt.Sprintf("%#v", digest)} {
		if strings.Contains(rendered, "private digest sentinel") || strings.Contains(rendered, fmt.Sprintf("%v", bytes)) {
			t.Fatalf("digest leaked through formatting: %q", rendered)
		}
	}
}
