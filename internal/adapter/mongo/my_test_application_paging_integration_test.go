package mongo

import (
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	ad "iwut-app-center/internal/application/domain"
	cd "iwut-app-center/internal/catalog/domain"
	rd "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
	"reflect"
	"strings"
	"testing"
	"time"
)

func createMyTestMember(t *testing.T, db *m.Database, admin, name string, caller shared.AuthID) (shared.ApplicationID, *td.ApplicationTesterMembership) {
	t.Helper()
	app := createVersionTestApplication(t, db, admin, name)
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), app.ID(), app.AdminID(), nil, newIntegrationTesterJoinLink(t, app.ID(), app.AdminID())).JoinLink()
	member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, caller)).Membership()
	return app.ID(), member
}
func TestUCAPP038_BR_CAT_015_Scan500EmptyTokenAndBoundaryDuplicates(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	caller := shared.AuthID("scan-user")
	ids := []shared.ApplicationID{}
	members := []*td.ApplicationTesterMembership{}
	for i := range 501 {
		app, member := createMyTestMember(t, db, fmt.Sprintf("scan-admin-%d", i/100), fmt.Sprintf("Scan_%03d", i), caller)
		ids = append(ids, app)
		members = append(members, member)
		if i < 500 {
			if _, err := NewApplicationOperationsRepository(db).Set(t.Context(), app, "operator", ad.PlatformAvailabilitySuspended, 1, 1, "scan test", ad.ApplicationOperationEventID(nextIntegrationTesterJoinLinkID(t)), time.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	r := myTestRepository(db)
	page, err := r.ListMyTest(t.Context(), caller, 1, nil, 20, "")
	if err != nil || len(page.Items) != 0 || page.NextPageToken == "" {
		t.Fatal("empty scan page", page, err)
	}
	bind := myTestBinding(caller, 1, nil)
	last, err := r.decodeCursor(page.NextPageToken, bind)
	if err != nil || last != ids[499].String() {
		t.Fatal("scan consumption", last, err)
	}
	next, err := r.ListMyTest(t.Context(), caller, 1, nil, 100, page.NextPageToken)
	if err != nil || len(next.Items) != 1 || next.Items[0].ApplicationID != ids[500] || next.NextPageToken != "" {
		t.Fatal("continuation", next, err)
	}
	// A duplicate immediately after the lookahead must fail the first page,
	// even though no private facts for that Application have been loaded.
	if err = db.Collection(applicationTesterMembershipsCollectionName).Indexes().DropOne(t.Context(), testerMembershipActiveUniqueIndexName); err != nil {
		t.Fatal(err)
	}
	duplicate := testerMembershipToDocument(members[500])
	duplicate.MembershipID = nextIntegrationTesterJoinLinkID(t).String()
	if _, err = db.Collection(applicationTesterMembershipsCollectionName).InsertOne(t.Context(), duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ListMyTest(t.Context(), caller, 1, nil, 1, ""); !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
		t.Fatal("lookahead duplicate", err)
	}
	if _, err = db.Collection(applicationTesterMembershipsCollectionName).DeleteOne(t.Context(), bson.M{"membershipId": duplicate.MembershipID}); err != nil {
		t.Fatal(err)
	}
	duplicate = testerMembershipToDocument(members[499])
	duplicate.MembershipID = nextIntegrationTesterJoinLinkID(t).String()
	if _, err = db.Collection(applicationTesterMembershipsCollectionName).InsertOne(t.Context(), duplicate); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ListMyTest(t.Context(), caller, 1, nil, 1, ""); !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
		t.Fatal("scan boundary duplicate", err)
	}
	cursor, err := db.Collection(applicationTesterMembershipsCollectionName).Indexes().List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close(t.Context())
	found := false
	for cursor.Next(t.Context()) {
		var d struct {
			Name string `bson:"name"`
			Key  bson.D `bson:"key"`
		}
		if err = cursor.Decode(&d); err != nil {
			t.Fatal(err)
		}
		if d.Name == myTestMembershipScanIndex {
			found = true
			if !reflect.DeepEqual(d.Key, bson.D{{Key: "testerAuthId", Value: int32(1)}, {Key: "status", Value: int32(1)}, {Key: "applicationId", Value: int32(1)}, {Key: "membershipId", Value: int32(1)}}) {
				t.Fatal("index shape")
			}
		}
	}
	if !found {
		t.Fatal("missing index")
	}
}
func createLargeMyTestFixture(t *testing.T, db *m.Database, name string, scopeBytes int) testLaunchFixture {
	t.Helper()
	app, member := createMyTestMember(t, db, "large-admin", name, "large-user")
	version, err := NewApplicationVersionRepository(db).CreateDraft(t.Context(), "large-admin", integrationApplicationVersionDraftWithSets(t, app, "large-admin", "v1", []string{"camera.read.v1"}, []string{"scope." + strings.Repeat("a", scopeBytes)}, nil))
	if err != nil {
		t.Fatal(err)
	}
	seed := decidableReview{applicationID: app, versionID: rd.ApplicationVersionID(version.ID()), reviewID: nextIntegrationApplicationReviewID(t), adminID: "large-admin"}
	reviews := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, reviews, app, version.ID(), "large-admin", 1)
	if _, err = reviews.Submit(t.Context(), candidate, seed.reviewID, seed.adminID, 41, "public-https.v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	approvePublicationVersion(t, db, seed)
	approvePublicationProfile(t, db, seed)
	pubs := NewApplicationPublicationRepository(db)
	publication := placePublication(t, pubs, loadPublicationCandidate(t, pubs, seed, 1, nil), seed).Publication()
	return testLaunchFixture{db, seed, publication, member}
}
func TestUCAPP038_BR_CAT_015_ByteBudgetNeverSkipsAndSingleLargeItem(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	first := createLargeMyTestFixture(t, db, "Large_first", 1700000)
	second := createLargeMyTestFixture(t, db, "Large_second", 1700000)
	r := myTestRepository(db)
	page, err := r.ListMyTest(t.Context(), first.membership.TesterAuthID(), 1, myTestHost, 100, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ApplicationID != first.seed.applicationID || page.NextPageToken == "" {
		t.Fatal("byte stop", err)
	}
	next, err := r.ListMyTest(t.Context(), first.membership.TesterAuthID(), 1, myTestHost, 1, page.NextPageToken)
	if err != nil || len(next.Items) != 1 || next.Items[0].ApplicationID != second.seed.applicationID || next.NextPageToken != "" {
		t.Fatal("byte skipped item", err)
	}
	third := createLargeMyTestFixture(t, db, "Large_single", 3300000)
	bind := myTestBinding(first.membership.TesterAuthID(), 1, myTestHost)
	bind.Last = second.seed.applicationID.String()
	token, _ := r.encodeCursor(bind)
	single, err := r.ListMyTest(t.Context(), first.membership.TesterAuthID(), 1, myTestHost, 100, token)
	if err != nil || len(single.Items) != 1 || single.Items[0].ApplicationID != third.seed.applicationID || myTestItemBytes(single.Items[0]) < catalogResponseBudget {
		t.Fatal("single oversized item", err)
	}
}
