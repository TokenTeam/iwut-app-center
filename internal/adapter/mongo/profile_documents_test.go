package mongo

import (
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"testing"
)

func TestProfileDocuments_BR_PRF_003_005_007_RejectCorruptionWithoutRepair(t *testing.T) {
	app := shared.ApplicationID("0195271e-7a00-7000-8000-000000000001")
	revision, err := profileDraftFixture(t, app, "actor").AssignSequence(1)
	if err != nil {
		t.Fatal(err)
	}
	base := profileRevisionToDocument(revision)
	encoded, err := bson.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(bson.M)
	}{
		{"missing description", func(d bson.M) { delete(d, "description") }}, {"missing icon", func(d bson.M) { delete(d, "icon") }},
		{"noncanonical text", func(d bson.M) { d["displayName"] = "Cafe\u0301" }}, {"sequence type", func(d bson.M) { d["sequence"] = int64(1) }},
		{"revision type", func(d bson.M) { d["revision"] = int32(1) }}, {"invalid audit", func(d bson.M) { d["updatedBy"] = "different" }},
		{"nullable type", func(d bson.M) { d["icon"] = int32(0) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var doc bson.M
			if err := bson.Unmarshal(encoded, &doc); err != nil {
				t.Fatal(err)
			}
			test.mutate(doc)
			raw, err := bson.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			result, err := profileRevisionFromRaw(raw)
			if result != nil || !errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
				t.Fatalf("result=%v err=%v", result, err)
			}
		})
	}
	for _, doc := range []bson.M{
		{"applicationId": app.String(), "currentPublishedProfileRevisionId": nil},
		{"applicationId": app.String(), "workingProfileRevisionId": nil},
		{"applicationId": app.String(), "workingProfileRevisionId": "invalid", "currentPublishedProfileRevisionId": nil},
		{"applicationId": app.String(), "workingProfileRevisionId": nil, "currentPublishedProfileRevisionId": int32(1)},
	} {
		raw, err := bson.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := profileFromRaw(raw); !errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
			t.Fatalf("projection error=%v", err)
		}
	}
}
