package mongo

import (
	"context"
	"errors"
	"fmt"
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
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

func TestApplicationTesterRevocationRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-TST-031 BR-TST-032 BR-TST-035 BR-TST-036 manual audit idempotency and independent memberships", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-audit")
		memberships := NewApplicationTesterMembershipRepository(db)
		active := joinIntegrationTesterMembership(t, memberships, link, newIntegrationTesterMembership(t, link, "active-tester")).Membership()
		removed := joinIntegrationTesterMembership(t, memberships, link, newIntegrationTesterMembership(t, link, "removed-tester")).Membership()
		if _, err := memberships.Remove(t.Context(), link.ApplicationID(), removed.MembershipID(), link.CreatedBy(), time.Now()); err != nil {
			t.Fatal(err)
		}
		activeBefore := readTesterMembershipDocument(t, db, active.MembershipID())
		removedBefore := readTesterMembershipDocument(t, db, removed.MembershipID())
		appBefore := readTesterLinkApplication(t, db, link.ApplicationID())
		// Monitor every revocation command: checking only resulting rows would miss an
		// accidental membership/count read, which this capability must not perform.
		var mu sync.Mutex
		var forbidden []string
		monitored, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
			if e.DatabaseName != db.Name() {
				return
			}
			for _, name := range []string{applicationTesterMembershipsCollectionName, applicationPublicationsCollectionName, applicationVersionsCollectionName, applicationReviewsCollectionName} {
				if strings.Contains(e.Command.String(), name) {
					mu.Lock()
					forbidden = append(forbidden, e.CommandName)
					mu.Unlock()
				}
			}
		}}))
		if err != nil {
			t.Fatal(err)
		}
		defer monitored.Disconnect(context.Background())
		repo := NewApplicationTesterJoinLinkRepository(monitored.Database(db.Name()))
		candidate, err := repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy())
		if err != nil || !reflect.DeepEqual(candidate.JoinLink(), link) {
			t.Fatalf("candidate=%v error=%v", candidate, err)
		}
		at := time.Now().UTC()
		result, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), at)
		if err != nil || !result.Revoked() {
			t.Fatalf("revoke=%v error=%v", result, err)
		}
		revoked := result.JoinLink()
		if revoked.Status() != testerdomain.JoinLinkStatusRevoked || *revoked.RevocationReason() != testerdomain.RevocationReasonManual || *revoked.RevokedBy() != link.CreatedBy() || !revoked.RevokedAt().Equal(at.Truncate(time.Millisecond)) || revoked.ReplacedByJoinLinkID() != nil || revoked.TokenHash() != link.TokenHash() || revoked.CreatedBy() != link.CreatedBy() || revoked.CreatedAt() != link.CreatedAt() {
			t.Fatal("revocation changed immutable facts or has incorrect audit")
		}
		stored := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
		if !reflect.DeepEqual(stored, testerJoinLinkToDocument(revoked)) {
			t.Fatal("response audit differs from persistence")
		}
		candidate, err = repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy())
		if err != nil || !reflect.DeepEqual(candidate.JoinLink(), revoked) {
			t.Fatalf("revoked candidate=%v error=%v", candidate, err)
		}
		again, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Time{})
		if err != nil || again.Revoked() || !reflect.DeepEqual(again.JoinLink(), revoked) || !reflect.DeepEqual(stored, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
			t.Fatalf("idempotent revoke=%v error=%v", again, err)
		}
		mu.Lock()
		unexpected := append([]string{}, forbidden...)
		mu.Unlock()
		if len(unexpected) != 0 {
			t.Fatalf("revocation accessed out-of-scope collection: %v", unexpected)
		}
		appAfter := readTesterLinkApplication(t, db, link.ApplicationID())
		appAfter.CoordinationRevision = appBefore.CoordinationRevision
		if !reflect.DeepEqual(appBefore, appAfter) || !reflect.DeepEqual(activeBefore, readTesterMembershipDocument(t, db, active.MembershipID())) || !reflect.DeepEqual(removedBefore, readTesterMembershipDocument(t, db, removed.MembershipID())) {
			t.Fatal("revocation changed Application business fields or Membership history")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 2, 1)
		assertTesterJoinLinkCounts(t, db, link.ApplicationID(), 1, 0)
		replacement := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), link.ApplicationID(), link.CreatedBy(), nil, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())).JoinLink()
		if replacement.Status() != testerdomain.JoinLinkStatusActive || !reflect.DeepEqual(stored, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
			t.Fatal("new link creation altered revoked history")
		}
		// Existing members stay eligible; even the prior removed user may independently
		// rejoin through a new link, proving no blacklist is created by revocation.
		joined := joinIntegrationTesterMembership(t, memberships, replacement, newIntegrationTesterMembership(t, replacement, removed.TesterAuthID()))
		if !joined.Joined() || joined.ActiveTesterCount() != 2 {
			t.Fatal("revocation prevented legitimate subsequent join")
		}
		assertTesterJoinLinkCounts(t, db, link.ApplicationID(), 2, 1)
	})
	t.Run("BR-TST-036 malformed BSON audit types use the stable invariant error", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-bson-corrupt")
		repo := NewApplicationTesterJoinLinkRepository(db)
		filter := bson.D{{Key: "joinLinkId", Value: link.JoinLinkID().String()}}
		if _, err := db.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(t.Context(), filter, bson.D{{Key: "$set", Value: bson.D{{Key: "revokedAt", Value: "private-invalid-audit"}}}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal(err)
		}
		appBefore := readTesterLinkApplication(t, db, link.ApplicationID())
		if candidate, err := repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy()); candidate != nil || !errors.Is(err, testerport.ErrApplicationTesterJoinLinkStateInconsistent) {
			t.Fatalf("malformed BSON candidate error=%v", err)
		}
		if result, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now()); result != nil || !errors.Is(err, testerport.ErrApplicationTesterJoinLinkStateInconsistent) {
			t.Fatalf("malformed BSON revoke error=%v", err)
		}
		if !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("invariant failure retained a fence write")
		}
	})
	t.Run("BR-TST-029 BR-TST-030 missing application unknown or foreign link and nonadmin", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-errors")
		repo := NewApplicationTesterJoinLinkRepository(db)
		other := createVersionTestApplication(t, db, link.CreatedBy().String(), "foreign-link-app")
		for _, tc := range []struct {
			name  string
			app   shared.ApplicationID
			id    testerdomain.ApplicationTesterJoinLinkID
			admin shared.AuthID
			want  error
		}{
			{"missing application", shared.ApplicationID(nextIntegrationTesterJoinLinkID(t)), link.JoinLinkID(), link.CreatedBy(), testerport.ErrApplicationTesterJoinLinkNotFound},
			{"missing link", link.ApplicationID(), nextIntegrationTesterJoinLinkID(t), link.CreatedBy(), testerport.ErrApplicationTesterJoinLinkNotFound},
			{"foreign link", other.ID(), link.JoinLinkID(), link.CreatedBy(), testerport.ErrApplicationTesterJoinLinkNotFound},
			{"nonadmin", link.ApplicationID(), link.JoinLinkID(), "other-admin", testerport.ErrApplicationAdminRequired},
		} {
			t.Run(tc.name, func(t *testing.T) {
				before := readTesterLinkApplication(t, db, link.ApplicationID())
				otherBefore := readTesterLinkApplication(t, db, other.ID())
				original := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
				if result, err := repo.LoadRevocationCandidate(t.Context(), tc.app, tc.id, tc.admin); result != nil || !errors.Is(err, tc.want) {
					t.Fatalf("load error=%v", err)
				}
				if result, err := repo.Revoke(t.Context(), tc.app, tc.id, tc.admin, time.Now()); result != nil || !errors.Is(err, tc.want) {
					t.Fatalf("revoke error=%v", err)
				}
				if !reflect.DeepEqual(before, readTesterLinkApplication(t, db, link.ApplicationID())) || !reflect.DeepEqual(otherBefore, readTesterLinkApplication(t, db, other.ID())) || !reflect.DeepEqual(original, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
					t.Fatal("rejected revocation retained changes")
				}
			})
		}
	})
	t.Run("BR-TST-030 BR-TST-032 late old ROTATED revoke preserves replacement", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-old")
		repo := NewApplicationTesterJoinLinkRepository(db)
		id := link.JoinLinkID()
		replacement := createIntegrationTesterJoinLink(t, repo, link.ApplicationID(), link.CreatedBy(), &id, newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())).JoinLink()
		before := readTesterJoinLinkDocument(t, db, id)
		replacementBefore := readTesterJoinLinkDocument(t, db, replacement.JoinLinkID())
		candidate, err := repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), id, link.CreatedBy())
		if err != nil || *candidate.JoinLink().RevocationReason() != testerdomain.RevocationReasonRotated {
			t.Fatalf("old candidate error=%v", err)
		}
		result, err := repo.Revoke(t.Context(), link.ApplicationID(), id, link.CreatedBy(), time.Time{})
		if err != nil || result.Revoked() || *result.JoinLink().ReplacedByJoinLinkID() != replacement.JoinLinkID() {
			t.Fatalf("old revoke=%v error=%v", result, err)
		}
		if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, id)) || !reflect.DeepEqual(replacementBefore, readTesterJoinLinkDocument(t, db, replacement.JoinLinkID())) {
			t.Fatal("late revoke changed link history or replacement")
		}
	})
	t.Run("BR-TST-031 BR-TST-034 concurrent revocations write exactly one audit", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-contention")
		repo := NewApplicationTesterJoinLinkRepository(db)
		session := startTesterMembershipTransaction(t, client)
		first, err := repo.revokeTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond))
		if err != nil || !first.Revoked() {
			t.Fatalf("first=%v error=%v", first, err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan testerRevocationOutcome, 1)
		go func() {
			result, err := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name())).Revoke(ctx, link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now().Add(time.Hour))
			done <- testerRevocationOutcome{result, err}
		}()
		awaitMongoCommand(t, ctx, started, "concurrent revocation fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		out := awaitTesterRevocation(t, ctx, done)
		if out.err != nil || out.result.Revoked() || !reflect.DeepEqual(out.result.JoinLink(), first.JoinLink()) {
			t.Fatalf("second revoke=%v error=%v", out.result, out.err)
		}
		assertTesterJoinLinkCounts(t, db, link.ApplicationID(), 1, 0)
	})
	t.Run("BR-TST-033 BR-TST-034 revocation commits before waiting join rejects stale candidate", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-before-join")
		memberships := NewApplicationTesterMembershipRepository(db)
		if _, err := memberships.ResolveJoinCandidate(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes()); err != nil {
			t.Fatal(err)
		}
		session := startTesterMembershipTransaction(t, client)
		if _, err := NewApplicationTesterJoinLinkRepository(db).revokeTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		input := newIntegrationTesterMembership(t, link, "joiner")
		go func() {
			_, err := NewApplicationTesterMembershipRepository(competing.Database(db.Name())).Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "revoke versus join fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		if err := awaitTesterRevocationError(t, ctx, done); !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
			t.Fatalf("revoked link join error=%v", err)
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 0, 0)
	})
	t.Run("BR-TST-033 BR-TST-034 join commits before waiting revoke preserves episode", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-join-before-revoke")
		session := startTesterMembershipTransaction(t, client)
		input := newIntegrationTesterMembership(t, link, "joiner")
		joined, err := NewApplicationTesterMembershipRepository(db).joinTesterTransaction(drivermongo.NewSessionContext(t.Context(), session), link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100)
		if err != nil || !joined.Joined() {
			t.Fatalf("join=%v error=%v", joined, err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan testerRevocationOutcome, 1)
		go func() {
			result, err := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name())).Revoke(ctx, link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now())
			done <- testerRevocationOutcome{result, err}
		}()
		awaitMongoCommand(t, ctx, started, "join versus revoke fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		out := awaitTesterRevocation(t, ctx, done)
		if out.err != nil || !out.result.Revoked() {
			t.Fatalf("revoke=%v error=%v", out.result, out.err)
		}
		expectedMembership := testerMembershipToDocument(joined.Membership())
		expectedMembership.JoinedAt = expectedMembership.JoinedAt.UTC().Truncate(time.Millisecond)
		if !reflect.DeepEqual(readTesterMembershipDocument(t, db, input.MembershipID()), expectedMembership) {
			t.Fatal("revocation modified joined episode")
		}
		assertTesterMembershipCounts(t, db, link.ApplicationID(), 1, 1)
		if result, err := NewApplicationTesterMembershipRepository(db).Join(ctx, link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100); result != nil || !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
			t.Fatalf("existing member bypassed revoked link: %v", err)
		}
	})
	t.Run("BR-TST-030 BR-TST-034 rotation commits before waiting revoke returns old terminal state", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-rotate-before-revoke")
		repo := NewApplicationTesterJoinLinkRepository(db)
		if _, err := repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy()); err != nil {
			t.Fatal(err)
		}
		session := startTesterMembershipTransaction(t, client)
		expected := link.JoinLinkID()
		replacement := newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())
		if _, err := repo.createOrRotateTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), link.CreatedBy(), &expected, replacement); err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan testerRevocationOutcome, 1)
		go func() {
			result, err := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name())).Revoke(ctx, link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now())
			done <- testerRevocationOutcome{result, err}
		}()
		awaitMongoCommand(t, ctx, started, "rotation versus revoke fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		out := awaitTesterRevocation(t, ctx, done)
		if out.err != nil || out.result.Revoked() || *out.result.JoinLink().RevocationReason() != testerdomain.RevocationReasonRotated || *out.result.JoinLink().ReplacedByJoinLinkID() != replacement.JoinLinkID() {
			t.Fatalf("revoke after rotation=%v error=%v", out.result, out.err)
		}
		if readTesterJoinLinkDocument(t, db, replacement.JoinLinkID()).Status != "ACTIVE" {
			t.Fatal("revoke followed replacement")
		}
		assertTesterJoinLinkCounts(t, db, link.ApplicationID(), 2, 1)
	})
	t.Run("BR-TST-034 revocation commits before waiting rotation rejects expected link", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-before-rotate")
		repo := NewApplicationTesterJoinLinkRepository(db)
		session := startTesterMembershipTransaction(t, client)
		first, err := repo.revokeTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		done := make(chan error, 1)
		expected := link.JoinLinkID()
		replacement := newIntegrationTesterJoinLink(t, link.ApplicationID(), link.CreatedBy())
		go func() {
			_, err := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name())).CreateOrRotate(ctx, link.ApplicationID(), link.CreatedBy(), &expected, replacement)
			done <- err
		}()
		awaitMongoCommand(t, ctx, started, "revoke versus rotation fence")
		if err := session.CommitTransaction(ctx); err != nil {
			t.Fatal(err)
		}
		if err := awaitTesterRevocationError(t, ctx, done); !errors.Is(err, testerport.ErrApplicationTesterJoinLinkNotFound) {
			t.Fatalf("rotation error=%v", err)
		}
		if !reflect.DeepEqual(readTesterJoinLinkDocument(t, db, link.JoinLinkID()), testerJoinLinkToDocument(first.JoinLink())) {
			t.Fatal("rotation changed manual audit")
		}
		assertTesterJoinLinkCounts(t, db, link.ApplicationID(), 1, 0)
	})
	t.Run("BR-TST-029 BR-TST-032 BR-TST-034 admin transfer fences final revoke and revoked-candidate shortcut", func(t *testing.T) {
		for _, revoked := range []bool{false, true} {
			t.Run(fmt.Sprintf("revoked=%t", revoked), func(t *testing.T) {
				db, link := testerMembershipFixture(t, client, "tester-revoke-transfer")
				repo := NewApplicationTesterJoinLinkRepository(db)
				if _, err := repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy()); err != nil {
					t.Fatal(err)
				}
				if revoked {
					if _, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				before := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
				session := startTesterMembershipTransaction(t, client)
				if _, err := db.Collection(applicationsCollectionName).UpdateOne(drivermongo.NewSessionContext(t.Context(), session), bson.D{{Key: "id", Value: link.ApplicationID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "new-admin"}}}}); err != nil {
					t.Fatal(err)
				}
				competing, started := monitoredMongoClient(t, db.Name(), "findAndModify")
				competitor := NewApplicationTesterJoinLinkRepository(competing.Database(db.Name()))
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					if revoked {
						_, err = competitor.LoadRevocationCandidate(ctx, link.ApplicationID(), link.JoinLinkID(), link.CreatedBy())
					} else {
						_, err = competitor.Revoke(ctx, link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now())
					}
					done <- err
				}()
				awaitMongoCommand(t, ctx, started, "admin transfer versus revoke fence")
				if err := session.CommitTransaction(ctx); err != nil {
					t.Fatal(err)
				}
				if err := awaitTesterRevocationError(t, ctx, done); !errors.Is(err, testerport.ErrApplicationAdminRequired) {
					t.Fatalf("old admin error=%v", err)
				}
				if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) {
					t.Fatal("old administrator modified link")
				}
				result, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), "new-admin", time.Now())
				if err != nil || result.Revoked() == revoked {
					t.Fatalf("new admin revoke=%v error=%v", result, err)
				}
				actor := shared.AuthID("new-admin")
				if revoked {
					actor = link.CreatedBy()
				}
				if *result.JoinLink().RevokedBy() != actor {
					t.Fatal("incorrect final admin audit")
				}
			})
		}
	})
	t.Run("BR-TST-031 BR-TST-036 abort and real storage failure roll back revocation and fence", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "tester-revoke-rollback")
		repo := NewApplicationTesterJoinLinkRepository(db)
		before := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
		appBefore := readTesterLinkApplication(t, db, link.ApplicationID())
		session := startTesterMembershipTransaction(t, client)
		if _, err := repo.revokeTesterJoinLinkTransaction(drivermongo.NewSessionContext(t.Context(), session), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now().UTC().Truncate(time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if err := session.AbortTransaction(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) || !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("abort retained revocation")
		}
		if err := db.RunCommand(t.Context(), bson.D{{Key: "collMod", Value: applicationTesterJoinLinksCollectionName}, {Key: "validator", Value: bson.D{{Key: "status", Value: "ACTIVE"}}}}).Err(); err != nil {
			t.Fatal(err)
		}
		result, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now())
		if err == nil || result != nil || !strings.Contains(err.Error(), "document validation") || strings.Contains(err.Error(), link.JoinLinkID().String()) || strings.Contains(err.Error(), "tokenHash") || errors.Unwrap(err) != nil {
			t.Fatalf("validation error leaked or succeeded: %v", err)
		}
		if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) || !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, link.ApplicationID())) {
			t.Fatal("failed update retained revocation or fence")
		}
	})
	t.Run("BR-TST-031 BR-TST-036 malformed persisted states produce stable internal error", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			fields bson.D
		}{
			{"active audit", bson.D{{Key: "revokedBy", Value: "unexpected"}}},
			{"revoked without audit", bson.D{{Key: "status", Value: "REVOKED"}}},
			{"manual with replacement", bson.D{{Key: "status", Value: "REVOKED"}, {Key: "revokedBy", Value: "admin"}, {Key: "revokedAt", Value: time.Now()}, {Key: "revocationReason", Value: "MANUAL"}, {Key: "replacedByJoinLinkId", Value: nextIntegrationTesterJoinLinkID(t).String()}}},
			{"rotated without replacement", bson.D{{Key: "status", Value: "REVOKED"}, {Key: "revokedBy", Value: "admin"}, {Key: "revokedAt", Value: time.Now()}, {Key: "revocationReason", Value: "ROTATED"}}},
			{"invalid hash shape", bson.D{{Key: "tokenHash", Value: bson.Binary{Subtype: 0, Data: []byte("private-hash")}}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				db, link := testerMembershipFixture(t, client, "tester-revoke-corrupt")
				repo := NewApplicationTesterJoinLinkRepository(db)
				if _, err := db.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(t.Context(), bson.D{{Key: "joinLinkId", Value: link.JoinLinkID().String()}}, bson.D{{Key: "$set", Value: tc.fields}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
				before := readTesterJoinLinkDocument(t, db, link.JoinLinkID())
				appBefore := readTesterLinkApplication(t, db, link.ApplicationID())
				if result, err := repo.LoadRevocationCandidate(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy()); result != nil || !errors.Is(err, testerport.ErrApplicationTesterJoinLinkStateInconsistent) {
					t.Fatalf("malformed load=%v", err)
				}
				if result, err := repo.Revoke(t.Context(), link.ApplicationID(), link.JoinLinkID(), link.CreatedBy(), time.Now()); result != nil || !errors.Is(err, testerport.ErrApplicationTesterJoinLinkStateInconsistent) {
					t.Fatalf("malformed revoke=%v", err)
				}
				if !reflect.DeepEqual(before, readTesterJoinLinkDocument(t, db, link.JoinLinkID())) || !reflect.DeepEqual(appBefore, readTesterLinkApplication(t, db, link.ApplicationID())) {
					t.Fatal("invariant error retained writes")
				}
			})
		}
	})
}

type testerRevocationOutcome struct {
	result *testerdomain.RevokeTesterJoinLinkResult
	err    error
}

func awaitTesterRevocation(t *testing.T, ctx context.Context, done <-chan testerRevocationOutcome) testerRevocationOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return testerRevocationOutcome{}
	}
}
func awaitTesterRevocationError(t *testing.T, ctx context.Context, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return ctx.Err()
	}
}
func TestTesterRevocationRepositoryInputAndSafeErrors_BR_TST_029_BR_TST_036(t *testing.T) {
	var nilRepo *ApplicationTesterJoinLinkRepository
	for _, repo := range []*ApplicationTesterJoinLinkRepository{nilRepo, NewApplicationTesterJoinLinkRepository(nil)} {
		if result, err := repo.LoadRevocationCandidate(t.Context(), "", "", ""); result != nil || err == nil {
			t.Fatal("invalid candidate input accepted")
		}
		if result, err := repo.Revoke(t.Context(), "", "", "", time.Time{}); result != nil || err == nil {
			t.Fatal("invalid revocation input accepted")
		}
	}
	for _, sentinel := range []error{testerport.ErrApplicationTesterJoinLinkNotFound, testerport.ErrApplicationAdminRequired, testerport.ErrApplicationTesterJoinLinkStateInconsistent} {
		if !errors.Is(safeTesterJoinLinkError(sentinel), sentinel) {
			t.Fatal("business sentinel lost")
		}
	}
	if !errors.Is(safeTesterJoinLinkError(testerdomain.ErrApplicationTesterJoinLinkStateInconsistent), testerport.ErrApplicationTesterJoinLinkStateInconsistent) {
		t.Fatal("domain consistency sentinel lost")
	}
	for _, input := range []error{drivermongo.CommandError{Code: 121, Message: "private-tokenHash credential"}, drivermongo.WriteException{WriteErrors: drivermongo.WriteErrors{{Code: 11000, Message: "private-tokenHash credential"}}}, fmt.Errorf("private-tokenHash credential")} {
		safe := safeTesterJoinLinkError(input)
		if strings.Contains(fmt.Sprintf("%v %+v %#v", safe, safe, safe), "private-tokenHash") || errors.Unwrap(safe) != nil {
			t.Fatal("raw storage error retained")
		}
	}
}
