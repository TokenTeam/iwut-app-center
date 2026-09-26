package generator

import "testing"

func TestProfileRevisionUUIDv7(t *testing.T) {
	generator := NewApplicationProfileRevisionUUIDv7Generator()
	first, err := generator.NewUUIDv7()
	if err != nil || !first.IsValid() {
		t.Fatalf("UUIDv7: %v", err)
	}
	second, err := generator.NewUUIDv7()
	if err != nil || !second.IsValid() || first == second {
		t.Fatalf("independent UUIDv7: %v", err)
	}
}
