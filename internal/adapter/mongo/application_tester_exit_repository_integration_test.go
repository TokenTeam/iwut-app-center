package mongo

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
	tp "iwut-app-center/internal/tester/port"
)

func TestUCAPP040BRTST044047RetainedStatesSelfExitIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, life := range []string{"ACTIVE", "CLOSING", "CLOSED"} {
		for _, availability := range []string{"AVAILABLE", "SUSPENDED"} {
			for _, archive := range []string{"UNARCHIVED", "ARCHIVED"} {
				t.Run(life+availability+archive, func(t *testing.T) {
					db, link := testerMembershipFixture(t, client, "tester-exit-retained")
					repo := NewApplicationTesterMembershipRepository(db)
					member := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
					_, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": link.ApplicationID().String()}, bson.M{"$set": bson.M{"lifecycleStatus": life, "platformAvailabilityStatus": availability, "archiveStatus": archive}})
					if err != nil {
						t.Fatal(err)
					}
					// No Profile or Test publication exists; a revoked source remains legitimate.
					_, err = db.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(t.Context(), bson.M{"joinLinkId": link.JoinLinkID().String()}, bson.M{"$set": bson.M{"status": "REVOKED", "revokedBy": link.CreatedBy().String(), "revokedAt": time.Now(), "revocationReason": "MANUAL"}})
					if err != nil {
						t.Fatal(err)
					}
					beforeApp := readTesterLinkApplication(t, db, link.ApplicationID())
					beforeLink := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
					candidate, err := repo.LoadExitCandidate(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID())
					if err != nil || candidate.Status() != td.MembershipStatusActive {
						t.Fatalf("candidate error=%v", err)
					}
					at := time.Now()
					result, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), at)
					if err != nil || !result.Left() || *result.Membership().RemovedBy() != member.TesterAuthID() || !result.Membership().RemovedAt().Equal(at.Truncate(time.Millisecond)) {
						t.Fatalf("exit error=%v", err)
					}
					again, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Time{})
					if err != nil || again.Left() || !reflect.DeepEqual(again.Membership(), result.Membership()) {
						t.Fatalf("no-op error=%v", err)
					}
					afterApp := readTesterLinkApplication(t, db, link.ApplicationID())
					afterApp.CoordinationRevision = beforeApp.CoordinationRevision
					if !reflect.DeepEqual(beforeApp, afterApp) || !reflect.DeepEqual(beforeLink, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
						t.Fatal("exit changed gate or link")
					}
					assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 0)
				})
			}
		}
	}
}
func TestUCAPP040BRTST045046049RejoinAndHidingIntegration(t *testing.T) {
	client := integrationClient(t)
	db, link := testerMembershipFixture(t, client, "tester-exit-episodes")
	repo := NewApplicationTesterMembershipRepository(db)
	member := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
	for _, tc := range []struct {
		app    shared.ApplicationID
		id     td.ApplicationTesterMembershipID
		caller shared.AuthID
	}{
		{link.ApplicationID(), member.MembershipID(), link.CreatedBy()},
		{shared.ApplicationID(nextIntegrationTesterJoinLinkID(t)), member.MembershipID(), member.TesterAuthID()},
		{link.ApplicationID(), td.ApplicationTesterMembershipID(nextIntegrationTesterJoinLinkID(t)), member.TesterAuthID()},
	} {
		before := readTesterLinkApplication(t, db, link.ApplicationID())
		if r, err := repo.Leave(t.Context(), tc.app, tc.id, tc.caller, time.Now()); r != nil || !errors.Is(err, tp.ErrApplicationTesterMembershipNotFound) {
			t.Fatalf("hidden=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("unauthorized fence")
		}
	}
	seedTesterMemberships(t, db, link, 99)
	r, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now())
	if err != nil || !r.Left() {
		t.Fatal(err)
	}
	next := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, member.TesterAuthID())).Membership()
	if next.MembershipID() == member.MembershipID() {
		t.Fatal("episode reused")
	}
	again, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now())
	if err != nil || again.Left() || !reflect.DeepEqual(r.Membership(), again.Membership()) || readTesterMembershipDocument(t, db, next.MembershipID()).Status != "ACTIVE" {
		t.Fatal("old retry affected new episode", err)
	}
	assertTesterMembershipCounts(t, db, link.ApplicationID(), 101, 100)
	// Preserve a prior administrator removal's original attribution.
	removed, err := repo.Remove(t.Context(), link.ApplicationID(), next.MembershipID(), link.CreatedBy(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	again, err = repo.Leave(t.Context(), link.ApplicationID(), next.MembershipID(), member.TesterAuthID(), time.Time{})
	if err != nil || again.Left() || !reflect.DeepEqual(again.Membership(), removed.Membership()) {
		t.Fatal("admin audit changed", err)
	}
	// Physically cleaned episodes stay absent and never get recreated.
	_, err = db.Collection(applicationTesterMembershipsCollectionName).DeleteOne(t.Context(), bson.M{"membershipId": member.MembershipID().String()})
	if err != nil {
		t.Fatal(err)
	}
	if r, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); r != nil || !errors.Is(err, tp.ErrApplicationTesterMembershipNotFound) {
		t.Fatal("cleaned episode resurrected", err)
	}
}
func TestUCAPP040BRTST048SharedWriterFenceIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, writer := range []string{"self-exit", "admin-remove", "join", "transfer", "archive", "suspend", "closure", "cleanup"} {
		t.Run(writer, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "tester-exit-fences")
			repo := NewApplicationTesterMembershipRepository(db)
			member := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
			session := startTesterMembershipTransaction(t, client)
			tx := m.NewSessionContext(t.Context(), session)
			switch writer {
			case "self-exit":
				if _, err := repo.leaveTesterTransaction(tx, link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); err != nil {
					t.Fatal(err)
				}
			case "admin-remove":
				if _, err := repo.removeTesterTransaction(tx, link.ApplicationID(), member.MembershipID(), link.CreatedBy(), time.Now().Truncate(time.Millisecond)); err != nil {
					t.Fatal(err)
				}
			case "join":
				input := newIntegrationTesterMembership(t, link, member.TesterAuthID())
				r, err := repo.joinTesterTransaction(tx, link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
				if err != nil || r.Joined() {
					t.Fatal("pre-exit Join should be idempotent", err)
				}
			default:
				// Existing governance writers and cleanup share this root write fence. Hold
				// that precise conflict boundary until the competing exit reaches it.
				changes := bson.M{}
				switch writer {
				case "transfer":
					changes["adminId"] = "new-admin"
				case "archive":
					changes["archiveStatus"] = "ARCHIVED"
				case "suspend":
					changes["platformAvailabilityStatus"] = "SUSPENDED"
				case "closure":
					changes["lifecycleStatus"] = "CLOSED"
				}
				update := bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}}
				if len(changes) > 0 {
					update["$set"] = changes
				}
				if _, err := db.Collection(applicationsCollectionName).UpdateOne(tx, bson.M{"id": link.ApplicationID().String()}, update); err != nil {
					t.Fatal(err)
				}
				if writer == "cleanup" {
					if _, err := db.Collection(applicationTesterMembershipsCollectionName).DeleteOne(tx, bson.M{"membershipId": member.MembershipID().String()}); err != nil {
						t.Fatal(err)
					}
				}
			}
			competing, started := monitoredMongoClient(t, db.Name(), "update")
			competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			type outcome struct {
				r   *td.LeaveApplicationAsTesterResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				r, err := competitor.Leave(ctx, link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now().Add(time.Minute))
				done <- outcome{r, err}
			}()
			awaitMongoCommand(t, ctx, started, "exit shared fence")
			if err := session.CommitTransaction(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case out := <-done:
				if writer == "cleanup" {
					if out.r != nil || !errors.Is(out.err, tp.ErrApplicationTesterMembershipNotFound) {
						t.Fatal("cleanup race", out.err)
					}
					return
				}
				if out.err != nil {
					t.Fatal(out.err)
				}
				already := writer == "self-exit" || writer == "admin-remove"
				if out.r.Left() == already {
					t.Fatal("wrong transition winner")
				}
				actor := member.TesterAuthID()
				if writer == "admin-remove" {
					actor = link.CreatedBy()
				}
				if *out.r.Membership().RemovedBy() != actor {
					t.Fatal("audit winner changed")
				}
				assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 0)
				if writer == "transfer" {
					if r, err := repo.Leave(ctx, link.ApplicationID(), member.MembershipID(), "new-admin", time.Now()); r != nil || !errors.Is(err, tp.ErrApplicationTesterMembershipNotFound) {
						t.Fatal("admin substituted for tester")
					}
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
func TestUCAPP040BRTST048ExitBeforeJoinAndCapacityCompetitionIntegration(t *testing.T) {
	client := integrationClient(t)
	db, link := testerMembershipFixture(t, client, "tester-exit-before-join")
	repo := NewApplicationTesterMembershipRepository(db)
	member := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
	seedTesterMemberships(t, db, link, 99)
	session := startTesterMembershipTransaction(t, client)
	if _, err := repo.leaveTesterTransaction(m.NewSessionContext(t.Context(), session), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
	competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	input := newIntegrationTesterMembership(t, link, member.TesterAuthID())
	type outcome struct {
		r   *td.JoinApplicationAsTesterResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := competitor.Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
		done <- outcome{r, err}
	}()
	awaitMongoCommand(t, ctx, started, "join waits for exit")
	if err := session.CommitTransaction(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if out.err != nil || !out.r.Joined() || out.r.Membership().MembershipID() == member.MembershipID() || out.r.ActiveTesterCount() != 100 {
			t.Fatal("post-exit rejoin", out.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// The old retry releases no extra capacity to another user.
	if _, err := repo.Leave(ctx, link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	other := newIntegrationTesterMembership(t, link, "capacity-competitor")
	if r, err := repo.Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), other.TesterAuthID(), other, 100); r != nil || !errors.Is(err, tp.ErrApplicationTesterLimitReached) {
		t.Fatal("old retry released extra slot", err)
	}
}
func TestUCAPP040BRTST048051RollbackAndCorruptionIntegration(t *testing.T) {
	client := integrationClient(t)
	kinds := []string{"write-failure", "missing-root", "missing-link", "foreign-link", "missing-audit", "active-audit", "terminal-order", "bad-status", "bad-time", "bad-utf8", "fence-negative", "fence-overflow", "fence-type", "duplicate-bson", "duplicate-id", "duplicate-active", "duplicate-root", "duplicate-link", "101-active"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "tester-exit-corrupt")
			repo := NewApplicationTesterMembershipRepository(db)
			member := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
			collection := applicationTesterMembershipsCollectionName
			filter := bson.M{"membershipId": member.MembershipID().String()}
			updates := bson.M{}
			switch kind {
			case "write-failure":
				if err := db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: collection}, {Key: "validator", Value: bson.M{"status": "ACTIVE"}}}).Err(); err != nil {
					t.Fatal(err)
				}
			case "missing-root":
				if _, err := db.Collection(applicationsCollectionName).DeleteOne(t.Context(), bson.M{"id": link.ApplicationID().String()}); err != nil {
					t.Fatal(err)
				}
			case "missing-link":
				if _, err := db.Collection(applicationTesterJoinLinksCollectionName).DeleteOne(t.Context(), bson.M{"joinLinkId": link.JoinLinkID().String()}); err != nil {
					t.Fatal(err)
				}
			case "foreign-link":
				collection = applicationTesterJoinLinksCollectionName
				filter = bson.M{"joinLinkId": link.JoinLinkID().String()}
				updates["applicationId"] = nextIntegrationTesterJoinLinkID(t)
			case "missing-audit":
				if _, err := db.Collection(collection).UpdateOne(t.Context(), filter, bson.M{"$unset": bson.M{"removedAt": ""}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			case "active-audit":
				updates["removedBy"] = "actor"
			case "terminal-order":
				updates = bson.M{"status": "REMOVED", "removedBy": "actor", "removedAt": member.JoinedAt().Add(-time.Second)}
			case "bad-status":
				updates["status"] = "LEFT"
			case "bad-time":
				updates["joinedAt"] = bson.DateTime(math.MaxInt64)
			case "bad-utf8":
				updates = bson.M{"status": "REMOVED", "removedAt": time.Now(), "removedBy": string([]byte{0xff})}
			case "fence-negative", "fence-overflow", "fence-type":
				collection = applicationsCollectionName
				filter = bson.M{"id": link.ApplicationID().String()}
				updates["coordinationRevision"] = int64(-1)
				if kind == "fence-overflow" {
					updates["coordinationRevision"] = int64(math.MaxInt64)
				}
				if kind == "fence-type" {
					updates["coordinationRevision"] = "1"
				}
			case "duplicate-bson":
				var d bson.D
				if err := db.Collection(collection).FindOne(t.Context(), filter).Decode(&d); err != nil {
					t.Fatal(err)
				}
				d = append(d, bson.E{Key: "removedAt", Value: nil})
				if _, err := db.Collection(collection).ReplaceOne(t.Context(), filter, d, options.Replace().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			case "duplicate-id", "duplicate-active":
				index := testerMembershipIDUniqueIndexName
				if kind == "duplicate-active" {
					index = testerMembershipActiveUniqueIndexName
				}
				if err := db.Collection(collection).Indexes().DropOne(t.Context(), index); err != nil {
					t.Fatal(err)
				}
				d := testerMembershipToDocument(newIntegrationTesterMembership(t, link, "tester"))
				if kind == "duplicate-id" {
					d.MembershipID = member.MembershipID().String()
					d.TesterAuthID = "other"
				}
				if _, err := db.Collection(collection).InsertOne(t.Context(), d); err != nil {
					t.Fatal(err)
				}
			case "duplicate-root", "duplicate-link":
				collection = applicationsCollectionName
				filter = bson.M{"id": link.ApplicationID().String()}
				if kind == "duplicate-link" {
					collection = applicationTesterJoinLinksCollectionName
					filter = bson.M{"joinLinkId": link.JoinLinkID().String()}
				}
				if err := db.Collection(collection).Indexes().DropAll(t.Context()); err != nil {
					t.Fatal(err)
				}
				var d bson.M
				if err := db.Collection(collection).FindOne(t.Context(), filter).Decode(&d); err != nil {
					t.Fatal(err)
				}
				delete(d, "_id")
				if _, err := db.Collection(collection).InsertOne(t.Context(), d); err != nil {
					t.Fatal(err)
				}
			case "101-active":
				seedTesterMemberships(t, db, link, 100)
			}
			if len(updates) > 0 {
				if _, err := db.Collection(collection).UpdateOne(t.Context(), filter, bson.M{"$set": updates}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}
			// Compare raw facts so even malformed source bytes remain unchanged on failure.
			before, _ := db.Collection(applicationTesterMembershipsCollectionName).FindOne(t.Context(), bson.M{"membershipId": member.MembershipID().String()}).Raw()
			rootBefore, _ := db.Collection(applicationsCollectionName).FindOne(t.Context(), bson.M{"id": link.ApplicationID().String()}).Raw()
			want := tp.ErrApplicationTesterStateInconsistent
			if kind == "write-failure" {
				want = tp.ErrApplicationTesterExitUnavailable
			}
			if result, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); result != nil || !errors.Is(err, want) {
				t.Fatalf("%s err=%v", kind, err)
			}
			after, _ := db.Collection(applicationTesterMembershipsCollectionName).FindOne(t.Context(), bson.M{"membershipId": member.MembershipID().String()}).Raw()
			rootAfter, _ := db.Collection(applicationsCollectionName).FindOne(t.Context(), bson.M{"id": link.ApplicationID().String()}).Raw()
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(rootBefore, rootAfter) {
				t.Fatal("failed exit committed partial mutation")
			}
			// Other callers still see 404 even if the actual owner's sources are corrupt.
			if r, err := repo.Leave(t.Context(), link.ApplicationID(), member.MembershipID(), "other-caller", time.Now()); r != nil || !errors.Is(err, tp.ErrApplicationTesterMembershipNotFound) {
				t.Fatal("corruption replaced existence hiding", err)
			}
		})
	}
}
