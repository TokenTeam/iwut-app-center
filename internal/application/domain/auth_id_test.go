package domain

import (
	"errors"
	"testing"
)

func TestAuthID_BR_APP_002_RequiresTrustedIdentity(t *testing.T) {
	t.Parallel()

	id, err := NewAuthID("")
	if id != "" || !errors.Is(err, ErrDeveloperIdentityRequired) {
		t.Fatalf("NewAuthID() = (%q, %v), want empty and DeveloperIdentityRequired", id, err)
	}
	if errCode(t, err) != ErrorCodeDeveloperIdentityRequired {
		t.Fatalf("error code = %q, want %q", errCode(t, err), ErrorCodeDeveloperIdentityRequired)
	}
	var applicationError *Error
	if !errors.As(err, &applicationError) || applicationError.Category() != ErrorCategoryAuthentication {
		t.Fatalf("error category = %v, want Authentication", applicationError)
	}
}

func TestAuthID_BR_APP_002_PreservesOpaqueValue(t *testing.T) {
	t.Parallel()

	const opaqueValue = "auth-provider:value/42"
	id, err := NewAuthID(opaqueValue)
	if err != nil {
		t.Fatalf("NewAuthID() error = %v", err)
	}
	if id.String() != opaqueValue {
		t.Fatalf("String() = %q, want %q", id.String(), opaqueValue)
	}
}
