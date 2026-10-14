package mongo

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	od "iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/shared"
	tp "iwut-app-center/internal/tester/port"
)

func TestUCAPP040BRTST047048049AccountBarrierAndCleanupIntegration(t *testing.T) {
	db, link := testerMembershipFixture(t, integrationClient(t), "tester-exit-account-barrier")
	repo := NewApplicationTesterMembershipRepository(db)
	member := joinIntegrationTesterMembership(t, repo, link, newIntegrationTesterMembership(t, link, "exit-account")).Membership()
	exit := NewAccountOwnerExitRepository(db)
	prepare := exitPrepare(member.TesterAuthID().String(), od.Closure)
	prepared, err := exit.Prepare(t.Context(), prepare, exitReceipt(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.Collection(ownerFences).FindOne(t.Context(), bson.M{"authId": member.TesterAuthID().String()}).Raw()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadExitCandidate(t.Context(), member.ApplicationID(), member.MembershipID(), member.TesterAuthID()); err != nil {
		t.Fatal("barrier blocked candidate", err)
	}
	if r, err := repo.Leave(t.Context(), member.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); err != nil || !r.Left() {
		t.Fatal("barrier blocked exit", err)
	}
	after, err := db.Collection(ownerFences).FindOne(t.Context(), bson.M{"authId": member.TesterAuthID().String()}).Raw()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("exit modified account barrier", err)
	}
	input := newIntegrationTesterMembership(t, link, member.TesterAuthID())
	if r, err := repo.Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), input.TesterAuthID(), input, 100); r != nil || !errors.Is(err, shared.ErrAccountExitBlocked) {
		t.Fatal("exit bypassed join barrier", err)
	}
	if _, err := exit.Finish(t.Context(), od.Finish{Prepare: prepare, ReceiptID: prepared.ReceiptID, Decision: od.Committed}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := exit.CleanupBatch(t.Context(), prepare.Key, 100); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := repo.Leave(t.Context(), member.ApplicationID(), member.MembershipID(), member.TesterAuthID(), time.Now()); r != nil || !errors.Is(err, tp.ErrApplicationTesterMembershipNotFound) {
		t.Fatal("cleaned episode restored", err)
	}
}
