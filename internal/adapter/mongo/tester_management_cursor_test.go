package mongo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"iwut-app-center/internal/shared"
	testerport "iwut-app-center/internal/tester/port"
	"strings"
	"testing"
)

func TestBR_TST_041_TesterManagementCursorCanonicalSignedBindings(t *testing.T) {
	key, err := DeriveTesterManagementCursorKey([]byte("test-material"))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := DeriveApplicationVersionManagementCursorKey([]byte("test-material"))
	if bytes.Equal(key.value[:], other.value[:]) {
		t.Fatal("query domains share key")
	}
	r := NewApplicationTesterManagementRepository(nil, key)
	bind := testerManagementPosition{Version: 1, Caller: "caller", Application: "01890f47-0000-7000-8000-000000003701", Ownership: 1, Kind: "membership", Status: "ACTIVE"}
	position := bind
	position.Millis = 1700000000000
	position.ID = "01890f47-0000-7000-8000-000000003702"
	token, _ := r.encodeCursor(position)
	if c, err := r.decodeCursor(token, bind); err != nil || *c != position {
		t.Fatal("valid token failed")
	}
	for _, field := range []string{"caller", "application", "ownership", "kind", "filter"} {
		b := bind
		switch field {
		case "caller":
			b.Caller = "other"
		case "application":
			b.Application = "01890f47-0000-7000-8000-000000003703"
		case "ownership":
			b.Ownership++
		case "kind":
			b.Kind = "link"
		case "filter":
			b.Status = "ALL"
		}
		if _, err := r.decodeCursor(token, b); !errors.Is(err, testerport.ErrInvalidManagementPageToken) {
			t.Fatal("binding accepted", field)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	var c testerManagementCursor
	_ = json.Unmarshal(raw, &c)
	c.Position.Millis++
	altered, _ := json.Marshal(c)
	for _, bad := range []string{token + "=", strings.Repeat("x", 4097), "!", base64.RawURLEncoding.EncodeToString(append([]byte(" "), raw...)), base64.RawURLEncoding.EncodeToString(altered), base64.RawURLEncoding.EncodeToString(bytes.Replace(raw, []byte(`"v":1`), []byte(`"v":1,"v":1`), 1))} {
		if _, err := r.decodeCursor(bad, bind); !errors.Is(err, testerport.ErrInvalidManagementPageToken) {
			t.Fatal("noncanonical/tampered token accepted")
		}
	}
	if _, err := DeriveTesterManagementCursorKey(nil); err == nil {
		t.Fatal("empty key accepted")
	}
	longBind := bind
	longBind.Caller = testerManagementCaller(shared.AuthID(strings.Repeat("opaque-auth-id", 10000)))
	longPosition := position
	longPosition.Caller = longBind.Caller
	longToken, err := r.encodeCursor(longPosition)
	if err != nil || len(longToken) > 4096 {
		t.Fatal("long caller issued unusable cursor")
	}
	if _, err = r.decodeCursor(longToken, longBind); err != nil {
		t.Fatal("long caller cursor roundtrip", err)
	}
	oversized := position
	oversized.Caller = strings.Repeat("x", 4096)
	if _, err = r.encodeCursor(oversized); err == nil {
		t.Fatal("oversized cursor output")
	}
	outOfRange := position
	outOfRange.Millis = 253402300800000
	badTime, _ := r.encodeCursor(outOfRange)
	if _, err = r.decodeCursor(badTime, bind); err == nil {
		t.Fatal("out of range cursor time")
	}
}
