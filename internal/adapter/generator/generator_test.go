package generator

import (
	"testing"
	"time"
)

func TestUUIDv7Generator_ProducesValidIDs(t *testing.T) {
	t.Parallel()

	generator := NewUUIDv7Generator()
	seen := make(map[string]struct{})
	for index := 0; index < 16; index++ {
		id, err := generator.NewUUIDv7()
		if err != nil {
			t.Fatalf("NewUUIDv7() error = %v", err)
		}
		if !id.IsValid() {
			t.Fatalf("NewUUIDv7() = %q, not a valid UUIDv7", id)
		}
		if _, duplicate := seen[id.String()]; duplicate {
			t.Fatalf("NewUUIDv7() produced duplicate %q", id)
		}
		seen[id.String()] = struct{}{}
	}
}

func TestSystemClock_ReturnsUTCTime(t *testing.T) {
	t.Parallel()

	now := NewSystemClock().Now()
	if now.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", now.Location())
	}
	if time.Since(now) > time.Minute {
		t.Fatalf("clock returned %v, too far from now", now)
	}
}
