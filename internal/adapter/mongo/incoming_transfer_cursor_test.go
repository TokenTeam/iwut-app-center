package mongo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func TestIncomingTransferCursor_UCAPP050_BRAPP062(t *testing.T) {
	key, err := DeriveIncomingTransferCursorKey([]byte("incoming-test-only-material"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewIncomingApplicationAdminTransferRepository(nil, key)
	last := domain.IncomingApplicationAdminTransfer{TransferID: "019a0000-0000-7000-8000-000000000001", RequestedAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)}
	token, err := r.encodeCursor("target", last)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := r.decodeCursor(token, "target"); err != nil || p.ID != last.TransferID.String() {
		t.Fatal("round trip", err)
	}
	if _, err := r.decodeCursor(token, "other"); !errors.Is(err, domain.ErrInvalidIncomingTransferPageToken) {
		t.Fatal("actor binding", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	var c incomingTransferCursor
	_ = json.Unmarshal(raw, &c)
	c.Position.Purpose = "MY_TEST_APPLICATIONS"
	altered, _ := json.Marshal(c)
	for _, bad := range []string{token + "=", "!", strings.Repeat("x", 4097), base64.RawURLEncoding.EncodeToString(append(raw, ' ')), base64.RawURLEncoding.EncodeToString(altered), strings.Replace(token, "A", "B", 1)} {
		if bad == token {
			continue
		}
		if _, err := r.decodeCursor(bad, "target"); !errors.Is(err, domain.ErrInvalidIncomingTransferPageToken) {
			t.Fatalf("invalid cursor accepted %v", err)
		}
	}
	other, _ := DeriveIncomingTransferCursorKey([]byte("different material"))
	if _, err := NewIncomingApplicationAdminTransferRepository(nil, other).decodeCursor(token, "target"); !errors.Is(err, domain.ErrInvalidIncomingTransferPageToken) {
		t.Fatal("MAC binding", err)
	}
	longCaller := shared.AuthID(strings.Repeat("x", 4096))
	longToken, err := r.encodeCursor(longCaller, last)
	if err != nil || len(longToken) > 4096 {
		t.Fatal("caller binding must remain bounded", err)
	}
	if _, err = r.decodeCursor(longToken, longCaller); err != nil {
		t.Fatal("long opaque caller", err)
	}
	if _, err := DeriveIncomingTransferCursorKey(nil); err == nil {
		t.Fatal("empty secret material")
	}
}
