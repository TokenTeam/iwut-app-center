package mongo

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	sd "iwut-app-center/internal/script/domain"
	sp "iwut-app-center/internal/script/port"
)

func approvedDistributionScript(t *testing.T, db *dm.Database, at time.Time, label, text string, publish bool, versionIDs ...sd.VersionID) sd.Version {
	t.Helper()
	resource, err := sd.NewResource(sd.ResourceID(nextIntegrationApplicationID(t)), sd.Metadata{Name: label, Purpose: "Opaque local configuration"}, "resource-owner", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewScriptResourceRepository(db).CreateResource(t.Context(), resource); err != nil {
		t.Fatal(err)
	}
	content, err := sd.NewContent(sd.JSONFormat, text)
	if err != nil {
		t.Fatal(err)
	}
	versionID := sd.VersionID(nextIntegrationApplicationID(t))
	if len(versionIDs) > 0 {
		versionID = versionIDs[0]
	}
	version, err := sd.NewDraft(versionID, resource.ID, label, content, resource.OwnerAuthID, at)
	if err != nil {
		t.Fatal(err)
	}
	repo := NewScriptVersionRepository(db)
	if _, err := repo.CreateDraft(t.Context(), version); err != nil {
		t.Fatal(err)
	}
	reviewID := sd.ReviewID(nextIntegrationApplicationID(t))
	if _, err := repo.SubmitDraft(t.Context(), sp.SubmitDraft{ResourceID: resource.ID, VersionID: version.ID, ReviewID: reviewID, ExpectedRevision: 1, Actor: resource.OwnerAuthID, At: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	seedTerminalScriptDependency(t, db, string(version.ID), string(reviewID), "APPROVED", at.Add(2*time.Second))
	if publish {
		if _, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), scriptPublicationCommand(t, resource.ID, version.ID, sd.PublishScript, 1, at.Add(3*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	return version
}

func TestScriptDistributionContentIntegration_UCAPP047_BRSCR021022(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	const text = "\ufeff{\r\n intentionally opaque and not JSON ]\r\n"
	version := approvedDistributionScript(t, db, at, "opaque-script", text, false)
	reader := NewScriptDistributionRepository(db, ScriptQueryCursorKey{7})
	if _, err := reader.GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, version.Content.Digest, time.Now()); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("unpublished content", err)
	}
	writer := NewScriptPublicationRepository(db)
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, version.ID, sd.PublishScript, 1, at.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		content, err := reader.GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, version.Content.Digest, time.Now())
		if err != nil || content.Text != text || content.Descriptor.ByteLength != int64(len(text)) || content.Validate() != nil {
			t.Fatal("precise opaque bytes", err)
		}
	}
	check()
	page, err := reader.ListPublishedScripts(t.Context(), "ordinary-user", sp.ScriptPageRequest{Limit: 20}, time.Now())
	if err != nil || len(page.Items) != 1 || page.Items[0].Head.VersionID != version.ID || page.Items[0].VersionLabel != version.Label {
		t.Fatal("independent recommendation", err)
	}
	if _, err := reader.GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, strings.Repeat("0", 64), time.Now()); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("wrong expected digest", err)
	}
	if _, err := reader.GetPublishedScriptContent(t.Context(), sd.ResourceID(nextIntegrationApplicationID(t)), version.ID, version.Content.Digest, time.Now()); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("foreign resource", err)
	}
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.ClearScriptHead, 2, at.Add(4*time.Second))); err != nil {
		t.Fatal(err)
	}
	page, err = reader.ListPublishedScripts(t.Context(), "ordinary-user", sp.ScriptPageRequest{Limit: 20}, time.Now())
	if err != nil || len(page.Items) != 0 || page.Items == nil {
		t.Fatal("clear head recommendation", err)
	}
	check()
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.SuspendScript, 1, at.Add(5*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, version.Content.Digest, time.Now()); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("suspended exact content", err)
	}
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.RestoreScript, 2, at.Add(6*time.Second))); err != nil {
		t.Fatal(err)
	}
	check()
	// Historical content must not depend on an unrelated current head pointer.
	if _, err := db.Collection(scriptPublicationsCollection).UpdateOne(t.Context(), bson.M{"resourceId": version.ResourceID}, bson.M{"$set": bson.M{"head": "malformed"}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	check()
	if _, err := reader.ListPublishedScripts(t.Context(), "ordinary-user", sp.ScriptPageRequest{Limit: 20}, time.Now()); !errors.Is(err, sd.ErrDistributionStateInconsistent) {
		t.Fatal("damaged head was hidden", err)
	}
	if _, err := db.Collection(scriptPublicationsCollection).UpdateOne(t.Context(), bson.M{"resourceId": version.ResourceID}, bson.M{"$set": bson.M{"head": nil}}); err != nil {
		t.Fatal(err)
	}
	// An unpublished review is private even when its private policy is damaged.
	unpublished := approvedDistributionScript(t, db, at, "unpublished-script", "unpublished text", false)
	if _, err := db.Collection(scriptReviewsCollection).UpdateOne(t.Context(), bson.M{"snapshot.versionId": unpublished.ID}, bson.M{"$unset": bson.M{"policyBinding": ""}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetPublishedScriptContent(t.Context(), unpublished.ResourceID, unpublished.ID, unpublished.Content.Digest, time.Now()); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("private review affected public error", err)
	}
	if _, err := db.Collection(scriptPublishedVersionsCollection).DeleteOne(t.Context(), bson.M{"resourceId": version.ResourceID, "version.versionId": version.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, version.Content.Digest, time.Now()); !errors.Is(err, sd.ErrDistributionStateInconsistent) {
		t.Fatal("lost permanent proof became unpublished", err)
	}
}

func TestScriptDistributionPagingIntegration_UCAPP047_BRSCR022(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	versions := []sd.Version{}
	for _, label := range []string{"page-a", "page-b", "page-c"} {
		versions = append(versions, approvedDistributionScript(t, db, at, label, "opaque text", true))
	}
	reader := NewScriptDistributionRepository(db, ScriptQueryCursorKey{9})
	first, err := reader.ListPublishedScripts(t.Context(), "caller", sp.ScriptPageRequest{Limit: 1}, time.Now())
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatal("first page", err)
	}
	if _, err := reader.ListPublishedScripts(t.Context(), "different-caller", sp.ScriptPageRequest{Limit: 1, Cursor: first.NextCursor}, time.Now()); !errors.Is(err, sd.ErrInvalidDistributionCursor) {
		t.Fatal("cursor crossed users", err)
	}
	seen := map[sd.ResourceID]bool{first.Items[0].ResourceID: true}
	token := first.NextCursor
	for token != "" {
		page, err := reader.ListPublishedScripts(t.Context(), "caller", sp.ScriptPageRequest{Limit: 2, Cursor: token}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if seen[item.ResourceID] {
				t.Fatal("same-time duplicate")
			}
			seen[item.ResourceID] = true
		}
		token = page.NextCursor
	}
	if len(seen) != len(versions) {
		t.Fatal("same-time resource skipped")
	}
	management := NewScriptQueryRepository(db, ScriptQueryCursorKey{9})
	managed, err := management.ListResources(t.Context(), sp.ScriptReadScope{Actor: "caller"}, sp.ScriptPageRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ListPublishedScripts(t.Context(), "caller", sp.ScriptPageRequest{Limit: 1, Cursor: managed.NextCursor}, time.Now()); !errors.Is(err, sd.ErrInvalidDistributionCursor) {
		t.Fatal("cursor crossed query kinds", err)
	}
	// Five hundred invisible resources consume a bounded scan without skipping
	// the older, still eligible resource on the continuation page.
	db = migratedIntegrationDatabase(t, integrationClient(t))
	older := approvedDistributionScript(t, db, at, "older-published", "precise", true)
	for i := 0; i < 500; i++ {
		resource, err := sd.NewResource(sd.ResourceID(nextIntegrationApplicationID(t)), sd.Metadata{Name: "unpublished", Purpose: "bounded scan"}, "author", at.Add(10*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewScriptResourceRepository(db).CreateResource(t.Context(), resource); err != nil {
			t.Fatal(err)
		}
	}
	reader = NewScriptDistributionRepository(db, ScriptQueryCursorKey{9})
	empty, err := reader.ListPublishedScripts(t.Context(), "caller", sp.ScriptPageRequest{Limit: 20}, time.Now())
	if err != nil || len(empty.Items) != 0 || empty.NextCursor == "" {
		t.Fatal("bounded empty continuation", err)
	}
	last, err := reader.ListPublishedScripts(t.Context(), "caller", sp.ScriptPageRequest{Limit: 20, Cursor: empty.NextCursor}, time.Now())
	if err != nil || len(last.Items) != 1 || last.Items[0].Head.VersionID != older.ID || last.NextCursor != "" {
		t.Fatal("scan skipped eligible continuation", err)
	}
}

func TestScriptDistributionSnapshotIntegration_UCAPP047_BRSCR021022(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	version := approvedDistributionScript(t, db, at, "snapshot-script", "snapshot text", true)
	paused, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "find" && e.Command.Lookup("find").StringValue() == scriptAvailabilityCollection {
			once.Do(func() {
				close(paused)
				select {
				case <-resume:
				case <-ctx.Done():
				}
			})
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	read := make(chan error, 1)
	go func() {
		content, err := NewScriptDistributionRepository(client.Database(db.Name()), ScriptQueryCursorKey{1}).GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, version.Content.Digest, time.Now())
		if err == nil && content.Text != version.Content.Text {
			err = errors.New("mixed snapshot text")
		}
		read <- err
	}()
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("reader did not reach gate")
	}
	if _, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.SuspendScript, 1, time.Now())); err != nil {
		t.Fatal(err)
	}
	close(resume)
	if err := <-read; err != nil {
		t.Fatal("pre-pause snapshot was mixed", err)
	}
	if _, err := NewScriptDistributionRepository(db, ScriptQueryCursorKey{1}).GetPublishedScriptContent(t.Context(), version.ResourceID, version.ID, version.Content.Digest, time.Now()); !errors.Is(err, sd.ErrScriptContentUnavailable) {
		t.Fatal("new read reused pre-pause eligibility", err)
	}
}
