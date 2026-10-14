package mongo

import (
	"context"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

// The adapter test uses a deterministic budget port; actual JSON/protobuf
// sizes are exercised by the transport and production-Wire E2E tests.
type managementTestSizer struct{ perItem int }

func (s managementTestSizer) ListSize(p *pd.ManagedProfilePage) (int, error) {
	return 200 + len(p.Items)*s.perItem, nil
}

func TestProfileManagementIntegration_BRPRF049_050_052_056_History(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	app, ids := managedProfileHistory(t, db, "admin", "REJECTED")
	var original applicationProfileReviewDocument
	if e := db.Collection(applicationProfileReviewsCollectionName).FindOne(t.Context(), bson.M{"profileRevisionId": ids[0].String()}).Decode(&original); e != nil {
		t.Fatal(e)
	}
	decisionRaw, _ := bson.Marshal(original.Decision)
	var decision profileReviewDecisionDocument
	if e := bson.Unmarshal(decisionRaw, &decision); e != nil {
		t.Fatal(e)
	}
	var latestAt time.Time
	// Historical attempts are independent immutable facts. The newest attempt
	// remains visible when the requested history page contains an earlier one.
	for attempt := int32(2); attempt <= 4; attempt++ {
		next := original
		next.ProfileReviewID = profileReviewID(t).String()
		next.Attempt = attempt
		next.SourceRevision = int64(attempt)*2 - 1
		next.SubmittedAt = decision.DecidedAt.Add(time.Duration(attempt) * time.Second)
		currentDecision := decision
		currentDecision.DecidedAt = next.SubmittedAt.Add(time.Millisecond)
		next.Decision = currentDecision
		latestAt = currentDecision.DecidedAt
		if _, e := db.Collection(applicationProfileReviewsCollectionName).InsertOne(t.Context(), next); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), bson.M{"profileRevisionId": ids[0].String()}, bson.M{"$set": bson.M{"revision": int64(9), "updatedAt": latestAt}}); e != nil {
		t.Fatal(e)
	}
	repo := profileManagementTestRepository(db, managementTestSizer{perItem: pd.ProfileManagementResponseBudget/2 + 100})
	token := ""
	for attempt := int32(1); attempt <= 4; attempt++ {
		d, e := repo.Get(t.Context(), "admin", app, ids[0], 4, token)
		if e != nil || len(d.Reviews) != 1 || d.Reviews[0].Attempt != attempt || d.Profile.LatestReview.Attempt != 4 {
			t.Fatalf("history=%#v %v", d, e)
		}
		token = d.NextReviewPageToken
		if attempt == 1 {
			other := pd.ApplicationProfileRevisionID(nextIntegrationTesterJoinLinkID(t))
			if _, e = repo.Get(t.Context(), "admin", app, other, 1, token); !errors.Is(e, pp.ErrProfileManagementNotFound) {
				t.Fatal("missing parent", e)
			}
		}
	}
	if token != "" {
		t.Fatal("history continuation loop")
	}
	// Collision with an item beyond the page's lookahead is still a damaged
	// necessary ID; cardinality checks must not depend on the requested size.
	if e := db.Collection(applicationProfileReviewsCollectionName).Indexes().DropOne(t.Context(), profileReviewIDUniqueIndexName); e != nil {
		t.Fatal(e)
	}
	duplicate := original
	duplicate.ProfileRevisionID = profileReviewID(t).String()
	if _, e := db.Collection(applicationProfileReviewsCollectionName).InsertOne(t.Context(), duplicate); e != nil {
		t.Fatal(e)
	}
	if d, e := repo.Get(t.Context(), "admin", app, ids[0], 1, ""); d != nil || !errors.Is(e, pp.ErrProfileManagementStateInconsistent) {
		t.Fatal("history collision", d, e)
	}
}

func TestProfileManagementIntegration_BRPRF051_055_ConcurrentSnapshot(t *testing.T) {
	client := integrationClient(t)
	for _, mutation := range []string{"create", "edit", "submit", "approve", "reject", "transfer", "archive", "suspend", "close"} {
		t.Run(mutation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			db := migratedIntegrationDatabase(t, client)
			var app shared.ApplicationID
			var ids []pd.ApplicationProfileRevisionID
			if mutation == "create" {
				app = createVersionTestApplication(t, db, "admin", "snapshot_create").ID()
			} else {
				status := "DRAFT"
				if mutation == "approve" || mutation == "reject" {
					status = "SUBMITTED"
				}
				app, ids = managedProfileHistory(t, db, "admin", status)
			}
			before, e := profileManagementTestRepository(db, managementTestSizer{}).List(ctx, "admin", app, nil, 20, "")
			if e != nil {
				t.Fatal(e)
			}
			reached, release := make(chan struct{}), make(chan struct{})
			var once, releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(release) }) }
			defer resume()
			var writes int
			reader, e := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(commandContext context.Context, ev *event.CommandStartedEvent) {
				if ev.DatabaseName != db.Name() {
					return
				}
				if ev.CommandName == "insert" || ev.CommandName == "update" || ev.CommandName == "delete" || ev.CommandName == "findAndModify" {
					writes++
				}
				if ev.CommandName == "find" && ev.Command.Lookup("find").StringValue() == applicationProfilesCollectionName {
					once.Do(func() {
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
			defer reader.Disconnect(context.Background())
			type result struct {
				page *pd.ManagedProfilePage
				err  error
			}
			done := make(chan result, 1)
			go func() {
				p, e := profileManagementTestRepository(reader.Database(db.Name()), managementTestSizer{}).List(ctx, "admin", app, nil, 20, "")
				done <- result{p, e}
			}()
			awaitMongoCommand(t, ctx, reached, "profile query snapshot after ownership read")
			writer := NewApplicationProfileRevisionRepository(db)
			switch mutation {
			case "create":
				_, e = writer.CreateDraft(ctx, "admin", profileDraftFixture(t, app, "admin"))
			case "edit":
				replacement, _ := pd.NewDraftApplicationProfileReplacement("edited", nil, nil)
				_, e = writer.ReplaceDraft(ctx, app, ids[0], "admin", 1, replacement, time.Now())
			case "submit":
				_, e = writer.SubmitDraft(ctx, app, ids[0], "admin", 1, profileReviewID(t), time.Now())
			case "approve", "reject":
				var review applicationProfileReviewDocument
				if e = db.Collection(applicationProfileReviewsCollectionName).FindOne(ctx, bson.M{"profileRevisionId": ids[0].String()}).Decode(&review); e != nil {
					t.Fatal(e)
				}
				outcome := "APPROVE"
				checks := pd.InitialProfileReviewChecks()
				reason := "reject"
				if mutation == "reject" {
					outcome = "REJECT"
					checks = nil
				}
				_, e = writer.DecideReview(ctx, pp.ProfileReviewDecisionInput{ApplicationID: app, ProfileRevisionID: ids[0], ProfileReviewID: pd.ApplicationProfileReviewID(review.ProfileReviewID), ReviewerID: "independent-reviewer", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: 2, PolicyVersion: pd.InitialProfileReviewPolicyVersion, Outcome: outcome, ConfirmedCheckIDs: checks, Reason: &reason, DecidedAt: time.Now()})
			default:
				set := bson.M{}
				inc := bson.M{}
				switch mutation {
				case "transfer":
					set["adminId"] = "new-admin"
					inc["ownershipRevision"] = int64(1)
				case "archive":
					set["archiveStatus"] = "ARCHIVED"
					inc["archiveRevision"] = int64(1)
				case "suspend":
					set["platformAvailabilityStatus"] = "SUSPENDED"
					inc["platformAvailabilityRevision"] = int64(1)
				case "close":
					set["lifecycleStatus"] = "CLOSED"
					inc["lifecycleRevision"] = int64(1)
				}
				_, e = db.Collection(applicationsCollectionName).UpdateOne(ctx, bson.M{"id": app.String()}, bson.M{"$set": set, "$inc": inc}, options.UpdateOne().SetBypassDocumentValidation(true))
			}
			if e != nil {
				t.Fatal(e)
			}
			resume()
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				if !reflect.DeepEqual(got.page.Items, before.Items) || !reflect.DeepEqual(got.page.ProfileState, before.ProfileState) {
					t.Fatalf("torn %s: before=%#v after=%#v", mutation, before, got.page)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if writes != 0 {
				t.Fatal("read wrote state/fence/audit", writes)
			}
			after, e := profileManagementTestRepository(db, managementTestSizer{}).List(ctx, "admin", app, nil, 20, "")
			if mutation == "transfer" {
				if !errors.Is(e, pp.ErrProfileManagementNotFound) {
					t.Fatal("ownership after commit", e)
				}
			} else if e != nil {
				t.Fatal(e)
			} else if mutation == "create" || mutation == "edit" || mutation == "submit" || mutation == "approve" || mutation == "reject" {
				if reflect.DeepEqual(after.Items, before.Items) {
					t.Fatal("postcommit did not see new facts")
				}
			}
		})
	}
}
func (s managementTestSizer) DetailSize(p *pd.ManagedProfileDetail) (int, error) {
	return 200 + len(p.Reviews)*s.perItem, nil
}
func profileManagementTestRepository(db *dm.Database, s managementTestSizer) *ProfileManagementRepository {
	key, _ := DeriveProfileManagementCursorKey([]byte("test-management-key"))
	return NewProfileManagementRepository(db, key, s)
}
func managedProfileHistory(t *testing.T, db *dm.Database, admin shared.AuthID, statuses ...string) (shared.ApplicationID, []pd.ApplicationProfileRevisionID) {
	t.Helper()
	app := createVersionTestApplication(t, db, admin.String(), "managed_profile")
	repo := NewApplicationProfileRevisionRepository(db)
	ids := []pd.ApplicationProfileRevisionID{}
	for _, status := range statuses {
		draft, e := repo.CreateDraft(t.Context(), admin, profileDraftFixture(t, app.ID(), admin))
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, draft.ProfileRevisionID())
		if status == "DRAFT" {
			continue
		}
		sub, e := repo.SubmitDraft(t.Context(), app.ID(), draft.ProfileRevisionID(), admin, draft.Revision(), profileReviewID(t), time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if status == "SUBMITTED" {
			continue
		}
		outcome := "APPROVE"
		checks := pd.InitialProfileReviewChecks()
		if status == "REJECTED" {
			outcome = "REJECT"
			checks = nil
		}
		reason := "historical decision"
		var published *pd.ApplicationProfileRevisionID
		if p := readProfileProjection(t, db, app.ID()).CurrentPublishedProfileRevisionID; p != nil {
			id := pd.ApplicationProfileRevisionID(*p)
			published = &id
		}
		_, e = repo.DecideReview(t.Context(), pp.ProfileReviewDecisionInput{ApplicationID: app.ID(), ProfileRevisionID: draft.ProfileRevisionID(), ProfileReviewID: sub.Review.ProfileReviewID(), ReviewerID: "independent-reviewer", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: sub.ProfileRevision.Revision(), ExpectedPublishedID: published, PolicyVersion: pd.InitialProfileReviewPolicyVersion, Outcome: outcome, ConfirmedCheckIDs: checks, Reason: &reason, DecidedAt: time.Now()})
		if e != nil {
			t.Fatal(e)
		}
	}
	return app.ID(), ids
}
func TestProfileManagementIntegration_BRPRF047_050_053_055(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	admin := shared.AuthID(strings.Repeat("opaque-admin-", 500))
	app, ids := managedProfileHistory(t, db, admin, "APPROVED", "REJECTED", "APPROVED", "DRAFT")
	repo := profileManagementTestRepository(db, managementTestSizer{})
	before := readTesterLinkApplication(t, db, app)
	page, e := repo.List(t.Context(), admin, app, nil, 1, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 1 || page.Items[0].ProfileRevisionID != ids[3].String() || page.ProfileState.CurrentPublishedProfileRevisionID == nil || *page.ProfileState.CurrentPublishedProfileRevisionID != ids[2].String() || page.NextPageToken == "" {
		t.Fatalf("page=%#v", page)
	}
	next, e := repo.List(t.Context(), admin, app, nil, 3, page.NextPageToken)
	if e != nil || len(next.Items) != 3 || next.NextPageToken != "" {
		t.Fatalf("next=%#v %v", next, e)
	}
	if next.Items[2].IsCurrentPublished || !next.Items[0].IsCurrentPublished || next.Items[1].LatestReview.Decision.Outcome != "REJECTED" {
		t.Fatal("history flags")
	}
	status := pd.ReviewStatusRejected
	filtered, e := repo.List(t.Context(), admin, app, &status, 20, "")
	if e != nil || len(filtered.Items) != 1 {
		t.Fatalf("filter=%v %v", filtered, e)
	}
	if _, e = repo.List(t.Context(), admin, app, &status, 20, page.NextPageToken); !errors.Is(e, pp.ErrInvalidProfileManagementPageToken) {
		t.Fatal("filter binding", e)
	}
	for _, caller := range []shared.AuthID{"stranger", "independent-reviewer"} {
		if _, e = repo.List(t.Context(), caller, app, nil, 1, strings.Repeat("x", 5000)); !errors.Is(e, pp.ErrProfileManagementNotFound) {
			t.Fatal("owner before cursor", e)
		}
	}
	if _, e = repo.Get(t.Context(), admin, app, ids[0], 20, page.NextPageToken); !errors.Is(e, pp.ErrInvalidProfileManagementPageToken) {
		t.Fatal("kind binding", e)
	}
	// Rejected terminal content retains legacy text, including empty strings and
	// text that no longer passes current draft validation.
	rawName, empty, icon := "  Cafe\u0301  ", "", "\u200b"
	if _, e = db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), bson.M{"profileRevisionId": ids[1].String()}, bson.M{"$set": bson.M{"displayName": rawName, "description": empty, "icon": icon}}, options.UpdateOne().SetBypassDocumentValidation(true)); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), bson.M{"profileRevisionId": ids[1].String()}, bson.M{"$set": bson.M{"snapshot.displayName": rawName, "snapshot.description": empty, "snapshot.icon": icon}}, options.UpdateOne().SetBypassDocumentValidation(true)); e != nil {
		t.Fatal(e)
	}
	detail, e := repo.Get(t.Context(), admin, app, ids[1], 20, "")
	if e != nil {
		t.Fatal(e)
	}
	if detail.Profile.DisplayName != rawName || detail.Profile.Description == nil || *detail.Profile.Description != "" || *detail.Profile.Icon != icon || len(detail.Reviews) != 1 || !reflect.DeepEqual(detail.Profile.LatestReview, &detail.Reviews[0]) {
		t.Fatalf("legacy=%#v", detail)
	}
	if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, app)) {
		t.Fatal("query wrote application/fences")
	}
	for _, life := range []string{"ACTIVE", "CLOSING", "CLOSED"} {
		for _, availability := range []string{"AVAILABLE", "SUSPENDED"} {
			for _, archive := range []string{"UNARCHIVED", "ARCHIVED"} {
				_, e = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": app.String()}, bson.M{"$set": bson.M{"lifecycleStatus": life, "platformAvailabilityStatus": availability, "archiveStatus": archive}}, options.UpdateOne().SetBypassDocumentValidation(true))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = repo.List(t.Context(), admin, app, nil, 20, ""); e != nil {
					t.Fatalf("retained %s/%s/%s: %v", life, availability, archive, e)
				}
			}
		}
	}
	_, e = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": app.String()}, bson.M{"$set": bson.M{"adminId": "new-admin"}, "$inc": bson.M{"ownershipRevision": int64(1)}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.List(t.Context(), admin, app, nil, 1, page.NextPageToken); !errors.Is(e, pp.ErrProfileManagementNotFound) {
		t.Fatal("transferred owner", e)
	}
	_, e = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": app.String()}, bson.M{"$set": bson.M{"adminId": admin.String()}, "$inc": bson.M{"ownershipRevision": int64(1)}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.List(t.Context(), admin, app, nil, 1, page.NextPageToken); !errors.Is(e, pp.ErrInvalidProfileManagementPageToken) {
		t.Fatal("transfer back", e)
	}
}
func TestProfileManagementIntegration_BRPRF048_052_056(t *testing.T) {
	client := integrationClient(t)
	t.Run("legal submitted maximum revision remains readable", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app, ids := managedProfileHistory(t, db, "admin", "DRAFT")
		if _, e := db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), bson.M{"profileRevisionId": ids[0].String()}, bson.M{"$set": bson.M{"revision": int64(math.MaxInt64 - 1)}}); e != nil {
			t.Fatal(e)
		}
		if _, e := NewApplicationProfileRevisionRepository(db).SubmitDraft(t.Context(), app, ids[0], "admin", math.MaxInt64-1, profileReviewID(t), time.Now()); e != nil {
			t.Fatal(e)
		}
		repo := profileManagementTestRepository(db, managementTestSizer{})
		if p, e := repo.List(t.Context(), "admin", app, nil, 20, ""); e != nil || len(p.Items) != 1 || p.Items[0].Revision != math.MaxInt64 {
			t.Fatalf("maximum submitted list=%#v %v", p, e)
		}
		if d, e := repo.Get(t.Context(), "admin", app, ids[0], 20, ""); e != nil || d.Profile.Revision != math.MaxInt64 {
			t.Fatalf("maximum submitted detail=%#v %v", d, e)
		}
	})
	t.Run("empty and explicit migration", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "admin", "empty_managed")
		repo := profileManagementTestRepository(db, managementTestSizer{})
		p, e := repo.List(t.Context(), app.AdminID(), app.ID(), nil, 20, "")
		if e != nil || len(p.Items) != 0 || p.ProfileState.WorkingProfileRevisionID != nil {
			t.Fatalf("empty=%v %v", p, e)
		}
		c, e := db.Collection(applicationProfileRevisionsCollectionName).Indexes().List(t.Context())
		if e != nil {
			t.Fatal(e)
		}
		var indexes []bson.M
		if e = c.All(t.Context(), &indexes); e != nil {
			t.Fatal(e)
		}
		found := false
		for _, i := range indexes {
			if i["name"] == profileManagementStatusIndex {
				found = true
			}
		}
		if !found {
			t.Fatal("0032 index absent")
		}
	})
	t.Run("budget no skipped item", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app, ids := managedProfileHistory(t, db, "admin", "APPROVED", "REJECTED", "DRAFT")
		repo := profileManagementTestRepository(db, managementTestSizer{perItem: pd.ProfileManagementResponseBudget/2 + 100})
		token := ""
		for i := 2; i >= 0; i-- {
			p, e := repo.List(t.Context(), "admin", app, nil, 3, token)
			if e != nil || len(p.Items) != 1 || p.Items[0].ProfileRevisionID != ids[i].String() {
				t.Fatalf("budget=%v %v", p, e)
			}
			token = p.NextPageToken
		}
		if token != "" {
			t.Fatal("nonterminal cursor")
		}
		repo.sizer = managementTestSizer{perItem: pd.ProfileManagementResponseBudget}
		if p, e := repo.List(t.Context(), "admin", app, nil, 3, ""); p != nil || !errors.Is(e, pp.ErrProfileManagementResponseTooLarge) {
			t.Fatal("oversize", p, e)
		}
	})
	for _, damage := range []string{"lookahead snapshot", "revision source", "published pointer", "review app", "review decision", "sequence type", "time", "missing root", "missing review", "duplicate sequence", "duplicate application id"} {
		t.Run(damage, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			app, ids := managedProfileHistory(t, db, "admin", "APPROVED", "REJECTED", "DRAFT")
			var e error
			rf := bson.M{"profileRevisionId": ids[1].String()}
			collection := applicationProfileRevisionsCollectionName
			filter := rf
			set := bson.M{}
			switch damage {
			case "duplicate application id":
				if e = db.Collection(applicationsCollectionName).Indexes().DropOne(t.Context(), applicationIDUniqueIndexName); e != nil {
					t.Fatal(e)
				}
				var duplicate bson.M
				if e = db.Collection(applicationsCollectionName).FindOne(t.Context(), bson.M{"id": app.String()}).Decode(&duplicate); e != nil {
					t.Fatal(e)
				}
				delete(duplicate, "_id")
				duplicate["adminId"] = "other-admin"
				_, e = db.Collection(applicationsCollectionName).InsertOne(t.Context(), duplicate)
			case "lookahead snapshot":
				collection = applicationProfileReviewsCollectionName
				set["snapshot.displayName"] = "drift"
			case "revision source":
				set["revision"] = int64(7)
			case "published pointer":
				collection = applicationProfilesCollectionName
				filter = bson.M{"applicationId": app.String()}
				set["currentPublishedProfileRevisionId"] = ids[1].String()
			case "review app":
				collection = applicationProfileReviewsCollectionName
				set["applicationId"] = nextIntegrationTesterJoinLinkID(t).String()
			case "review decision":
				collection = applicationProfileReviewsCollectionName
				set["decision.outcome"] = "APPROVED"
			case "sequence type":
				set["sequence"] = "2"
			case "time":
				set["updatedAt"] = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			case "missing root":
				_, e = db.Collection(applicationProfilesCollectionName).DeleteOne(t.Context(), bson.M{"applicationId": app.String()})
			case "missing review":
				_, e = db.Collection(applicationProfileReviewsCollectionName).DeleteOne(t.Context(), rf)
			case "duplicate sequence":
				if e = db.Collection(applicationProfileRevisionsCollectionName).Indexes().DropOne(t.Context(), profileRevisionSequenceUniqueIndexName); e != nil {
					t.Fatal(e)
				}
				set["sequence"] = int32(1)
			}
			if len(set) > 0 {
				_, e = db.Collection(collection).UpdateOne(t.Context(), filter, bson.M{"$set": set}, options.UpdateOne().SetBypassDocumentValidation(true))
			}
			if e != nil {
				t.Fatal(e)
			}
			p, e := profileManagementTestRepository(db, managementTestSizer{}).List(context.Background(), "admin", app, nil, 1, "")
			if p != nil || !errors.Is(e, pp.ErrProfileManagementStateInconsistent) {
				t.Fatalf("partial=%v err=%v", p, e)
			}
		})
	}
}
