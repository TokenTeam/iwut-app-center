package mongo

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func TestScriptVersionRepositoryIntegration_UCAPP042_BRSCR004006(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	resources := NewScriptResourceRepository(db)
	versions := NewScriptVersionRepository(db)
	documents := db.Collection(scriptVersionsCollection)
	audits := db.Collection(scriptVersionAuditsCollection)
	assertIndexNames(t, documents, []string{"_id_", scriptVersionIDIndex, scriptVersionLabelIndex, "ix_script_versions_resource_created", "ix_script_versions_resource_state_created", "ix_script_candidate_created"})
	assertIndexNames(t, audits, []string{"_id_", scriptVersionAuditIndex})

	at := time.Date(2026, 11, 6, 12, 0, 0, 123456789, time.UTC)
	firstResource := domain.ResourceID(nextIntegrationApplicationID(t))
	secondResource := domain.ResourceID(nextIntegrationApplicationID(t))
	metadata := domain.Metadata{Name: "Crawler", Purpose: "Description"}
	for _, id := range []domain.ResourceID{firstResource, secondResource} {
		resource, err := domain.NewResource(id, metadata, "resource-creator", at)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := resources.CreateResource(t.Context(), resource); err != nil {
			t.Fatal(err)
		}
	}
	const raw = "\uFEFF{\r\n  \"b\": 2, \"a\":1\r\n}\r\n "
	content, err := domain.NewContent(domain.JSONFormat, raw)
	if err != nil {
		t.Fatal(err)
	}
	candidate := func(resourceID domain.ResourceID, label string) domain.Version {
		t.Helper()
		draft, err := domain.NewDraft(domain.VersionID(nextIntegrationApplicationID(t)), resourceID, label, content, "creator", at)
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}
	assertStored := func(want domain.Version) {
		t.Helper()
		got, err := versions.loadVersion(t.Context(), want.ResourceID, want.ID)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("persisted script version differs from result: %v", err)
		}
	}

	created, err := versions.CreateDraft(t.Context(), candidate(firstResource, "Release.1"))
	if err != nil || created.Revision != 1 || created.State != domain.VersionDraft || created.Content.Text != raw ||
		!created.CreatedAt.Equal(at.Truncate(time.Millisecond)) {
		t.Fatalf("invalid draft result: %v", err)
	}
	assertStored(created)

	for _, distinct := range []domain.Version{candidate(firstResource, "release.1"), candidate(secondResource, "Release.1")} {
		if _, err := versions.CreateDraft(t.Context(), distinct); err != nil {
			t.Fatalf("case-sensitive, resource-scoped label rejected: %v", err)
		}
	}
	if _, err := versions.CreateDraft(t.Context(), candidate(domain.ResourceID(nextIntegrationApplicationID(t)), "Release.1")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing resource: %v", err)
	}
	runRace := func(write func(int) error, expected error) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan error, 2)
		var writers sync.WaitGroup
		for index := range 2 {
			writers.Add(1)
			go func() {
				defer writers.Done()
				<-start
				results <- write(index)
			}()
		}
		close(start)
		writers.Wait()
		close(results)
		successes, conflicts := classifyConcurrentResults(t, results, expected)
		if successes != 1 || conflicts != 1 {
			t.Fatalf("competing writes: %d successes, %d conflicts", successes, conflicts)
		}
	}
	candidates := []domain.Version{candidate(firstResource, "race"), candidate(firstResource, "race")}
	runRace(func(index int) error {
		_, err := versions.CreateDraft(t.Context(), candidates[index])
		return err
	}, domain.ErrVersionLabelConflict)
	assertCollectionCount(t, db, scriptVersionsCollection, 4)
	assertCollectionCount(t, db, scriptVersionAuditsCollection, 4)

	cmd := port.ReplaceDraft{
		ResourceID: firstResource, VersionID: created.ID, ExpectedRevision: 1,
		Content: content, Actor: "no-op-editor", At: at.Add(time.Second),
	}
	if noop, err := versions.ReplaceDraft(t.Context(), cmd); err != nil || !reflect.DeepEqual(noop, created) {
		t.Fatalf("no-op altered draft or contributors: %v", err)
	}
	assertCollectionCount(t, db, scriptVersionAuditsCollection, 4)
	wrongResource := cmd
	wrongResource.ResourceID = secondResource
	if _, err := versions.ReplaceDraft(t.Context(), wrongResource); !errors.Is(err, domain.ErrVersionNotFound) {
		t.Fatalf("wrong resource did not hide version: %v", err)
	}
	edits := []string{"{\"a\":1,\"b\":2}", "{\"b\":2,\"a\":1}"}
	editors := []shared.AuthID{"editor-a", "editor-b"}
	runRace(func(index int) error {
		edit := cmd
		edit.Content, _ = domain.NewContent(domain.JSONFormat, edits[index])
		edit.Actor = editors[index]
		_, err := versions.ReplaceDraft(t.Context(), edit)
		return err
	}, domain.ErrRevisionConflict)
	winner, err := versions.loadVersion(t.Context(), firstResource, created.ID)
	if err != nil || winner.Revision != 2 || len(winner.Contributors) != 2 ||
		winner.Contributors[0] != "creator" || winner.Contributors[1] != winner.UpdatedBy || winner.Validate() != nil {
		t.Fatalf("replacement snapshot is inconsistent: %v", err)
	}
	var audit scriptVersionAuditDocument
	if err := audits.FindOne(t.Context(), bson.M{"versionId": winner.ID, "revision": int64(2)}).Decode(&audit); err != nil ||
		audit.Digest != winner.Content.Digest || audit.Actor != winner.UpdatedBy ||
		!audit.ChangedAt.Equal(winner.UpdatedAt) || audit.Action != scriptDraftReplaced {
		t.Fatalf("version and audit differ: %v", err)
	}
	if _, err := versions.ReplaceDraft(t.Context(), cmd); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("stale replacement: %v", err)
	}

	if _, err := audits.InsertOne(t.Context(), scriptVersionAuditDocument{
		ResourceID: winner.ResourceID, VersionID: winner.ID, Revision: 3, Digest: winner.Content.Digest,
		Action: scriptDraftReplaced, Actor: "injected-audit-conflict", ChangedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	cmd.ExpectedRevision = 2
	if _, err := versions.ReplaceDraft(t.Context(), cmd); err == nil {
		t.Fatal("replacement committed when audit failed")
	}
	assertStored(winner)

	for _, state := range []domain.VersionState{domain.VersionSubmitted, domain.VersionApproved, domain.VersionRejected} {
		if _, err := documents.UpdateOne(t.Context(), bson.M{"versionId": winner.ID},
			bson.M{"$set": bson.M{"state": state, "revision": int64(3)}}); err != nil {
			t.Fatal(err)
		}
		cmd.ExpectedRevision = 3
		if _, err := versions.ReplaceDraft(t.Context(), cmd); !errors.Is(err, domain.ErrVersionNotDraft) {
			t.Fatalf("%s version accepted replacement: %v", state, err)
		}
	}
	if _, err := versions.CreateDraft(t.Context(), candidate(firstResource, created.Label)); !errors.Is(err, domain.ErrVersionLabelConflict) {
		t.Fatalf("rejected label reused: %v", err)
	}
}
