package mongo

import (
	"context"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	rd "iwut-app-center/internal/review/domain"
	rp "iwut-app-center/internal/review/port"
)

// These are explicitly synthetic repository policies, never production releases.
func fixtureNextPolicy(from versionReviewPolicyDocument, version, check string) versionReviewPolicyDocument {
	next := from
	next.Version, next.Status, next.CoordinationRevision = version, "ACTIVE", 1
	next.RequiredChecks = append(append([]string(nil), from.RequiredChecks...), check)
	next.Definitions = append(append([]versionReviewCheckDocument(nil), from.Definitions...), versionReviewCheckDocument{ID: check, Title: "Fixture additional check", Description: "Synthetic policy definition for repository regression only."})
	return next
}

func fixtureRelease(kind string, from, to versionReviewPolicyDocument) reviewPolicyRelease {
	return reviewPolicyRelease{MigrationID: "fixture-" + kind + "-" + to.Version, Kind: kind, From: from, To: to, ApprovalReference: "fixture-approval", ReleaseReference: "fixture-release"}
}

func TestReviewPolicyBindingIntegration_VersionAAfterBAndResubmission(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	a := formalVersionReviewPolicy()
	b := fixtureNextPolicy(a, "fixture-version-policy-b", "fixture-new-check")
	approve := createDecidableReview(t, db, "binding-admin-approve", "binding_approve", "v1")
	reject := createDecidableReview(t, db, "binding-admin-reject", "binding_reject", "v1")
	decisionRepo := NewApplicationReviewDecisionRepository(db)
	approveCandidate := loadDecisionCandidate(t, decisionRepo, approve, "binding-reviewer")
	rejectCandidate := loadDecisionCandidate(t, decisionRepo, reject, "binding-reviewer")
	if _, err := NewMigrator(db).applyReviewPolicyRelease(t.Context(), fixtureRelease("VERSION", a, b), time.Now()); err != nil {
		t.Fatal(err)
	}
	var before versionReviewPolicyDocument
	if err := db.Collection(versionReviewPoliciesCollectionName).FindOne(t.Context(), bson.M{"version": a.Version}).Decode(&before); err != nil {
		t.Fatal(err)
	}
	approved, err := decisionRepo.Decide(t.Context(), approveCandidate, "binding-reviewer", integrationApprovedDecision(t, a.Version, a.RequiredChecks, 41, "fixture-preflight", "binding-reviewer", time.Now()))
	if err != nil || approved.Review().Decision().ReviewPolicyVersion().String() != a.Version {
		t.Fatal("A approval after B", err)
	}
	denied, err := rd.NewRejectedDecision(rd.ReviewPolicyVersion(a.Version), "Fixture rejection", "binding-reviewer", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decisionRepo.Decide(t.Context(), rejectCandidate, "binding-reviewer", denied); err != nil {
		t.Fatal("A rejection after B", err)
	}
	var after versionReviewPolicyDocument
	if err := db.Collection(versionReviewPoliciesCollectionName).FindOne(t.Context(), bson.M{"version": a.Version}).Decode(&after); err != nil || after.CoordinationRevision != before.CoordinationRevision || after.Status != "RETIRED" {
		t.Fatal("decisions wrote retired policy", err)
	}
	detail, err := NewApplicationReviewQueryRepository(db).Get(t.Context(), "binding-reviewer", approve.applicationID, approve.versionID, approve.reviewID)
	if err != nil || detail.ReviewPolicy.Version != a.Version || len(detail.ReviewPolicy.RequiredCheckDefinitions) != len(a.Definitions) {
		t.Fatal("historical detail changed policy", err)
	}
	newReview := createDecidableReview(t, db, "binding-admin-new", "binding_new", "v1")
	newCandidate := loadDecisionCandidate(t, decisionRepo, newReview, "binding-reviewer")
	if newCandidate.Review().PolicyVersion().String() != b.Version {
		t.Fatal("new submission did not bind B")
	}
	if _, err := decisionRepo.Decide(t.Context(), newCandidate, "binding-reviewer", integrationApprovedDecision(t, a.Version, a.RequiredChecks, 41, "fixture-preflight", "binding-reviewer", time.Now())); !errors.Is(err, rp.ErrReviewPolicyMismatch) {
		t.Fatal("wrong binding decided B", err)
	}
	if _, err := NewApplicationReviewRestorationRepository(db).RestoreDraft(t.Context(), reject.applicationID, reject.versionID, reject.reviewID, reject.adminID, 3, time.Now()); err != nil {
		t.Fatal(err)
	}
	submitter := NewApplicationReviewRepository(db)
	candidate, err := submitter.LoadSubmissionCandidate(t.Context(), reject.applicationID, reject.versionID, reject.adminID, 4)
	if err != nil {
		t.Fatal(err)
	}
	resubmitted, err := submitter.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), reject.adminID, 41, "fixture-preflight", time.Now())
	if err != nil || resubmitted.Review().PolicyVersion().String() != b.Version || resubmitted.Review().Attempt() != 2 {
		t.Fatal("resubmission did not create B attempt", err)
	}
	if got := readReviewDocument(t, db, reject.reviewID); got.PolicyBinding.Version != a.Version || got.Decision.ReviewPolicyVersion != a.Version {
		t.Fatal("resubmission rewrote old attempt")
	}
}

func bindingProfileFixture(t *testing.T, db *dm.Database, name string) (*ApplicationProfileRevisionRepository, pp.ProfileReviewDecisionInput) {
	t.Helper()
	app := createVersionTestApplication(t, db, "binding-profile-admin", name)
	repo := NewApplicationProfileRevisionRepository(db)
	draft, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
	if err != nil {
		t.Fatal(err)
	}
	submission, err := repo.SubmitDraft(t.Context(), app.ID(), draft.ProfileRevisionID(), app.AdminID(), 1, profileReviewID(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return repo, pp.ProfileReviewDecisionInput{ApplicationID: app.ID(), ProfileRevisionID: draft.ProfileRevisionID(), ProfileReviewID: submission.Review.ProfileReviewID(), ReviewerID: "binding-profile-reviewer", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: 2, PolicyVersion: submission.Review.PolicyVersion(), Outcome: "APPROVE", ConfirmedCheckIDs: pd.InitialProfileReviewChecks(), DecidedAt: time.Now()}
}

func TestReviewPolicyBindingIntegration_ProfileAAfterBAndResubmission(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	a := formalProfileReviewPolicy()
	b := fixtureNextPolicy(a, "fixture-profile-policy-b", "fixture-new-check")
	repo, approve := bindingProfileFixture(t, db, "binding_profile_approve")
	_, reject := bindingProfileFixture(t, db, "binding_profile_reject")
	if _, err := NewMigrator(db).applyReviewPolicyRelease(t.Context(), fixtureRelease("PROFILE", a, b), time.Now()); err != nil {
		t.Fatal(err)
	}
	approved, err := repo.DecideReview(t.Context(), approve)
	if err != nil || approved.Review.Decision().PolicyVersion != a.Version {
		t.Fatal("A approval after B", err)
	}
	reason := "Fixture rejection"
	reject.Outcome, reject.ConfirmedCheckIDs, reject.Reason = "REJECT", nil, &reason
	if _, err := repo.DecideReview(t.Context(), reject); err != nil {
		t.Fatal("A rejection after B", err)
	}
	detail, err := NewApplicationProfileReviewQueryRepository(db).Get(t.Context(), approve.ReviewerID, approve.ApplicationID, approve.ProfileRevisionID, approve.ProfileReviewID)
	if err != nil || detail.ReviewPolicy.Version != a.Version || len(detail.ReviewPolicy.RequiredCheckDefinitions) != len(a.Definitions) {
		t.Fatal("historical detail changed policy", err)
	}
	draft, err := repo.CreateDraft(t.Context(), "binding-profile-admin", profileDraftFixture(t, reject.ApplicationID, "binding-profile-admin"))
	if err != nil {
		t.Fatal(err)
	}
	submitted, err := repo.SubmitDraft(t.Context(), reject.ApplicationID, draft.ProfileRevisionID(), "binding-profile-admin", 1, profileReviewID(t), time.Now())
	if err != nil || submitted.Review.PolicyVersion() != b.Version {
		t.Fatal("new profile revision did not bind B", err)
	}
	wrong := reject
	wrong.ProfileRevisionID, wrong.ProfileReviewID = draft.ProfileRevisionID(), submitted.Review.ProfileReviewID()
	if _, err := repo.DecideReview(t.Context(), wrong); !errors.Is(err, pd.ErrProfileReviewPolicyMismatch) {
		t.Fatal("wrong policy decided B", err)
	}
	original, err := profileReviewDecisionCandidate(readProfileReview(t, db, reject.ProfileReviewID))
	if err != nil || original.PolicyVersion() != a.Version || original.Decision().PolicyVersion != a.Version {
		t.Fatal("new revision rewrote old attempt", err)
	}
}

func TestReviewPolicyReleaseIntegration_ReceiptReplayAndAtomicFailure(t *testing.T) {
	for _, kind := range []string{"VERSION", "PROFILE"} {
		t.Run(kind, func(t *testing.T) {
			db := reusableIntegrationDatabase(t, integrationClient(t))
			a, collection := formalVersionReviewPolicy(), versionReviewPoliciesCollectionName
			if kind == "PROFILE" {
				a, collection = formalProfileReviewPolicy(), profileReviewPoliciesCollectionName
			}
			b := fixtureNextPolicy(a, "fixture-policy-b", "fixture-b-check")
			c := fixtureNextPolicy(b, "fixture-policy-c", "fixture-c-check")
			m, ab := NewMigrator(db), fixtureRelease(kind, a, b)
			first, err := m.applyReviewPolicyRelease(t.Context(), ab, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.applyReviewPolicyRelease(t.Context(), fixtureRelease(kind, b, c), time.Now()); err != nil {
				t.Fatal(err)
			}
			replayed, err := m.applyReviewPolicyRelease(t.Context(), ab, time.Now().Add(time.Hour))
			if err != nil || *first != *replayed {
				t.Fatal("historical receipt replay changed result", err)
			}
			ab.ReleaseReference = "different-release"
			if _, err := m.applyReviewPolicyRelease(t.Context(), ab, time.Now()); err == nil {
				t.Fatal("same migration ID accepted a different manifest")
			}
			var active versionReviewPolicyDocument
			if err := db.Collection(collection).FindOne(t.Context(), bson.M{"status": "ACTIVE"}).Decode(&active); err != nil || active.Version != c.Version {
				t.Fatal("replay reactivated a historical version", err)
			}
			// A durable receipt insertion failure must roll back the policy switch.
			if err := db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: migrationLedgerCollectionName}, {Key: "validator", Value: bson.M{"$expr": false}}}).Err(); err != nil {
				t.Fatal(err)
			}
			d := fixtureNextPolicy(c, "fixture-policy-d", "fixture-d-check")
			if _, err := m.applyReviewPolicyRelease(t.Context(), fixtureRelease(kind, c, d), time.Now()); err == nil {
				t.Fatal("receipt failure committed a release")
			}
			if err := db.Collection(collection).FindOne(t.Context(), bson.M{"version": d.Version}).Err(); !errors.Is(err, dm.ErrNoDocuments) {
				t.Fatal("rolled-back target remains", err)
			}
			if err := db.Collection(collection).FindOne(t.Context(), bson.M{"version": c.Version, "status": "ACTIVE"}).Err(); err != nil {
				t.Fatal("failed release retired current policy", err)
			}
		})
	}
}

func bindingMonitoredClient(t *testing.T, database string, holdCommit bool, policyUpdates chan<- struct{}) (*dm.Client, <-chan struct{}, func()) {
	t.Helper()
	reached, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(resume)
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if holdCommit && e.CommandName == "commitTransaction" {
			once.Do(func() {
				close(reached)
				select {
				case <-release:
				case <-ctx.Done():
				}
			})
		}
		if policyUpdates != nil && e.DatabaseName == database && e.CommandName == "update" {
			collection, ok := e.Command.Lookup("update").StringValueOK()
			if ok && (collection == versionReviewPoliciesCollectionName || collection == profileReviewPoliciesCollectionName) {
				select {
				case policyUpdates <- struct{}{}:
				default:
				}
			}
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resume(); _ = client.Disconnect(context.Background()) })
	return client, reached, resume
}

func TestReviewPolicyBindingIntegration_SubmissionReleaseCommitOrder(t *testing.T) {
	for _, kind := range []string{"VERSION", "PROFILE"} {
		for _, first := range []string{"submission", "release"} {
			t.Run(kind+"/"+first, func(t *testing.T) {
				db := reusableIntegrationDatabase(t, integrationClient(t))
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				a := formalVersionReviewPolicy()
				var submit func(*dm.Database) (string, error)
				if kind == "VERSION" {
					app, version := createReviewTestVersion(t, db, "binding-order-admin", "binding_order", "v1")
					candidate := loadReviewCandidate(t, NewApplicationReviewRepository(db), app.ID(), version.ID(), app.AdminID().String(), 1)
					id := nextIntegrationApplicationReviewID(t)
					submit = func(d *dm.Database) (string, error) {
						r, err := NewApplicationReviewRepository(d).Submit(ctx, candidate, id, app.AdminID(), 41, "fixture-preflight", time.Now())
						if err != nil {
							return "", err
						}
						return r.Review().PolicyVersion().String(), nil
					}
				} else {
					a = formalProfileReviewPolicy()
					_, draft := submitProfileFixture(t, db)
					id := profileReviewID(t)
					submit = func(d *dm.Database) (string, error) {
						r, err := NewApplicationProfileRevisionRepository(d).SubmitDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, id, time.Now())
						if err != nil {
							return "", err
						}
						return r.Review.PolicyVersion(), nil
					}
				}
				b := fixtureNextPolicy(a, "fixture-order-b", "fixture-order-check")
				winner, held, resume := bindingMonitoredClient(t, db.Name(), true, nil)
				started := make(chan struct{}, 1)
				competitor, _, _ := bindingMonitoredClient(t, db.Name(), false, started)
				type submissionResult struct {
					version string
					err     error
				}
				submitted, released := make(chan submissionResult, 1), make(chan error, 1)
				submitOn := func(c *dm.Client) { v, err := submit(c.Database(db.Name())); submitted <- submissionResult{v, err} }
				releaseOn := func(c *dm.Client) {
					_, err := NewMigrator(c.Database(db.Name())).applyReviewPolicyRelease(ctx, fixtureRelease(kind, a, b), time.Now())
					released <- err
				}
				if first == "submission" {
					go submitOn(winner)
				} else {
					go releaseOn(winner)
				}
				awaitMongoCommand(t, ctx, held, "first transaction before commit")
				if first == "submission" {
					go releaseOn(competitor)
				} else {
					go submitOn(competitor)
				}
				awaitMongoCommand(t, ctx, started, "competing policy write")
				resume()
				var result submissionResult
				select {
				case result = <-submitted:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if result.err != nil {
					t.Fatal(result.err)
				}
				select {
				case err := <-released:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				want := a.Version
				if first == "release" {
					want = b.Version
				}
				if result.version != want {
					t.Fatal("successful submission bound policy from wrong commit order")
				}
			})
		}
	}
}

func TestReviewPolicyBindingIntegration_SubmissionFailureHasNoPartialBinding(t *testing.T) {
	for _, kind := range []string{"VERSION", "PROFILE"} {
		for _, fault := range []string{"missing", "overflow", "incomplete"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				db := reusableIntegrationDatabase(t, integrationClient(t))
				coll, policy := versionReviewPoliciesCollectionName, formalVersionReviewPolicy()
				var submit func() error
				var unchanged func()
				if kind == "VERSION" {
					app, v := createReviewTestVersion(t, db, "binding-failure-admin", "binding_failure", "v1")
					r := NewApplicationReviewRepository(db)
					c := loadReviewCandidate(t, r, app.ID(), v.ID(), app.AdminID().String(), 1)
					submit = func() error {
						_, err := r.Submit(t.Context(), c, nextIntegrationApplicationReviewID(t), app.AdminID(), 41, "fixture-preflight", time.Now())
						return err
					}
					unchanged = func() {
						got := readVersionDocument(t, db, v.ID().String())
						if got.Revision != 1 || got.ReviewStatus != "DRAFT" {
							t.Fatal("partial Version transition")
						}
						assertCollectionCount(t, db, applicationReviewsCollectionName, 0)
					}
				} else {
					coll, policy = profileReviewPoliciesCollectionName, formalProfileReviewPolicy()
					r, d := submitProfileFixture(t, db)
					submit = func() error {
						_, err := r.SubmitDraft(t.Context(), d.ApplicationID(), d.ProfileRevisionID(), d.CreatedBy(), 1, profileReviewID(t), time.Now())
						return err
					}
					unchanged = func() {
						got := storedProfileRevision(t, db, d.ProfileRevisionID())
						if got.Lookup("revision").Int64() != 1 || got.Lookup("reviewStatus").StringValue() != "DRAFT" {
							t.Fatal("partial Profile transition")
						}
						assertCollectionCount(t, db, applicationProfileReviewsCollectionName, 0)
					}
				}
				c := db.Collection(coll)
				switch fault {
				case "missing":
					if _, err := c.DeleteOne(t.Context(), bson.M{"version": policy.Version}); err != nil {
						t.Fatal(err)
					}
				case "overflow":
					if _, err := c.UpdateOne(t.Context(), bson.M{"version": policy.Version}, bson.M{"$set": bson.M{"coordinationRevision": int64(math.MaxInt64)}}); err != nil {
						t.Fatal(err)
					}
				case "incomplete":
					if _, err := c.UpdateOne(t.Context(), bson.M{"version": policy.Version}, bson.M{"$unset": bson.M{"definitions": ""}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
						t.Fatal(err)
					}
				}
				err := submit()
				want := rp.ErrApplicationReviewDataInconsistent
				if kind == "PROFILE" {
					want = pp.ErrApplicationProfileStateInconsistent
				}
				if !errors.Is(err, want) {
					t.Fatal("invalid policy submission", err, want)
				}
				unchanged()
			})
		}
	}
}

func TestReviewPolicyReleaseIntegration_UnknownCommitRecovery(t *testing.T) {
	for _, kind := range []string{"VERSION", "PROFILE"} {
		t.Run(kind, func(t *testing.T) {
			db := reusableIntegrationDatabase(t, integrationClient(t))
			a := formalVersionReviewPolicy()
			coll := versionReviewPoliciesCollectionName
			if kind == "PROFILE" {
				a = formalProfileReviewPolicy()
				coll = profileReviewPoliciesCollectionName
			}
			appName := "fixture-policy-unknown-" + kind
			var commits atomic.Int32
			client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetAppName(appName).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
				if e.CommandName == "commitTransaction" {
					commits.Add(1)
				}
			}}))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Disconnect(context.Background())
			admin := db.Client().Database("admin")
			if err := admin.RunCommand(t.Context(), bson.D{{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: bson.M{"times": 1}}, {Key: "data", Value: bson.M{"failCommands": bson.A{"commitTransaction"}, "appName": appName, "writeConcernError": bson.M{"code": 64, "errmsg": "fixture unknown commit result"}, "errorLabels": bson.A{"UnknownTransactionCommitResult"}}}}).Err(); err != nil {
				t.Fatal(err)
			}
			defer admin.RunCommand(context.Background(), bson.D{{Key: "configureFailPoint", Value: "failCommand"}, {Key: "mode", Value: "off"}})
			b := fixtureNextPolicy(a, "fixture-unknown-policy-b", "fixture-unknown-check")
			release := fixtureRelease(kind, a, b)
			result, err := NewMigrator(client.Database(db.Name())).applyReviewPolicyRelease(t.Context(), release, time.Now())
			if err != nil || result == nil || commits.Load() < 2 {
				t.Fatal("original commit did not recover", err, commits.Load())
			}
			recovered, err := NewMigrator(db).applyReviewPolicyRelease(t.Context(), release, time.Now().Add(time.Hour))
			if err != nil || *result != *recovered {
				t.Fatal("receipt recovery changed result", err)
			}
			count, err := db.Collection(migrationLedgerCollectionName).CountDocuments(t.Context(), bson.M{"_id": release.MigrationID})
			if err != nil || count != 1 {
				t.Fatal("duplicate release receipt", err)
			}
			if err := db.Collection(coll).FindOne(t.Context(), bson.M{"version": b.Version, "status": "ACTIVE"}).Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReviewPolicyBindingIntegration_InvalidBoundDataFailsClosed(t *testing.T) {
	for _, kind := range []string{"VERSION", "PROFILE"} {
		for _, fault := range []string{"missing binding", "missing definition", "missing policy"} {
			t.Run(kind+"/"+fault, func(t *testing.T) {
				db := reusableIntegrationDatabase(t, integrationClient(t))
				var query, decide func() error
				var pending func()
				policyColl, policyVersion := versionReviewPoliciesCollectionName, formalVersionReviewPolicy().Version
				var reviewColl string
				var reviewFilter bson.M
				if kind == "VERSION" {
					seed := createDecidableReview(t, db, "binding-corrupt-admin", "binding_corrupt", "v1")
					r := NewApplicationReviewDecisionRepository(db)
					reviewColl, reviewFilter = applicationReviewsCollectionName, bson.M{"reviewId": seed.reviewID.String()}
					query = func() error {
						_, err := NewApplicationReviewQueryRepository(db).Get(t.Context(), "binding-reviewer", seed.applicationID, seed.versionID, seed.reviewID)
						return err
					}
					decide = func() error {
						c, err := r.LoadDecisionCandidate(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, "binding-reviewer")
						if err != nil {
							return err
						}
						_, err = r.Decide(t.Context(), c, "binding-reviewer", integrationApprovedDecision(t, policyVersion, formalVersionReviewPolicy().RequiredChecks, 41, "fixture-preflight", "binding-reviewer", time.Now()))
						return err
					}
					pending = func() {
						if d := readReviewDocument(t, db, seed.reviewID); d.Status != "PENDING" || d.Decision != nil {
							t.Fatal("invalid data wrote Version decision")
						}
					}
				} else {
					policyColl, policyVersion = profileReviewPoliciesCollectionName, formalProfileReviewPolicy().Version
					r, in := bindingProfileFixture(t, db, "binding_profile_corrupt")
					reviewColl, reviewFilter = applicationProfileReviewsCollectionName, bson.M{"profileReviewId": in.ProfileReviewID.String()}
					query = func() error {
						_, err := NewApplicationProfileReviewQueryRepository(db).Get(t.Context(), in.ReviewerID, in.ApplicationID, in.ProfileRevisionID, in.ProfileReviewID)
						return err
					}
					decide = func() error { _, err := r.DecideReview(t.Context(), in); return err }
					pending = func() {
						if d := readProfileReview(t, db, in.ProfileReviewID); d.Lookup("status").StringValue() != "PENDING" || d.Lookup("decision").Type != bson.TypeNull {
							t.Fatal("invalid data wrote Profile decision")
						}
					}
				}
				switch fault {
				case "missing binding":
					if _, err := db.Collection(reviewColl).UpdateOne(t.Context(), reviewFilter, bson.M{"$unset": bson.M{"policyBinding": ""}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
						t.Fatal(err)
					}
				case "missing definition":
					if _, err := db.Collection(policyColl).UpdateOne(t.Context(), bson.M{"version": policyVersion}, bson.M{"$unset": bson.M{"definitions": ""}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
						t.Fatal(err)
					}
				case "missing policy":
					if _, err := db.Collection(policyColl).DeleteOne(t.Context(), bson.M{"version": policyVersion}); err != nil {
						t.Fatal(err)
					}
				}
				qe, de := query(), decide()
				wantQuery, wantDecision := rp.ErrReviewQueryStateInconsistent, rp.ErrApplicationReviewDataInconsistent
				if kind == "PROFILE" {
					wantQuery, wantDecision = pp.ErrProfileReviewQueryStateInconsistent, pd.ErrApplicationProfileReviewStateInconsistent
				}
				if !errors.Is(qe, wantQuery) || !errors.Is(de, wantDecision) {
					t.Fatal("invalid bound data did not fail closed", qe, de)
				}
				pending()
			})
		}
	}
}

func TestReviewPolicyReleaseIntegration_CompetingReleaseKeepsOriginalBaseline(t *testing.T) {
	for _, kind := range []string{"VERSION", "PROFILE"} {
		t.Run(kind, func(t *testing.T) {
			db := reusableIntegrationDatabase(t, integrationClient(t))
			a := formalVersionReviewPolicy()
			coll := versionReviewPoliciesCollectionName
			if kind == "PROFILE" {
				a = formalProfileReviewPolicy()
				coll = profileReviewPoliciesCollectionName
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			b := fixtureNextPolicy(a, "fixture-race-b", "fixture-race-b-check")
			c := fixtureNextPolicy(a, "fixture-race-c", "fixture-race-c-check")
			winner, reached, resume := bindingMonitoredClient(t, db.Name(), true, nil)
			done := make(chan error, 1)
			go func() {
				_, err := NewMigrator(winner.Database(db.Name())).applyReviewPolicyRelease(ctx, fixtureRelease(kind, a, b), time.Now())
				done <- err
			}()
			awaitMongoCommand(t, ctx, reached, "first release holds original ACTIVE write")
			updates := make(chan struct{}, 1)
			loser, _, _ := bindingMonitoredClient(t, db.Name(), false, updates)
			lost := make(chan error, 1)
			go func() {
				_, err := NewMigrator(loser.Database(db.Name())).applyReviewPolicyRelease(ctx, fixtureRelease(kind, a, c), time.Now())
				lost <- err
			}()
			awaitMongoCommand(t, ctx, updates, "competing release attempts original ACTIVE write")
			resume()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			select {
			case err := <-lost:
				if err == nil {
					t.Fatal("competing release silently changed its fromVersion")
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := db.Collection(coll).FindOne(t.Context(), bson.M{"version": b.Version, "status": "ACTIVE"}).Err(); err != nil {
				t.Fatal(err)
			}
			if err := db.Collection(coll).FindOne(t.Context(), bson.M{"version": c.Version}).Err(); !errors.Is(err, dm.ErrNoDocuments) {
				t.Fatal("failed competing target remains", err)
			}
			count, err := db.Collection(migrationLedgerCollectionName).CountDocuments(t.Context(), bson.M{"_id": fixtureRelease(kind, a, c).MigrationID})
			if err != nil || count != 0 {
				t.Fatal("failed release recorded success", err)
			}
		})
	}
}
