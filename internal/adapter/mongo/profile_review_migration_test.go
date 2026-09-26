package mongo

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestProfileReviewMigrationIntegration(t *testing.T) {
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	_, draft := submitProfileFixture(t, db)
	submission, err := draft.SubmitDraft(1, profileReviewID(t), 1, draft.CreatedBy(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	base := profileReviewToDocument(submission.Review)
	collection := db.Collection(applicationProfileReviewsCollectionName)
	if _, err = collection.InsertOne(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	t.Run("BR-PRF-019 identity attempt source and pending unique indexes", func(t *testing.T) {
		assertIndexNames(t, collection, []string{"_id_", profileReviewIDUniqueIndexName, profileReviewAttemptUniqueIndexName, profileReviewSourceUniqueIndexName, profileReviewPendingUniqueIndexName})
		for _, test := range []struct {
			name   string
			mutate func(*applicationProfileReviewDocument)
		}{
			{profileReviewIDUniqueIndexName, func(d *applicationProfileReviewDocument) {
				d.ProfileRevisionID = nextIntegrationTesterJoinLinkID(t).String()
			}},
			{profileReviewAttemptUniqueIndexName, func(d *applicationProfileReviewDocument) {
				d.ProfileReviewID = nextIntegrationTesterJoinLinkID(t).String()
				d.SourceRevision = 2
			}},
			{profileReviewSourceUniqueIndexName, func(d *applicationProfileReviewDocument) {
				d.ProfileReviewID = nextIntegrationTesterJoinLinkID(t).String()
				d.Attempt = 2
			}},
			{profileReviewPendingUniqueIndexName, func(d *applicationProfileReviewDocument) {
				d.ProfileReviewID = nextIntegrationTesterJoinLinkID(t).String()
				d.Attempt = 2
				d.SourceRevision = 2
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				doc := base
				test.mutate(&doc)
				_, err := collection.InsertOne(t.Context(), doc)
				if !drivermongo.IsDuplicateKeyError(err) || !strings.Contains(err.Error(), test.name) {
					t.Fatalf("expected index %s, got %v", test.name, err)
				}
			})
		}
		// The PENDING constraint is partial: future terminal histories do not occupy
		// it. Such states currently require bypass because their migration is UC016.
		terminal := base
		terminal.ProfileReviewID = nextIntegrationTesterJoinLinkID(t).String()
		terminal.Attempt = 3
		terminal.SourceRevision = 3
		terminal.Status = "APPROVED"
		if _, err := collection.InsertOne(t.Context(), terminal, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal("partial index", err)
		}
	})
	t.Run("BR-PRF-017018019 schema requires complete typed review and null decision", func(t *testing.T) {
		mutations := []struct {
			name   string
			change func(bson.M)
		}{
			{"missing decision", func(d bson.M) { delete(d, "decision") }}, {"object decision", func(d bson.M) { d["decision"] = bson.M{} }},
			{"nonpending status", func(d bson.M) { d["status"] = "APPROVED" }}, {"invalid identity", func(d bson.M) { d["profileReviewId"] = "not-uuid" }},
			{"invalid application", func(d bson.M) { d["applicationId"] = "not-uuid" }}, {"invalid revision identity", func(d bson.M) { d["profileRevisionId"] = "not-uuid" }},
			{"zero attempt", func(d bson.M) { d["attempt"] = int32(0) }}, {"wrong attempt type", func(d bson.M) { d["attempt"] = int64(1) }},
			{"zero source", func(d bson.M) { d["sourceRevision"] = int64(0) }}, {"wrong source type", func(d bson.M) { d["sourceRevision"] = int32(1) }},
			{"missing snapshot", func(d bson.M) { delete(d, "snapshot") }}, {"missing description", func(d bson.M) { delete(d["snapshot"].(bson.M), "description") }},
			{"missing icon", func(d bson.M) { delete(d["snapshot"].(bson.M), "icon") }}, {"empty name", func(d bson.M) { d["snapshot"].(bson.M)["displayName"] = "" }},
			{"long name", func(d bson.M) { d["snapshot"].(bson.M)["displayName"] = strings.Repeat("界", 81) }},
			{"invalid description", func(d bson.M) { d["snapshot"].(bson.M)["description"] = "bad\ntext" }},
			{"invalid icon", func(d bson.M) { d["snapshot"].(bson.M)["icon"] = " bad" }},
			{"empty submitter", func(d bson.M) { d["submittedBy"] = "" }}, {"wrong time type", func(d bson.M) { d["submittedAt"] = "today" }},
			{"snapshot extra", func(d bson.M) { d["snapshot"].(bson.M)["launchUrl"] = "https://example.test" }}, {"review extra", func(d bson.M) { d["scopes"] = bson.A{} }},
		}
		for _, test := range mutations {
			t.Run(test.name, func(t *testing.T) {
				doc := bson.M{"profileReviewId": nextIntegrationTesterJoinLinkID(t).String(), "profileRevisionId": nextIntegrationTesterJoinLinkID(t).String(), "applicationId": draft.ApplicationID().String(), "attempt": int32(1), "sourceRevision": int64(1), "status": "PENDING", "snapshot": bson.M{"displayName": "valid", "description": nil, "icon": nil}, "submittedBy": "actor", "submittedAt": time.Now(), "decision": nil}
				test.change(doc)
				_, err := collection.InsertOne(t.Context(), doc)
				var server drivermongo.ServerError
				if !errors.As(err, &server) || !server.HasErrorCode(121) {
					t.Fatalf("expected validation error, got %v", err)
				}
			})
		}
	})
	t.Run("explicit migration readiness and idempotence", func(t *testing.T) {
		if err := NewMigrator(db).Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		if count, err := db.Collection(migrationLedgerCollectionName).CountDocuments(t.Context(), bson.M{"_id": applicationProfileReviewMigrationID}); err != nil || count != 1 {
			t.Fatal(count, err)
		}
		if _, err := db.Collection(migrationLedgerCollectionName).DeleteOne(t.Context(), bson.M{"_id": applicationProfileReviewMigrationID}); err != nil {
			t.Fatal(err)
		}
		if err := VerifyDeploymentReadiness(t.Context(), db); err == nil {
			t.Fatal("readiness accepted missing review migration")
		}
		if err := NewMigrator(db).Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := VerifyDeploymentReadiness(t.Context(), db); err != nil {
			t.Fatal(err)
		}
	})
}
