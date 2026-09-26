package mongo

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func profileReplacement(t *testing.T, name string) pd.DraftApplicationProfileReplacement {
	t.Helper()
	r, e := pd.NewDraftApplicationProfileReplacement(name, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestReplaceProfileDraftRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-PRF-009010011012014 replace clear normalized noop audit and OCC", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "admin", "profile_replace")
		repo := NewApplicationProfileRevisionRepository(db)
		original, e := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
		if e != nil {
			t.Fatal(e)
		}
		at := time.Date(2026, 9, 28, 0, 0, 0, 123456789, time.UTC)
		description, icon := "de\u0301tail", "opaque:icon"
		replacement, _ := pd.NewDraftApplicationProfileReplacement("new", &description, &icon)
		got, e := repo.ReplaceDraft(t.Context(), app.ID(), original.ProfileRevisionID(), app.AdminID(), 1, replacement, at)
		if e != nil {
			t.Fatal(e)
		}
		if got.Revision() != 2 || got.Description().String() != "détail" || got.Icon().String() != icon || got.Sequence() != original.Sequence() || got.CreatedBy() != original.CreatedBy() || !got.CreatedAt().Equal(original.CreatedAt()) || !got.UpdatedAt().Equal(at.Truncate(time.Millisecond)) {
			t.Fatal("replacement facts")
		}
		same, e := repo.ReplaceDraft(t.Context(), app.ID(), original.ProfileRevisionID(), app.AdminID(), 2, replacement, at.Add(time.Hour))
		if e != nil || !reflect.DeepEqual(same, got) {
			t.Fatal("noop modified audit", e)
		}
		if _, e = repo.ReplaceDraft(t.Context(), app.ID(), original.ProfileRevisionID(), app.AdminID(), 1, replacement, at); !errors.Is(e, pp.ErrApplicationProfileRevisionConflict) {
			t.Fatal("stale noop", e)
		}
		got, e = repo.ReplaceDraft(t.Context(), app.ID(), original.ProfileRevisionID(), app.AdminID(), 2, profileReplacement(t, "Cafe\u0301"), at)
		if e != nil || got.Revision() != 3 || got.Description() != nil || got.Icon() != nil {
			t.Fatal("clear", e)
		}
		filter := bson.M{"profileRevisionId": original.ProfileRevisionID().String()}
		raw, e := db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), filter).Raw()
		if e != nil {
			t.Fatal(e)
		}
		stored, e := profileRevisionFromRaw(raw)
		if e != nil || !reflect.DeepEqual(stored, got) || raw.Lookup("description").Type != bson.TypeNull || raw.Lookup("icon").Type != bson.TypeNull {
			t.Fatal("stored mismatch", e)
		}
		before := readTesterLinkApplication(t, db, app.ID())
		projection := readProfileProjection(t, db, app.ID())
		if _, e = repo.ReplaceDraft(t.Context(), app.ID(), original.ProfileRevisionID(), "other", 3, profileReplacement(t, "Café"), at); !errors.Is(e, pp.ErrApplicationAdminRequired) {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, app.ID())) || !reflect.DeepEqual(projection, readProfileProjection(t, db, app.ID())) {
			t.Fatal("failed command changed app")
		}
		otherApp := createVersionTestApplication(t, db, "admin", "profile_other")
		if _, e = repo.ReplaceDraft(t.Context(), otherApp.ID(), original.ProfileRevisionID(), app.AdminID(), 3, profileReplacement(t, "new"), at); !errors.Is(e, pp.ErrApplicationProfileRevisionNotFound) {
			t.Fatal("foreign revision", e)
		}
		for _, ids := range [][2]string{{string(nextIntegrationTesterJoinLinkID(t)), original.ProfileRevisionID().String()}, {app.ID().String(), string(nextIntegrationTesterJoinLinkID(t))}} {
			if _, e = repo.ReplaceDraft(t.Context(), shared.ApplicationID(ids[0]), pd.ApplicationProfileRevisionID(ids[1]), app.AdminID(), 3, profileReplacement(t, "new"), at); !errors.Is(e, pp.ErrApplicationProfileRevisionNotFound) {
				t.Fatal(e)
			}
		}
	})
	t.Run("BR-PRF-011013 simultaneous edits only one commits", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "admin", "profile_edit_race")
		repo := NewApplicationProfileRevisionRepository(db)
		r, e := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
		if e != nil {
			t.Fatal(e)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, name := range []string{"first", "second"} {
			replacement := profileReplacement(t, name)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, e := repo.ReplaceDraft(t.Context(), app.ID(), r.ProfileRevisionID(), app.AdminID(), 1, replacement, time.Now())
				results <- e
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		success, conflict := 0, 0
		for e := range results {
			if e == nil {
				success++
			} else if errors.Is(e, pp.ErrApplicationProfileRevisionConflict) {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatal(success, conflict)
		}
	})
	for _, mode := range []string{"admin transfer", "DRAFT freeze"} {
		t.Run("BR-PRF-008013 "+mode, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			app := createVersionTestApplication(t, db, "admin", "profile_protected")
			repo := NewApplicationProfileRevisionRepository(db)
			r, e := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
			if e != nil {
				t.Fatal(e)
			}
			session := startTesterMembershipTransaction(t, client)
			tx := drivermongo.NewSessionContext(t.Context(), session)
			update := bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}}
			want := pp.ErrApplicationProfileRevisionNotDraft
			if mode == "admin transfer" {
				update = bson.M{"$set": bson.M{"adminId": "new-admin"}}
				want = pp.ErrApplicationAdminRequired
			}
			if _, e = db.Collection(applicationsCollectionName).UpdateOne(tx, bson.M{"id": app.ID().String()}, update); e != nil {
				t.Fatal(e)
			}
			if mode == "DRAFT freeze" {
				if _, e = db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(tx, bson.M{"profileRevisionId": r.ProfileRevisionID().String()}, bson.M{"$set": bson.M{"reviewStatus": "SUBMITTED", "revision": int64(2)}}); e != nil {
					t.Fatal(e)
				}
			}
			competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			done := make(chan error, 1)
			replacement := profileReplacement(t, r.DisplayName().String())
			go func() {
				_, e := NewApplicationProfileRevisionRepository(competing.Database(db.Name())).ReplaceDraft(ctx, app.ID(), r.ProfileRevisionID(), app.AdminID(), 1, replacement, time.Now())
				done <- e
			}()
			awaitMongoCommand(t, ctx, started, "profile edit protection")
			if e = session.CommitTransaction(ctx); e != nil {
				t.Fatal(e)
			}
			select {
			case e := <-done:
				if !errors.Is(e, want) {
					t.Fatalf("want %v got %v", want, e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			raw, e := db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), bson.M{"profileRevisionId": r.ProfileRevisionID().String()}).Raw()
			if e != nil {
				t.Fatal(e)
			}
			if raw.Lookup("displayName").StringValue() != r.DisplayName().String() {
				t.Fatal("late editor changed frozen content")
			}
			if mode == "admin transfer" {
				got, e := repo.ReplaceDraft(t.Context(), app.ID(), r.ProfileRevisionID(), "new-admin", 1, profileReplacement(t, "new"), time.Now())
				if e != nil || got.CreatedBy() != "admin" || got.UpdatedBy() != "new-admin" {
					t.Fatal("transferred audit", e)
				}
			}
		})
	}
	t.Run("BR-PRF-013 rollback pointer corruption and overflow", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "admin", "profile_atomic")
		repo := NewApplicationProfileRevisionRepository(db)
		r, e := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
		if e != nil {
			t.Fatal(e)
		}
		filter := bson.M{"profileRevisionId": r.ProfileRevisionID().String()}
		appBefore := readTesterLinkApplication(t, db, app.ID())
		rawBefore, e := db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), filter).Raw()
		if e != nil {
			t.Fatal(e)
		}
		if e = db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: applicationProfileRevisionsCollectionName}, {Key: "validator", Value: bson.M{"displayName": bson.M{"$ne": "forbidden"}}}}).Err(); e != nil {
			t.Fatal(e)
		}
		if _, e = repo.ReplaceDraft(t.Context(), app.ID(), r.ProfileRevisionID(), app.AdminID(), 1, profileReplacement(t, "forbidden"), time.Now()); e == nil {
			t.Fatal("expected storage failure")
		}
		rawAfter, _ := db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), filter).Raw()
		if !reflect.DeepEqual(rawBefore, rawAfter) || !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("partial update")
		}
		if _, e = db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": app.ID().String()}, bson.M{"$set": bson.M{"workingProfileRevisionId": nil}}); e != nil {
			t.Fatal(e)
		}
		if _, e = repo.ReplaceDraft(t.Context(), app.ID(), r.ProfileRevisionID(), app.AdminID(), 1, profileReplacement(t, r.DisplayName().String()), time.Now()); !errors.Is(e, pp.ErrApplicationProfileStateInconsistent) {
			t.Fatal("noop pointer", e)
		}
		if _, e = db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": app.ID().String()}, bson.M{"$set": bson.M{"workingProfileRevisionId": r.ProfileRevisionID().String()}}); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), filter, bson.M{"$set": bson.M{"revision": int64(math.MaxInt64)}}); e != nil {
			t.Fatal(e)
		}
		if _, e = repo.ReplaceDraft(t.Context(), app.ID(), r.ProfileRevisionID(), app.AdminID(), math.MaxInt64, profileReplacement(t, "changed"), time.Now()); !errors.Is(e, pp.ErrApplicationProfileStateInconsistent) {
			t.Fatal("overflow", e)
		}
		if _, e = repo.ReplaceDraft(t.Context(), app.ID(), r.ProfileRevisionID(), app.AdminID(), math.MaxInt64, profileReplacement(t, r.DisplayName().String()), time.Now()); e != nil {
			t.Fatal("max noop", e)
		}
	})
}
