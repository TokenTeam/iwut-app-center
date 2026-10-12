package mongo

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	cd "iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/shared"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUCAPP038_BR_CAT_015_CursorSignedCanonicalBindings(t *testing.T) {
	key, _ := DeriveMyTestApplicationCursorKey([]byte("key material"))
	r := NewMyTestApplicationRepository(nil, key)
	host, _ := cd.NormalizeHostCapabilities([]string{"z.v1", "a.v2", "a.v2"})
	bind := myTestBinding("user", 3, host)
	p := bind
	p.Last = "01890f47-0000-7000-8000-000000000aaa"
	token, err := r.encodeCursor(p)
	if err != nil {
		t.Fatal(err)
	}
	if last, err := r.decodeCursor(token, bind); err != nil || last != p.Last {
		t.Fatal(last, err)
	}
	for _, change := range []func(*myTestPosition){func(p *myTestPosition) { p.Caller = myTestBinding("other", 3, host).Caller }, func(p *myTestPosition) { p.Runtime = myTestBinding("user", 4, host).Runtime }, func(p *myTestPosition) { p.Runtime = myTestBinding("user", 3, nil).Runtime }, func(p *myTestPosition) { p.Kind = "PUBLIC" }, func(p *myTestPosition) { p.Version = 2 }} {
		other := bind
		change(&other)
		if _, err := r.decodeCursor(token, other); !errors.Is(err, cd.ErrInvalidMyTestApplicationPageToken) {
			t.Fatal("binding", err)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	var cursor myTestCursor
	_ = json.Unmarshal(raw, &cursor)
	cursor.Position.Last = "01890f47-0000-7000-8000-000000000bbb"
	changed, _ := json.Marshal(cursor)
	for _, invalid := range []string{token + "=", base64.RawURLEncoding.EncodeToString(append(raw, ' ')), base64.RawURLEncoding.EncodeToString(changed), strings.Repeat("x", 4097), "invalid"} {
		if _, err := r.decodeCursor(invalid, bind); !errors.Is(err, cd.ErrInvalidMyTestApplicationPageToken) {
			t.Fatal("tampered", err)
		}
	}
	long := myTestBinding(shared.AuthID(strings.Repeat("opaque", 100000)), 3, host)
	long.Last = p.Last
	issued, err := r.encodeCursor(long)
	if err != nil || len(issued) > 4096 {
		t.Fatal("unbounded caller", err)
	}
	long.Last = ""
	if _, err = r.decodeCursor(issued, long); err != nil {
		t.Fatal(err)
	}
	otherKey, _ := DeriveTesterManagementCursorKey([]byte("key material"))
	if reflect.DeepEqual(key.value, otherKey.value) {
		t.Fatal("cursor domains overlap")
	}
	if _, err := DeriveMyTestApplicationCursorKey(nil); err == nil {
		t.Fatal("empty key")
	}
}
func TestUCAPP038_BR_CAT_016_StrictBSONShapeAndTime(t *testing.T) {
	valid := applicationTesterMembershipDocument{MembershipID: "01890f47-0000-7000-8000-000000000aaa", ApplicationID: "01890f47-0000-7000-8000-000000000bbb", TesterAuthID: "user", Status: "ACTIVE", JoinedViaJoinLinkID: "01890f47-0000-7000-8000-000000000ccc", JoinedAt: time.Now()}
	raw, _ := bson.Marshal(valid)
	if !pureTestRawValid(raw) || !pureTestShape(raw, reflect.TypeOf(valid)) {
		t.Fatal("valid BSON rejected")
	}
	for _, d := range []bson.D{{{Key: "id", Value: valid.ApplicationID}, {Key: "id", Value: valid.ApplicationID}}, {{Key: "id", Value: strings.ToUpper(valid.ApplicationID)}}, {{Key: "nested", Value: bson.D{{Key: "x", Value: 1}, {Key: "x", Value: 2}}}}, {{Key: "joinedAt", Value: bson.DateTime(253402300800000)}}, {{Key: "createdAt", Value: bson.DateTime(-62135596800001)}}} {
		raw, _ := bson.Marshal(d)
		if pureTestRawValid(raw) {
			t.Fatal("bad BSON accepted", d)
		}
	}
	var fields bson.D
	_ = bson.Unmarshal(raw, &fields)
	if pureTestValueShape(bson.RawValue{Type: bson.TypeInt32, Value: []byte{1, 0, 0, 0}}, reflect.TypeOf(int64(0))) {
		t.Fatal("numeric coercion")
	}
}
