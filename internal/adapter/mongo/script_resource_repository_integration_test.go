package mongo

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
)

func TestScriptResourceRepositoryIntegration_UCAPP041_BRSCR001003(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo := NewScriptResourceRepository(db)
	resources := db.Collection(scriptResourcesCollectionName)
	audits := db.Collection(scriptResourceAuditsCollectionName)
	assertIndexNames(t, resources, []string{"_id_", scriptResourceIDIndexName, "ix_script_resources_created", scriptOwnerIndexName})
	assertIndexNames(t, audits, []string{"_id_", scriptResourceAuditIndexName})
	if _, err := resources.InsertOne(t.Context(), bson.D{{Key: "name", Value: "missing required fields"}}); err == nil {
		t.Fatal("incomplete resource passed validator")
	} else {
		assertDocumentValidationFailure(t, err)
	}

	at := time.Date(2026, 11, 6, 12, 0, 0, 123456789, time.UTC)
	const id domain.ResourceID = "018f0000-0000-7000-8000-000000000041"
	const otherID domain.ResourceID = "018f0000-0000-7000-8000-000000000042"
	const rolledBackID domain.ResourceID = "018f0000-0000-7000-8000-000000000043"
	metadata := domain.Metadata{Name: "Campus", Purpose: "Original purpose"}
	create := func(target domain.ResourceID) (domain.Resource, error) {
		value, err := domain.NewResource(target, metadata, "creator", at)
		if err != nil {
			t.Fatal(err)
		}
		return repo.CreateResource(t.Context(), value)
	}
	created, err := create(id)
	if err != nil || created.Revision != 1 || !created.CreatedAt.Equal(at.Truncate(time.Millisecond)) {
		t.Fatalf("create: %#v, %v", created, err)
	}
	if second, err := create(otherID); err != nil || second.ID == created.ID {
		t.Fatalf("same name rejected or ID reused: %#v, %v", second, err)
	}
	if _, err := create(id); err == nil {
		t.Fatal("duplicate resource identity accepted")
	}
	assertCollectionCount(t, db, scriptResourcesCollectionName, 2)
	assertCollectionCount(t, db, scriptResourceAuditsCollectionName, 2)

	cmd := port.ReplaceMetadata{
		ID: id, ExpectedRevision: 1, Metadata: metadata, Actor: "creator", At: at.Add(time.Second),
	}
	noop, err := repo.ReplaceResourceMetadata(t.Context(), cmd)
	if err != nil || noop != created {
		t.Fatalf("matching no-op changed stored resource: %#v, %v", noop, err)
	}
	assertCollectionCount(t, db, scriptResourceAuditsCollectionName, 2)

	start := make(chan struct{})
	results := make(chan error, 2)
	var writers sync.WaitGroup
	for _, name := range []string{"First edit", "Second edit"} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			<-start
			edit := cmd
			edit.Metadata.Name = name
			_, err := repo.ReplaceResourceMetadata(t.Context(), edit)
			results <- err
		}()
	}
	close(start)
	writers.Wait()
	close(results)
	committed, conflicts := classifyConcurrentResults(t, results, domain.ErrRevisionConflict)
	if committed != 1 || conflicts != 1 {
		t.Fatalf("same expected revision: committed=%d conflicts=%d", committed, conflicts)
	}
	var current scriptResourceDocument
	if err := resources.FindOne(t.Context(), bson.M{"resourceId": id}).Decode(&current); err != nil {
		t.Fatal(err)
	}
	var audit scriptResourceAuditDocument
	if err := audits.FindOne(t.Context(), bson.M{"resourceId": id, "revision": int64(2)}).Decode(&audit); err != nil {
		t.Fatal(err)
	}
	if current.Revision != 2 || current.CreatedBy != "creator" || current.UpdatedBy != "creator" ||
		current.Purpose != metadata.Purpose || (current.Name != "First edit" && current.Name != "Second edit") ||
		audit.Action != scriptResourceMetadataReplaced || audit.Actor != current.UpdatedBy ||
		!audit.ChangedAt.Equal(current.UpdatedAt) || !current.UpdatedAt.Equal(cmd.At.Truncate(time.Millisecond)) {
		t.Fatalf("inconsistent revision or audit: %#v %#v", current, audit)
	}

	cmd.Metadata = current.resource().Metadata
	if _, err := repo.ReplaceResourceMetadata(t.Context(), cmd); !errors.Is(err, domain.ErrRevisionConflict) {
		t.Fatalf("same content with stale revision: %v", err)
	}
	assertCollectionCount(t, db, scriptResourceAuditsCollectionName, 3)

	for _, badAudit := range []scriptResourceAuditDocument{
		{ResourceID: string(rolledBackID), Revision: 1, Action: scriptResourceCreated, Actor: "fixture", ChangedAt: at},
		{ResourceID: string(id), Revision: 3, Action: scriptResourceMetadataReplaced, Actor: "fixture", ChangedAt: at},
	} {
		if _, err := audits.InsertOne(t.Context(), badAudit); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := create(rolledBackID); err == nil {
		t.Fatal("creation succeeded without creation audit")
	}
	assertCollectionCount(t, db, scriptResourcesCollectionName, 2)
	cmd.ExpectedRevision = 2
	cmd.Metadata.Name = "Must roll back"
	if _, err := repo.ReplaceResourceMetadata(t.Context(), cmd); err == nil {
		t.Fatal("update succeeded without its audit")
	}
	var afterFailure scriptResourceDocument
	if err := resources.FindOne(t.Context(), bson.M{"resourceId": id}).Decode(&afterFailure); err != nil || afterFailure != current {
		t.Fatalf("audit failure left partial update: %#v, %v", afterFailure, err)
	}
	cmd.ID = rolledBackID
	if _, err := repo.ReplaceResourceMetadata(t.Context(), cmd); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("rolled back resource not reported missing: %v", err)
	}
}
