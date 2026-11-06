package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestBRSCR005ContentPreservesAcceptedBytes(t *testing.T) {
	for _, raw := range []string{
		"\uFEFF{\r\n  \"b\":2,\"a\":1\r\n}\r\n ",
		"{\"a\":1,\"b\":2}",
		"{deliberately invalid JSON",
		strings.Repeat("é", MaxContentBytes/2),
	} {
		content, err := NewContent(JSONFormat, raw)
		sum := sha256.Sum256([]byte(raw))
		if err != nil || content.Text != raw || content.Digest != hex.EncodeToString(sum[:]) {
			t.Fatalf("accepted text or digest changed for %d bytes: %v", len(raw), err)
		}
	}
	for _, raw := range []string{"", " \t\n\u3000", "x\x00y", "\xff", strings.Repeat("é", MaxContentBytes/2) + "x"} {
		if _, err := NewContent(JSONFormat, raw); !errors.Is(err, ErrInvalidScriptText) {
			t.Fatalf("invalid text of %d bytes accepted: %v", len(raw), err)
		}
	}
	for _, format := range []string{"", "JSON", "lua"} {
		if _, err := NewContent(format, "{}"); !errors.Is(err, ErrUnsupportedScriptFormat) {
			t.Fatalf("format %q accepted: %v", format, err)
		}
	}
}
