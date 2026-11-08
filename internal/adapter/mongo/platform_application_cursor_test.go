package mongo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func TestPlatformApplicationCursor_UCAPP051_BRAPP065(t *testing.T) {
	key, err := DerivePlatformApplicationCursorKey([]byte("incoming-test-only-material"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewPlatformApplicationQueryRepository(nil, key)
	last := domain.PlatformApplicationSummary{ApplicationID: "019a0000-0000-7000-8000-000000000001", Name: "App"}
	q, _ := domain.NewPlatformApplicationQuery(domain.PlatformApplicationQuery{})
	token, err := r.encodeCursor("target", q, last)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := r.decodeCursor(token, "target", q); err != nil || p.ID != last.ApplicationID.String() {
		t.Fatal("round trip", err)
	}
	if _, err := r.decodeCursor(token, "other", q); !errors.Is(err, domain.ErrInvalidPlatformApplicationPageToken) {
		t.Fatal("actor binding", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	var c platformApplicationCursor
	_ = json.Unmarshal(raw, &c)
	c.Position.Purpose = "MY_TEST_APPLICATIONS"
	altered, _ := json.Marshal(c)
	for _, bad := range []string{token + "=", "!", strings.Repeat("x", 4097), base64.RawURLEncoding.EncodeToString(append(raw, ' ')), base64.RawURLEncoding.EncodeToString(altered), strings.Replace(token, "A", "B", 1)} {
		if bad == token {
			continue
		}
		if _, err := r.decodeCursor(bad, "target", q); !errors.Is(err, domain.ErrInvalidPlatformApplicationPageToken) {
			t.Fatalf("invalid cursor accepted %v", err)
		}
	}
	other, _ := DerivePlatformApplicationCursorKey([]byte("different material"))
	if _, err := NewPlatformApplicationQueryRepository(nil, other).decodeCursor(token, "target", q); !errors.Is(err, domain.ErrInvalidPlatformApplicationPageToken) {
		t.Fatal("MAC binding", err)
	}
	longCaller := shared.AuthID(strings.Repeat("x", 4096))
	longToken, err := r.encodeCursor(longCaller, q, last)
	if err != nil || len(longToken) > 4096 {
		t.Fatal("caller binding must remain bounded", err)
	}
	if _, err = r.decodeCursor(longToken, longCaller, q); err != nil {
		t.Fatal("long opaque caller", err)
	}
	if _, err := DerivePlatformApplicationCursorKey(nil); err == nil {
		t.Fatal("empty secret material")
	}
}
