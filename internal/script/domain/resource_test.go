package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestBRSCR003ResourceMetadataUTF8ByteBounds(t *testing.T) {
	for _, tc := range []struct {
		name, purpose string
		want          error
	}{
		{" " + strings.Repeat("é", 64) + "\t", strings.Repeat("é", 1024), nil},
		{strings.Repeat("é", 65), "purpose", ErrInvalidName},
		{"name", strings.Repeat("é", 1025), ErrInvalidPurpose},
		{" \t", "purpose", ErrInvalidName},
		{"name", "", ErrInvalidPurpose},
		{"n\x00ame", "purpose", ErrInvalidName},
		{"name", "p\x00urpose", ErrInvalidPurpose},
		{"\xff", "purpose", ErrInvalidName},
		{"name", "\xff", ErrInvalidPurpose},
	} {
		if _, err := NewMetadata(tc.name, tc.purpose); !errors.Is(err, tc.want) {
			t.Fatalf("name=%d bytes, purpose=%d bytes: got %v; want %v", len(tc.name), len(tc.purpose), err, tc.want)
		}
	}
}
