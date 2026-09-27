package mongo

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func decideProfileFixture(t *testing.T, db *dm.Database) (*ApplicationProfileRevisionRepository, pp.ProfileReviewDecisionInput) {
	t.Helper()
	repo, draft := submitProfileFixture(t, db)
	submission, e := repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, profileReviewID(t), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	return repo, pp.ProfileReviewDecisionInput{ApplicationID: draft.ApplicationID(), ProfileRevisionID: draft.ProfileRevisionID(), ProfileReviewID: submission.Review.ProfileReviewID(), ReviewerID: "independent-reviewer", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: 2, PolicyVersion: pd.InitialProfileReviewPolicyVersion, Outcome: "APPROVE", ConfirmedCheckIDs: pd.InitialProfileReviewChecks(), DecidedAt: time.Now()}
}
func readProfileReview(t *testing.T, db *dm.Database, id pd.ApplicationProfileReviewID) bson.Raw {
	t.Helper()
	raw, e := db.Collection(applicationProfileReviewsCollectionName).FindOne(t.Context(), bson.M{"profileReviewId": id.String()}).Raw()
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestDecideProfileReviewIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, outcome := range []string{"APPROVE", "REJECT"} {
		t.Run("BR-PRF-024026030031032 "+outcome, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			repo, in := decideProfileFixture(t, db)
			in.Outcome = outcome
			reason := "Cafe\u0301 manual reason"
			in.Reason = &reason
			if outcome == "REJECT" {
				in.ConfirmedCheckIDs = nil
			}
			before := readProfileReview(t, db, in.ProfileReviewID)
			result, e := repo.DecideReview(t.Context(), in)
			if e != nil {
				t.Fatal(e)
			}
			if result.ProfileRevision.Revision() != 3 || result.Review.Decision() == nil || *result.Review.Decision().Reason != reason || result.ProfileRevision.UpdatedBy() != in.ReviewerID {
				t.Fatal("bad decision")
			}
			projection := readProfileProjection(t, db, in.ApplicationID)
			if projection.WorkingProfileRevisionID != nil || (outcome == "APPROVE") != (projection.CurrentPublishedProfileRevisionID != nil) {
				t.Fatal("pointers")
			}
			after := readProfileReview(t, db, in.ProfileReviewID)
			for _, key := range []string{"snapshot", "sourceRevision", "submittedBy", "submittedAt", "attempt"} {
				if !reflect.DeepEqual(before.Lookup(key), after.Lookup(key)) {
					t.Fatal("immutable", key)
				}
			}
			if _, e = profileReviewDecisionCandidate(after); e != nil {
				t.Fatal("restore", e)
			}
			if _, e = repo.DecideReview(t.Context(), in); !errors.Is(e, pd.ErrApplicationProfileReviewAlreadyDecided) {
				t.Fatal("retry", e)
			}
			if !reflect.DeepEqual(after, readProfileReview(t, db, in.ProfileReviewID)) {
				t.Fatal("overwritten")
			}
			if _, e = repo.CreateDraft(t.Context(), "profile-admin", profileDraftFixture(t, in.ApplicationID, "profile-admin")); e != nil {
				t.Fatal("new work slot", e)
			}
		})
	}
	for _, mode := range []string{"stale revision", "stale publication", "creator conflict", "submitter conflict", "admin conflict", "wrong permission", "missing policy", "retired policy", "missing checks", "unknown check", "duplicate checks", "snapshot drift", "source drift", "wrong pointer", "missing nullable", "bad decision", "revision overflow", "app fence overflow", "policy fence overflow", "late attempt", "review write failure", "revision write failure", "pointer write failure"} {
		t.Run("BR-PRF-023032 rollback "+mode, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			repo, in := decideProfileFixture(t, db)
			want := error(pd.ErrApplicationProfileReviewStateInconsistent)
			revFilter := bson.M{"profileRevisionId": in.ProfileRevisionID.String()}
			reviewFilter := bson.M{"profileReviewId": in.ProfileReviewID.String()}
			var e error
			switch mode {
			case "stale revision":
				in.ExpectedRevision = 1
				want = pd.ErrApplicationProfileRevisionConflict
			case "stale publication":
				id := pd.ApplicationProfileRevisionID(nextIntegrationTesterJoinLinkID(t))
				in.ExpectedPublishedID = &id
				want = pd.ErrApplicationProfilePublicationConflict
			case "creator conflict", "submitter conflict", "admin conflict":
				in.ReviewerID = "profile-admin"
				want = pd.ErrApplicationProfileReviewConflictOfInterest
			case "wrong permission":
				in.Permissions = []string{"app.version.review"}
				want = pd.ErrApplicationProfileReviewPermissionRequired
			case "missing policy":
				in.PolicyVersion = "missing"
				want = pd.ErrProfileReviewPolicyUnavailable
			case "retired policy":
				_, e = db.Collection(profileReviewPoliciesCollectionName).UpdateOne(t.Context(), bson.M{"version": in.PolicyVersion}, bson.M{"$set": bson.M{"status": "RETIRED"}})
				want = pd.ErrProfileReviewPolicyUnavailable
			case "missing checks":
				in.ConfirmedCheckIDs = in.ConfirmedCheckIDs[:1]
				want = pd.ErrProfileReviewChecksIncomplete
			case "unknown check":
				in.ConfirmedCheckIDs[0] = "unknown"
				want = pd.ErrProfileReviewChecksIncomplete
			case "duplicate checks":
				in.ConfirmedCheckIDs = append(in.ConfirmedCheckIDs, in.ConfirmedCheckIDs[0])
				want = pd.ErrProfileReviewChecksIncomplete
			case "snapshot drift":
				_, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), reviewFilter, bson.M{"$set": bson.M{"snapshot.displayName": "drift"}})
			case "source drift":
				_, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), reviewFilter, bson.M{"$set": bson.M{"sourceRevision": int64(5)}})
			case "wrong pointer":
				_, e = db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": in.ApplicationID.String()}, bson.M{"$set": bson.M{"workingProfileRevisionId": nextIntegrationTesterJoinLinkID(t).String()}})
			case "missing nullable":
				_, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), reviewFilter, bson.M{"$unset": bson.M{"snapshot.icon": ""}}, options.UpdateOne().SetBypassDocumentValidation(true))
			case "bad decision":
				_, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), reviewFilter, bson.M{"$set": bson.M{"decision": bson.M{}}}, options.UpdateOne().SetBypassDocumentValidation(true))
			case "revision overflow":
				in.ExpectedRevision = math.MaxInt64
				_, e = db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), revFilter, bson.M{"$set": bson.M{"revision": in.ExpectedRevision}})
				if e == nil {
					_, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), reviewFilter, bson.M{"$set": bson.M{"sourceRevision": int64(math.MaxInt64 - 1)}})
				}
			case "app fence overflow":
				_, e = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": in.ApplicationID.String()}, bson.M{"$set": bson.M{"coordinationRevision": int64(math.MaxInt64)}})
			case "policy fence overflow":
				_, e = db.Collection(profileReviewPoliciesCollectionName).UpdateOne(t.Context(), bson.M{"version": in.PolicyVersion}, bson.M{"$set": bson.M{"coordinationRevision": int64(math.MaxInt64)}})
			case "late attempt":
				var d bson.M
				if e = bson.Unmarshal(readProfileReview(t, db, in.ProfileReviewID), &d); e == nil {
					delete(d, "_id")
					d["profileReviewId"] = profileReviewID(t).String()
					d["attempt"] = int32(2)
					d["sourceRevision"] = int64(3)
					d["status"] = "REJECTED"
					_, e = db.Collection(applicationProfileReviewsCollectionName).InsertOne(t.Context(), d, options.InsertOne().SetBypassDocumentValidation(true))
				}
			case "review write failure", "revision write failure", "pointer write failure":
				collection := applicationProfileReviewsCollectionName
				if mode == "revision write failure" {
					collection = applicationProfileRevisionsCollectionName
				}
				if mode == "pointer write failure" {
					collection = applicationProfilesCollectionName
				}
				e = db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: collection}, {Key: "validator", Value: bson.M{"impossible": bson.M{"$exists": true}}}}).Err()
				want = nil
			}
			if e != nil {
				t.Fatal(e)
			}
			appBefore := readTesterLinkApplication(t, db, in.ApplicationID)
			revBefore := storedProfileRevision(t, db, in.ProfileRevisionID)
			reviewBefore := readProfileReview(t, db, in.ProfileReviewID)
			projectionBefore := readProfileProjection(t, db, in.ApplicationID)
			_, e = repo.DecideReview(t.Context(), in)
			if e == nil || want != nil && !errors.Is(e, want) || strings.Contains(e.Error(), "Cafe") {
				t.Fatalf("%v want %v", e, want)
			}
			if !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, in.ApplicationID)) || !reflect.DeepEqual(revBefore, storedProfileRevision(t, db, in.ProfileRevisionID)) || !reflect.DeepEqual(reviewBefore, readProfileReview(t, db, in.ProfileReviewID)) || !reflect.DeepEqual(projectionBefore, readProfileProjection(t, db, in.ApplicationID)) {
				t.Fatal("partial mutation")
			}
		})
	}
	t.Run("BR-PRF-024 concurrent decisions one winner", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		repo, in := decideProfileFixture(t, db)
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; _, e := repo.DecideReview(context.Background(), in); errs <- e }()
		}
		close(start)
		wg.Wait()
		close(errs)
		success := 0
		for e := range errs {
			if e == nil {
				success++
			} else if !errors.Is(e, pd.ErrApplicationProfileReviewAlreadyDecided) {
				t.Fatal(e)
			}
		}
		if success != 1 {
			t.Fatal("winner count", success)
		}
	})
	t.Run("BR-PRF-029 reject preserves invalid content and old publication", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		repo, in := decideProfileFixture(t, db)
		bad := " invalid\ncontent "
		for _, m := range []struct {
			collection, key string
			filter          bson.M
		}{{applicationProfileRevisionsCollectionName, "displayName", bson.M{"profileRevisionId": in.ProfileRevisionID.String()}}, {applicationProfileReviewsCollectionName, "snapshot.displayName", bson.M{"profileReviewId": in.ProfileReviewID.String()}}} {
			if _, e := db.Collection(m.collection).UpdateOne(t.Context(), m.filter, bson.M{"$set": bson.M{m.key: bad}}, options.UpdateOne().SetBypassDocumentValidation(true)); e != nil {
				t.Fatal(e)
			}
		}
		if _, e := repo.DecideReview(t.Context(), in); !errors.Is(e, pd.ErrInvalidApplicationProfileContent) {
			t.Fatal("approve", e)
		}
		in.Outcome = "REJECT"
		in.ConfirmedCheckIDs = nil
		reason := "not acceptable"
		in.Reason = &reason
		got, e := repo.DecideReview(t.Context(), in)
		if e != nil || got.ProfileRevision.DisplayName().String() != bad || got.Review.Snapshot().DisplayName().String() != bad {
			t.Fatal("reject", e)
		}
		if _, e = profileRevisionFromRaw(storedProfileRevision(t, db, in.ProfileRevisionID)); e != nil {
			t.Fatal("rejected restore", e)
		}
		if _, e = profileReviewDecisionCandidate(readProfileReview(t, db, in.ProfileReviewID)); e != nil {
			t.Fatal("rejected review restore", e)
		}
	})
}

func TestDecideProfileReviewFenceIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, kind := range []string{"administrator", "policy"} {
		for _, winner := range []string{"mutation", "decision"} {
			t.Run("BR-PRF-023028032 "+kind+" "+winner, func(t *testing.T) {
				db := migratedIntegrationDatabase(t, client)
				_, in := decideProfileFixture(t, db)
				ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
				defer cancel()
				mutate := func(tx context.Context) error {
					collection := applicationsCollectionName
					filter := bson.M{"id": in.ApplicationID.String()}
					set := bson.M{"adminId": in.ReviewerID.String()}
					if kind == "policy" {
						collection = profileReviewPoliciesCollectionName
						filter = bson.M{"version": in.PolicyVersion}
						set = bson.M{"status": "RETIRED"}
					}
					_, e := db.Collection(collection).UpdateOne(tx, filter, bson.M{"$set": set})
					return e
				}
				if winner == "mutation" {
					session := startTesterMembershipTransaction(t, client)
					if e := mutate(dm.NewSessionContext(ctx, session)); e != nil {
						t.Fatal(e)
					}
					competitor, started := monitoredMongoClient(t, db.Name(), "update")
					done := make(chan error, 1)
					go func() {
						_, e := NewApplicationProfileRevisionRepository(competitor.Database(db.Name())).DecideReview(ctx, in)
						done <- e
					}()
					awaitMongoCommand(t, ctx, started, "decision attempts fence")
					if e := session.CommitTransaction(ctx); e != nil {
						t.Fatal(e)
					}
					want := pd.ErrApplicationProfileReviewConflictOfInterest
					if kind == "policy" {
						want = pd.ErrProfileReviewPolicyUnavailable
					}
					select {
					case e := <-done:
						if !errors.Is(e, want) {
							t.Fatal(e, want)
						}
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					if readProfileReview(t, db, in.ProfileReviewID).Lookup("status").StringValue() != "PENDING" {
						t.Fatal("decision after exclusion")
					}
				} else {
					reached, release := make(chan struct{}), make(chan struct{})
					var gateOnce, releaseOnce sync.Once
					resume := func() { releaseOnce.Do(func() { close(release) }) }
					defer resume()
					winnerClient, e := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(commandContext context.Context, e *event.CommandStartedEvent) {
						if e.CommandName == "commitTransaction" {
							gateOnce.Do(func() {
								close(reached)
								select {
								case <-release:
								case <-commandContext.Done():
								}
							})
						}
					}}))
					if e != nil {
						t.Fatal(e)
					}
					defer winnerClient.Disconnect(context.Background())
					done := make(chan error, 1)
					go func() {
						_, e := NewApplicationProfileRevisionRepository(winnerClient.Database(db.Name())).DecideReview(ctx, in)
						done <- e
					}()
					awaitMongoCommand(t, ctx, reached, "decision holds fences before commit")
					// An independent real update must wait for the decision's policy/Application write.
					competitor, started := monitoredMongoClient(t, db.Name(), "update")
					mutationDone := make(chan error, 1)
					go func() {
						collection := applicationsCollectionName
						filter := bson.M{"id": in.ApplicationID.String()}
						set := bson.M{"adminId": in.ReviewerID.String()}
						if kind == "policy" {
							collection = profileReviewPoliciesCollectionName
							filter = bson.M{"version": in.PolicyVersion}
							set = bson.M{"status": "RETIRED"}
						}
						_, e := competitor.Database(db.Name()).Collection(collection).UpdateOne(ctx, filter, bson.M{"$set": set})
						mutationDone <- e
					}()
					awaitMongoCommand(t, ctx, started, "mutation attempts fence")
					resume()
					for _, ch := range []chan error{done, mutationDone} {
						select {
						case e := <-ch:
							if e != nil {
								t.Fatal(e)
							}
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						}
					}
					if readProfileReview(t, db, in.ProfileReviewID).Lookup("status").StringValue() != "APPROVED" {
						t.Fatal("decision lost")
					}
				}
			})
		}
	}
}
