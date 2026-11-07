package mongo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	cd "iwut-app-center/internal/catalog/domain"
	pd "iwut-app-center/internal/publication/domain"
	pp "iwut-app-center/internal/publication/port"
	sd "iwut-app-center/internal/script/domain"
	sp "iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func TestScriptDistributionAtomicMultiDependencyIntegration_UCAPP047_BRSCR020(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	a := approvedDistributionScript(t, db, at, "resource-a", "opaque a", true, "0194a000-0000-7000-8000-000000000002")
	b := approvedDistributionScript(t, db, at, "resource-b", "opaque b", true, "0194a000-0000-7000-8000-000000000001")
	base := createApprovedPublicationVersion(t, db, "multi-script-publication")
	app := addScriptPublicationVersion(t, db, base, []shared.ScriptReference{{ResourceID: string(a.ResourceID), VersionID: string(a.ID)}, {ResourceID: string(b.ResourceID), VersionID: string(b.ID)}}, "two-scripts", nil)
	writes := []string{}
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "update" && e.Command.Lookup("update").StringValue() == scriptAvailabilityCollection {
			updates := e.Command.Lookup("updates").Array()
			values, _ := updates.Values()
			q := values[0].Document().Lookup("q").Document()
			writes = append(writes, q.Lookup("resourceId").StringValue())
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	repo := NewApplicationPublicationRepository(client.Database(db.Name()))
	first := placePublication(t, repo, loadPublicationCandidate(t, repo, app, 1, nil), app)
	expected := []string{string(a.ResourceID), string(b.ResourceID)}
	slices.Sort(expected)
	if !slices.Equal(writes, expected) {
		t.Fatal("resource fences did not use deterministic resource order", writes, expected)
	}
	readGates := func() []scriptAvailabilityDocument {
		t.Helper()
		out := []scriptAvailabilityDocument{}
		for _, version := range []sd.Version{a, b} {
			var gate scriptAvailabilityDocument
			if err := db.Collection(scriptAvailabilityCollection).FindOne(t.Context(), bson.M{"resourceId": version.ResourceID}).Decode(&gate); err != nil {
				t.Fatal(err)
			}
			out = append(out, gate)
		}
		return out
	}
	before := readGates()
	candidate := loadPublicationCandidate(t, repo, app, 2, nil)
	id := pd.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
	if _, err := repo.PlaceInTest(t.Context(), candidate, &id, first.History().HistoryID(), app.adminID, publicationTestValidation(), time.Now()); !dm.IsDuplicateKeyError(err) {
		t.Fatal("history collision did not abort final transaction", err)
	}
	if !reflect.DeepEqual(before, readGates()) {
		t.Fatal("history failure left a partial script fence")
	}
	assertCollectionCount(t, db, applicationPublicationsCollectionName, 1)
	assertCollectionCount(t, db, applicationPublicationHistoryCollectionName, 1)
	if _, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), scriptPublicationCommand(t, b.ResourceID, "", sd.SuspendScript, 1, time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadTestPlacementCandidate(t.Context(), app.applicationID, 2, pd.ApplicationVersionID(app.versionID), app.adminID, nil); !errors.Is(err, pp.ErrApplicationScriptDependencyUnavailable) {
		t.Fatal("one unavailable dependency did not block whole publication", err)
	}
}

func TestScriptDistributionBatchIntegration_UCAPP047_BRSCR019020(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	refs := []shared.ScriptReference{}
	versions := []sd.Version{}
	for i := 0; i < 17; i++ {
		version := approvedDistributionScript(t, db, at, fmt.Sprintf("batch-script-%d", i), fmt.Sprintf("opaque body %d", i), true)
		versions = append(versions, version)
		refs = append(refs, shared.ScriptReference{ResourceID: string(version.ResourceID), VersionID: string(version.ID)})
	}
	const caller = "batch-runtime-reader"
	var first decidableReview
	for i, selected := range [][]shared.ScriptReference{refs[:16], refs[16:]} {
		base := createApprovedPublicationVersion(t, db, fmt.Sprintf("batch-app-%d", i))
		app := addScriptPublicationVersion(t, db, base, selected, "batch-version", nil)
		if i == 0 {
			first = app
		}
		repo := NewApplicationPublicationRepository(db)
		placePublication(t, repo, loadPublicationCandidate(t, repo, app, 1, nil), app)
		link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), app.applicationID, app.adminID, nil, newIntegrationTesterJoinLink(t, app.applicationID, app.adminID)).JoinLink()
		joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, caller))
	}
	var resourceReads atomic.Int32
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" && e.Command.Lookup("find").StringValue() == scriptResourcesCollectionName {
			resourceReads.Add(1)
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	page, err := myTestRepository(client.Database(db.Name())).ListMyTest(t.Context(), caller, 1, nil, 20, "")
	if err != nil || len(page.Items) != 2 || resourceReads.Load() != 2 {
		t.Fatal("script reads were not bounded chunks across applications", err, resourceReads.Load())
	}
	for _, item := range page.Items {
		if item.Availability != cd.MyTestReady {
			t.Fatal("valid batch target unavailable")
		}
	}
	runtime, _ := sd.NewScriptRuntimeQuery(sd.ScriptRuntimePureTest, 1, nil)
	request := sp.ApplicationScriptRequest{Caller: caller, ApplicationID: first.applicationID, ApplicationVersionID: first.versionID.String(), Runtime: runtime, ObservedAt: time.Now()}
	manifest, err := NewScriptDistributionRepository(db, ScriptQueryCursorKey{1}).ResolveApplicationScriptManifest(t.Context(), request)
	if err != nil || len(manifest.Scripts) != 16 {
		t.Fatal("maximum manifest", err)
	}
	if _, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), scriptPublicationCommand(t, versions[0].ResourceID, "", sd.SuspendScript, 1, time.Now())); err != nil {
		t.Fatal(err)
	}
	page, err = myTestRepository(db).ListMyTest(t.Context(), caller, 1, nil, 20, "")
	if err != nil || len(page.Items) != 2 {
		t.Fatal("batch availability page", err)
	}
	for _, item := range page.Items {
		expected := cd.MyTestReady
		if item.ApplicationID == first.applicationID {
			expected = cd.MyTestScriptDependencyUnavailable
		}
		if item.Availability != expected {
			t.Fatal("dependency result crossed application boundary")
		}
	}
	if _, err := NewScriptDistributionRepository(db, ScriptQueryCursorKey{1}).ResolveApplicationScriptManifest(t.Context(), request); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("partial maximum manifest escaped", err)
	}
}
