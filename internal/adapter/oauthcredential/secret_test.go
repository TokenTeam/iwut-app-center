package oauthcredential

import (
	"encoding/base64"
	"testing"

	"iwut-app-center/internal/oauthclient/domain"
)

func TestSecretFactoryProducesOpaqueUniqueSecrets(t *testing.T) {
	id, _ := domain.ParseClientID("550e8400-e29b-41d4-a716-446655440000")
	factory := NewSecretFactory()
	first, firstDigest, err := factory.NewSecret(id)
	if err != nil {
		t.Fatal(err)
	}
	second, secondDigest, err := factory.NewSecret(id)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || firstDigest.Bytes() == secondDigest.Bytes() {
		t.Fatal("secrets or digests repeated")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret encoding invalid: len=%d err=%v", len(raw), err)
	}
	if firstDigest.String() != "[redacted oauth client secret digest]" {
		t.Fatal("digest String leaked")
	}
}
