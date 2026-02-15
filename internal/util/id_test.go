package util

import (
	"testing"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNewUUIDv7String(t *testing.T) {
	idStr, err := NewUUIDv7String()
	if err != nil {
		t.Fatalf("NewUUIDv7String error: %v", err)
	}
	parsed, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("Parse UUID: %v", err)
	}
	if parsed.Version() != 7 {
		t.Fatalf("expected UUIDv7, got v%d", parsed.Version())
	}
}

func TestNewObjectIDHex(t *testing.T) {
	hex := NewObjectIDHex()
	if len(hex) != 24 {
		t.Fatalf("expected 24-char hex, got %d", len(hex))
	}
	if _, err := bson.ObjectIDFromHex(hex); err != nil {
		t.Fatalf("ObjectIDFromHex error: %v", err)
	}
}

func TestNewObjectID(t *testing.T) {
	id := NewObjectID()
	if id == bson.NilObjectID {
		t.Fatalf("expected non-nil ObjectID")
	}
}
