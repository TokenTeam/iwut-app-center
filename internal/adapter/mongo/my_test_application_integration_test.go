package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	od "iwut-app-center/internal/ownerexit/domain"
	pd "iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
	"reflect"
	"strings"
	"testing"
	"time"
)

func myTestRepository(db *m.Database) *MyTestApplicationRepository {
	key, _ := DeriveMyTestApplicationCursorKey([]byte("integration-key"))
	return NewMyTestApplicationRepository(db, key)
}

var myTestHost = []cd.CapabilityName{"camera.read.v1"}

func myTestDetail(t *testing.T, f testLaunchFixture) *cd.MyTestApplicationItem {
	t.Helper()
	d, err := myTestRepository(f.db).GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost)
	if err != nil {
		t.Fatal(err)
	}
	return d.Item
}
func TestUCAPP038_BR_CAT_011_012_013_014_017_ProjectionStagesAndEpisodes(t *testing.T) {
	client := integrationClient(t)
	t.Run("legitimate no pointer then approved profile without target", func(t *testing.T) {
		db, link := testerMembershipFixture(t, client, "my-test-before-profile")
		member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "ordinary")).Membership()
		r := myTestRepository(db)
		monitored, trace := testLaunchMonitoredClient(t, db.Name(), "")
		d, err := myTestRepository(monitored.Database(db.Name())).GetMyTest(t.Context(), link.ApplicationID(), member.TesterAuthID(), 1, nil)
		if err != nil || d.Item.Availability != cd.MyTestProfileRequired || d.Item.Profile != nil || d.Item.LaunchTarget != nil {
			t.Fatal(d, err)
		}
		for _, c := range trace.collections() {
			if c == applicationVersionsCollectionName || c == applicationPublicationsCollectionName || c == applicationFiltersCollectionName {
				t.Fatal("stage read", c)
			}
		}
		approvePublicationProfile(t, db, decidableReview{applicationID: link.ApplicationID(), adminID: link.CreatedBy()})
		d, err = r.GetMyTest(t.Context(), link.ApplicationID(), member.TesterAuthID(), 1, nil)
		if err != nil || d.Item.Availability != cd.MyTestTargetUnavailable || d.Item.Profile == nil || d.Item.Membership.MembershipID.String() != member.MembershipID().String() {
			t.Fatal(d, err)
		}
		for _, auth := range []shared.AuthID{link.CreatedBy(), "reviewer", "outsider"} {
			page, err := r.ListMyTest(t.Context(), auth, 1, nil, 20, "")
			if err != nil || len(page.Items) != 0 {
				t.Fatal("implicit membership", err)
			}
			if _, err = r.GetMyTest(t.Context(), link.ApplicationID(), auth, 1, nil); !errors.Is(err, cd.ErrMyTestApplicationNotFound) {
				t.Fatal("hidden detail", err)
			}
		}
	})
	t.Run("pure Test clear retained EMPTY recovery Stable-backed and revoked source", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		r := myTestRepository(f.db)
		before := readTestLaunchFacts(t, f.db)
		item := myTestDetail(t, f)
		if item.Availability != cd.MyTestReady || item.LaunchTarget.VersionID() != f.seed.versionID.String() {
			t.Fatal(item)
		}
		if !reflect.DeepEqual(before, readTestLaunchFacts(t, f.db)) {
			t.Fatal("query wrote")
		}
		d, err := r.GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 2, myTestHost)
		if err != nil || d.Item.Availability != cd.MyTestTargetUnavailable {
			t.Fatal("other major", d, err)
		}
		d, err = r.GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []cd.CapabilityName{"unrelated.v1"})
		if err != nil || d.Item.Availability != cd.MyTestCapabilitiesInsufficient || d.Item.LaunchTarget != nil || !reflect.DeepEqual(d.Item.MissingCapabilities, myTestHost) {
			t.Fatal("missing capabilities", d, err)
		}
		pubs := NewApplicationPublicationRepository(f.db)
		cleared, err := pubs.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 1, clearTestAudit(t))
		if err != nil {
			t.Fatal(err)
		}
		item = myTestDetail(t, f)
		if item.Availability != cd.MyTestTargetUnavailable || item.Membership.MembershipID.String() != f.membership.MembershipID().String() || !item.Membership.JoinedAt.Equal(f.membership.JoinedAt()) {
			t.Fatal("clear lost episode")
		}
		expected := cleared.Publication().Revision()
		placed := placePublication(t, pubs, loadPublicationCandidate(t, pubs, f.seed, 1, &expected), f.seed).Publication()
		if myTestDetail(t, f).Availability != cd.MyTestReady {
			t.Fatal("set failed recovery")
		}
		expected = placed.Revision()
		stable, err := pubs.LoadStablePlacementCandidate(t.Context(), f.seed.applicationID, 1, pd.ApplicationVersionID(f.seed.versionID), f.seed.adminID, &expected)
		if err != nil {
			t.Fatal(err)
		}
		set, err := pubs.SetStable(t.Context(), stable, nil, pd.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, publicationTestValidation(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		// Neither an unselected Grey fact nor a poisoned Filter is consulted,
		// even on an otherwise Stable-backed Application.
		setTestLaunchFields(t, f.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "greyRollout", Value: "private-unselected"}})
		if _, err := f.db.Collection(applicationFiltersCollectionName).InsertOne(t.Context(), bson.M{"applicationId": f.seed.applicationID.String(), "currentFilterRevisionId": "broken-private"}, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal(err)
		}
		monitored, trace := testLaunchMonitoredClient(t, f.db.Name(), "")
		page, err := myTestRepository(monitored.Database(f.db.Name())).ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, "")
		if err != nil || len(page.Items) != 1 || page.Items[0].LaunchTarget.VersionID() != f.seed.versionID.String() {
			t.Fatal(page, err)
		}
		trace.assertReadOnlySnapshot(t)
		for _, c := range trace.collections() {
			if c == applicationFiltersCollectionName || c == applicationFilterRevisionsCollectionName {
				t.Fatal("read Filter")
			}
		}
		setTestLaunchFields(t, f.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "greyRollout", Value: nil}})
		if _, err = pubs.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, set.Publication().Revision(), clearTestAudit(t)); err != nil {
			t.Fatal(err)
		}
		if myTestDetail(t, f).Availability != cd.MyTestTargetUnavailable {
			t.Fatal("Stable fallback")
		}
		members := NewApplicationTesterMembershipRepository(f.db)
		if _, err = members.Remove(t.Context(), f.seed.applicationID, f.membership.MembershipID(), f.seed.adminID, time.Now()); err != nil {
			t.Fatal(err)
		}
		page, err = r.ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, "")
		if err != nil || len(page.Items) != 0 {
			t.Fatal("removed list", err)
		}
		if _, err = r.GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost); !errors.Is(err, cd.ErrMyTestApplicationNotFound) {
			t.Fatal("removed detail", err)
		}
		var linkDoc applicationTesterJoinLinkDocument
		_ = f.db.Collection(applicationTesterJoinLinksCollectionName).FindOne(t.Context(), bson.M{"joinLinkId": f.membership.JoinedViaJoinLinkID().String()}).Decode(&linkDoc)
		link, _ := testerJoinLinkFromDocument(linkDoc)
		rejoined := joinIntegrationTesterMembership(t, members, link, newIntegrationTesterMembership(t, link, f.membership.TesterAuthID())).Membership()
		f.membership = rejoined
		if myTestDetail(t, f).Membership.MembershipID.String() != rejoined.MembershipID().String() {
			t.Fatal("rejoin episode")
		}
		if _, err = NewApplicationTesterJoinLinkRepository(f.db).Revoke(t.Context(), f.seed.applicationID, link.JoinLinkID(), f.seed.adminID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if myTestDetail(t, f).Availability != cd.MyTestTargetUnavailable {
			t.Fatal("revoked source invalidated member")
		}
	})
}
func TestUCAPP038_BR_CAT_011_016_017_GatesCleanupAndFailureClassification(t *testing.T) {
	f := newTestLaunchFixture(t, integrationClient(t))
	r := myTestRepository(f.db)
	assertHidden := func() {
		t.Helper()
		page, err := r.ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, "")
		if err != nil || len(page.Items) != 0 {
			t.Fatal("hidden list", page, err)
		}
		if _, err = r.GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost); !errors.Is(err, cd.ErrMyTestApplicationNotFound) {
			t.Fatal("hidden detail", err)
		}
	}
	operations := NewApplicationOperationsRepository(f.db)
	if _, err := operations.Set(t.Context(), f.seed.applicationID, "operator", ad.PlatformAvailabilitySuspended, 1, 1, "test", ad.ApplicationOperationEventID(nextIntegrationTesterJoinLinkID(t)), time.Now()); err != nil {
		t.Fatal(err)
	}
	assertHidden()
	memberDoc := testerMembershipToDocument(f.membership)
	setTestLaunchFields(t, f.db, applicationTesterMembershipsCollectionName, bson.D{}, bson.D{{Key: "joinedViaJoinLinkId", Value: "private-invalid-source"}, {Key: "joinedAt", Value: "private-invalid-time"}})
	assertHidden()
	setTestLaunchFields(t, f.db, applicationTesterMembershipsCollectionName, bson.D{}, bson.D{{Key: "joinedViaJoinLinkId", Value: memberDoc.JoinedViaJoinLinkID}, {Key: "joinedAt", Value: memberDoc.JoinedAt}})
	// A gated candidate must not read its intentionally broken private runtime.
	setTestLaunchFields(t, f.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "testVersionId", Value: "broken-private"}})
	assertHidden()
	setTestLaunchFields(t, f.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "testVersionId", Value: f.seed.versionID.String()}})
	if _, err := operations.Set(t.Context(), f.seed.applicationID, "operator", ad.PlatformAvailabilityAvailable, 1, 2, "test", ad.ApplicationOperationEventID(nextIntegrationTesterJoinLinkID(t)), time.Now()); err != nil {
		t.Fatal(err)
	}
	if myTestDetail(t, f).Availability != cd.MyTestReady {
		t.Fatal("restore")
	}
	archives := NewApplicationArchivalRepository(f.db)
	audit := func() (ad.ApplicationArchiveEventID, time.Time, error) {
		return ad.ApplicationArchiveEventID(nextIntegrationTesterJoinLinkID(t)), time.Now(), nil
	}
	if _, err := archives.SetArchiveState(t.Context(), f.seed.applicationID, f.seed.adminID, ad.ApplicationArchiveArchived, 1, audit); err != nil {
		t.Fatal(err)
	}
	assertHidden()
	if _, err := archives.SetArchiveState(t.Context(), f.seed.applicationID, f.seed.adminID, ad.ApplicationArchiveUnarchived, 2, audit); err != nil {
		t.Fatal(err)
	}
	if myTestDetail(t, f).Availability != cd.MyTestReady {
		t.Fatal("unarchive")
	}
	exit := NewAccountOwnerExitRepository(f.db)
	prepare := exitPrepare(f.membership.TesterAuthID().String(), od.Closure)
	out, err := exit.Prepare(t.Context(), prepare, exitReceipt(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exit.Finish(t.Context(), od.Finish{Prepare: prepare, ReceiptID: out.ReceiptID, Decision: od.Committed}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = exit.CleanupBatch(t.Context(), prepare.Key, 100); err != nil {
			t.Fatal(err)
		}
	}
	assertHidden()
	assertCollectionCount(t, f.db, applicationTesterMembershipsCollectionName, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = r.ListMyTest(ctx, f.membership.TesterAuthID(), 1, myTestHost, 20, ""); !errors.Is(err, cd.ErrMyTestApplicationUnavailable) {
		t.Fatal("driver not503", err)
	}
	// A new genuine episode belongs to a different user; closure hides it.
	var linkDoc applicationTesterJoinLinkDocument
	_ = f.db.Collection(applicationTesterJoinLinksCollectionName).FindOne(t.Context(), bson.D{}).Decode(&linkDoc)
	link, _ := testerJoinLinkFromDocument(linkDoc)
	f.membership = joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(f.db), link, newIntegrationTesterMembership(t, link, "retained")).Membership()
	now := time.Now()
	closure := NewApplicationClosureRepository(f.db)
	id := ad.ApplicationClosureID(nextIntegrationTesterJoinLinkID(t))
	proof := ad.ApplicationCloseProof{Subject: f.seed.adminID, ApplicationID: f.seed.applicationID, JTI: nextIntegrationTesterJoinLinkID(t).String(), AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if _, err = closure.Start(t.Context(), f.seed.applicationID, f.seed.adminID, 1, 1, proof, id, now); err != nil {
		t.Fatal(err)
	}
	assertHidden()
	if _, err = closure.Complete(t.Context(), id, "test-receipt", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertHidden()
}
func TestUCAPP038_BR_CAT_016_CorruptNecessaryFactsAndUC012OAuth(t *testing.T) {
	client := integrationClient(t)
	for _, tc := range []struct {
		name, collection, field string
		value                   any
	}{
		{"source missing", applicationTesterMembershipsCollectionName, "joinedViaJoinLinkId", "01890f47-0000-7000-8000-000000009999"},
		{"source cross app", applicationTesterJoinLinksCollectionName, "applicationId", "01890f47-0000-7000-8000-000000009999"},
		{"profile missing", applicationProfilesCollectionName, "currentPublishedProfileRevisionId", "01890f47-0000-7000-8000-000000009999"},
		{"profile nonapproved", applicationProfileRevisionsCollectionName, "reviewStatus", "DRAFT"},
		{"profile cross app", applicationProfileRevisionsCollectionName, "applicationId", "01890f47-0000-7000-8000-000000009999"},
		{"profile snapshot", applicationProfileReviewsCollectionName, "snapshot.displayName", "Drift"},
		{"profile review numeric type", applicationProfileReviewsCollectionName, "attempt", int64(1)},
		{"publication numeric type", applicationPublicationsCollectionName, "revision", int32(1)},
		{"version absent", applicationPublicationsCollectionName, "testVersionId", "01890f47-0000-7000-8000-000000009999"},
		{"version range", applicationVersionsCollectionName, "rpcApiMinVersion", int32(2)},
		{"version cross app", applicationVersionsCollectionName, "applicationId", "01890f47-0000-7000-8000-000000009999"},
		{"version content", applicationVersionsCollectionName, "launchUrl", "https://example.edu/drift"},
		{"review snapshot", applicationReviewsCollectionName, "snapshot.versionLabel", "drift"},
		{"oauth drift", applicationVersionOAuthConfigsCollectionName, "oauthRedirects.pkceRedirectUris", bson.A{"https://example.edu/new-callback"}},
		{"oauth ownership", applicationVersionOAuthConfigsCollectionName, "applicationId", "01890f47-0000-7000-8000-000000009999"},
		{"history bad approval", applicationPublicationHistoryCollectionName, "approvedReviewId", "01890f47-0000-7000-8000-000000009999"},
		{"history invalid policy", applicationPublicationHistoryCollectionName, "preflightPolicyVersion", ""},
		{"old SET pointer nulled", applicationPublicationsCollectionName, "testVersionId", nil},
		{"membership noncanonical", applicationTesterMembershipsCollectionName, "membershipId", "01890F47-0000-7000-8000-000000000AAA"},
		{"membership time overflow", applicationTesterMembershipsCollectionName, "joinedAt", bson.DateTime(253402300800000)},
		{"version time overflow", applicationVersionsCollectionName, "createdAt", bson.DateTime(-62135596800001)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestLaunchFixture(t, client)
			setTestLaunchFields(t, f.db, tc.collection, bson.D{}, bson.D{{Key: tc.field, Value: tc.value}})
			page, err := myTestRepository(f.db).ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, "")
			if page != nil || !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
				t.Fatal("not500", page, err)
			}
			if strings.Contains(fmt.Sprint(err), f.membership.TesterAuthID().String()) {
				t.Fatal("private error")
			}
			if strings.HasPrefix(tc.name, "oauth ") {
				_, err := NewTestLaunchResolver(f.db).ResolveForTester(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost)
				if !errors.Is(err, cp.ErrApplicationTestPublicationInconsistent) {
					t.Fatal("UC012 OAuth", err)
				}
			}
		})
	}
	t.Run("missing app list500 detail404", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		if _, err := f.db.Collection(applicationsCollectionName).DeleteMany(t.Context(), bson.D{}); err != nil {
			t.Fatal(err)
		}
		if _, err := myTestRepository(f.db).ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, ""); !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
			t.Fatal(err)
		}
		if _, err := myTestRepository(f.db).GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost); !errors.Is(err, cd.ErrMyTestApplicationNotFound) {
			t.Fatal(err)
		}
	})
	t.Run("duplicate ACTIVE", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		if err := f.db.Collection(applicationTesterMembershipsCollectionName).Indexes().DropOne(t.Context(), testerMembershipActiveUniqueIndexName); err != nil {
			t.Fatal(err)
		}
		d := testerMembershipToDocument(f.membership)
		d.MembershipID = nextIntegrationTesterJoinLinkID(t).String()
		if _, err := f.db.Collection(applicationTesterMembershipsCollectionName).InsertOne(t.Context(), d); err != nil {
			t.Fatal(err)
		}
		if _, err := myTestRepository(f.db).ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 1, ""); !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
			t.Fatal(err)
		}
	})
	t.Run("duplicate BSON nested snapshot", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		raw, err := f.db.Collection(applicationReviewsCollectionName).FindOne(t.Context(), bson.D{}).Raw()
		if err != nil {
			t.Fatal(err)
		}
		var d bson.D
		_ = bson.Unmarshal(raw, &d)
		for i, e := range d {
			if e.Key == "snapshot" {
				sn := e.Value.(bson.D)
				sn = append(sn, bson.E{Key: "versionLabel", Value: "drift"})
				d[i].Value = sn
			}
		}
		if _, err = f.db.Collection(applicationReviewsCollectionName).ReplaceOne(t.Context(), bson.D{}, d, options.Replace().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal(err)
		}
		if _, err = myTestRepository(f.db).ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, ""); !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
			t.Fatal(err)
		}
	})
}
