package mongo

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	ad "iwut-app-center/internal/application/domain"
	d "iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
	"sync"
	"testing"
	"time"
)

func exitPrepare(auth string, purpose d.Purpose) d.Prepare {
	return d.Prepare{Key: d.Key{AuthID: auth, OperationID: uuid.NewString()}, Purpose: purpose}
}
func exitReceipt(t *testing.T) string {
	t.Helper()
	id, e := uuid.NewV7()
	if e != nil {
		t.Fatal(e)
	}
	return id.String()
}
func TestBR_APP_008_009_OwnerExitMonotonicity(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	r := NewAccountOwnerExitRepository(db)
	now := time.Now().UTC()
	p := exitPrepare("owner", d.Withdrawal)
	receipt := exitReceipt(t)
	out, e := r.Prepare(t.Context(), p, receipt, now)
	if e != nil || out.ReceiptID != receipt {
		t.Fatalf("%v %v", out, e)
	}
	again, e := r.Prepare(t.Context(), p, exitReceipt(t), now)
	if e != nil || again != out {
		t.Fatalf("retry %v %v", again, e)
	}
	if e = requireOwnerWritable(t.Context(), db, p.AuthID, false); !errors.Is(e, shared.ErrAccountExitBlocked) {
		t.Fatal(e)
	}
	if e = requireOwnerWritable(t.Context(), db, p.AuthID, true); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Finish(t.Context(), d.Finish{Prepare: p, ReceiptID: exitReceipt(t), Decision: d.Committed}, now); !errors.Is(e, d.ErrConflict) {
		t.Fatal(e)
	}
	s, e := r.Finish(t.Context(), d.Finish{Prepare: p, ReceiptID: receipt, Decision: d.Committed}, now)
	if e != nil || s.Decision != d.Committed {
		t.Fatalf("%v %v", s, e)
	}
	if _, e = r.Finish(t.Context(), d.Finish{Prepare: p, ReceiptID: receipt, Decision: d.Cancelled}, now); !errors.Is(e, d.ErrConflict) {
		t.Fatal(e)
	}
	q := exitPrepare("owner", d.Closure)
	out, e = r.Prepare(t.Context(), q, exitReceipt(t), now)
	if e != nil {
		t.Fatal(e)
	}
	if e = requireOwnerWritable(t.Context(), db, q.AuthID, true); !errors.Is(e, shared.ErrAccountExitBlocked) {
		t.Fatal(e)
	}
	if _, e = r.Finish(t.Context(), d.Finish{Prepare: q, ReceiptID: "", Decision: d.Cancelled}, now); e != nil {
		t.Fatal(e)
	}
	if e = requireOwnerWritable(t.Context(), db, p.AuthID, false); !errors.Is(e, shared.ErrAccountExitBlocked) {
		t.Fatal("cancel unsealed prior withdrawal", e)
	}
	late := exitPrepare("cancel-first", d.Closure)
	if _, e = r.Finish(t.Context(), d.Finish{Prepare: late, Decision: d.Cancelled}, now); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Prepare(t.Context(), late, exitReceipt(t), now); !errors.Is(e, d.ErrConflict) {
		t.Fatal("late prepare", e)
	}
}
func TestBR_APP_008_OwnershipCreateRace(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	r := NewAccountOwnerExitRepository(db)
	repo := NewApplicationRepository(db)
	// Pre-establish fence so simultaneous transactions compete on its write.
	if _, e := lockOwnerFence(t.Context(), db, "racer"); e != nil {
		t.Fatal(e)
	}
	name, _ := ad.NewApplicationName("racer_app")
	id := shared.ApplicationID(exitReceipt(t))
	app, e := ad.NewApplication(id, name, "racer", time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	var created error
	var prepared d.Preparation
	var prepareErr error
	p := exitPrepare("racer", d.Closure)
	wg.Add(2)
	go func() { defer wg.Done(); <-start; created = repo.CreateWithinQuota(context.Background(), app, 10) }()
	go func() {
		defer wg.Done()
		<-start
		prepared, prepareErr = r.Prepare(context.Background(), p, exitReceipt(t), time.Now().UTC())
	}()
	close(start)
	wg.Wait()
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	if created == nil && !prepared.Blocked {
		t.Fatal("creation and zero-owner prepare both succeeded")
	}
	if !prepared.Blocked && !errors.Is(created, shared.ErrAccountExitBlocked) {
		t.Fatal(created)
	}
}
func TestBR_APP_008_OwnedApplicationBlocksIdempotently(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	createVersionTestApplication(t, db, "owned", "owned_app")
	r := NewAccountOwnerExitRepository(db)
	p := exitPrepare("owned", d.Closure)
	for range 2 {
		out, e := r.Prepare(t.Context(), p, exitReceipt(t), time.Now().UTC())
		if e != nil || !out.Blocked || out.ReceiptID != "" {
			t.Fatalf("%v %v", out, e)
		}
	}
}
func TestBR_APP_010_PersonalCleanupAndRestart(t *testing.T) {
	db, link := testerMembershipFixture(t, integrationClient(t), "exit-cleanup")
	members := NewApplicationTesterMembershipRepository(db)
	joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, "closing"))
	joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, "retained"))
	r := NewAccountOwnerExitRepository(db)
	p := exitPrepare("closing", d.Closure)
	o, e := r.Prepare(t.Context(), p, exitReceipt(t), time.Now().UTC())
	if e != nil {
		t.Fatal(e)
	}
	candidate := newIntegrationTesterMembership(t, link, "closing")
	if _, e = members.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), "closing", candidate, td.ApplicationTesterLimit); !errors.Is(e, shared.ErrAccountExitBlocked) {
		t.Fatal("old identity rejoined", e)
	}
	if _, e = r.Finish(t.Context(), d.Finish{Prepare: p, ReceiptID: o.ReceiptID, Decision: d.Committed}, time.Now().UTC()); e != nil {
		t.Fatal(e)
	}
	if e = r.CleanupBatch(t.Context(), p.Key, 1); e != nil {
		t.Fatal(e)
	}
	r = NewAccountOwnerExitRepository(db)
	for range 2 {
		if e = r.CleanupBatch(t.Context(), p.Key, 1); e != nil {
			t.Fatal(e)
		}
	}
	state, e := r.Get(t.Context(), p.Key)
	if e != nil || state.Cleanup != d.Complete {
		t.Fatalf("%v %v", state, e)
	}
	n, e := db.Collection(applicationTesterMembershipsCollectionName).CountDocuments(t.Context(), bson.M{"testerAuthId": "retained"})
	if e != nil || n != 1 {
		t.Fatalf("other user %d %v", n, e)
	}
	if _, e = members.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), "closing", candidate, td.ApplicationTesterLimit); !errors.Is(e, shared.ErrAccountExitBlocked) {
		t.Fatal("closed rejoined", e)
	}
}
