package mongo

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
)

func newScriptSubmissionFixture(t *testing.T, db *dm.Database) (*ScriptVersionRepository, domain.Version, port.SubmitDraft) {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Millisecond)
	resource, err := domain.NewResource(domain.ResourceID(nextIntegrationApplicationID(t)),
		domain.Metadata{Name: "Original name", Purpose: "Original purpose"}, "resource-owner", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewScriptResourceRepository(db).CreateResource(t.Context(), resource); err != nil {
		t.Fatal(err)
	}
	body, err := domain.NewContent(domain.JSONFormat, "\ufeff{\r\n\"z\":2,\"a\":1\r\n}\r\n")
	if err != nil {
		t.Fatal(err)
	}
	draft, err := domain.NewDraft(domain.VersionID(nextIntegrationApplicationID(t)), resource.ID, "release-1", body, "author", at)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewScriptVersionRepository(db)
	if _, err := repo.CreateDraft(t.Context(), draft); err != nil {
		t.Fatal(err)
	}
	return repo, draft, port.SubmitDraft{
		ResourceID: resource.ID, VersionID: draft.ID, ReviewID: domain.ReviewID(nextIntegrationApplicationID(t)),
		ExpectedRevision: 1, Actor: "submitter", At: at.Add(time.Second + 123*time.Microsecond),
	}
}

func assertBoundScriptPolicy(t *testing.T, db *dm.Database, reviewID domain.ReviewID, policy domain.ReviewPolicy) scriptReviewDocument {
	t.Helper()
	var document scriptReviewDocument
	if err := db.Collection(scriptReviewsCollection).FindOne(t.Context(), bson.M{"reviewId": reviewID}).Decode(&document); err != nil {
		t.Fatal(err)
	}
	bound, err := document.PolicyBinding.policy()
	if err != nil || !reflect.DeepEqual(bound, policy) {
		t.Fatalf("review did not retain the complete expected policy %q: %v", policy.Version, err)
	}
	return document
}

func TestScriptReviewSubmissionIntegration_UCAPP043_BRSCR007008(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, command := newScriptSubmissionFixture(t, db)
	result, err := repo.SubmitDraft(t.Context(), command)
	if err != nil || result.ReviewID != command.ReviewID || result.State != domain.VersionSubmitted ||
		result.Revision != 2 || result.PolicyVersion != domain.InitialPolicyVersion || !result.SubmittedAt.Equal(command.At.Truncate(time.Millisecond)) {
		t.Fatalf("submit result: %#v %v", result, err)
	}
	current, err := repo.loadVersion(t.Context(), command.ResourceID, command.VersionID)
	if err != nil || current.State != domain.VersionSubmitted || current.Revision != 2 || current.Content != draft.Content ||
		!reflect.DeepEqual(current.Contributors, draft.Contributors) || current.UpdatedBy != command.Actor || !current.UpdatedAt.Equal(result.SubmittedAt) {
		t.Fatal("submitted version diverged from draft or receipt", err)
	}
	review := assertBoundScriptPolicy(t, db, command.ReviewID, domain.InitialReviewPolicy())
	if review.Status != domain.ReviewPending || review.Snapshot.Text != draft.Content.Text || review.Snapshot.Digest != draft.Content.Digest ||
		review.Snapshot.SourceRevision != 1 || review.Snapshot.ResourceRevision != 1 || review.Snapshot.VersionLabel != draft.Label ||
		review.Snapshot.ResourceID != draft.ResourceID || review.Snapshot.VersionID != draft.ID ||
		!reflect.DeepEqual(review.Snapshot.Contributors, draft.Contributors) || review.SubmittedBy != command.Actor || !review.SubmittedAt.Equal(result.SubmittedAt) {
		t.Fatal("incomplete or inconsistent review snapshot")
	}
	var audit scriptVersionAuditDocument
	if err := db.Collection(scriptVersionAuditsCollection).FindOne(t.Context(), bson.M{"versionId": draft.ID, "revision": int64(2)}).Decode(&audit); err != nil ||
		audit.Action != scriptDraftSubmitted || audit.Digest != draft.Content.Digest || !audit.ChangedAt.Equal(result.SubmittedAt) {
		t.Fatal("submission audit mismatch", err)
	}
	if _, err := NewScriptResourceRepository(db).ReplaceResourceMetadata(t.Context(), port.ReplaceMetadata{
		ID: draft.ResourceID, ExpectedRevision: 1, Metadata: domain.Metadata{Name: "Updated", Purpose: "Updated purpose"},
		Actor: "resource-owner", At: command.At.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	after := assertBoundScriptPolicy(t, db, command.ReviewID, domain.InitialReviewPolicy())
	if after.Snapshot.ResourceName != "Original name" || after.Snapshot.ResourcePurpose != "Original purpose" || after.Snapshot.ResourceRevision != 1 {
		t.Fatal("resource edit modified the frozen context")
	}
	command.ReviewID = domain.ReviewID(nextIntegrationApplicationID(t))
	if _, err := repo.SubmitDraft(t.Context(), command); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatal("stale resubmission:", err)
	}
	command.ExpectedRevision = 2
	if _, err := repo.SubmitDraft(t.Context(), command); !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatal("submitted version accepted another attempt:", err)
	}
	if _, err := repo.ReplaceDraft(t.Context(), port.ReplaceDraft{
		ResourceID: draft.ResourceID, VersionID: draft.ID, ExpectedRevision: 2, Content: draft.Content,
		Actor: "editor", At: command.At,
	}); !errors.Is(err, domain.ErrVersionNotDraft) {
		t.Fatal("submitted version accepted edit:", err)
	}
	assertCollectionCount(t, db, scriptReviewsCollection, 1)
	assertCollectionCount(t, db, scriptVersionAuditsCollection, 2)
}

func TestScriptReviewSubmissionIntegration_FailClosedAndEditRace(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, command := newScriptSubmissionFixture(t, db)
	policies := db.Collection(scriptReviewPoliciesCollection)
	key := bson.M{"version": domain.InitialPolicyVersion}
	setPolicy := func(values bson.M) {
		t.Helper()
		if _, err := policies.UpdateOne(t.Context(), key, bson.M{"$set": values}); err != nil {
			t.Fatal(err)
		}
	}
	setPolicy(bson.M{"status": scriptPolicyRetired})
	if _, err := repo.SubmitDraft(t.Context(), command); !errors.Is(err, domain.ErrReviewPolicyUnavailable) {
		t.Fatal("submission without active policy:", err)
	}
	setPolicy(bson.M{"status": scriptPolicyActive, "checks.0.title": "Tampered"})
	if _, err := repo.SubmitDraft(t.Context(), command); !errors.Is(err, domain.ErrReviewPolicyUnavailable) {
		t.Fatal("submission with changed formal definition:", err)
	}
	setPolicy(bson.M{"checks": scriptReviewPolicyBinding(domain.InitialReviewPolicy()).Checks})
	audits := db.Collection(scriptVersionAuditsCollection)
	injected, err := audits.InsertOne(t.Context(), scriptVersionAuditDocument{
		ResourceID: draft.ResourceID, VersionID: draft.ID, Revision: 2, Digest: draft.Content.Digest,
		Action: scriptDraftSubmitted, Actor: "test-audit-collision", ChangedAt: command.At,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SubmitDraft(t.Context(), command); err == nil {
		t.Fatal("audit failure did not abort the submission")
	}
	assertCollectionCount(t, db, scriptReviewsCollection, 0)
	v, err := repo.loadVersion(t.Context(), draft.ResourceID, draft.ID)
	if err != nil || !reflect.DeepEqual(v, draft) {
		t.Fatal("failed submission changed draft", err)
	}
	var policy scriptReviewPolicyDocument
	if err := policies.FindOne(t.Context(), key).Decode(&policy); err != nil || policy.CoordinationRevision != 1 {
		t.Fatal("failed submission advanced policy fence", err)
	}
	if _, err := audits.DeleteOne(t.Context(), bson.M{"_id": injected.InsertedID}); err != nil {
		t.Fatal(err)
	}

	replacement, err := domain.NewContent(domain.JSONFormat, "{\"a\":1,\"z\":2}")
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		submitted bool
		err       error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	go func() {
		<-start
		_, err := repo.SubmitDraft(t.Context(), command)
		results <- outcome{submitted: true, err: err}
	}()
	go func() {
		<-start
		_, err := repo.ReplaceDraft(t.Context(), port.ReplaceDraft{
			ResourceID: draft.ResourceID, VersionID: draft.ID, ExpectedRevision: 1,
			Content: replacement, Actor: "editor", At: command.At,
		})
		results <- outcome{err: err}
	}()
	close(start)
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || !errors.Is(second.err, domain.ErrRevisionConflict) {
		t.Fatalf("edit/submit did not consume the revision exactly once: %v / %v", first.err, second.err)
	}
	v, err = repo.loadVersion(t.Context(), draft.ResourceID, draft.ID)
	if err != nil || v.Revision != 2 {
		t.Fatal("competing commands left an invalid revision", err)
	}
	if first.submitted {
		review := assertBoundScriptPolicy(t, db, command.ReviewID, domain.InitialReviewPolicy())
		if v.State != domain.VersionSubmitted || review.Snapshot.Text != draft.Content.Text || review.Snapshot.Digest != draft.Content.Digest {
			t.Fatal("submission won with an inconsistent snapshot")
		}
	} else {
		assertCollectionCount(t, db, scriptReviewsCollection, 0)
		if v.State != domain.VersionDraft || v.Content != replacement {
			t.Fatal("edit won with an inconsistent draft")
		}
	}
}

func TestScriptReviewSubmissionIntegration_PolicyActivationOrders(t *testing.T) {
	initial := domain.InitialReviewPolicy()
	next := domain.ReviewPolicy{Version: "test-crawler-policy-v2", Checks: []domain.ReviewCheck{
		{ID: "v2-check", Title: "V2 fixture", Description: "Synthetic policy for integration only.", Mandatory: true},
	}}
	for _, activateFirst := range []bool{true, false} {
		name := "submit-first"
		want := initial
		if activateFirst {
			name, want = "activate-first", next
		}
		t.Run(name, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			_, _, cmd := newScriptSubmissionFixture(t, db)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()

			var blockedOnce sync.Once
			held := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			defer unblock()
			stopAt := scriptReviewsCollection
			if activateFirst {
				stopAt = scriptReviewPoliciesCollection
			}
			// Pause immediately before inserting, after the transaction has
			// updated the old policy document. The contender reaches its
			// update on that same policy before the winner can commit.
			winnerClient, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{
				Started: func(ctx context.Context, e *event.CommandStartedEvent) {
					if e.CommandName != "insert" || e.DatabaseName != db.Name() || e.Command.Lookup("insert").StringValue() != stopAt {
						return
					}
					blockedOnce.Do(func() {
						close(held)
						select {
						case <-release:
						case <-ctx.Done():
						}
					})
				},
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer winnerClient.Disconnect(context.Background())
			loserClient, loserUpdate := monitoredMongoClient(t, db.Name(), "update")

			activate := func(client *dm.Client) error {
				target := client.Database(db.Name())
				_, err := withScriptTransaction(ctx, target, func(tx context.Context) (bool, error) {
					coll := target.Collection(scriptReviewPoliciesCollection)
					retired, err := coll.UpdateOne(tx, bson.M{"version": initial.Version, "status": scriptPolicyActive},
						bson.M{"$set": bson.M{"status": scriptPolicyRetired}, "$inc": bson.M{"coordinationRevision": int64(1)}})
					if err != nil {
						return false, err
					}
					if retired.MatchedCount != 1 {
						return false, errors.New("fixture activation did not retire the old policy")
					}
					_, err = coll.InsertOne(tx, scriptReviewPolicyDocument{
						Binding: scriptReviewPolicyBinding(next), Status: scriptPolicyActive, CoordinationRevision: 1,
					})
					return err == nil, err
				})
				return err
			}
			results := make(chan error, 2)
			submit := func(client *dm.Client) {
				_, err := NewScriptVersionRepository(client.Database(db.Name())).SubmitDraft(ctx, cmd)
				results <- err
			}
			if activateFirst {
				go func() { results <- activate(winnerClient) }()
			} else {
				go submit(winnerClient)
			}
			awaitMongoCommand(t, ctx, held, "winner policy write")
			if activateFirst {
				go submit(loserClient)
			} else {
				go func() { results <- activate(loserClient) }()
			}
			awaitMongoCommand(t, ctx, loserUpdate, "competing policy write")
			unblock()
			if first, second := <-results, <-results; first != nil || second != nil {
				t.Fatalf("policy activation and submission did not serialize: %v / %v", first, second)
			}
			assertBoundScriptPolicy(t, db, cmd.ReviewID, want)
			assertCollectionCount(t, db, scriptReviewsCollection, 1)
		})
	}
}
