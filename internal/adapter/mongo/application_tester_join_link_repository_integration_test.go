package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

func TestApplicationTesterJoinLinkRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-TST-002 BR-TST-004 BR-TST-005 BR-TST-007 BR-TST-009 create rotate immutable history and no side effects", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-success")
		repo := NewApplicationTesterJoinLinkRepository(db)
		beforeApp := readTesterLinkApplication(t, db, app.ID())
		candidate, err := repo.LoadCurrent(t.Context(), app.ID(), app.AdminID())
		if err != nil || candidate.ActiveLink() != nil {
			t.Fatalf("initial candidate error=%v", err)
		}
		firstInput := newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())
		first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, firstInput)
		if first.ReplacedJoinLinkID() != nil || first.JoinLink().Status() != testerdomain.JoinLinkStatusActive || !first.JoinLink().CreatedAt().Equal(firstInput.CreatedAt().Truncate(time.Millisecond)) {
			t.Fatal("incorrect first result")
		}
		original := readTesterJoinLinkDocument(t, db, first.JoinLink().JoinLinkID())
		if original.TokenHash.Subtype != 0 || len(original.TokenHash.Data) != 32 || original.RevokedAt != nil || original.RevokedBy != nil || original.RevocationReason != nil || original.ReplacedByJoinLinkID != nil {
			t.Fatal("incorrect active document shape")
		}
		expected := first.JoinLink().JoinLinkID()
		secondInput := newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())
		second := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), &expected, secondInput)
		if second.ReplacedJoinLinkID() == nil || *second.ReplacedJoinLinkID() != expected {
			t.Fatal("rotation lost replaced ID")
		}
		revoked := readTesterJoinLinkDocument(t, db, expected)
		if revoked.Status != "REVOKED" || revoked.RevokedBy == nil || *revoked.RevokedBy != app.AdminID().String() || revoked.RevokedAt == nil || !revoked.RevokedAt.Equal(second.JoinLink().CreatedAt()) || revoked.RevocationReason == nil || *revoked.RevocationReason != "ROTATED" || revoked.ReplacedByJoinLinkID == nil || *revoked.ReplacedByJoinLinkID != second.JoinLink().JoinLinkID().String() {
			t.Fatal("incomplete rotation audit")
		}
		if !reflect.DeepEqual(original.TokenHash, revoked.TokenHash) || original.CreatedBy != revoked.CreatedBy || !original.CreatedAt.Equal(revoked.CreatedAt) {
			t.Fatal("rotation changed immutable creation facts")
		}
		expected = second.JoinLink().JoinLinkID()
		third := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), &expected, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		if !reflect.DeepEqual(revoked, readTesterJoinLinkDocument(t, db, first.JoinLink().JoinLinkID())) {
			t.Fatal("later rotation changed historical audit")
		}
		candidate, err = repo.LoadCurrent(t.Context(), app.ID(), app.AdminID())
		if err != nil || candidate.ActiveLink().JoinLinkID() != third.JoinLink().JoinLinkID() {
			t.Fatalf("wrong active link error=%v", err)
		}
		assertTesterJoinLinkCounts(t, db, app.ID(), 3, 1)
		afterApp := readTesterLinkApplication(t, db, app.ID())
		afterApp.CoordinationRevision = beforeApp.CoordinationRevision
		if !reflect.DeepEqual(beforeApp, afterApp) {
			t.Fatal("link operation changed application business fields")
		}
		for _, collection := range []string{applicationPublicationsCollectionName, applicationPublicationHistoryCollectionName, "application_tester_memberships"} {
			assertCollectionCount(t, db, collection, 0)
		}
		// Stored shape is an allowlist; it cannot contain a raw token or complete URL.
		var raw bson.M
		if err = db.Collection(applicationTesterJoinLinksCollectionName).FindOne(t.Context(), bson.D{{Key: "joinLinkId", Value: third.JoinLink().JoinLinkID().String()}}).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"secret", "rawToken", "token", "joinUrl", "rpcApiMajor", "versionId", "publicationId", "authId"} {
			if _, ok := raw[field]; ok {
				t.Fatalf("unexpected persisted field %s", field)
			}
		}
	})
	t.Run("BR-TST-003 BR-TST-006 simultaneous create and rotate have one winner", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-race")
		repo := NewApplicationTesterJoinLinkRepository(db)
		runTesterJoinLinkRace(t, repo, app.ID(), app.AdminID(), nil, testerport.ErrApplicationTesterJoinLinkAlreadyExists)
		assertTesterJoinLinkCounts(t, db, app.ID(), 1, 1)
		candidate, err := repo.LoadCurrent(t.Context(), app.ID(), app.AdminID())
		if err != nil {
			t.Fatal(err)
		}
		expected := candidate.ActiveLink().JoinLinkID()
		runTesterJoinLinkRace(t, repo, app.ID(), app.AdminID(), &expected, testerport.ErrApplicationTesterJoinLinkChanged)
		assertTesterJoinLinkCounts(t, db, app.ID(), 2, 1)
	})
	t.Run("BR-TST-004 BR-TST-007 duplicate hash rolls back old revocation and fence without leaking hash", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-rollback")
		repo := NewApplicationTesterJoinLinkRepository(db)
		first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		expected := first.JoinLink().JoinLinkID()
		old := readTesterJoinLinkDocument(t, db, expected)
		beforeApp := readTesterLinkApplication(t, db, app.ID())
		replacement, err := testerdomain.NewActiveTesterJoinLink(nextIntegrationTesterJoinLinkID(t), app.ID(), first.JoinLink().TokenHash(), app.AdminID(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		result, err := repo.CreateOrRotate(t.Context(), app.ID(), app.AdminID(), &expected, replacement)
		if result != nil || err == nil {
			t.Fatal("duplicate hash rotation succeeded")
		}
		hash := first.JoinLink().TokenHash().Bytes()
		for _, render := range []string{fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err)} {
			for _, secretValue := range []string{hex.EncodeToString(hash[:]), base64.StdEncoding.EncodeToString(hash[:])} {
				if strings.Contains(render, secretValue) {
					t.Fatal("persistence error disclosed hash")
				}
			}
		}
		if errors.Unwrap(err) != nil {
			t.Fatal("credential-bearing driver error retained as cause")
		}
		if !reflect.DeepEqual(old, readTesterJoinLinkDocument(t, db, expected)) || !reflect.DeepEqual(beforeApp, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("failed insert left partial revocation or fence")
		}
		assertTesterJoinLinkCounts(t, db, app.ID(), 1, 1)
	})
	t.Run("BR-TST-005 BR-TST-007 revoked records cannot be reused or hashes replaced", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-immutable")
		repo := NewApplicationTesterJoinLinkRepository(db)
		first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		firstID := first.JoinLink().JoinLinkID()
		second := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), &firstID, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		secondID := second.JoinLink().JoinLinkID()
		old := readTesterJoinLinkDocument(t, db, firstID)
		active := readTesterJoinLinkDocument(t, db, secondID)
		// A stale expectation cannot select or mutate the revoked row.
		if result, err := repo.CreateOrRotate(t.Context(), app.ID(), app.AdminID(), &firstID, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())); result != nil || !errors.Is(err, testerport.ErrApplicationTesterJoinLinkChanged) {
			t.Fatalf("stale rotation error=%v", err)
		}
		// Even using the current expectation cannot resurrect a historical ID with
		// a new hash: insertion fails its immutable identity constraint atomically.
		hash := sha256.Sum256([]byte("new hash for historical identifier"))
		resurrection, err := testerdomain.NewActiveTesterJoinLink(firstID, app.ID(), testerdomain.NewTesterJoinTokenHash(hash), app.AdminID(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if result, err := repo.CreateOrRotate(t.Context(), app.ID(), app.AdminID(), &secondID, resurrection); result != nil || err == nil {
			t.Fatal("historical identity resurrected")
		}
		if !reflect.DeepEqual(old, readTesterJoinLinkDocument(t, db, firstID)) || !reflect.DeepEqual(active, readTesterJoinLinkDocument(t, db, secondID)) {
			t.Fatal("immutable historical or active facts changed")
		}
		assertTesterJoinLinkCounts(t, db, app.ID(), 2, 1)
	})
	t.Run("BR-TST-007 explicit transaction abort restores active link and application fence", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-abort")
		repo := NewApplicationTesterJoinLinkRepository(db)
		first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		expected := first.JoinLink().JoinLinkID()
		beforeApp := readTesterLinkApplication(t, db, app.ID())
		before := readTesterJoinLinkDocument(t, db, expected)
		session, err := client.StartSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.EndSession(context.Background())
		if err = session.StartTransaction(); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.createOrRotateTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), app.ID(), app.AdminID(), &expected, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())); err != nil {
			t.Fatal(err)
		}
		if err = session.AbortTransaction(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, expected)) || !reflect.DeepEqual(beforeApp, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("transaction abort retained changes")
		}
		assertTesterJoinLinkCounts(t, db, app.ID(), 1, 1)
	})
	t.Run("BR-TST-001 final admin recheck and transaction write fence", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "old-link-admin", "tester-link-transfer")
		repo := NewApplicationTesterJoinLinkRepository(db)
		first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		expected := first.JoinLink().JoinLinkID()
		before := readTesterJoinLinkDocument(t, db, expected)
		if _, err := repo.LoadCurrent(t.Context(), app.ID(), app.AdminID()); err != nil {
			t.Fatal(err)
		}
		session, err := client.StartSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.EndSession(context.Background())
		if err = session.StartTransaction(); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Collection(applicationsCollectionName).UpdateOne(drivermongo.NewSessionContext(t.Context(), session), bson.D{{Key: "id", Value: app.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "new-link-admin"}}}}); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		competitor := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name()))
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		replacement := newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())
		go func() {
			_, err := competitor.CreateOrRotate(ctx, app.ID(), app.AdminID(), &expected, replacement)
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "tester join link administrator fence")
		if err = session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-done:
			if !errors.Is(err, testerport.ErrApplicationAdminRequired) {
				t.Fatalf("old administrator error=%v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, expected)) {
			t.Fatal("old administrator revoked existing link")
		}
		assertTesterJoinLinkCounts(t, db, app.ID(), 1, 1)
		if _, err = repo.LoadCurrent(t.Context(), app.ID(), app.AdminID()); !errors.Is(err, testerport.ErrApplicationAdminRequired) {
			t.Fatalf("old admin can load current: %v", err)
		}
		result := createIntegrationTesterJoinLink(t, repo, app.ID(), "new-link-admin", &expected, newIntegrationTesterJoinLink(t, app.ID(), "new-link-admin"))
		if result.JoinLink().CreatedBy() != "new-link-admin" {
			t.Fatal("new administrator audit incorrect")
		}
	})
	t.Run("BR-TST-005 existence and expectation mismatch fail without writes", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-expected")
		repo := NewApplicationTesterJoinLinkRepository(db)
		unknown := nextIntegrationTesterJoinLinkID(t)
		beforeApp := readTesterLinkApplication(t, db, app.ID())
		if result, err := repo.CreateOrRotate(t.Context(), app.ID(), app.AdminID(), &unknown, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())); result != nil || !errors.Is(err, testerport.ErrApplicationTesterJoinLinkNotFound) {
			t.Fatalf("missing active error=%v", err)
		}
		if !reflect.DeepEqual(beforeApp, readTesterLinkApplication(t, db, app.ID())) {
			t.Fatal("expectation failure left fence")
		}
		first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		for _, test := range []struct {
			expected *testerdomain.ApplicationTesterJoinLinkID
			want     error
		}{{nil, testerport.ErrApplicationTesterJoinLinkAlreadyExists}, {&unknown, testerport.ErrApplicationTesterJoinLinkChanged}} {
			if result, err := repo.CreateOrRotate(t.Context(), app.ID(), app.AdminID(), test.expected, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())); result != nil || !errors.Is(err, test.want) {
				t.Fatalf("mismatch error=%v want=%v", err, test.want)
			}
		}
		foreignApp := shared.ApplicationID(nextIntegrationTesterJoinLinkID(t))
		if candidate, err := repo.LoadCurrent(t.Context(), foreignApp, app.AdminID()); candidate != nil || !errors.Is(err, testerport.ErrApplicationNotFound) {
			t.Fatalf("missing app error=%v", err)
		}
		if result, err := repo.CreateOrRotate(t.Context(), foreignApp, app.AdminID(), nil, newIntegrationTesterJoinLink(t, foreignApp, app.AdminID())); result != nil || !errors.Is(err, testerport.ErrApplicationNotFound) {
			t.Fatalf("missing app mutation error=%v", err)
		}
		current, err := repo.LoadCurrent(t.Context(), app.ID(), app.AdminID())
		if err != nil || current.ActiveLink().JoinLinkID() != first.JoinLink().JoinLinkID() {
			t.Fatal("mismatches changed active link")
		}
		assertTesterJoinLinkCounts(t, db, app.ID(), 1, 1)
	})
}

func nextIntegrationTesterJoinLinkID(t *testing.T) testerdomain.ApplicationTesterJoinLinkID {
	t.Helper()
	return testerdomain.ApplicationTesterJoinLinkID(nextIntegrationApplicationReviewID(t))
}
func newIntegrationTesterJoinLink(t *testing.T, app shared.ApplicationID, by shared.AuthID) *testerdomain.ApplicationTesterJoinLink {
	t.Helper()
	id := nextIntegrationTesterJoinLinkID(t)
	hash := sha256.Sum256([]byte(id.String()))
	link, err := testerdomain.NewActiveTesterJoinLink(id, app, testerdomain.NewTesterJoinTokenHash(hash), by, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return link
}
func createIntegrationTesterJoinLink(t *testing.T, repo *ApplicationTesterJoinLinkRepository, app shared.ApplicationID, by shared.AuthID, expected *testerdomain.ApplicationTesterJoinLinkID, newLink *testerdomain.ApplicationTesterJoinLink) *testerdomain.CreateOrRotateTesterJoinLinkResult {
	t.Helper()
	result, err := repo.CreateOrRotate(t.Context(), app, by, expected, newLink)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func readTesterJoinLinkDocument(t *testing.T, db *drivermongo.Database, id testerdomain.ApplicationTesterJoinLinkID) applicationTesterJoinLinkDocument {
	t.Helper()
	var d applicationTesterJoinLinkDocument
	if err := db.Collection(applicationTesterJoinLinksCollectionName).FindOne(t.Context(), bson.D{{Key: "joinLinkId", Value: id.String()}}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func readTesterLinkApplication(t *testing.T, db *drivermongo.Database, id shared.ApplicationID) applicationDocument {
	t.Helper()
	var d applicationDocument
	if err := db.Collection(applicationsCollectionName).FindOne(t.Context(), bson.D{{Key: "id", Value: id.String()}}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func assertTesterJoinLinkCounts(t *testing.T, db *drivermongo.Database, app shared.ApplicationID, total, active int64) {
	t.Helper()
	collection := db.Collection(applicationTesterJoinLinksCollectionName)
	for _, check := range []struct {
		filter bson.D
		want   int64
	}{{bson.D{{Key: "applicationId", Value: app.String()}}, total}, {bson.D{{Key: "applicationId", Value: app.String()}, {Key: "status", Value: "ACTIVE"}}, active}} {
		count, err := collection.CountDocuments(t.Context(), check.filter)
		if err != nil || count != check.want {
			t.Fatalf("join link count=%d error=%v want=%d", count, err, check.want)
		}
	}
}
func runTesterJoinLinkRace(t *testing.T, repo *ApplicationTesterJoinLinkRepository, app shared.ApplicationID, admin shared.AuthID, expected *testerdomain.ApplicationTesterJoinLinkID, want error) {
	t.Helper()
	start := make(chan struct{})
	done := make(chan error, 2)
	var group sync.WaitGroup
	for i := 0; i < 2; i++ {
		newLink := newIntegrationTesterJoinLink(t, app, admin)
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := repo.CreateOrRotate(t.Context(), app, admin, expected, newLink)
			done <- err
		}()
	}
	close(start)
	group.Wait()
	close(done)
	success, conflict := 0, 0
	for err := range done {
		switch {
		case err == nil:
			success++
		case errors.Is(err, want):
			conflict++
		default:
			t.Errorf("unexpected race error=%v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("race success=%d conflict=%d", success, conflict)
	}
}

func TestApplicationTesterJoinLinkMigrationIntegration(t *testing.T) {
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	app := createVersionTestApplication(t, db, "auth-link-admin", "tester-link-schema")
	repo := NewApplicationTesterJoinLinkRepository(db)
	first := createIntegrationTesterJoinLink(t, repo, app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
	base := testerJoinLinkToDocument(first.JoinLink())
	t.Run("BR-TST-003 BR-TST-004 unique link ID hash and active application", func(t *testing.T) {
		tests := []struct {
			name, index string
			mutate      func(*applicationTesterJoinLinkDocument)
		}{
			{"join link ID", testerJoinLinkIDUniqueIndexName, func(d *applicationTesterJoinLinkDocument) {
				d.ApplicationID = nextIntegrationTesterJoinLinkID(t).String()
				hash := sha256.Sum256([]byte(d.ApplicationID))
				d.TokenHash = bson.Binary{Subtype: 0, Data: hash[:]}
			}},
			{"token hash", testerJoinLinkTokenHashUniqueIndexName, func(d *applicationTesterJoinLinkDocument) {
				d.JoinLinkID = nextIntegrationTesterJoinLinkID(t).String()
				d.ApplicationID = nextIntegrationTesterJoinLinkID(t).String()
			}},
			{"single active", testerJoinLinkActiveUniqueIndexName, func(d *applicationTesterJoinLinkDocument) {
				d.JoinLinkID = nextIntegrationTesterJoinLinkID(t).String()
				hash := sha256.Sum256([]byte(d.JoinLinkID))
				d.TokenHash = bson.Binary{Subtype: 0, Data: hash[:]}
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				d := base
				test.mutate(&d)
				_, err := db.Collection(applicationTesterJoinLinksCollectionName).InsertOne(t.Context(), d)
				if !drivermongo.IsDuplicateKeyError(err) || !strings.Contains(err.Error(), test.index) {
					t.Fatalf("expected duplicate index %s", test.index)
				}
			})
		}
	})
	t.Run("BR-TST-007 stored manual revocation requires no replacement", func(t *testing.T) {
		document := testerJoinLinkToDocument(newIntegrationTesterJoinLink(t, app.ID(), app.AdminID()))
		document.Status = "REVOKED"
		actor := app.AdminID().String()
		at := time.Now()
		reason := "MANUAL"
		document.RevokedBy = &actor
		document.RevokedAt = &at
		document.RevocationReason = &reason
		if _, err := db.Collection(applicationTesterJoinLinksCollectionName).InsertOne(t.Context(), document); err != nil {
			t.Fatal("valid manual revocation shape rejected")
		}
		stored := readTesterJoinLinkDocument(t, db, testerdomain.ApplicationTesterJoinLinkID(document.JoinLinkID))
		if _, err := testerJoinLinkFromDocument(stored); err != nil {
			t.Fatal("valid manual revocation failed mapping")
		}
	})
	t.Run("BR-TST-004 BR-TST-007 strict binary size and lifecycle audit validator", func(t *testing.T) {
		raw, err := bson.Marshal(base)
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			name   string
			mutate func(bson.M)
		}{
			{"missing hash", func(d bson.M) { delete(d, "tokenHash") }},
			{"string hash", func(d bson.M) { d["tokenHash"] = "not-binary" }},
			{"31 byte hash", func(d bson.M) { d["tokenHash"] = bson.Binary{Subtype: 0, Data: make([]byte, 31)} }},
			{"33 byte hash", func(d bson.M) { d["tokenHash"] = bson.Binary{Subtype: 0, Data: make([]byte, 33)} }},
			{"missing audit", func(d bson.M) { delete(d, "createdAt") }},
			{"unknown raw secret", func(d bson.M) { d["secret"] = "must-never-persist" }},
			{"unknown join URL", func(d bson.M) { d["joinUrl"] = "https://app.example/#secret=must-never-persist" }},
			{"active revokedBy", func(d bson.M) { d["revokedBy"] = "admin" }},
			{"active revokedAt", func(d bson.M) { d["revokedAt"] = time.Now() }},
			{"active reason", func(d bson.M) { d["revocationReason"] = "ROTATED" }},
			{"active replacement", func(d bson.M) { d["replacedByJoinLinkId"] = nextIntegrationTesterJoinLinkID(t).String() }},
			{"revoked without audit", func(d bson.M) { d["status"] = "REVOKED" }},
			{"rotated without replacement", func(d bson.M) { setRevokedTesterLinkFixture(d); d["replacedByJoinLinkId"] = nil }},
			{"rotated without actor", func(d bson.M) { setRevokedTesterLinkFixture(d); d["revokedBy"] = nil }},
			{"rotated without time", func(d bson.M) { setRevokedTesterLinkFixture(d); d["revokedAt"] = nil }},
			{"rotated without reason", func(d bson.M) { setRevokedTesterLinkFixture(d); d["revocationReason"] = nil }},
			{"manual with replacement", func(d bson.M) {
				setRevokedTesterLinkFixture(d)
				d["revocationReason"] = "MANUAL"
			}},
			{"self replacement", func(d bson.M) { setRevokedTesterLinkFixture(d); d["replacedByJoinLinkId"] = d["joinLinkId"] }},
		} {
			t.Run(test.name, func(t *testing.T) {
				var d bson.M
				if err = bson.Unmarshal(raw, &d); err != nil {
					t.Fatal(err)
				}
				d["joinLinkId"] = nextIntegrationTesterJoinLinkID(t).String()
				d["applicationId"] = nextIntegrationTesterJoinLinkID(t).String()
				hash := sha256.Sum256([]byte(d["joinLinkId"].(string)))
				d["tokenHash"] = bson.Binary{Subtype: 0, Data: hash[:]}
				test.mutate(d)
				_, err = db.Collection(applicationTesterJoinLinksCollectionName).InsertOne(t.Context(), d)
				var serverError drivermongo.ServerError
				if !errors.As(err, &serverError) || !serverError.HasErrorCode(121) {
					t.Fatal("expected document validation rejection")
				}
			})
		}
	})
}
func setRevokedTesterLinkFixture(d bson.M) {
	d["status"] = "REVOKED"
	d["revokedBy"] = "auth-admin"
	d["revokedAt"] = time.Now()
	d["revocationReason"] = "ROTATED"
	d["replacedByJoinLinkId"] = "01890f49-0000-7000-8000-fffffffffffe"
}

func TestTesterJoinLinkDocumentRedactionAndMapping(t *testing.T) {
	app := shared.ApplicationID("01890f49-0000-7000-8000-000000000001")
	link := newIntegrationTesterJoinLink(t, app, "admin")
	document := testerJoinLinkToDocument(link)
	mapped, err := testerJoinLinkFromDocument(document)
	if err != nil || !reflect.DeepEqual(link, mapped) {
		t.Fatalf("round trip failed: %v", err)
	}
	hash := link.TokenHash().Bytes()
	for _, render := range []string{fmt.Sprintf("%v", document), fmt.Sprintf("%+v", document), fmt.Sprintf("%#v", document)} {
		if strings.Contains(render, hex.EncodeToString(hash[:])) || strings.Contains(render, base64.StdEncoding.EncodeToString(hash[:])) || !strings.Contains(render, "redacted") {
			t.Fatal("document formatting not redacted")
		}
	}
	document.TokenHash.Data = make([]byte, 31)
	if _, err = testerJoinLinkFromDocument(document); err == nil {
		t.Fatal("short hash decoded")
	}
	document.TokenHash.Data = make([]byte, 32)
	document.TokenHash.Subtype = 4
	if _, err = testerJoinLinkFromDocument(document); err == nil {
		t.Fatal("unsupported hash subtype decoded")
	}
}

func TestApplicationTesterJoinLinkRepositoryConstructionDoesNotMutateSchema(t *testing.T) {
	client := integrationClient(t)
	db := integrationDatabase(t, client)
	_ = NewApplicationTesterJoinLinkRepository(db)
	names, err := db.ListCollectionNames(t.Context(), bson.D{})
	if err != nil || len(names) != 0 {
		t.Fatalf("constructor created schema: names=%v error=%v", names, err)
	}
}

var _ testerport.ApplicationTesterJoinLinkRepository = (*ApplicationTesterJoinLinkRepository)(nil)

func TestTesterJoinLinkMigrationIntegration_RedactsDuplicateHashFailure(t *testing.T) {
	client := integrationClient(t)
	db := integrationDatabase(t, client)
	app := shared.ApplicationID(nextIntegrationTesterJoinLinkID(t))
	first := testerJoinLinkToDocument(newIntegrationTesterJoinLink(t, app, "admin"))
	second := first
	second.JoinLinkID = nextIntegrationTesterJoinLinkID(t).String()
	second.ApplicationID = nextIntegrationTesterJoinLinkID(t).String()
	if _, err := db.Collection(applicationTesterJoinLinksCollectionName).InsertMany(t.Context(), []any{first, second}); err != nil {
		t.Fatal("failed to seed duplicate hash migration fixture")
	}
	err := NewMigrator(db).applyApplicationTesterJoinLinkMigration(t.Context())
	if err == nil {
		t.Fatal("unique hash index accepted duplicate existing data")
	}
	var driverError drivermongo.ServerError
	if errors.As(err, &driverError) {
		t.Fatal("migration retained credential-bearing driver cause")
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		rendered := fmt.Sprintf("%v %+v %#v", current, current, current)
		if strings.Contains(rendered, hex.EncodeToString(first.TokenHash.Data)) || strings.Contains(rendered, base64.StdEncoding.EncodeToString(first.TokenHash.Data)) {
			t.Fatal("migration error exposed token hash")
		}
	}
}
