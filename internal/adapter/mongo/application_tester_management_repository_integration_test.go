package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
	od "iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
	tp "iwut-app-center/internal/tester/port"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

func testerManagementRepository(t *testing.T, db *drivermongo.Database) *ApplicationTesterManagementRepository {
	t.Helper()
	key, err := DeriveTesterManagementCursorKey([]byte("integration-key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewApplicationTesterManagementRepository(db, key)
}
func TestUCAPP037_BR_TST_037_038_039_040_043_ManagementIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("authoritative zero and hundred capacity independent of current link", func(t *testing.T) {
		for _, count := range []int{0, 100} {
			for _, activeLink := range []bool{false, true} {
				t.Run(fmt.Sprintf("count%d-link%t", count, activeLink), func(t *testing.T) {
					db, link := testerMembershipFixture(t, client, "management-capacity")
					if count > 0 {
						seedTesterMemberships(t, db, link, count)
					}
					if !activeLink {
						if _, err := NewApplicationTesterJoinLinkRepository(db).Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now()); err != nil {
							t.Fatal(err)
						}
					}
					repo := testerManagementRepository(t, db)
					before := readTesterLinkApplication(t, db, link.ApplicationID())
					result, err := repo.GetManagement(t.Context(), link.CreatedBy(), link.ApplicationID())
					if err != nil || result.ActiveTesterCount != int32(count) || result.TesterLimit != 100 || (result.ActiveJoinLink != nil) != activeLink {
						t.Fatalf("capacity=%v err=%v", result, err)
					}
					if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
						t.Fatal("query changed application")
					}
				})
			}
		}
	})
	t.Run("episode history removed source rotated replacement later revoked and cleanup", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "management-history")
		repo := testerManagementRepository(t, db)
		members := NewApplicationTesterMembershipRepository(db)
		old := joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		if _, err := members.Remove(t.Context(), link.ApplicationID(), old.MembershipID(), link.CreatedBy(), time.Now()); err != nil {
			t.Fatal(err)
		}
		current := joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		expected := link.JoinLinkID()
		replacement := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), link.ApplicationID(), link.CreatedBy(), &expected, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())).JoinLink()
		if _, err := NewApplicationTesterJoinLinkRepository(db).Revoke(t.Context(), link.ApplicationID(), replacement.JoinLinkID(), link.CreatedBy(), time.Now()); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			status td.MembershipQueryStatus
			count  int
		}{{"ACTIVE", 1}, {"REMOVED", 1}, {"ALL", 2}} {
			p, err := repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), tc.status, 20, "")
			if err != nil || len(p.Items) != tc.count {
				t.Fatal("membership page", err)
			}
			if tc.status == "ACTIVE" && p.Items[0].MembershipID != current.MembershipID() {
				t.Fatal("wrong episode")
			}
			if tc.status == "REMOVED" && p.Items[0].MembershipID != old.MembershipID() {
				t.Fatal("wrong old episode")
			}
		}
		for _, tc := range []struct {
			status td.JoinLinkQueryStatus
			count  int
		}{{"ACTIVE", 0}, {"REVOKED", 2}, {"ALL", 2}} {
			p, err := repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), tc.status, 20, "")
			if err != nil || len(p.Items) != tc.count {
				t.Fatal("link page", err)
			}
		}
		exit := NewAccountOwnerExitRepository(db)
		prepare := exitPrepare("tester", od.Closure)
		o, err := exit.Prepare(t.Context(), prepare, exitReceipt(t), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = exit.Finish(t.Context(), od.Finish{Prepare: prepare, ReceiptID: o.ReceiptID, Decision: od.Committed}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err = exit.CleanupBatch(t.Context(), prepare.Key, 100); err != nil {
			t.Fatal(err)
		}
		p, err := repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
		if err != nil || len(p.Items) != 0 {
			t.Fatal("cleaned history recreated", err)
		}
	})
	t.Run("membership time ties and pageSize changes", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "management-ties")
		repo := testerManagementRepository(t, db)
		at := time.Now().UTC().Truncate(time.Millisecond)
		ids := []td.ApplicationTesterMembershipID{}
		for i := 0; i < 4; i++ {
			m, err := td.NewActiveTesterMembership(td.ApplicationTesterMembershipID(nextIntegrationTesterJoinLinkID(t)), link.ApplicationID(), shared.AuthID(fmt.Sprintf("tie-%d", i)), link.JoinLinkID(), at)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, m.MembershipID())
			joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, m)
		}
		p, err := repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 1, "")
		if err != nil || len(p.Items) != 1 || p.NextPageToken == "" || p.Items[0].MembershipID != ids[3] {
			t.Fatal("first page", err)
		}
		q, err := repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 100, p.NextPageToken)
		if err != nil || len(q.Items) != 3 || q.NextPageToken != "" || q.Items[2].MembershipID != ids[0] {
			t.Fatal("next page", err)
		}
		if _, err = repo.ListMemberships(t.Context(), "other", link.ApplicationID(), "ALL", 1, p.NextPageToken); !errors.Is(err, tp.ErrManagementNotFound) {
			t.Fatal("authorization must precede token")
		}
		if _, err = repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ACTIVE", 1, p.NextPageToken); !errors.Is(err, tp.ErrInvalidManagementPageToken) {
			t.Fatal("filter binding")
		}
		if _, err = repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 1, p.NextPageToken); !errors.Is(err, tp.ErrInvalidManagementPageToken) {
			t.Fatal("kind binding")
		}
		if _, err = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": link.ApplicationID().String()}, bson.M{"$inc": bson.M{"ownershipRevision": int64(1)}}); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 1, p.NextPageToken); !errors.Is(err, tp.ErrInvalidManagementPageToken) {
			t.Fatal("ownership binding")
		}
	})
}
func TestUCAPP037_BR_TST_042_CorruptionFailsWholeQueryIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, kind := range []string{"missing-source", "cross-source", "source-bson", "membership-bson", "hidden-removedBy", "active-removal", "removed-no-terminal", "bad-reason", "missing-replacement", "cross-replacement", "self-replacement", "hidden-createdBy", "active-revocation", "over-capacity", "duplicate-active-member", "duplicate-active-link", "lookahead-corruption"} {
		t.Run(kind, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "management-corrupt")
			repo := testerManagementRepository(t, db)
			m := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "tester")).Membership()
			links := db.Collection(applicationTesterJoinLinksCollectionName)
			members := db.Collection(applicationTesterMembershipsCollectionName)
			update := func(c *drivermongo.Collection, filter, fields bson.M) {
				if _, err := c.UpdateOne(t.Context(), filter, bson.M{"$set": fields}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}
			lf := bson.M{"joinLinkId": link.JoinLinkID().String()}
			mf := bson.M{"membershipId": m.MembershipID().String()}
			switch kind {
			case "missing-source":
				if _, err := links.DeleteOne(t.Context(), lf); err != nil {
					t.Fatal(err)
				}
			case "cross-source":
				update(links, lf, bson.M{"applicationId": "01890f47-0000-7000-8000-000000009999"})
			case "source-bson":
				update(links, lf, bson.M{"createdAt": "bad"})
			case "membership-bson":
				update(members, mf, bson.M{"joinedAt": "bad"})
			case "hidden-removedBy":
				update(members, mf, bson.M{"status": "REMOVED", "removedAt": time.Now(), "removedBy": ""})
			case "active-removal":
				update(members, mf, bson.M{"removedAt": time.Now(), "removedBy": "admin"})
			case "removed-no-terminal":
				update(members, mf, bson.M{"status": "REMOVED"})
			case "hidden-createdBy":
				update(links, lf, bson.M{"createdBy": ""})
			case "active-revocation":
				update(links, lf, bson.M{"revokedBy": "admin"})
			case "bad-reason":
				update(links, lf, bson.M{"status": "REVOKED", "revokedAt": time.Now(), "revokedBy": "admin", "revocationReason": "INVALID"})
			case "self-replacement", "missing-replacement", "cross-replacement":
				id := nextIntegrationTesterJoinLinkID(t)
				if kind == "self-replacement" {
					id = link.JoinLinkID()
				}
				if kind == "cross-replacement" {
					other := createVersionTestApplication(t, db, "other-admin", "foreign-link")
					foreign := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), other.ID(), other.AdminID(), nil, newIntegrationTesterJoinLink(t, other.ID(), other.AdminID())).JoinLink()
					id = foreign.JoinLinkID()
				}
				update(links, lf, bson.M{"status": "REVOKED", "revokedAt": time.Now(), "revokedBy": "admin", "revocationReason": "ROTATED", "replacedByJoinLinkId": id.String()})
			case "over-capacity":
				seedTesterMemberships(t, db, link, 100)
			case "duplicate-active-member":
				if err := members.Indexes().DropOne(t.Context(), testerMembershipActiveUniqueIndexName); err != nil {
					t.Fatal(err)
				}
				dup := testerMembershipToDocument(newIntegrationTesterMembership(t, link, "tester"))
				if _, err := members.InsertOne(t.Context(), dup); err != nil {
					t.Fatal(err)
				}
			case "duplicate-active-link":
				if err := links.Indexes().DropOne(t.Context(), testerJoinLinkActiveUniqueIndexName); err != nil {
					t.Fatal(err)
				}
				if _, err := links.InsertOne(t.Context(), testerJoinLinkToDocument(newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy()))); err != nil {
					t.Fatal(err)
				}
			case "lookahead-corruption":
				second := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "other")).Membership()
				update(members, bson.M{"membershipId": second.MembershipID().String()}, bson.M{"joinedAt": m.JoinedAt().Add(-time.Hour), "removedBy": "admin"})
			}
			var result any
			var err error
			if kind == "over-capacity" || kind == "duplicate-active-member" || kind == "duplicate-active-link" {
				result, err = repo.GetManagement(t.Context(), link.CreatedBy(), link.ApplicationID())
			} else if kind == "bad-reason" || kind == "missing-replacement" || kind == "cross-replacement" || kind == "self-replacement" || kind == "hidden-createdBy" || kind == "active-revocation" {
				result, err = repo.ListJoinLinks(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", 20, "")
			} else {
				size := int32(20)
				if kind == "lookahead-corruption" {
					size = 1
				}
				result, err = repo.ListMemberships(t.Context(), link.CreatedBy(), link.ApplicationID(), "ALL", size, "")
			}
			if !errors.Is(err, tp.ErrManagementStateInconsistent) || !reflect.ValueOf(result).IsNil() {
				t.Fatalf("corruption result nonempty or error=%v", err)
			}
		})
	}
}
func TestUCAPP037_BR_TST_037_042_SnapshotConcurrentCommandsIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, kind := range []string{"join", "remove", "rotate", "revoke", "transfer", "close", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			db, link := testerMembershipFixture(t, client, "management-concurrent")
			members := NewApplicationTesterMembershipRepository(db)
			m := joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
			var transferID ad.ApplicationAdminTransferID
			var prepare od.Prepare
			var receipt string
			now := time.Now().UTC().Truncate(time.Millisecond)
			if kind == "transfer" {
				transferID = ad.ApplicationAdminTransferID(nextIntegrationTesterJoinLinkID(t))
				if _, err := NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 10).Initiate(t.Context(), link.ApplicationID(), link.CreatedBy(), "new-admin", 1, transferID, now); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "cleanup" {
				prepare = exitPrepare("tester", od.Closure)
				exit := NewAccountOwnerExitRepository(db)
				o, err := exit.Prepare(t.Context(), prepare, exitReceipt(t), now)
				if err != nil {
					t.Fatal(err)
				}
				receipt = o.ReceiptID
				if _, err = exit.Finish(t.Context(), od.Finish{Prepare: prepare, ReceiptID: receipt, Decision: od.Committed}, now); err != nil {
					t.Fatal(err)
				}
			}
			readReady := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var once sync.Once
			var mu sync.Mutex
			commands := []string{}
			snapshotCount := 0
			monitored, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
				if e.DatabaseName != db.Name() {
					return
				}
				mu.Lock()
				commands = append(commands, e.CommandName)
				if v, ok := e.Command.Lookup("startTransaction").BooleanOK(); ok && v {
					snapshotCount++
					if e.Command.Lookup("readConcern").Document().Lookup("level").StringValue() != "snapshot" {
						t.Error("not a snapshot")
					}
				}
				mu.Unlock()
			}, Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
				if e.DatabaseName == db.Name() && e.CommandName == "find" {
					once.Do(func() { close(readReady); <-release })
				}
			}}))
			if err != nil {
				t.Fatal(err)
			}
			defer monitored.Disconnect(context.Background())
			repo := testerManagementRepository(t, monitored.Database(db.Name()))
			done := make(chan struct{})
			var result *td.TesterManagement
			var queryErr error
			go func() {
				result, queryErr = repo.GetManagement(t.Context(), link.CreatedBy(), link.ApplicationID())
				close(done)
			}()
			<-readReady
			switch kind {
			case "join":
				joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, "new-tester"))
			case "remove":
				_, err = members.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), now)
			case "rotate":
				id := link.JoinLinkID()
				createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), link.ApplicationID(), link.CreatedBy(), &id, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy()))
			case "revoke":
				_, err = NewApplicationTesterJoinLinkRepository(db).Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), now)
			case "transfer":
				_, err = NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 10).Accept(t.Context(), transferID, "new-admin", ad.ConfidentialCredentialKeep, now.Add(time.Minute))
			case "close":
				proof := ad.ApplicationCloseProof{Subject: link.CreatedBy(), ApplicationID: link.ApplicationID(), JTI: nextIntegrationTesterJoinLinkID(t).String(), AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
				_, err = NewApplicationClosureRepository(db).Start(t.Context(), link.ApplicationID(), link.CreatedBy(), 1, 1, proof, ad.ApplicationClosureID(nextIntegrationTesterJoinLinkID(t)), now)
			case "cleanup":
				err = NewAccountOwnerExitRepository(db).CleanupBatch(t.Context(), prepare.Key, 100)
			}
			releaseOnce.Do(func() { close(release) })
			<-done
			if err != nil {
				t.Fatal("concurrent command", err)
			}
			if queryErr != nil || result.ActiveTesterCount != 1 || result.ActiveJoinLink == nil || result.ActiveJoinLink.JoinLinkID != link.JoinLinkID() {
				t.Fatalf("mixed snapshot result or query error=%v", queryErr)
			}
			mu.Lock()
			defer mu.Unlock()
			if snapshotCount != 1 {
				t.Fatalf("snapshot count=%d", snapshotCount)
			}
			for _, name := range commands {
				switch name {
				case "find", "getMore", "killCursors", "commitTransaction", "abortTransaction":
				default:
					t.Fatal("query write command", name)
				}
			}
			currentRepo := testerManagementRepository(t, db)
			caller := link.CreatedBy()
			if kind == "transfer" {
				if _, err = currentRepo.GetManagement(t.Context(), caller, link.ApplicationID()); !errors.Is(err, tp.ErrManagementNotFound) {
					t.Fatal("old admin retained read")
				}
				caller = "new-admin"
			}
			after, err := currentRepo.GetManagement(t.Context(), caller, link.ApplicationID())
			if err != nil {
				t.Fatal("subsequent snapshot", err)
			}
			if kind == "remove" || kind == "cleanup" {
				if after.ActiveTesterCount != 0 {
					t.Fatal("membership change invisible")
				}
			}
			if kind == "join" && after.ActiveTesterCount != 2 {
				t.Fatal("join invisible")
			}
			if (kind == "revoke" || kind == "close" || kind == "transfer") && after.ActiveJoinLink != nil {
				t.Fatal("revocation invisible")
			}
			if kind == "rotate" && after.ActiveJoinLink.JoinLinkID == link.JoinLinkID() {
				t.Fatal("rotation invisible")
			}
			page, pageErr := currentRepo.ListJoinLinks(t.Context(), caller, link.ApplicationID(), "ALL", 20, "")
			if pageErr != nil || len(page.Items) == 0 {
				t.Fatal("history after command", pageErr)
			}
			if kind == "transfer" && (page.Items[0].RevocationReason == nil || *page.Items[0].RevocationReason != td.RevocationReasonAdminTransfer) {
				t.Fatal("transfer reason projection")
			}
		})
	}
}
