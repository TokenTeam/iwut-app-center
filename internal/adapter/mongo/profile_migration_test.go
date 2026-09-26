package mongo

import (
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"strings"
	"testing"
	"time"
)

func TestApplicationProfileMigrationIntegration(t *testing.T) {
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	app := createVersionTestApplication(t, db, "profile-schema", "profile_schema")
	revision, err := profileDraftFixture(t, app.ID(), app.AdminID()).AssignSequence(1)
	if err != nil {
		t.Fatal(err)
	}
	base := profileRevisionToDocument(revision)
	collection := db.Collection(applicationProfileRevisionsCollectionName)
	if _, err = collection.InsertOne(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	t.Run("BR-PRF-001 BR-PRF-006 identity sequence and working partial unique indexes", func(t *testing.T) {
		assertIndexNames(t, collection, []string{"_id_", profileRevisionIDUniqueIndexName, profileRevisionSequenceUniqueIndexName, profileWorkingRevisionUniqueIndexName})
		assertIndexNames(t, db.Collection(applicationProfilesCollectionName), []string{"_id_", profileApplicationUniqueIndexName})
		for _, test := range []struct {
			name   string
			mutate func(*applicationProfileRevisionDocument)
		}{
			{profileRevisionIDUniqueIndexName, func(d *applicationProfileRevisionDocument) {
				d.ApplicationID = nextIntegrationTesterJoinLinkID(t).String()
			}},
			{profileRevisionSequenceUniqueIndexName, func(d *applicationProfileRevisionDocument) {
				d.ProfileRevisionID = nextIntegrationTesterJoinLinkID(t).String()
				d.ReviewStatus = "APPROVED"
				d.Revision = 3
			}},
			{profileWorkingRevisionUniqueIndexName, func(d *applicationProfileRevisionDocument) {
				d.ProfileRevisionID = nextIntegrationTesterJoinLinkID(t).String()
				d.Sequence = 2
				d.ReviewStatus = "SUBMITTED"
				d.Revision = 2
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				doc := base
				test.mutate(&doc)
				_, err := collection.InsertOne(t.Context(), doc)
				if !drivermongo.IsDuplicateKeyError(err) || !strings.Contains(err.Error(), test.name) {
					t.Fatalf("expected unique index %s", test.name)
				}
			})
		}
		for index, status := range []string{"APPROVED", "REJECTED"} {
			doc := base
			doc.ProfileRevisionID = nextIntegrationTesterJoinLinkID(t).String()
			doc.Sequence = int32(index + 2)
			doc.ReviewStatus = status
			doc.Revision = 3
			if _, err := collection.InsertOne(t.Context(), doc); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("BR-PRF-003 BR-PRF-004 BR-PRF-005 BR-PRF-007 validator rejects missing nullables invalid text and audit", func(t *testing.T) {
		raw, err := bson.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			name   string
			mutate func(bson.M)
		}{
			{"missing description", func(d bson.M) { delete(d, "description") }}, {"missing icon", func(d bson.M) { delete(d, "icon") }},
			{"empty description", func(d bson.M) { d["description"] = "" }}, {"empty icon", func(d bson.M) { d["icon"] = "" }},
			{"empty display", func(d bson.M) { d["displayName"] = "" }}, {"long display", func(d bson.M) { d["displayName"] = strings.Repeat("界", 81) }},
			{"long description", func(d bson.M) { d["description"] = strings.Repeat("界", 1001) }}, {"long icon", func(d bson.M) { d["icon"] = strings.Repeat("界", 513) }},
			{"unicode leading space", func(d bson.M) { d["displayName"] = "\u2003text" }}, {"unicode trailing space", func(d bson.M) { d["description"] = "text\u0085" }},
			{"format", func(d bson.M) { d["icon"] = "a\u200db" }}, {"control", func(d bson.M) { d["displayName"] = "a\tb" }},
			{"line separator", func(d bson.M) { d["description"] = "a\u2028b" }}, {"paragraph separator", func(d bson.M) { d["icon"] = "a\u2029b" }},
			{"state", func(d bson.M) { d["reviewStatus"] = "OTHER" }}, {"audit actor", func(d bson.M) { d["updatedBy"] = "other" }},
			{"audit time", func(d bson.M) { d["updatedAt"] = time.Now() }}, {"zero sequence", func(d bson.M) { d["sequence"] = int32(0) }},
			{"sequence BSON type", func(d bson.M) { d["sequence"] = int64(1) }}, {"revision BSON type", func(d bson.M) { d["revision"] = int32(1) }},
			{"unknown field", func(d bson.M) { d["launchUrl"] = "https://example.test" }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				var doc bson.M
				if err := bson.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				doc["profileRevisionId"] = nextIntegrationTesterJoinLinkID(t).String()
				doc["applicationId"] = nextIntegrationTesterJoinLinkID(t).String()
				test.mutate(doc)
				_, err := collection.InsertOne(t.Context(), doc)
				var serverError drivermongo.ServerError
				if !errors.As(err, &serverError) || !serverError.HasErrorCode(121) {
					t.Fatalf("expected validation error for %s", test.name)
				}
			})
		}
	})
	t.Run("BR-PRF-006 projection requires nullable UUIDs unique application", func(t *testing.T) {
		profiles := db.Collection(applicationProfilesCollectionName)
		base := bson.M{"applicationId": app.ID().String(), "workingProfileRevisionId": nil, "currentPublishedProfileRevisionId": nil}
		if _, err := profiles.InsertOne(t.Context(), base); err != nil {
			t.Fatal(err)
		}
		if _, err := profiles.InsertOne(t.Context(), base); !drivermongo.IsDuplicateKeyError(err) {
			t.Fatal("projection application not unique")
		}
		for _, field := range []string{"workingProfileRevisionId", "currentPublishedProfileRevisionId"} {
			for _, missing := range []bool{false, true} {
				doc := bson.M{"applicationId": nextIntegrationTesterJoinLinkID(t).String(), "workingProfileRevisionId": nil, "currentPublishedProfileRevisionId": nil}
				if missing {
					delete(doc, field)
				} else {
					doc[field] = "invalid"
				}
				_, err := profiles.InsertOne(t.Context(), doc)
				var serverError drivermongo.ServerError
				if !errors.As(err, &serverError) || !serverError.HasErrorCode(121) {
					t.Fatal("projection invalid nullable field accepted")
				}
			}
		}
	})
	t.Run("explicit idempotent migration and constructor does not mutate schema", func(t *testing.T) {
		empty := integrationDatabase(t, client)
		_ = NewApplicationProfileRevisionRepository(empty)
		names, err := empty.ListCollectionNames(t.Context(), bson.D{})
		if err != nil || len(names) != 0 {
			t.Fatalf("constructor mutated schema: %v", err)
		}
		if err := NewMigrator(db).Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		n, err := db.Collection(migrationLedgerCollectionName).CountDocuments(t.Context(), bson.D{{Key: "_id", Value: applicationProfileRevisionMigrationID}})
		if err != nil || n != 1 {
			t.Fatalf("migration count=%d err=%v", n, err)
		}
	})
}
