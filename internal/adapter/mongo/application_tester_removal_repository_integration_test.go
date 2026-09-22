package mongo

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

func TestApplicationTesterRemovalRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-TST-020 BR-TST-022 BR-TST-023 BR-TST-026 BR-TST-028 admin removes own episode with immutable audit and unchanged links", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-self")
		repo := NewApplicationTesterMembershipRepository(db)
		joined := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, link.CreatedBy()))
		m := joined.Membership()
		appBefore := readTesterLinkApplication(t, db, link.ApplicationID())
		linkBefore := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
		candidate, err := repo.LoadRemovalCandidate(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy())
		if err != nil || candidate.Membership().Status() != testerdomain.MembershipStatusActive || candidate.ActiveTesterCount() != 1 || !candidate.ActiveJoinLinkExists() {
			t.Fatalf("candidate=%v error=%v", candidate, err)
		}
		at := time.Now().UTC()
		result, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), at)
		if err != nil || !result.Removed() || result.ActiveTesterCount() != 0 || result.TesterLimit() != 100 || !result.ActiveJoinLinkExists() {
			t.Fatalf("remove=%v error=%v", result, err)
		}
		removed := result.Membership()
		if removed.Status() != testerdomain.MembershipStatusRemoved || *removed.RemovedBy() != link.CreatedBy() || !removed.RemovedAt().Equal(at.Truncate(time.Millisecond)) || removed.JoinedAt() != m.JoinedAt() || removed.JoinedViaJoinLinkID() != m.JoinedViaJoinLinkID() || removed.TesterAuthID() != m.TesterAuthID() {
			t.Fatal("removed audit or joined facts are incorrect")
		}
		stored := readTesterMembershipDocument(t, db, m.MembershipID())
		if !reflect.DeepEqual(stored, testerMembershipToDocument(removed)) {
			t.Fatal("response does not match persisted audit")
		}
		candidate, err = repo.LoadRemovalCandidate(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy())
		if err != nil || !reflect.DeepEqual(candidate.Membership(), removed) || candidate.ActiveTesterCount() != 0 || !candidate.ActiveJoinLinkExists() {
			t.Fatalf("removed candidate=%v error=%v", candidate, err)
		}
		again, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Time{})
		if err != nil || again.Removed() || again.ActiveTesterCount() != 0 || !reflect.DeepEqual(again.Membership(), removed) || !reflect.DeepEqual(stored, readTesterMembershipDocument(t, db, m.MembershipID())) {
			t.Fatalf("idempotent remove=%v error=%v", again, err)
		}
		appAfter := readTesterLinkApplication(t, db, link.ApplicationID())
		appAfter.CoordinationRevision = appBefore.CoordinationRevision
		if !reflect.DeepEqual(appBefore, appAfter) || !reflect.DeepEqual(linkBefore, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
			t.Fatal("removal changed application business fields or link")
		}
		for _, name := range []string{applicationVersionsCollectionName, applicationReviewsCollectionName, applicationPublicationsCollectionName, applicationPublicationHistoryCollectionName} {
			count, err := db.Collection(name).CountDocuments(t.Context(), bson.D{})
			if err != nil || count != 0 {
				t.Fatalf("unexpected %s side effect", name)
			}
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 0)
	})
	t.Run("BR-TST-020 BR-TST-021 missing application unknown or foreign episode and nonadmin fail without changes", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-errors")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		other := createVersionTestApplication(t, db, link.CreatedBy().String(), "foreign-application")
		for _, tc := range []struct {
			name       string
			app        shared.ApplicationID
			membership testerdomain.ApplicationTesterMembershipID
			admin      shared.AuthID
			want       error
		}{
			{"missing application", shared.ApplicationID(nextIntegrationTesterJoinLinkID(t)), m.MembershipID(), link.CreatedBy(), testerport.ErrApplicationTesterMembershipNotFound},
			{"missing episode", link.ApplicationID(), testerdomain.ApplicationTesterMembershipID(nextIntegrationTesterJoinLinkID(t)), link.CreatedBy(), testerport.ErrApplicationTesterMembershipNotFound},
			{"foreign episode", other.ID(), m.MembershipID(), link.CreatedBy(), testerport.ErrApplicationTesterMembershipNotFound},
			{"not admin", link.ApplicationID(), m.MembershipID(), "other-admin", testerport.ErrApplicationAdminRequired},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := readTesterLinkApplication(t, db, link.ApplicationID())
				foreignBefore := readTesterLinkApplication(t, db, other.ID())
				if result, err := repo.LoadRemovalCandidate(t.Context(), tc.app, tc.membership, tc.admin); result != nil || !errors.Is(err, tc.want) {
					t.Fatalf("load error=%v", err)
				}
				if result, err := repo.Remove(t.Context(), tc.app, tc.membership, tc.admin, time.Now()); result != nil || !errors.Is(err, tc.want) {
					t.Fatalf("remove error=%v", err)
				}
				if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) || !reflect.DeepEqual(foreignBefore, readTesterLinkApplication(t, db, other.ID())) {
					t.Fatal("failed removal retained fence")
				}
				assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 1)
			})
		}
	})
	t.Run("BR-TST-024 BR-TST-025 old episode no-op preserves rejoin and released full capacity is reusable", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-rejoin")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		seedTesterMemberships(t, db, link, 99)
		removed, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now())
		if err != nil || !removed.Removed() || removed.ActiveTesterCount() != 99 {
			t.Fatalf("full removal=%v error=%v", removed, err)
		}
		rejoined := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, m.TesterAuthID()))
		if !rejoined.Joined() || rejoined.ActiveTesterCount() != 100 || rejoined.Membership().MembershipID() == m.MembershipID() {
			t.Fatal("freed capacity did not create a new episode")
		}
		old := readTesterMembershipDocument(t, db, m.MembershipID())
		current := readTesterMembershipDocument(t, db, rejoined.Membership().MembershipID())
		candidate, err := repo.LoadRemovalCandidate(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy())
		if err != nil || candidate.Membership().Status() != testerdomain.MembershipStatusRemoved || candidate.ActiveTesterCount() != 100 {
			t.Fatalf("old candidate=%v error=%v", candidate, err)
		}
		again, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now().Add(time.Hour))
		if err != nil || again.Removed() || again.ActiveTesterCount() != 100 || !reflect.DeepEqual(again.Membership(), removed.Membership()) {
			t.Fatalf("old removal=%v error=%v", again, err)
		}
		if !reflect.DeepEqual(old, readTesterMembershipDocument(t, db, m.MembershipID())) || !reflect.DeepEqual(current, readTesterMembershipDocument(t, db, rejoined.Membership().MembershipID())) {
			t.Fatal("old episode removal changed history or new episode")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 101, 100)
	})
	t.Run("BR-TST-026 no active link does not prevent removal or rewrite revoked history", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-no-link")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		_, err := db.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(t.Context(), bson.D{{Key: "joinLinkId", Value: link.JoinLinkID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REVOKED"}, {Key: "revokedBy", Value: link.CreatedBy().String()}, {Key: "revokedAt", Value: time.Now()}, {Key: "revocationReason", Value: "MANUAL"}}}})
		if err != nil {
			t.Fatal(err)
		}
		before := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
		candidate, err := repo.LoadRemovalCandidate(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy())
		if err != nil || candidate.ActiveJoinLinkExists() {
			t.Fatalf("no-link candidate=%v error=%v", candidate, err)
		}
		result, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now())
		if err != nil || !result.Removed() || result.ActiveJoinLinkExists() || !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
			t.Fatalf("no-link removal=%v error=%v", result, err)
		}
	})
	t.Run("BR-TST-022 BR-TST-027 concurrent removals perform one transition and preserve first audit", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-contention")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		session := startTesterMembershipTransaction(t, client)
		first, err := repo.removeTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond))
		if err != nil || !first.Removed() {
			t.Fatalf("first=%v error=%v", first, err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan testerRemovalOutcome, 1)
		go func() {
			result, err := competitor.Remove(ctx, link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now().Add(time.Hour))
			done <- testerRemovalOutcome{result, err}
		}()
		awaitMongoCommand(t, ctx, started, "concurrent removal fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		out := awaitTesterRemoval(t, ctx, done)
		if out.err != nil || out.result.Removed() || out.result.ActiveTesterCount() != 0 || !reflect.DeepEqual(out.result.Membership(), first.Membership()) {
			t.Fatalf("second removal=%v error=%v", out.result, out.err)
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 0)
	})
	t.Run("BR-TST-027 removal commits before waiting rejoin creates a new episode", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-removal-first")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		session := startTesterMembershipTransaction(t, client)
		if _, err := repo.removeTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		type outcome struct {
			result *testerdomain.JoinApplicationAsTesterResult
			err    error
		}
		done := make(chan outcome, 1)
		input := newIntegrationTesterMembership(t, link, m.TesterAuthID())
		go func() {
			result, err := competitor.Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
			done <- outcome{result, err}
		}()
		awaitMongoCommand(t, ctx, started, "removal versus rejoin fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case out := <-done:
			if out.err != nil || !out.result.Joined() || out.result.Membership().MembershipID() == m.MembershipID() || out.result.ActiveTesterCount() != 1 {
				t.Fatalf("rejoin=%v error=%v", out.result, out.err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 2, 1)
	})
	t.Run("BR-TST-027 join commits before waiting removal removes that exact active episode", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-rejoin-first")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		session := startTesterMembershipTransaction(t, client)
		input := newIntegrationTesterMembership(t, link, m.TesterAuthID())
		joined, err := repo.joinTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
		if err != nil || joined.Joined() || joined.Membership().MembershipID() != m.MembershipID() {
			t.Fatalf("join first=%v error=%v", joined, err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan testerRemovalOutcome, 1)
		go func() {
			result, err := competitor.Remove(ctx, link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now())
			done <- testerRemovalOutcome{result, err}
		}()
		awaitMongoCommand(t, ctx, started, "join versus removal fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		out := awaitTesterRemoval(t, ctx, done)
		if out.err != nil || !out.result.Removed() || out.result.ActiveTesterCount() != 0 || out.result.Membership().MembershipID() != m.MembershipID() {
			t.Fatalf("removal after join=%v error=%v", out.result, out.err)
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 0)
	})
	t.Run("BR-TST-020 BR-TST-022 BR-TST-027 admin transfer invalidates both final removal and removed-candidate shortcut", func(t *testing.T) {
		for _, removed := range []bool{false, true} {
			name := "active final removal"
			if removed {
				name = "removed candidate"
			}
			t.Run(name, func(t *testing.T) {
				db, link := testerMembershipFixture(t, client, "tester-removal-transfer")
				repo := NewApplicationTesterMembershipRepository(db)
				m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
				if _, err := repo.LoadRemovalCandidate(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy()); err != nil {
					t.Fatal(err)
				}
				if removed {
					if _, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				before := readTesterMembershipDocument(t, db, m.MembershipID())
				session := startTesterMembershipTransaction(t, client)
				if _, err := db.Collection(applicationsCollectionName).UpdateOne(drivermongo.NewSessionContext(t.Context(), session), bson.D{{Key: "id", Value: link.ApplicationID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "new-admin"}}}}); err != nil {
					t.Fatal(err)
				}
				competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
				competitor := NewApplicationTesterMembershipRepository(competing.Database(db.Name()))
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					if removed {
						_, err = competitor.LoadRemovalCandidate(ctx, link.ApplicationID(), m.MembershipID(), link.CreatedBy())
					} else {
						_, err = competitor.Remove(ctx, link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now())
					}
					done <- err
				}()
				awaitMongoCommand(t, ctx, started, "administrator transfer versus removal fence")
				if err := session.CommitTransaction(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if !errors.Is(err, testerport.ErrApplicationAdminRequired) {
						t.Fatalf("old administrator error=%v", err)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if !reflect.DeepEqual(before, readTesterMembershipDocument(t, db, m.MembershipID())) {
					t.Fatal("old admin changed episode")
				}
				result, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), "new-admin", time.Now())
				if err != nil || result.Removed() == removed {
					t.Fatalf("new admin removal=%v error=%v", result, err)
				}
				wantActor := shared.AuthID("new-admin")
				if removed {
					wantActor = link.CreatedBy()
				}
				if *result.Membership().RemovedBy() != wantActor {
					t.Fatal("removal audit changed or used stale admin")
				}
			})
		}
	})
	t.Run("BR-TST-024 transaction abort and update validation failure roll back removal and fence", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-rollback")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "private-tester")).Membership()
		before := readTesterMembershipDocument(t, db, m.MembershipID())
		appBefore := readTesterLinkApplication(t, db, link.ApplicationID())
		session := startTesterMembershipTransaction(t, client)
		if _, err := repo.removeTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if err := session.AbortTransaction(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, readTesterMembershipDocument(t, db, m.MembershipID())) || !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("aborted removal persisted")
		}
		// A stricter validator in this isolated test database forces a real update
		// failure after the transaction has already acquired the Application fence.
		if err := db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: applicationTesterMembershipsCollectionName}, {Key: "validator", Value: bson.D{{Key: "status", Value: "ACTIVE"}}}}).Err(); err != nil {
			t.Fatal(err)
		}
		result, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now())
		if err == nil || result != nil || !strings.Contains(err.Error(), "document validation") || strings.Contains(err.Error(), "private-tester") || errors.Unwrap(err) != nil {
			t.Fatalf("update failure leaked or succeeded: %v", err)
		}
		if !reflect.DeepEqual(before, readTesterMembershipDocument(t, db, m.MembershipID())) || !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("failed removal retained changes")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 1)
	})
	t.Run("BR-TST-024 capacity corruption reports stable internal invariant error without writes", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-remove-invariant")
		repo := NewApplicationTesterMembershipRepository(db)
		m := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "tester")).Membership()
		seedTesterMemberships(t, db, link, 100)
		before := readTesterLinkApplication(t, db, link.ApplicationID())
		if result, err := repo.LoadRemovalCandidate(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy()); result != nil || !errors.Is(err, testerport.ErrApplicationTesterStateInconsistent) {
			t.Fatalf("inconsistent load=%v", err)
		}
		if result, err := repo.Remove(t.Context(), link.ApplicationID(), m.MembershipID(), link.CreatedBy(), time.Now()); result != nil || !errors.Is(err, testerport.ErrApplicationTesterStateInconsistent) {
			t.Fatalf("inconsistent remove=%v", err)
		}
		if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("invariant failure retained fence")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 101, 101)
	})
}

type testerRemovalOutcome struct {
	result *testerdomain.RemoveApplicationTesterResult
	err    error
}

func awaitTesterRemoval(t *testing.T, ctx context.Context, done <-chan testerRemovalOutcome) testerRemovalOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return testerRemovalOutcome{}
	}
}

func TestTesterRemovalRepositoryInputAndSafeErrors_BR_TST_024_BR_TST_028(t *testing.T) {
	var nilRepo *ApplicationTesterMembershipRepository
	for _, repo := range []*ApplicationTesterMembershipRepository{nilRepo, NewApplicationTesterMembershipRepository(nil)} {
		if result, err := repo.LoadRemovalCandidate(t.Context(), "", "", ""); err == nil || result != nil {
			t.Fatal("invalid candidate input accepted")
		}
		if result, err := repo.Remove(t.Context(), "", "", "", time.Time{}); err == nil || result != nil {
			t.Fatal("invalid removal input accepted")
		}
	}
	for _, sentinel := range []error{testerport.ErrApplicationTesterMembershipNotFound, testerport.ErrApplicationAdminRequired, testerport.ErrApplicationTesterStateInconsistent} {
		if !errors.Is(safeTesterMembershipError(sentinel), sentinel) {
			t.Fatal("business sentinel lost")
		}
	}
	if !errors.Is(safeTesterMembershipError(testerdomain.ErrApplicationTesterStateInconsistent), testerport.ErrApplicationTesterStateInconsistent) {
		t.Fatal("domain consistency sentinel lost")
	}
}
