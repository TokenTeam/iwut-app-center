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

func TestSecretFactoryVerifiesOnlyMatchingClientAndSecret(t *testing.T) {
	t.Parallel()
	factory := NewSecretFactory()
	clientID, err := domain.ParseClientID("123e4567-e89b-42d3-a456-426614174000")
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := domain.ParseClientID("123e4567-e89b-42d3-a456-426614174001")
	if err != nil {
		t.Fatal(err)
	}
	plain, digest, err := factory.NewSecret(clientID)
	if err != nil {
		t.Fatal(err)
	}
	if !factory.Verify(clientID, plain, digest) {
		t.Fatal("matching secret did not verify")
	}
	if factory.Verify(clientID, plain+"x", digest) || factory.Verify(otherID, plain, digest) {
		t.Fatal("wrong secret or client verified")
	}
}
