package mongo

import (
	"encoding/base64"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	pp "iwut-app-center/internal/profile/port"
	"strings"
	"testing"
)

func TestProfileManagementCursor_BRPRF050_OwnershipKindAndLongCaller(t *testing.T) {
	key, err := DeriveProfileManagementCursorKey([]byte("profile-query-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	r := &ProfileManagementRepository{cursorKey: key}
	app := "01890a5d-ac96-774b-bcce-b302099a8057"
	id := "01890a5d-ac96-774b-bcce-b302099a8058"
	caller := strings.Repeat("opaque-", 2000)
	binding := profileManagementBinding(caller, app, "REVISIONS", "", "DRAFT", 1)
	token, err := r.encodeCursor(binding, 7, id)
	if err != nil || len(token) > 4096 {
		t.Fatalf("token size=%d error=%v", len(token), err)
	}
	p, err := r.decodeCursor(token, binding)
	if err != nil || p.Ordinal != 7 || p.ID != id {
		t.Fatal(err)
	}
	for _, badBinding := range []string{
		profileManagementBinding("other", app, "REVISIONS", "", "DRAFT", 1),
		profileManagementBinding(caller, id, "REVISIONS", "", "DRAFT", 1),
		profileManagementBinding(caller, app, "REVIEWS", id, "DRAFT", 1),
		profileManagementBinding(caller, app, "REVISIONS", "", "APPROVED", 1),
		profileManagementBinding(caller, app, "REVISIONS", "", "DRAFT", 3),
	} {
		if _, err := r.decodeCursor(token, badBinding); !errors.Is(err, pp.ErrInvalidProfileManagementPageToken) {
			t.Fatal("cross-context token accepted")
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	for _, bad := range []string{token + "=", strings.Repeat("x", 4097), base64.RawURLEncoding.EncodeToString(append(raw[:len(raw)-1], []byte(`,"extra":1}`)...)), token[:len(token)-2] + "AA"} {
		if _, err := r.decodeCursor(bad, binding); !errors.Is(err, pp.ErrInvalidProfileManagementPageToken) {
			t.Fatal("tampered token accepted")
		}
	}
	other, _ := DeriveProfileManagementCursorKey([]byte("other-key"))
	r.cursorKey = other
	if _, err := r.decodeCursor(token, binding); !errors.Is(err, pp.ErrInvalidProfileManagementPageToken) {
		t.Fatal("rotated signing key accepted old token")
	}
	if _, err := DeriveProfileManagementCursorKey(nil); err == nil {
		t.Fatal("empty key accepted")
	}
}
func TestProfileManagementSources_BRPRF052_NestedDuplicateUTF8AndTimeBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		doc   bson.D
		valid bool
	}{
		{"normal", bson.D{{Key: "x", Value: "valid"}}, true},
		{"duplicate root", bson.D{{Key: "x", Value: 1}, {Key: "x", Value: 2}}, false},
		{"duplicate nested", bson.D{{Key: "decision", Value: bson.D{{Key: "outcome", Value: "APPROVED"}, {Key: "outcome", Value: "REJECTED"}}}}, false},
		{"invalid UTF8", bson.D{{Key: "x", Value: string([]byte{0xff})}}, false},
		{"invalid date", bson.D{{Key: "x", Value: bson.DateTime(253402300800000)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := bson.Marshal(tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			if got := profileManagementRawValid(raw, false); got != tc.valid {
				t.Fatalf("valid=%t", got)
			}
		})
	}
}
