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
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

func profileReviewID(t *testing.T) pd.ApplicationProfileReviewID {
	t.Helper()
	return pd.ApplicationProfileReviewID(nextIntegrationTesterJoinLinkID(t))
}
func storedProfileRevision(t *testing.T, db *drivermongo.Database, id pd.ApplicationProfileRevisionID) bson.Raw {
	t.Helper()
	raw, err := db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), bson.M{"profileRevisionId": id.String()}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func submitProfileFixture(t *testing.T, db *drivermongo.Database) (*ApplicationProfileRevisionRepository, *pd.ApplicationProfileRevision) {
	t.Helper()
	app := createVersionTestApplication(t, db, "profile-admin", "submit_profile")
	repo := NewApplicationProfileRevisionRepository(db)
	draft, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
	if err != nil {
		t.Fatal(err)
	}
	return repo, draft
}
func TestSubmitProfileDraftRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-PRF-015018019020022 immutable snapshot audit published and working pointers", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		repo, original := submitProfileFixture(t, db)
		// Seed a previously approved immutable revision; UC015 must preserve it.
		historical := profileRevisionToDocument(original)
		historical.ProfileRevisionID = nextIntegrationTesterJoinLinkID(t).String()
		historical.Sequence = 2
		historical.ReviewStatus = "APPROVED"
		historical.Revision = 3
		if _, err := db.Collection(applicationProfileRevisionsCollectionName).InsertOne(t.Context(), historical); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": original.ApplicationID().String()}, bson.M{"$set": bson.M{"currentPublishedProfileRevisionId": historical.ProfileRevisionID}}); err != nil {
			t.Fatal(err)
		}
		description, icon := "de\u0301tail", "opaque:icon"
		replacement, _ := pd.NewDraftApplicationProfileReplacement("new", &description, &icon)
		draft, err := repo.ReplaceDraft(t.Context(), original.ApplicationID(), original.ProfileRevisionID(), original.CreatedBy(), 1, replacement, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		before := readProfileProjection(t, db, draft.ApplicationID())
		id := profileReviewID(t)
		at := time.Date(2026, 9, 29, 0, 0, 0, 123456789, time.UTC)
		result, err := repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 2, id, at)
		if err != nil {
			t.Fatal(err)
		}
		revision, review := result.ProfileRevision, result.Review
		if revision.Revision() != 3 || revision.ReviewStatus() != pd.ReviewStatusSubmitted || revision.CreatedBy() != draft.CreatedBy() || !revision.CreatedAt().Equal(draft.CreatedAt()) || revision.Sequence() != draft.Sequence() || revision.DisplayName() != draft.DisplayName() || revision.Description().String() != draft.Description().String() || revision.Icon().String() != draft.Icon().String() || revision.UpdatedBy() != draft.CreatedBy() || !revision.UpdatedAt().Equal(at.Truncate(time.Millisecond)) {
			t.Fatal("incorrect submitted revision")
		}
		if review.ProfileReviewID() != id || review.SourceRevision() != 2 || review.Attempt() != 1 || string(review.Status()) != "PENDING" || review.Snapshot().Description().String() != "détail" || review.Snapshot().Icon().String() != icon || !review.SubmittedAt().Equal(revision.UpdatedAt()) {
			t.Fatal("incorrect review")
		}
		var stored applicationProfileReviewDocument
		if err = db.Collection(applicationProfileReviewsCollectionName).FindOne(t.Context(), bson.M{"profileReviewId": id.String()}).Decode(&stored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(stored, profileReviewToDocument(review)) || stored.Decision != nil || !reflect.DeepEqual(before, readProfileProjection(t, db, draft.ApplicationID())) {
			t.Fatal("stored snapshot or pointers changed")
		}
		appBefore := readTesterLinkApplication(t, db, draft.ApplicationID())
		rawBefore := storedProfileRevision(t, db, draft.ProfileRevisionID())
		if _, err = repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 2, profileReviewID(t), at); !errors.Is(err, pp.ErrApplicationProfileRevisionNotDraft) {
			t.Fatal("duplicate", err)
		}
		if _, err = repo.ReplaceDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 3, profileReplacement(t, "late"), at); !errors.Is(err, pp.ErrApplicationProfileRevisionNotDraft) {
			t.Fatal("late edit", err)
		}
		if _, err = repo.CreateDraft(t.Context(), draft.CreatedBy(), profileDraftFixture(t, draft.ApplicationID(), draft.CreatedBy())); !errors.Is(err, pp.ErrApplicationProfileWorkRevisionAlreadyExists) {
			t.Fatal("lost slot", err)
		}
		if !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, draft.ApplicationID())) || !reflect.DeepEqual(rawBefore, storedProfileRevision(t, db, draft.ProfileRevisionID())) {
			t.Fatal("failed retry changed state")
		}
		assertCollectionCount(t, db, applicationProfileReviewsCollectionName, 1)
		for _, name := range []string{applicationVersionsCollectionName, applicationReviewsCollectionName, applicationTesterMembershipsCollectionName} {
			assertCollectionCount(t, db, name, 0)
		}
	})
	t.Run("BR-PRF-016019021 simultaneous submissions only one attempt", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		repo, draft := submitProfileFixture(t, db)
		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			id := profileReviewID(t)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, id, time.Now())
				errs <- err
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		success, conflict := 0, 0
		for err := range errs {
			if err == nil {
				success++
			} else if errors.Is(err, pp.ErrApplicationProfileRevisionNotDraft) || errors.Is(err, pp.ErrApplicationProfileRevisionConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal(success, conflict)
		}
		assertCollectionCount(t, db, applicationProfileReviewsCollectionName, 1)
		if storedProfileRevision(t, db, draft.ProfileRevisionID()).Lookup("revision").Int64() != 2 {
			t.Fatal("multiple increments")
		}
	})
	t.Run("BR-PRF-015016 missing foreign stale and transferred administrator", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		repo, draft := submitProfileFixture(t, db)
		for _, tc := range []struct {
			app      shared.ApplicationID
			id       pd.ApplicationProfileRevisionID
			admin    shared.AuthID
			expected int64
			want     error
		}{
			{shared.ApplicationID(nextIntegrationTesterJoinLinkID(t)), draft.ProfileRevisionID(), draft.CreatedBy(), 1, pp.ErrApplicationProfileRevisionNotFound},
			{draft.ApplicationID(), pd.ApplicationProfileRevisionID(nextIntegrationTesterJoinLinkID(t)), draft.CreatedBy(), 1, pp.ErrApplicationProfileRevisionNotFound},
			{draft.ApplicationID(), draft.ProfileRevisionID(), "outsider", 1, pp.ErrApplicationAdminRequired},
			{draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 2, pp.ErrApplicationProfileRevisionConflict},
		} {
			if _, err := repo.SubmitDraft(t.Context(), tc.app, tc.id, tc.admin, tc.expected, profileReviewID(t), time.Now()); !errors.Is(err, tc.want) {
				t.Fatal(err, tc.want)
			}
		}
		other := createVersionTestApplication(t, db, "profile-admin", "other")
		if _, err := repo.SubmitDraft(t.Context(), other.ID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, profileReviewID(t), time.Now()); !errors.Is(err, pp.ErrApplicationProfileRevisionNotFound) {
			t.Fatal("foreign", err)
		}
		session := startTesterMembershipTransaction(t, client)
		if _, err := db.Collection(applicationsCollectionName).UpdateOne(drivermongo.NewSessionContext(t.Context(), session), bson.M{"id": draft.ApplicationID().String()}, bson.M{"$set": bson.M{"adminId": "new-admin"}}); err != nil {
			t.Fatal(err)
		}
		competitor, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		id := profileReviewID(t)
		go func() {
			_, err := NewApplicationProfileRevisionRepository(competitor.Database(db.Name())).SubmitDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, id, time.Now())
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "administrator transfer versus submission")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, pp.ErrApplicationAdminRequired) {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		result, err := repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), "new-admin", 1, id, time.Now())
		if err != nil || result.ProfileRevision.CreatedBy() != draft.CreatedBy() || result.ProfileRevision.UpdatedBy() != "new-admin" || result.Review.SubmittedBy() != "new-admin" {
			t.Fatal("new administrator", err)
		}
	})
	for _, winner := range []string{"edit", "submit"} {
		t.Run("BR-PRF-016021 real edit versus submit winner "+winner, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			repo, draft := submitProfileFixture(t, db)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			// Pause the complete public command immediately before its commit. Both
			// contenders use their public Repository entry points and real transactions.
			reached, release := make(chan struct{}), make(chan struct{})
			var gateOnce, releaseOnce sync.Once
			resume := func() { releaseOnce.Do(func() { close(release) }) }
			defer resume()
			winnerClient, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(commandContext context.Context, e *event.CommandStartedEvent) {
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
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				resume()
				if err := winnerClient.Disconnect(context.Background()); err != nil {
					t.Error(err)
				}
			})
			winnerRepo := NewApplicationProfileRevisionRepository(winnerClient.Database(db.Name()))
			replacement := profileReplacement(t, "edited")
			id := profileReviewID(t)
			at := time.Now().UTC().Truncate(time.Millisecond)
			winning := make(chan error, 1)
			go func() {
				var err error
				if winner == "edit" {
					_, err = winnerRepo.ReplaceDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, replacement, at)
				} else {
					_, err = winnerRepo.SubmitDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, id, at)
				}
				winning <- err
			}()
			awaitMongoCommand(t, ctx, reached, "winning public command reached commit")
			competitor, started := monitoredMongoClient(t, db.Name(), "findAndModify")
			loserRepo := NewApplicationProfileRevisionRepository(competitor.Database(db.Name()))
			done := make(chan error, 1)
			go func() {
				var err error
				if winner == "edit" {
					_, err = loserRepo.SubmitDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, id, at)
				} else {
					_, err = loserRepo.ReplaceDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, replacement, at)
				}
				done <- err
			}()
			awaitMongoCommand(t, ctx, started, "losing public command attempts write fence")
			resume()
			select {
			case err := <-winning:
				if err != nil {
					t.Fatal("winner", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			want := pp.ErrApplicationProfileRevisionConflict
			if winner == "submit" {
				want = pp.ErrApplicationProfileRevisionNotDraft
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatal(err, want)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			raw := storedProfileRevision(t, db, draft.ProfileRevisionID())
			if raw.Lookup("revision").Int64() != 2 {
				t.Fatal("loser incremented revision")
			}
			expectedSnapshot := draft.DisplayName().String()
			expectedSource := int64(1)
			if winner == "edit" {
				assertCollectionCount(t, db, applicationProfileReviewsCollectionName, 0)
				if raw.Lookup("displayName").StringValue() != "edited" || raw.Lookup("reviewStatus").StringValue() != "DRAFT" {
					t.Fatal("edit winner state")
				}
				// A fresh submission must snapshot the winning edited content, never the
				// losing submission's earlier expectation of source revision one.
				result, err := repo.SubmitDraft(ctx, draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 2, id, at)
				if err != nil || result.Review.Snapshot().DisplayName().String() != "edited" || result.Review.SourceRevision() != 2 {
					t.Fatal("fresh submission after edit", err)
				}
				expectedSnapshot, expectedSource = "edited", 2
			} else if raw.Lookup("displayName").StringValue() != draft.DisplayName().String() || raw.Lookup("reviewStatus").StringValue() != "SUBMITTED" {
				t.Fatal("submission winner state")
			}
			assertCollectionCount(t, db, applicationProfileReviewsCollectionName, 1)
			var review applicationProfileReviewDocument
			if err := db.Collection(applicationProfileReviewsCollectionName).FindOne(ctx, bson.M{"profileReviewId": id.String()}).Decode(&review); err != nil {
				t.Fatal(err)
			}
			if review.Snapshot.DisplayName != expectedSnapshot || review.Snapshot.Description != nil || review.Snapshot.Icon != nil || review.SourceRevision != expectedSource || review.Attempt != 1 {
				t.Fatal("snapshot does not match winning content")
			}
		})
	}

	for _, mode := range []string{"review insert", "revision update", "missing pointer", "wrong pointer", "pending review", "same source", "revision overflow", "attempt overflow", "invalid name", "non-NFC description", "invalid icon", "missing nullable", "bad audit"} {
		t.Run("BR-PRF-017019021 atomic failure "+mode, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			repo, draft := submitProfileFixture(t, db)
			id := profileReviewID(t)
			expected := int64(1)
			want := pp.ErrApplicationProfileStateInconsistent
			reviewCount := 0
			revisions := db.Collection(applicationProfileRevisionsCollectionName)
			filter := bson.M{"profileRevisionId": draft.ProfileRevisionID().String()}
			var err error
			switch mode {
			case "review insert", "revision update":
				collection := applicationProfileReviewsCollectionName
				if mode == "revision update" {
					collection = applicationProfileRevisionsCollectionName
				}
				err = db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: collection}, {Key: "validator", Value: bson.M{"mustNotExist": bson.M{"$exists": true}}}}).Err()
				want = nil
			case "missing pointer":
				_, err = db.Collection(applicationProfilesCollectionName).DeleteMany(t.Context(), bson.M{})
			case "wrong pointer":
				_, err = db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": draft.ApplicationID().String()}, bson.M{"$set": bson.M{"workingProfileRevisionId": nextIntegrationTesterJoinLinkID(t).String()}})
			case "pending review", "same source", "attempt overflow":
				result, e := draft.SubmitDraft(1, id, 1, draft.CreatedBy(), time.Now())
				if e != nil {
					t.Fatal(e)
				}
				doc := profileReviewToDocument(result.Review)
				if mode == "same source" {
					doc.Status = "APPROVED"
				}
				if mode == "attempt overflow" {
					doc.Status = "APPROVED"
					doc.SourceRevision = 2
					doc.Attempt = math.MaxInt32
				}
				_, err = db.Collection(applicationProfileReviewsCollectionName).InsertOne(t.Context(), doc, options.InsertOne().SetBypassDocumentValidation(true))
				reviewCount = 1
			case "revision overflow":
				expected = math.MaxInt64
				_, err = revisions.UpdateOne(t.Context(), filter, bson.M{"$set": bson.M{"revision": expected}})
			case "invalid name":
				want = pp.ErrInvalidApplicationProfileContent
				_, err = revisions.UpdateOne(t.Context(), filter, bson.M{"$set": bson.M{"displayName": " invalid "}}, options.UpdateOne().SetBypassDocumentValidation(true))
			case "non-NFC description":
				want = pp.ErrInvalidApplicationProfileContent
				_, err = revisions.UpdateOne(t.Context(), filter, bson.M{"$set": bson.M{"description": "de\u0301tail"}})
			case "invalid icon":
				want = pp.ErrInvalidApplicationProfileContent
				_, err = revisions.UpdateOne(t.Context(), filter, bson.M{"$set": bson.M{"icon": "bad\nicon"}}, options.UpdateOne().SetBypassDocumentValidation(true))
			case "missing nullable":
				_, err = revisions.UpdateOne(t.Context(), filter, bson.M{"$unset": bson.M{"description": ""}}, options.UpdateOne().SetBypassDocumentValidation(true))
			case "bad audit":
				_, err = revisions.UpdateOne(t.Context(), filter, bson.M{"$set": bson.M{"updatedBy": "wrong"}}, options.UpdateOne().SetBypassDocumentValidation(true))
			}
			if err != nil {
				t.Fatal(err)
			}
			appBefore := readTesterLinkApplication(t, db, draft.ApplicationID())
			rawBefore := storedProfileRevision(t, db, draft.ProfileRevisionID())
			_, err = repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), expected, profileReviewID(t), time.Now())
			if err == nil || (want != nil && !errors.Is(err, want)) || strings.Contains(err.Error(), "Café") {
				t.Fatal("expected safe failure", want, err)
			}
			if !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, draft.ApplicationID())) || !reflect.DeepEqual(rawBefore, storedProfileRevision(t, db, draft.ProfileRevisionID())) {
				t.Fatal("partial mutation")
			}
			assertCollectionCount(t, db, applicationProfileReviewsCollectionName, reviewCount)
		})
	}
	t.Run("BR-PRF-019 next attempt allocated from stored maximum", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		repo, draft := submitProfileFixture(t, db)
		old, err := draft.SubmitDraft(1, profileReviewID(t), 7, draft.CreatedBy(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		doc := profileReviewToDocument(old.Review)
		doc.Status = "APPROVED"
		doc.SourceRevision = 2
		if _, err = db.Collection(applicationProfileReviewsCollectionName).InsertOne(t.Context(), doc, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal(err)
		}
		got, err := repo.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), draft.CreatedBy(), 1, profileReviewID(t), time.Now())
		if err != nil || got.Review.Attempt() != 8 {
			t.Fatal("attempt", err)
		}
	})
}
