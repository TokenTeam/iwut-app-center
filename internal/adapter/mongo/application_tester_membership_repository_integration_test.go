package mongo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	applicationdomain "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

func TestApplicationTesterMembershipRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-TST-010 BR-TST-012 BR-TST-013 BR-TST-015 BR-TST-018 ordinary user joins idempotently then rejoins new episode", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-join-success")
		repo := NewApplicationTesterMembershipRepository(db)
		beforeApp := readTesterLinkApplication(t, db, link.ApplicationID())
		beforeLink := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
		candidate, err := repo.ResolveJoinCandidate(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes())
		if err != nil || candidate.ApplicationID() != link.ApplicationID() {
			t.Fatalf("resolve: %v", err)
		}
		input := newIntegrationTesterMembership(t, link, "ordinary-user")
		joined := joinIntegrationTesterMembership(t, repo, link, input)
		if !joined.Joined() || joined.ActiveTesterCount() != 1 || joined.TesterLimit() != 100 || !joined.Membership().JoinedAt().Equal(input.JoinedAt().Truncate(time.Millisecond)) {
			t.Fatal("incorrect joined result")
		}
		existing := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "ordinary-user"))
		if existing.Joined() || !reflect.DeepEqual(existing.Membership(), joined.Membership()) || existing.ActiveTesterCount() != 1 {
			t.Fatal("idempotency changed episode")
		}
		afterApp := readTesterLinkApplication(t, db, link.ApplicationID())
		afterApp.CoordinationRevision = beforeApp.CoordinationRevision
		if !reflect.DeepEqual(beforeApp, afterApp) || !reflect.DeepEqual(beforeLink, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
			t.Fatal("join changed application business or link state")
		}
		for _, name := range []string{applicationVersionsCollectionName, applicationReviewsCollectionName, applicationPublicationsCollectionName, applicationPublicationHistoryCollectionName} {
			count, err := db.Collection(name).CountDocuments(t.Context(), bson.D{})
			if err != nil || count != 0 {
				t.Fatalf("unexpected %s side effect", name)
			}
		}
		// Seed historical REMOVED state to isolate the UC009 join contract. The
		// removal command has its own transaction and concurrency integration suite.
		actor := "admin"
		removedAt := time.Now().UTC().Truncate(time.Millisecond)
		_, err = db.Collection(applicationTesterMembershipsCollectionName).UpdateOne(t.Context(), bson.D{{Key: "membershipId", Value: joined.Membership().MembershipID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REMOVED"}, {Key: "removedBy", Value: actor}, {Key: "removedAt", Value: removedAt}}}})
		if err != nil {
			t.Fatal(err)
		}
		old := readTesterMembershipDocument(t, db, joined.Membership().MembershipID())
		rejoined := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "ordinary-user"))
		if !rejoined.Joined() || rejoined.Membership().MembershipID() == joined.Membership().MembershipID() || rejoined.ActiveTesterCount() != 1 {
			t.Fatal("rejoin restored old episode")
		}
		if !reflect.DeepEqual(old, readTesterMembershipDocument(t, db, joined.Membership().MembershipID())) {
			t.Fatal("rejoin mutated history")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 2, 1)
	})
	t.Run("BR-TST-011 invalid missing revoked and orphaned links are indistinguishable", func(t *testing.T) {
		for _, kind := range []string{"missing", "wrong hash", "revoked", "missing app", "foreign candidate"} {
			t.Run(kind, func(t *testing.T) {
				db, link := testerMembershipFixture(t, client, "tester-invalid")
				repo := NewApplicationTesterMembershipRepository(db)
				hash := link.TokenHash().Bytes()
				id := link.JoinLinkID()
				membership := newIntegrationTesterMembership(t, link, "ordinary-user")
				switch kind {
				case "missing":
					id = nextIntegrationTesterJoinLinkID(t)
					membership = newIntegrationTesterMembershipWithLinkID(t, link.ApplicationID(), id, "ordinary-user")
				case "wrong hash":
					hash[0] ^= 1
				case "revoked":
					expected := link.JoinLinkID()
					createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), link.ApplicationID(), link.CreatedBy(), &expected, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy()))
				case "missing app":
					if _, err := db.Collection(applicationsCollectionName).DeleteOne(t.Context(), bson.D{{Key: "id", Value: link.ApplicationID().String()}}); err != nil {
						t.Fatal(err)
					}
				case "foreign candidate":
					other := createVersionTestApplication(t, db, "other-admin", "other-app")
					membership = newIntegrationTesterMembershipWithLinkID(t, other.ID(), id, "ordinary-user")
				}
				if kind != "foreign candidate" {
					if result, err := repo.ResolveJoinCandidate(t.Context(), id, hash); result != nil || !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
						t.Fatalf("resolve=%v err=%v", result, err)
					}
				}
				if result, err := repo.Join(t.Context(), id, hash, membership.TesterAuthID(), membership, 100); result != nil || !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
					t.Fatalf("join=%v err=%v", result, err)
				}
				assertTesterMembershipCounts(t, db, link.ApplicationID(), 0, 0)
			})
		}
	})
	t.Run("BR-TST-013 BR-TST-014 full list preserves idempotency and rejects new user", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-full")
		repo := NewApplicationTesterMembershipRepository(db)
		existing := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "existing"))
		seedTesterMemberships(t, db, link, 99)
		before := readTesterLinkApplication(t, db, link.ApplicationID())
		if result, err := repo.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), "new", newIntegrationTesterMembership(t, link, "new"), 100); result != nil || !errors.Is(err, testerport.ErrApplicationTesterLimitReached) {
			t.Fatalf("full error=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("failed join retained fence")
		}
		again := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "existing"))
		if again.Joined() || again.ActiveTesterCount() != 100 || !reflect.DeepEqual(existing.Membership(), again.Membership()) {
			t.Fatal("full-list idempotency failed")
		}
		expected := link.JoinLinkID()
		createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), link.ApplicationID(), link.CreatedBy(), &expected, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy()))
		if result, err := repo.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), "existing", newIntegrationTesterMembership(t, link, "existing"), 100); result != nil || !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
			t.Fatalf("idempotency bypassed revoked link: %v", err)
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 100, 100)
	})
	t.Run("BR-TST-016 BR-TST-017 last slot and same user serialize under actual contention", func(t *testing.T) {
		for _, same := range []bool{false, true} {
			t.Run(fmt.Sprintf("same user %t", same), func(t *testing.T) {
				db, link := testerMembershipFixture(t, client, "tester-contention")
				repo := NewApplicationTesterMembershipRepository(db)
				seedTesterMemberships(t, db, link, 99)
				session := startTesterMembershipTransaction(t, client)
				tx := drivermongo.NewSessionContext(t.Context(), session)
				winner := newIntegrationTesterMembership(t, link, "winner")
				first, err := repo.joinTesterTransaction(tx, link.JoinLinkID(), link.TokenHash().Bytes(), winner.TesterAuthID(), winner, 100)
				if err != nil || !first.Joined() {
					t.Fatalf("first=%v error=%v", first, err)
				}
				user := shared.AuthID("competitor")
				if same {
					user = "winner"
				}
				input := newIntegrationTesterMembership(t, link, user)
				command := "findAndModify"
				if same {
					command = "update"
				} // Same-user joins contend first on the account fence.
				competing, started := monitoredMongoClient(t, db.Name(), command)
				competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				type outcome struct {
					result *testerdomain.JoinApplicationAsTesterResult
					err    error
				}
				done := make(chan outcome, 1)
				go func() {
					result, err := competitor.Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), user, input, 100)
					done <- outcome{result, err}
				}()
				awaitMongoCommand(t, ctx, started, "tester capacity fence")
				if err = session.CommitTransaction(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case out := <-done:
					if same {
						if out.err != nil || out.result.Joined() || out.result.Membership().MembershipID() != winner.MembershipID() || out.result.ActiveTesterCount() != 100 {
							t.Fatalf("same user result=%v err=%v", out.result, out.err)
						}
					} else if out.result != nil || !errors.Is(out.err, testerport.ErrApplicationTesterLimitReached) {
						t.Fatalf("last slot result=%v err=%v", out.result, out.err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				assertTesterMembershipCounts(t, db, link.ApplicationID(), 100, 100)
			})
		}
	})
	t.Run("BR-TST-011 BR-TST-017 rotation commits before waiting join rechecks old link", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-rotate-wins")
		repo := NewApplicationTesterMembershipRepository(db)
		if _, err := repo.ResolveJoinCandidate(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes()); err != nil {
			t.Fatal(err)
		}
		session := startTesterMembershipTransaction(t, client)
		expected := link.JoinLinkID()
		if _, err := NewApplicationTesterJoinLinkRepository(db).createOrRotateTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), link.CreatedBy(), &expected, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		input := newIntegrationTesterMembership(t, link, "joiner")
		go func() {
			_, err := competitor.Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "rotation versus join fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
				t.Fatalf("rotated link join=%v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 0, 0)
	})
	t.Run("BR-TST-011 BR-TST-017 join commits before waiting rotation retains valid episode", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-join-wins")
		repo := NewApplicationTesterMembershipRepository(db)
		session := startTesterMembershipTransaction(t, client)
		input := newIntegrationTesterMembership(t, link, "joiner")
		if _, err := repo.joinTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		competitor := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name()))
		expected := link.JoinLinkID()
		replacement := newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		go func() {
			_, err := competitor.CreateOrRotate(ctx, link.ApplicationID(), link.CreatedBy(), &expected, replacement)
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "join versus rotation fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 1)
		if readTesterMembershipDocument(t, db, input.MembershipID()).JoinedViaJoinLinkID != link.JoinLinkID().String() {
			t.Fatal("rotation rewrote membership source")
		}
	})
	t.Run("BR-TST-017 transaction abort and insert error rollback fence and membership", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-rollback")
		repo := NewApplicationTesterMembershipRepository(db)
		before := readTesterLinkApplication(t, db, link.ApplicationID())
		session := startTesterMembershipTransaction(t, client)
		input := newIntegrationTesterMembership(t, link, "aborted")
		if _, err := repo.joinTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100); err != nil {
			t.Fatal(err)
		}
		if err := session.AbortTransaction(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 0, 0)
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("aborted fence persisted")
		}
		existing := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "existing"))
		duplicate, err := testerdomain.NewActiveTesterMembership(existing.Membership().MembershipID(), link.ApplicationID(), "different-user", link.JoinLinkID(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		before = readTesterLinkApplication(t, db, link.ApplicationID())
		result, err := repo.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), duplicate.TesterAuthID(), duplicate, 100)
		if result != nil || err == nil || errors.Is(err, testerport.ErrApplicationTesterLimitReached) || strings.Contains(err.Error(), existing.Membership().MembershipID().String()) || strings.Contains(err.Error(), "different-user") {
			t.Fatalf("unexpected insert failure=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("insert failure retained fence")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 1)
	})
}

func testerMembershipFixture(t *testing.T, client *drivermongo.Client, name string) (*drivermongo.Database, *testerdomain.ApplicationTesterJoinLink) {
	t.Helper()
	db := migratedIntegrationDatabase(t, client)
	app := createVersionTestApplication(t, db, "tester-admin", name)
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())).JoinLink()
	return db, link
}

func TestApplicationTesterMembershipRepositoryIntegration_UCAPP028_SuspendedApplicationRejectsNewJoin(t *testing.T) {
	db, link := testerMembershipFixture(t, integrationClient(t), "tester-suspended")
	eventID := applicationdomain.ApplicationOperationEventID("0199b33c-d051-7abc-8abc-123456789012")
	if _, err := NewApplicationOperationsRepository(db).Set(t.Context(), link.ApplicationID(), "platform-operator", applicationdomain.PlatformAvailabilitySuspended, 1, 1, "tester incident", eventID, time.Now()); err != nil {
		t.Fatal(err)
	}
	candidate := newIntegrationTesterMembership(t, link, "new-tester")
	result, err := NewApplicationTesterMembershipRepository(db).Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), candidate.TesterAuthID(), candidate, 100)
	if result != nil || !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
		t.Fatalf("Join()=(%#v,%v)", result, err)
	}
	if count, countErr := db.Collection(applicationTesterMembershipsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": link.ApplicationID().String()}); countErr != nil || count != 0 {
		t.Fatalf("memberships=%d error=%v", count, countErr)
	}
}
func newIntegrationTesterMembership(t *testing.T, link *testerdomain.ApplicationTesterJoinLink, user shared.AuthID) *testerdomain.ApplicationTesterMembership {
	t.Helper()
	return newIntegrationTesterMembershipWithLinkID(t, link.ApplicationID(), link.JoinLinkID(), user)
}
func newIntegrationTesterMembershipWithLinkID(t *testing.T, app shared.ApplicationID, linkID testerdomain.ApplicationTesterJoinLinkID, user shared.AuthID) *testerdomain.ApplicationTesterMembership {
	t.Helper()
	m, err := testerdomain.NewActiveTesterMembership(testerdomain.ApplicationTesterMembershipID(nextIntegrationTesterJoinLinkID(t)), app, user, linkID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func joinIntegrationTesterMembership(t *testing.T, repo *ApplicationTesterMembershipRepository, link *testerdomain.ApplicationTesterJoinLink, m *testerdomain.ApplicationTesterMembership) *testerdomain.JoinApplicationAsTesterResult {
	t.Helper()
	r, err := repo.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), m.TesterAuthID(), m, 100)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func seedTesterMemberships(t *testing.T, db *drivermongo.Database, link *testerdomain.ApplicationTesterJoinLink, count int) {
	t.Helper()
	documents := make([]any, count)
	for i := range count {
		documents[i] = testerMembershipToDocument(newIntegrationTesterMembership(t, link, shared.AuthID(fmt.Sprintf("seed-%d", i))))
	}
	if _, err := db.Collection(applicationTesterMembershipsCollectionName).InsertMany(t.Context(), documents); err != nil {
		t.Fatal(err)
	}
}
func assertTesterMembershipCounts(t *testing.T, db *drivermongo.Database, app shared.ApplicationID, total, active int64) {
	t.Helper()
	for _, check := range []struct {
		filter bson.D
		want   int64
	}{{bson.D{{Key: "applicationId", Value: app.String()}}, total}, {bson.D{{Key: "applicationId", Value: app.String()}, {Key: "status", Value: "ACTIVE"}}, active}} {
		count, err := db.Collection(applicationTesterMembershipsCollectionName).CountDocuments(t.Context(), check.filter)
		if err != nil || count != check.want {
			t.Fatalf("membership count=%d want=%d err=%v", count, check.want, err)
		}
	}
}
func readTesterMembershipDocument(t *testing.T, db *drivermongo.Database, id testerdomain.ApplicationTesterMembershipID) applicationTesterMembershipDocument {
	t.Helper()
	var d applicationTesterMembershipDocument
	if err := db.Collection(applicationTesterMembershipsCollectionName).FindOne(t.Context(), bson.D{{Key: "membershipId", Value: id.String()}}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func startTesterMembershipTransaction(t *testing.T, client *drivermongo.Client) *drivermongo.Session {
	t.Helper()
	s, err := client.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.EndSession(context.Background()) })
	if err = s.StartTransaction(options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority())); err != nil {
		t.Fatal(err)
	}
	return s
}
