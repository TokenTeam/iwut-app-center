package mongo

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func activityReader(db *dm.Database) *ManagementActivityRepository {
	key, _ := DeriveManagementActivityCursorKey([]byte("activity-test-only-material"))
	return NewManagementActivityRepository(db, key)
}
func activityQuery(kinds ...ad.ManagementActivityKind) ad.ManagementActivityQuery {
	q, _ := ad.NewManagementActivityQuery(ad.ManagementActivityQuery{Kinds: kinds})
	return q
}
func activityHistory(t *testing.T, db *dm.Database) (shared.ApplicationID, versionManagementFixture) {
	t.Helper()
	admin := shared.AuthID("activity-owner")
	app := createVersionTestApplication(t, db, admin.String(), "Activity_App")
	versions := createVersionManagementFixture(t, db, app.ID(), admin)
	repo := NewApplicationProfileRevisionRepository(db)
	for _, outcome := range []string{"APPROVE", "REJECT", "PENDING"} {
		draft, err := repo.CreateDraft(t.Context(), admin, profileDraftFixture(t, app.ID(), admin))
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
		sub, err := repo.SubmitDraft(t.Context(), app.ID(), draft.ProfileRevisionID(), admin, draft.Revision(), profileReviewID(t), at)
		if err != nil {
			t.Fatal(err)
		}
		if outcome == "PENDING" {
			continue
		}
		checks := pd.InitialProfileReviewChecks()
		if outcome == "REJECT" {
			checks = nil
		}
		reason := "original historical reason"
		var published *pd.ApplicationProfileRevisionID
		if p := readProfileProjection(t, db, app.ID()).CurrentPublishedProfileRevisionID; p != nil {
			id := pd.ApplicationProfileRevisionID(*p)
			published = &id
		}
		_, err = repo.DecideReview(t.Context(), pp.ProfileReviewDecisionInput{ApplicationID: app.ID(), ProfileRevisionID: draft.ProfileRevisionID(), ProfileReviewID: sub.Review.ProfileReviewID(), ReviewerID: "separate-reviewer", Permissions: []string{pd.ProfileReviewPermission}, ExpectedRevision: sub.ProfileRevision.Revision(), ExpectedPublishedID: published, PolicyVersion: pd.InitialProfileReviewPolicyVersion, Outcome: outcome, ConfirmedCheckIDs: checks, Reason: &reason, DecidedAt: at.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
	}
	return app.ID(), versions
}
func TestManagementActivityIntegration_BRAPP067068069071_HistoryAndPaging(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	app, versions := activityHistory(t, db)
	r := activityReader(db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	before := readTesterLinkApplication(t, db, app)
	full, err := r.ListManagementActivity(t.Context(), "activity-owner", app, activityQuery(), at)
	if err != nil || len(full.Items) != 12 || full.NextPageToken != "" {
		t.Fatalf("all sources: count=%d err=%v", len(full.Items), err)
	}
	seenKinds := map[ad.ManagementActivityKind]bool{}
	var historicID string
	for _, a := range full.Items {
		seenKinds[a.Kind] = true
		l := a.Locator()
		d, err := r.GetManagementActivity(t.Context(), "activity-owner", app, l, at)
		if err != nil || d.Activity.ActivityID != a.ActivityID {
			t.Fatal("detail", err)
		}
		rejected := a.Kind == ad.VersionReviewRejected || a.Kind == ad.ProfileReviewRejected
		if rejected != (d.RejectionReason != nil) {
			t.Fatal("reason disclosure", a.Kind)
		}
		if a.VersionReview != nil && a.VersionReview.VersionID == versions.restored.ID().String() && a.Kind == ad.VersionReviewRejected {
			historicID = a.ActivityID
		}
	}
	if len(seenKinds) != 6 || historicID == "" {
		t.Fatal("missing six kinds/restored attempt", seenKinds)
	}
	// Current labels/content/state do not rewrite an older immutable Review.
	if _, err = db.Collection(applicationVersionsCollectionName).UpdateOne(t.Context(), bson.M{"versionId": versions.restored.ID().String()}, bson.M{"$set": bson.M{"versionLabel": "edited_after_restore", "revision": int64(5)}}); err != nil {
		t.Fatal(err)
	}
	l, _ := ad.ParseManagementActivityID(historicID)
	d, err := r.GetManagementActivity(t.Context(), "activity-owner", app, l, at)
	if err != nil || *d.Activity.VersionLabelAtSubmission != "restored" {
		t.Fatal("historical snapshot", err)
	}
	q := activityQuery()
	q.PageSize = 1
	var collected []ad.ManagementActivity
	for step := 0; step < 20; step++ {
		p, err := r.ListManagementActivity(t.Context(), "activity-owner", app, q, at)
		if err != nil {
			t.Fatal(err)
		}
		collected = append(collected, p.Items...)
		if p.NextPageToken == "" {
			break
		}
		if len(p.Items) == 0 {
			t.Fatal("empty continuation")
		}
		q.PageToken = p.NextPageToken
		q.PageSize = int32(step%3 + 1)
	}
	if !reflect.DeepEqual(collected, full.Items) {
		t.Fatal("four-source merge lost/reordered events", len(collected))
	}
	for kind := range seenKinds {
		p, err := r.ListManagementActivity(t.Context(), "activity-owner", app, activityQuery(kind), at)
		if err != nil || len(p.Items) == 0 {
			t.Fatal("kind filter", kind, err)
		}
		for _, a := range p.Items {
			if a.Kind != kind {
				t.Fatal("wrong kind")
			}
		}
	}
	after := readTesterLinkApplication(t, db, app)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("read wrote application/fence")
	}
	// Historical reason keeps legal original whitespace, not current normalization.
	reason := "  original\nreason  "
	if _, err = db.Collection(applicationReviewsCollectionName).UpdateOne(t.Context(), bson.M{"reviewId": l.ReviewID}, bson.M{"$set": bson.M{"decision.reason": reason}}); err != nil {
		t.Fatal(err)
	}
	d, err = r.GetManagementActivity(t.Context(), "activity-owner", app, l, at)
	if err != nil || *d.RejectionReason != reason {
		t.Fatal("original reason", err)
	}
}
func TestManagementActivityIntegration_BRAPP067071_OwnerAndLifecycle(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	app, _ := activityHistory(t, db)
	r := activityReader(db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	q := activityQuery()
	q.PageSize = 1
	page, err := r.ListManagementActivity(t.Context(), "activity-owner", app, q, at)
	if err != nil || page.NextPageToken == "" {
		t.Fatal(err)
	}
	q.PageToken = page.NextPageToken
	for _, other := range []shared.AuthID{"reviewer", "platform-admin", "tester", "other"} {
		if _, err := r.ListManagementActivity(t.Context(), other, app, q, at); !errors.Is(err, ad.ErrManagementActivityNotFound) {
			t.Fatal("owner gate", err)
		}
	}
	for _, change := range []bson.M{{"lifecycleStatus": "CLOSING"}, {"lifecycleStatus": "CLOSED", "platformAvailabilityStatus": "SUSPENDED", "archiveStatus": "ARCHIVED"}} {
		if _, err = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": app.String()}, bson.M{"$set": change}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ListManagementActivity(t.Context(), "activity-owner", app, q, at); err != nil {
			t.Fatal("history lifecycle", err)
		}
	}
	for i, owner := range []string{"new-owner", "activity-owner"} {
		if _, err = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": app.String()}, bson.M{"$set": bson.M{"adminId": owner, "ownershipRevision": int64(i + 2)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.ListManagementActivity(t.Context(), shared.AuthID(owner), app, q, at); !errors.Is(err, ad.ErrInvalidManagementActivityPageToken) {
			t.Fatal("ownership binding", err)
		}
		p, err := r.ListManagementActivity(t.Context(), shared.AuthID(owner), app, activityQuery(), at)
		if err != nil || len(p.Items) != 12 {
			t.Fatal("new owner reads past", err)
		}
	}
	q.PageToken = "!"
	if _, err := r.ListManagementActivity(t.Context(), "former-owner", app, q, at); !errors.Is(err, ad.ErrManagementActivityNotFound) {
		t.Fatal("owner before token", err)
	}
	empty := createVersionTestApplication(t, db, "activity-owner", "Empty_App")
	p, err := r.ListManagementActivity(t.Context(), "activity-owner", empty.ID(), activityQuery(), at)
	if err != nil || p.Items == nil || len(p.Items) != 0 || p.NextPageToken != "" {
		t.Fatal("empty", err)
	}
}
func TestManagementActivityIntegration_BRAPP070_CorruptionAndIndexes(t *testing.T) {
	client := integrationClient(t)
	for _, damage := range []string{"parent", "reason", "binding", "time", "attempt type", "state", "parent app", "label"} {
		t.Run(damage, func(t *testing.T) {
			db := reusableIntegrationDatabase(t, client)
			seed := createRejectedReview(t, db, "activity-owner", "Corrupt_Activity")
			r := activityReader(db)
			at := time.Now().UTC().Truncate(time.Millisecond)
			update := bson.M{}
			switch damage {
			case "parent":
				_, err := db.Collection(applicationVersionsCollectionName).DeleteOne(t.Context(), bson.M{"versionId": seed.versionID.String()})
				if err != nil {
					t.Fatal(err)
				}
			case "parent app":
				_, err := db.Collection(applicationVersionsCollectionName).UpdateOne(t.Context(), bson.M{"versionId": seed.versionID.String()}, bson.M{"$set": bson.M{"applicationId": nextIntegrationApplicationID(t).String()}})
				if err != nil {
					t.Fatal(err)
				}
			case "reason":
				update["decision.reason"] = ""
			case "binding":
				update["policyBinding.version"] = "other-policy"
			case "time":
				update["decision.decidedAt"] = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			case "attempt type":
				update["attempt"] = int64(1)
			case "state":
				update["status"] = "PENDING"
			case "label":
				update["snapshot.versionLabel"] = int64(1)
			}
			if len(update) > 0 {
				if _, err := db.Collection(applicationReviewsCollectionName).UpdateOne(t.Context(), bson.M{"reviewId": seed.reviewID.String()}, bson.M{"$set": update}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}
			p, err := r.ListManagementActivity(t.Context(), "activity-owner", seed.applicationID, activityQuery(), at)
			if !errors.Is(err, ad.ErrManagementActivityStateInconsistent) || p.Items != nil {
				t.Fatal("whole page fails", err)
			}
			if _, err = r.GetManagementActivity(t.Context(), "activity-owner", seed.applicationID, ad.ManagementActivityLocator{Source: 0, Phase: 0, ReviewID: seed.reviewID.String()}, at); !errors.Is(err, ad.ErrManagementActivityStateInconsistent) {
				t.Fatal("detail fails", err)
			}
		})
	}
	db := reusableIntegrationDatabase(t, client)
	for source := 0; source < 2; source++ {
		collection, _, _, _ := activityFields(source)
		cur, err := db.Collection(collection).Indexes().List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var indexes []bson.M
		if err = cur.All(t.Context(), &indexes); err != nil {
			t.Fatal(err)
		}
		names := []string{}
		for _, index := range indexes {
			names = append(names, index["name"].(string))
		}
		if !slices.Contains(names, activitySubmissionIndexName) || !slices.Contains(names, activityDecisionIndexName) {
			t.Fatal("missing indexes", names)
		}
	}
	if err := NewMigrator(db).Migrate(t.Context()); err != nil {
		t.Fatal("repeat migration", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := activityReader(db).ListManagementActivity(ctx, "owner", nextIntegrationApplicationID(t), activityQuery(), time.Now().UTC().Truncate(time.Millisecond)); !errors.Is(err, ad.ErrManagementActivityUnavailable) {
		t.Fatal("storage failure", err)
	}
}
func TestManagementActivityIntegration_BRAPP070_SnapshotAndNoWrites(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	app, _ := activityHistory(t, db)
	reached, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	commands := []string{}
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		mu.Lock()
		commands = append(commands, e.CommandName)
		mu.Unlock()
	}, Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
		if e.CommandName == "find" {
			once.Do(func() {
				close(reached)
				select {
				case <-resume:
				case <-ctx.Done():
				}
			})
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	type result struct {
		p   ad.ManagementActivityPage
		err error
	}
	done := make(chan result, 1)
	at := time.Now().UTC().Truncate(time.Millisecond)
	go func() {
		p, e := activityReader(client.Database(db.Name())).ListManagementActivity(t.Context(), "activity-owner", app, activityQuery(), at)
		done <- result{p, e}
	}()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot not reached")
	}
	if _, err = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": app.String()}, bson.M{"$set": bson.M{"adminId": "new-owner", "ownershipRevision": int64(2), "lifecycleStatus": "CLOSED"}}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case got := <-done:
		if got.err != nil || len(got.p.Items) != 12 {
			t.Fatal("mixed snapshot", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot did not finish")
	}
	if _, err := activityReader(db).ListManagementActivity(t.Context(), "activity-owner", app, activityQuery(), at); !errors.Is(err, ad.ErrManagementActivityNotFound) {
		t.Fatal("new snapshot owner", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, cmd := range commands {
		if slices.Contains([]string{"insert", "update", "delete", "findAndModify"}, cmd) {
			t.Fatal("read wrote", cmd)
		}
	}
	// At most owner + four review streams + two parent batches, independent of history size.
	finds := 0
	for _, cmd := range commands {
		if strings.EqualFold(cmd, "find") {
			finds++
		}
	}
	if finds > 7 {
		t.Fatal("unbounded/N+1 reads", finds)
	}
}

func TestManagementActivityIntegration_BRAPP068070071_NewAttemptAndIndexPlans(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	seed := createRejectedReview(t, db, "activity-owner", "New_Attempt")
	r := activityReader(db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	old := ad.ManagementActivityLocator{Source: 0, Phase: 0, ReviewID: seed.reviewID.String()}
	d, err := r.GetManagementActivity(t.Context(), seed.adminID, seed.applicationID, old, at)
	if err != nil {
		t.Fatal(err)
	}
	restoredAt := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	if _, err := NewApplicationReviewRestorationRepository(db).RestoreDraft(t.Context(), seed.applicationID, seed.versionID, seed.reviewID, seed.adminID, 3, restoredAt); err != nil {
		t.Fatal(err)
	}
	submission := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, submission, seed.applicationID, seed.versionID, seed.adminID.String(), 4)
	next := nextIntegrationApplicationReviewID(t)
	if _, err := submission.Submit(t.Context(), candidate, next, seed.adminID, 41, "public-https.v1", restoredAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.GetManagementActivity(t.Context(), seed.adminID, seed.applicationID, old, at)
	if err != nil || !reflect.DeepEqual(d, fresh) {
		t.Fatal("new attempt overwrote old rejection", err)
	}
	page, err := r.ListManagementActivity(t.Context(), seed.adminID, seed.applicationID, activityQuery(), at)
	if err != nil || len(page.Items) != 3 || page.Items[0].VersionReview.Attempt != 2 {
		t.Fatal("new attempt history", err)
	}
	if _, err := r.GetManagementActivity(t.Context(), seed.adminID, seed.applicationID, ad.ManagementActivityLocator{Source: 0, Phase: 0, ReviewID: next.String()}, at); !errors.Is(err, ad.ErrManagementActivityNotFound) {
		t.Fatal("pending has no decision", err)
	}
	for source := 0; source < 2; source++ {
		for phase := 0; phase < 2; phase++ {
			collection, id, _, _ := activityFields(source)
			field, hint := "submittedAt", activitySubmissionIndexName
			filter := bson.M{"applicationId": seed.applicationID.String()}
			if phase == 0 {
				field, hint = "decision.decidedAt", activityDecisionIndexName
				filter["decision.outcome"] = bson.M{"$in": []string{"APPROVED", "REJECTED"}}
			}
			var plan bson.Raw
			err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: bson.D{{Key: "find", Value: collection}, {Key: "filter", Value: filter}, {Key: "sort", Value: bson.D{{Key: field, Value: -1}, {Key: id, Value: -1}}}, {Key: "hint", Value: hint}, {Key: "limit", Value: 101}}}, {Key: "verbosity", Value: "executionStats"}}).Decode(&plan)
			if err != nil {
				t.Fatal("indexed stream", err)
			}
			raw, err := bson.MarshalExtJSON(plan, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"stage":"SORT"`) || strings.Contains(string(raw), `"stage":"COLLSCAN"`) {
				t.Fatal("unbounded sort/scan", string(raw))
			}
		}
	}
}

func TestManagementActivityIntegration_BRAPP068070_DuplicateStableIDLookahead(t *testing.T) {
	db := reusableIntegrationDatabase(t, integrationClient(t))
	seed := createRejectedReview(t, db, "activity-owner", "Duplicate_Activity")
	original := readReviewDocument(t, db, seed.reviewID)
	duplicate := original
	duplicate.Attempt++
	duplicate.SourceVersionRevision += 3
	duplicate.SubmittedAt = duplicate.SubmittedAt.Add(time.Millisecond)
	decision := *original.Decision
	decision.DecidedAt = decision.DecidedAt.Add(time.Millisecond)
	duplicate.Decision = &decision
	if err := db.Collection(applicationReviewsCollectionName).Indexes().DropOne(t.Context(), applicationReviewIDUniqueIndexName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(applicationReviewsCollectionName).InsertOne(t.Context(), duplicate); err != nil {
		t.Fatal(err)
	}
	r := activityReader(db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, kind := range []ad.ManagementActivityKind{ad.VersionReviewRejected, ad.VersionReviewSubmitted} {
		q := activityQuery(kind)
		q.PageSize = 1
		p, err := r.ListManagementActivity(t.Context(), seed.adminID, seed.applicationID, q, at)
		if !errors.Is(err, ad.ErrManagementActivityStateInconsistent) || p.Items != nil {
			t.Fatal("duplicate necessary lookahead was returned", err)
		}
	}
	if _, err := r.GetManagementActivity(t.Context(), seed.adminID, seed.applicationID, ad.ManagementActivityLocator{Source: 0, Phase: 0, ReviewID: seed.reviewID.String()}, at); !errors.Is(err, ad.ErrManagementActivityStateInconsistent) {
		t.Fatal("duplicate detail source", err)
	}
}
