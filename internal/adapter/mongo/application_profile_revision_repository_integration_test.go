package mongo

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

func profileDraftFixture(t *testing.T, app shared.ApplicationID, admin shared.AuthID) *profiledomain.DraftApplicationProfileRevision {
	t.Helper()
	name, err := profiledomain.NewApplicationDisplayName("Cafe\u0301 课表")
	if err != nil {
		t.Fatal(err)
	}
	id := profiledomain.ApplicationProfileRevisionID(nextIntegrationTesterJoinLinkID(t))
	draft, err := profiledomain.NewDraftApplicationProfileRevision(id, app, name, nil, nil, admin, time.Date(2026, 9, 27, 0, 0, 0, 123456789, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return draft
}
func readProfileProjection(t *testing.T, db *drivermongo.Database, app shared.ApplicationID) applicationProfileDocument {
	t.Helper()
	var result applicationProfileDocument
	if err := db.Collection(applicationProfilesCollectionName).FindOne(t.Context(), bson.D{{Key: "applicationId", Value: app.String()}}).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestApplicationProfileRevisionRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-PRF-001 BR-PRF-003 BR-PRF-004 BR-PRF-005 BR-PRF-007 normalized complete draft explicit null audit and sequence", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "profile-admin", "profile_app")
		draft := profileDraftFixture(t, app.ID(), app.AdminID())
		repo := NewApplicationProfileRevisionRepository(db)
		result, err := repo.CreateDraft(t.Context(), app.AdminID(), draft)
		if err != nil {
			t.Fatal(err)
		}
		if result.Sequence() != 1 || result.Revision() != 1 || result.DisplayName().String() != "Café 课表" || result.Description() != nil || result.Icon() != nil || result.ReviewStatus() != profiledomain.ReviewStatusDraft || result.CreatedBy() != app.AdminID() || result.UpdatedBy() != app.AdminID() || !result.CreatedAt().Equal(draft.CreatedAt().Truncate(time.Millisecond)) || !result.UpdatedAt().Equal(result.CreatedAt()) {
			t.Fatal("incorrect draft or audit")
		}
		raw, err := db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), bson.D{{Key: "profileRevisionId", Value: result.ProfileRevisionID().String()}}).Raw()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := profileRevisionFromRaw(raw)
		if err != nil || !reflect.DeepEqual(result, restored) {
			t.Fatal("response and persistence differ")
		}
		for _, field := range []string{"description", "icon"} {
			if raw.Lookup(field).Type != bson.TypeNull {
				t.Fatalf("%s not explicit null", field)
			}
		}
		projection := readProfileProjection(t, db, app.ID())
		if projection.WorkingProfileRevisionID == nil || *projection.WorkingProfileRevisionID != result.ProfileRevisionID().String() || projection.CurrentPublishedProfileRevisionID != nil {
			t.Fatal("incorrect projection")
		}
		if readTesterLinkApplication(t, db, app.ID()).NextProfileRevisionSequence != 2 {
			t.Fatal("counter not advanced")
		}
		before := readTesterLinkApplication(t, db, app.ID())
		if _, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID())); !errors.Is(err, profileport.ErrApplicationProfileWorkRevisionAlreadyExists) {
			t.Fatalf("second error=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("conflict consumed sequence or fence")
		}
	})
	t.Run("BR-PRF-006 submitted occupies slot historical states release and published pointer survives", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "profile-history", "profile_history")
		repo := NewApplicationProfileRevisionRepository(db)
		first, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
		if err != nil {
			t.Fatal(err)
		}
		filter := bson.D{{Key: "profileRevisionId", Value: first.ProfileRevisionID().String()}}
		if _, err := db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), filter, bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: "SUBMITTED"}, {Key: "revision", Value: int64(2)}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID())); !errors.Is(err, profileport.ErrApplicationProfileWorkRevisionAlreadyExists) {
			t.Fatalf("submitted conflict=%v", err)
		}
		for _, status := range []string{"APPROVED", "REJECTED"} {
			if _, err := db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), filter, bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: status}, {Key: "revision", Value: int64(3)}}}}); err != nil {
				t.Fatal(err)
			}
			published := any(nil)
			if status == "APPROVED" {
				published = first.ProfileRevisionID().String()
			}
			if _, err := db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.D{{Key: "applicationId", Value: app.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "workingProfileRevisionId", Value: nil}, {Key: "currentPublishedProfileRevisionId", Value: published}}}}); err != nil {
				t.Fatal(err)
			}
			next, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
			if err != nil {
				t.Fatal(err)
			}
			projection := readProfileProjection(t, db, app.ID())
			if status == "APPROVED" && (projection.CurrentPublishedProfileRevisionID == nil || *projection.CurrentPublishedProfileRevisionID != first.ProfileRevisionID().String()) {
				t.Fatal("published pointer overwritten")
			}
			filter = bson.D{{Key: "profileRevisionId", Value: next.ProfileRevisionID().String()}}
		}
		if readTesterLinkApplication(t, db, app.ID()).NextProfileRevisionSequence != 4 {
			t.Fatal("sequence not monotonic")
		}
	})
	t.Run("BR-PRF-002 missing application wrong admin and committed transfer", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "profile-old", "profile_transfer")
		repo := NewApplicationProfileRevisionRepository(db)
		missing := shared.ApplicationID(nextIntegrationTesterJoinLinkID(t))
		if _, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, missing, app.AdminID())); !errors.Is(err, profileport.ErrApplicationNotFound) {
			t.Fatalf("missing=%v", err)
		}
		if _, err := repo.CreateDraft(t.Context(), "outsider", profileDraftFixture(t, app.ID(), "outsider")); !errors.Is(err, profileport.ErrApplicationAdminRequired) {
			t.Fatalf("admin=%v", err)
		}
		session := startTesterMembershipTransaction(t, client)
		if _, err := db.Collection(applicationsCollectionName).UpdateOne(drivermongo.NewSessionContext(t.Context(), session), bson.D{{Key: "id", Value: app.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "profile-new"}}}}); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		draft := profileDraftFixture(t, app.ID(), app.AdminID())
		go func() {
			_, err := NewApplicationProfileRevisionRepository(competing.Database(db.Name())).CreateDraft(ctx, app.AdminID(), draft)
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "profile creation versus administrator transfer")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, profileport.ErrApplicationAdminRequired) {
				t.Fatalf("old admin=%v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		assertCollectionCount(t, db, applicationProfileRevisionsCollectionName, 0)
		assertCollectionCount(t, db, applicationProfilesCollectionName, 0)
		if readTesterLinkApplication(t, db, app.ID()).NextProfileRevisionSequence != 1 {
			t.Fatal("failed admin consumed sequence")
		}
		if _, err := repo.CreateDraft(t.Context(), "profile-new", profileDraftFixture(t, app.ID(), "profile-new")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("BR-PRF-006 BR-PRF-007 concurrent creation only one commit independent application", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "profile-race", "profile_race")
		repo := NewApplicationProfileRevisionRepository(db)
		drafts := []*profiledomain.DraftApplicationProfileRevision{profileDraftFixture(t, app.ID(), app.AdminID()), profileDraftFixture(t, app.ID(), app.AdminID())}
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for _, draft := range drafts {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := repo.CreateDraft(t.Context(), app.AdminID(), draft)
				results <- err
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		successes, conflicts := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, profileport.ErrApplicationProfileWorkRevisionAlreadyExists) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if successes != 1 || conflicts != 1 || readTesterLinkApplication(t, db, app.ID()).NextProfileRevisionSequence != 2 {
			t.Fatal("creation race violated atomic slot")
		}
		other := createVersionTestApplication(t, db, "profile-race", "profile_other")
		if _, err := repo.CreateDraft(t.Context(), other.AdminID(), profileDraftFixture(t, other.ID(), other.AdminID())); err != nil {
			t.Fatal(err)
		}
		assertCollectionCount(t, db, applicationProfileRevisionsCollectionName, 2)
	})
	t.Run("BR-PRF-007 failed projection insertion rolls back revision counter and fence", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "profile-rollback", "profile_rollback")
		before := readTesterLinkApplication(t, db, app.ID())
		if err := db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: applicationProfilesCollectionName}, {Key: "validator", Value: bson.D{{Key: "forbidden", Value: bson.D{{Key: "$exists", Value: true}}}}}}).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := NewApplicationProfileRevisionRepository(db).CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID())); err == nil || strings.Contains(err.Error(), "Café") {
			t.Fatalf("rollback error=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("rollback changed application")
		}
		assertCollectionCount(t, db, applicationProfileRevisionsCollectionName, 0)
		assertCollectionCount(t, db, applicationProfilesCollectionName, 0)
	})
	t.Run("BR-PRF-007 counter overflow does not partially commit", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "profile-overflow", "profile_overflow")
		if _, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.D{{Key: "id", Value: app.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "nextProfileRevisionSequence", Value: int32(math.MaxInt32)}}}}); err != nil {
			t.Fatal(err)
		}
		before := readTesterLinkApplication(t, db, app.ID())
		if _, err := NewApplicationProfileRevisionRepository(db).CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID())); !errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
			t.Fatalf("overflow=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("overflow changed counter")
		}
		assertCollectionCount(t, db, applicationProfileRevisionsCollectionName, 0)
	})
	t.Run("BR-PRF-006 corrupt pointer missing fields and noncanonical persisted content fail closed", func(t *testing.T) {
		for _, mode := range []string{"dangling", "orphan", "missing nullable", "noncanonical", "wrong type"} {
			t.Run(mode, func(t *testing.T) {
				db := migratedIntegrationDatabase(t, client)
				app := createVersionTestApplication(t, db, "profile-corrupt", "profile_corrupt")
				repo := NewApplicationProfileRevisionRepository(db)
				first, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID()))
				if err != nil {
					t.Fatal(err)
				}
				filter := bson.D{{Key: "profileRevisionId", Value: first.ProfileRevisionID().String()}}
				switch mode {
				case "dangling":
					_, err = db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.D{{Key: "applicationId", Value: app.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "workingProfileRevisionId", Value: nextIntegrationTesterJoinLinkID(t).String()}}}})
				case "orphan":
					_, err = db.Collection(applicationProfilesCollectionName).DeleteMany(t.Context(), bson.D{})
				case "missing nullable":
					_, err = db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), filter, bson.D{{Key: "$unset", Value: bson.D{{Key: "description", Value: ""}}}}, options.UpdateOne().SetBypassDocumentValidation(true))
				case "noncanonical":
					_, err = db.Collection(applicationProfileRevisionsCollectionName).UpdateOne(t.Context(), filter, bson.D{{Key: "$set", Value: bson.D{{Key: "displayName", Value: "Cafe\u0301"}}}})
				case "wrong type":
					_, err = db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.D{{Key: "applicationId", Value: app.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "workingProfileRevisionId", Value: int32(1)}}}}, options.UpdateOne().SetBypassDocumentValidation(true))
				}
				if err != nil {
					t.Fatal(err)
				}
				before := readTesterLinkApplication(t, db, app.ID())
				if _, err := repo.CreateDraft(t.Context(), app.AdminID(), profileDraftFixture(t, app.ID(), app.AdminID())); !errors.Is(err, profileport.ErrApplicationProfileStateInconsistent) {
					t.Fatalf("corruption=%v", err)
				}
				if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, app.ID())) {
					t.Fatal("corrupt state auto-repaired")
				}
			})
		}
	})
}
