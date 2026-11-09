package mongo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	"testing"
	"time"
)

func TestManagementActivityCursor_BRAPP071_BindingsAndCanonicalMAC(t *testing.T) {
	key, _ := DeriveManagementActivityCursorKey([]byte("activity-test-only-material"))
	r := NewManagementActivityRepository(nil, key)
	q, _ := ad.NewManagementActivityQuery(ad.ManagementActivityQuery{})
	app := shared.ApplicationID("019a0000-0000-7000-8000-000000000001")
	binding := managementActivityBinding("owner", app, 1, q)
	a := ad.ManagementActivity{ActivityID: (ad.ManagementActivityLocator{Source: 1, Phase: 0, ReviewID: app.String()}).String(), OccurredAt: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)}
	token := r.encodeCursor(binding, a)
	p, err := r.decodeCursor(token, binding)
	if err != nil || p.ReviewID != app.String() || p.At != a.OccurredAt.UnixMilli() {
		t.Fatal(err)
	}
	q.PageSize = 99
	if _, err := r.decodeCursor(token, managementActivityBinding("owner", app, 1, q)); err != nil {
		t.Fatal("page size not bound", err)
	}
	for _, change := range []func(*managementActivityPosition){func(p *managementActivityPosition) { p.App = "other" }, func(p *managementActivityPosition) { p.Caller = "other" }, func(p *managementActivityPosition) { p.Ownership = 3 }, func(p *managementActivityPosition) { p.Kinds = "other" }, func(p *managementActivityPosition) { p.Purpose = "other" }} {
		b := binding
		change(&b)
		if _, err := r.decodeCursor(token, b); !errors.Is(err, ad.ErrInvalidManagementActivityPageToken) {
			t.Fatal("binding", err)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	var c managementActivityCursor
	json.Unmarshal(raw, &c)
	c.Position.At++
	changed, _ := json.Marshal(c)
	for _, bad := range []string{token + "=", "!", base64.RawURLEncoding.EncodeToString(append(raw, ' ')), base64.RawURLEncoding.EncodeToString(changed)} {
		if _, err := r.decodeCursor(bad, binding); !errors.Is(err, ad.ErrInvalidManagementActivityPageToken) {
			t.Fatal("MAC/canonical", err)
		}
	}
	if _, err := DeriveManagementActivityCursorKey(nil); err == nil {
		t.Fatal("empty key")
	}
}
