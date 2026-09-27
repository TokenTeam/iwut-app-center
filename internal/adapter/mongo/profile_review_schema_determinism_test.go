package mongo

import (
	"bytes"
	"go.mongodb.org/mongo-driver/v2/bson"
	"reflect"
	"testing"
)

// Fresh migrations and sequential upgrades must install the same ordered BSON
// schema. Multi-key maps make this nondeterministic even when validation itself
// is logically equivalent, so reject them recursively and compare encodings.
func TestProfileReviewDecisionSchemasHaveDeterministicBSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() bson.D
	}{
		{"review", profileReviewDecisionValidator}, {"revision", profileRevisionDecisionValidator}, {"policy", profileReviewPolicyValidator},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := tc.build()
			var walk func(reflect.Value)
			walk = func(v reflect.Value) {
				if !v.IsValid() {
					return
				}
				switch v.Kind() {
				case reflect.Interface, reflect.Pointer:
					if !v.IsNil() {
						walk(v.Elem())
					}
				case reflect.Map:
					if v.Len() > 1 {
						t.Fatal("unordered multi-key schema map")
					}
					iter := v.MapRange()
					for iter.Next() {
						walk(iter.Value())
					}
				case reflect.Slice, reflect.Array:
					for i := 0; i < v.Len(); i++ {
						walk(v.Index(i))
					}
				case reflect.Struct:
					for i := 0; i < v.NumField(); i++ {
						walk(v.Field(i))
					}
				}
			}
			walk(reflect.ValueOf(schema))
			expected, err := bson.Marshal(schema)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 64; i++ {
				got, err := bson.Marshal(tc.build())
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(expected, got) {
					t.Fatalf("encoding changed at construction %d", i)
				}
			}
		})
	}
}
