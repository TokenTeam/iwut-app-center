package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	"testing"
)

func TestProfileReviewDecisionMigrationIntegration(t *testing.T) {
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	repo, in := decideProfileFixture(t, db)
	result, e := repo.DecideReview(t.Context(), in)
	if e != nil {
		t.Fatal(e)
	}
	raw := readProfileReview(t, db, result.Review.ProfileReviewID())
	var base bson.M
	if e = bson.Unmarshal(raw, &base); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		mutate func(bson.M)
	}{
		{"decision missing", func(d bson.M) { delete(d, "decision") }}, {"null terminal", func(d bson.M) { d["decision"] = nil }}, {"outcome mismatch", func(d bson.M) { d["decision"].(bson.D)[0].Value = "REJECTED" }},
		{"pending with decision", func(d bson.M) { d["status"] = "PENDING" }}, {"bad approved content", func(d bson.M) { d["snapshot"].(bson.D)[0].Value = " bad " }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d bson.M
			bson.Unmarshal(raw, &d)
			delete(d, "_id")
			d["profileReviewId"] = profileReviewID(t).String()
			d["profileRevisionId"] = profileReviewID(t).String()
			tc.mutate(d)
			if _, e := db.Collection(applicationProfileReviewsCollectionName).InsertOne(t.Context(), d); e == nil {
				t.Fatal("invalid terminal accepted")
			}
		})
	}
	policies := db.Collection(profileReviewPoliciesCollectionName)
	assertIndexNames(t, policies, []string{"_id_", profileReviewPolicyVersionUniqueIndexName})
	if _, e = policies.UpdateOne(t.Context(), bson.M{"version": in.PolicyVersion}, bson.M{"$set": bson.M{"status": "RETIRED"}}); e != nil {
		t.Fatal(e)
	}
	if e = NewMigrator(db).applyApplicationProfileReviewDecisionMigration(t.Context()); e != nil {
		t.Fatal("idempotency with retirement", e)
	}
	var policy bson.M
	if e = policies.FindOne(t.Context(), bson.M{"version": in.PolicyVersion}).Decode(&policy); e != nil || policy["status"] != "RETIRED" {
		t.Fatal("retired policy reset", e)
	}
	if _, e = policies.UpdateOne(t.Context(), bson.M{"version": in.PolicyVersion}, bson.M{"$set": bson.M{"requiredChecks": []string{"different"}}}); e != nil {
		t.Fatal(e)
	}
	if e = NewMigrator(db).applyApplicationProfileReviewDecisionMigration(t.Context()); e == nil {
		t.Fatal("conflicting immutable seed overwritten")
	}
	// Corrupt terminal checks must be internal corruption, never AlreadyDecided.
	if _, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), bson.M{"profileReviewId": in.ProfileReviewID.String()}, bson.M{"$set": bson.M{"decision.confirmedCheckIds": []string{}}}, options.UpdateOne().SetBypassDocumentValidation(true)); e != nil {
		t.Fatal(e)
	}
	if _, e = profileReviewDecisionCandidate(readProfileReview(t, db, in.ProfileReviewID)); e != pd.ErrApplicationProfileReviewStateInconsistent {
		t.Fatal("malformed terminal", e)
	}
}
