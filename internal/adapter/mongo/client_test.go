package mongo

import (
	"strings"
	"testing"
)

func TestNewClient_RejectsEmptyURI(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(""); err == nil {
		t.Fatal("NewClient(\"\") error = nil, want error")
	}
	if _, err := NewClient("   "); err == nil {
		t.Fatal("NewClient(blank) error = nil, want error")
	}
}

func TestNewClient_RejectsMalformedURI(t *testing.T) {
	t.Parallel()

	_, err := NewClient("://not-a-uri")
	if err == nil {
		t.Fatal("NewClient(malformed) error = nil, want error")
	}
	if !strings.Contains(err.Error(), "mongodb") {
		t.Fatalf("NewClient(malformed) error = %v", err)
	}
}

func TestNewDatabase_RejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	if _, err := NewDatabase(nil, "app_center"); err == nil {
		t.Fatal("NewDatabase(nil, ...) error = nil, want error")
	}
	if _, err := NewDatabase(nil, ""); err == nil {
		t.Fatal("NewDatabase(nil, \"\") error = nil, want error")
	}
}
