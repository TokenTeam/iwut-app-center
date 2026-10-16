package mongo

import (
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	rd "iwut-app-center/internal/review/domain"
	rp "iwut-app-center/internal/review/port"
	"reflect"
	"testing"
	"time"
)

func TestBR_REV_016_034_FormalPolicyMigrationIntegration(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	m := NewMigrator(db)
	expected := formalVersionReviewPolicy()
	coll := db.Collection(versionReviewPoliciesCollectionName)
	if err := m.applyVersionReviewDefinitionsMigration(t.Context()); err != nil {
		t.Fatal(err)
	}
	var got versionReviewPolicyDocument
	if err := coll.FindOne(t.Context(), bson.M{"version": expected.Version}).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !equalVersionReviewPolicyDocuments(got, expected) {
		t.Fatal("formal policy differs")
	}
	provider := NewVersionReviewPolicyRepository(db)
	if _, err := provider.RequireUsable(t.Context(), "app-version-review-v2", rd.ApplicationVersionReviewSnapshot{}); !errors.Is(err, rp.ErrReviewPolicyChanged) {
		t.Fatal("old policy accepted", err)
	}
	policy, err := provider.RequireUsable(t.Context(), rd.ReviewPolicyVersion(expected.Version), rd.ApplicationVersionReviewSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.RequiredChecks()) != 4 || policy.RequiredChecks()[3].Title() != expected.Definitions[3].Title {
		t.Fatal("lost definitions")
	}
	if _, err = coll.UpdateOne(t.Context(), bson.M{"version": "app-version-review-v2"}, bson.M{"$set": bson.M{"status": "ACTIVE"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = coll.UpdateOne(t.Context(), bson.M{"version": expected.Version}, bson.M{"$set": bson.M{"definitions.0.title": "different immutable content"}}); err != nil {
		t.Fatal(err)
	}
	if err = m.applyVersionReviewDefinitionsMigration(t.Context()); err == nil {
		t.Fatal("overwrote immutable policy")
	}
	if err = coll.FindOne(t.Context(), bson.M{"version": "app-version-review-v2"}).Decode(&got); err != nil || got.Status != "ACTIVE" {
		t.Fatal("retirement escaped rollback", err)
	}
}
func TestBR_REV_034_InvalidPolicyDefinitionsFailClosedIntegration(t *testing.T) {
	for _, mode := range []string{"missing", "short", "duplicate", "mismatched", "empty text", "multiple active"} {
		t.Run(mode, func(t *testing.T) {
			db := reusableIntegrationDatabase(t, integrationClient(t))
			coll := db.Collection(versionReviewPoliciesCollectionName)
			seed := formalVersionReviewPolicy()
			update := bson.M{}
			switch mode {
			case "missing":
				update = bson.M{"$unset": bson.M{"definitions": ""}}
			case "short":
				update = bson.M{"$set": bson.M{"definitions": seed.Definitions[:3]}}
			case "duplicate":
				seed.Definitions[1] = seed.Definitions[0]
				update = bson.M{"$set": bson.M{"definitions": seed.Definitions}}
			case "mismatched":
				seed.Definitions[0].ID = "another"
				update = bson.M{"$set": bson.M{"definitions": seed.Definitions}}
			case "empty text":
				update = bson.M{"$set": bson.M{"definitions.0.description": ""}}
			case "multiple active":
				if _, err := coll.UpdateOne(t.Context(), bson.M{"version": "app-version-review-v2"}, bson.M{"$set": bson.M{"status": "ACTIVE"}}); err != nil {
					t.Fatal(err)
				}
			}
			if len(update) > 0 {
				if _, err := coll.UpdateOne(t.Context(), bson.M{"version": seed.Version}, update, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewApplicationReviewQueryRepository(db).loadCurrentReviewPolicy(t.Context()); !errors.Is(err, rp.ErrReviewQueryStateInconsistent) {
				t.Fatal("invalid policy exposed", err)
			}
		})
	}
}
func TestBR_PRF_043_046_PublishedReviewProjectionIntegration(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	repo, in := decideProfileFixture(t, db)
	query := NewApplicationProfileReviewQueryRepository(db)
	get := func() (*pd.ProfileReviewDetail, error) {
		return query.Get(t.Context(), in.ReviewerID, in.ApplicationID, in.ProfileRevisionID, in.ProfileReviewID)
	}
	first, err := get()
	if err != nil || first.CurrentPublishedProfile == nil || !first.CurrentPublishedProfile.None {
		t.Fatal("missing explicit NONE", err)
	}
	approved, err := repo.DecideReview(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	original := in
	draft, err := repo.CreateDraft(t.Context(), "profile-admin", profileDraftFixture(t, in.ApplicationID, "profile-admin"))
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := repo.SubmitDraft(t.Context(), in.ApplicationID, draft.ProfileRevisionID(), "profile-admin", 1, profileReviewID(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	in.ProfileRevisionID = draft.ProfileRevisionID()
	in.ProfileReviewID = submitted.Review.ProfileReviewID()
	detail, err := get()
	if err != nil {
		t.Fatal(err)
	}
	pub := detail.CurrentPublishedProfile.Published
	if detail.CurrentPublishedProfile.None || pub == nil || pub.ProfileRevisionID != original.ProfileRevisionID.String() || pub.DisplayName != approved.Review.Snapshot().DisplayName().String() {
		t.Fatal("wrong replacement projection")
	}
	// A stale explicit NONE must never overwrite an existing published revision.
	if _, err := repo.DecideReview(t.Context(), in); !errors.Is(err, pd.ErrApplicationProfilePublicationConflict) {
		t.Fatal("stale pointer accepted", err)
	}
	before, err := get()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.CurrentPublishedProfile, detail.CurrentPublishedProfile) {
		t.Fatal("failed decision changed publication")
	}
	for _, mode := range []string{"dangling", "cross application", "unapproved", "missing approval", "drift", "missing pointer"} {
		t.Run(mode, func(t *testing.T) {
			// Each corruption is restored from exact raw documents after its query.
			var target string
			var filter bson.M
			var update bson.M
			switch mode {
			case "dangling":
				target = applicationProfilesCollectionName
				filter = bson.M{"applicationId": in.ApplicationID.String()}
				update = bson.M{"$set": bson.M{"currentPublishedProfileRevisionId": nextIntegrationTesterJoinLinkID(t)}}
			case "cross application":
				target = applicationProfileRevisionsCollectionName
				filter = bson.M{"profileRevisionId": original.ProfileRevisionID.String()}
				update = bson.M{"$set": bson.M{"applicationId": nextIntegrationTesterJoinLinkID(t)}}
			case "unapproved":
				target = applicationProfileRevisionsCollectionName
				filter = bson.M{"profileRevisionId": original.ProfileRevisionID.String()}
				update = bson.M{"$set": bson.M{"reviewStatus": "REJECTED"}}
			case "missing approval":
				target = applicationProfileReviewsCollectionName
				filter = bson.M{"profileReviewId": original.ProfileReviewID.String()}
				update = bson.M{"$set": bson.M{"status": "REJECTED"}}
			case "drift":
				target = applicationProfileRevisionsCollectionName
				filter = bson.M{"profileRevisionId": original.ProfileRevisionID.String()}
				update = bson.M{"$set": bson.M{"displayName": "drift"}}
			case "missing pointer":
				target = applicationProfilesCollectionName
				filter = bson.M{"applicationId": in.ApplicationID.String()}
				update = bson.M{"$unset": bson.M{"currentPublishedProfileRevisionId": ""}}
			}
			c := db.Collection(target)
			saved, e := c.FindOne(t.Context(), filter).Raw()
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.UpdateOne(t.Context(), filter, update, options.UpdateOne().SetBypassDocumentValidation(true)); e != nil {
				t.Fatal(e)
			}
			if _, e = get(); !errors.Is(e, pp.ErrProfileReviewQueryStateInconsistent) {
				t.Fatal("corruption did not fail closed", e)
			}
			if _, e = c.ReplaceOne(t.Context(), bson.M{"_id": saved.Lookup("_id").ObjectID()}, saved, options.Replace().SetBypassDocumentValidation(true)); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestBR_REV_016_PolicyRetiredAfterPreflightCannotCommitIntegration(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	seed := createDecidableReview(t, db, "retirement-owner", "retirement_app", "v1")
	repo := NewApplicationReviewDecisionRepository(db)
	candidate := loadDecisionCandidate(t, repo, seed, "independent-reviewer")
	decision := integrationApprovedDecision(t, "app-version-review-v3", formalVersionReviewPolicy().RequiredChecks, 41, "public-https.v1", "independent-reviewer", time.Now())
	if _, err := db.Collection(versionReviewPoliciesCollectionName).UpdateOne(t.Context(), bson.M{"version": "app-version-review-v3"}, bson.M{"$set": bson.M{"status": "RETIRED"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Decide(t.Context(), candidate, "independent-reviewer", decision); !errors.Is(err, rp.ErrReviewPolicyChanged) {
		t.Fatal("stale preflight committed", err)
	}
	var saved applicationReviewDocument
	if err := db.Collection(applicationReviewsCollectionName).FindOne(t.Context(), bson.M{"reviewId": seed.reviewID.String()}).Decode(&saved); err != nil || saved.Status != "PENDING" || saved.Decision != nil {
		t.Fatal("retired policy changed review", err)
	}
}

func TestBR_PRF_046_PublishedProjectionConcurrentApprovalSnapshotIntegration(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	repo, in := decideProfileFixture(t, db)
	query := NewApplicationProfileReviewQueryRepository(db)
	start := make(chan struct{})
	done := make(chan error, 1)
	go func() { <-start; _, err := repo.DecideReview(t.Context(), in); done <- err }()
	close(start)
	for i := 0; i < 30; i++ {
		d, err := query.Get(t.Context(), in.ReviewerID, in.ApplicationID, in.ProfileRevisionID, in.ProfileReviewID)
		if err != nil {
			t.Fatal(err)
		}
		if d.CurrentPublishedProfile.None {
			if d.Review.Status() != pd.ProfileReviewStatusPending {
				t.Fatal("mixed snapshot: approved review with NONE")
			}
		} else {
			if d.Review.Status() != pd.ProfileReviewStatusApproved || d.CurrentPublishedProfile.Published.ProfileRevisionID != in.ProfileRevisionID.String() || d.CurrentPublishedProfile.Published.DisplayName != d.Review.Snapshot().DisplayName().String() {
				t.Fatal("mixed pointer/content snapshot")
			}
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
