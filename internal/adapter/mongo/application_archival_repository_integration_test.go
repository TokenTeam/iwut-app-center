package mongo

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func TestApplicationArchivalRepositoryIntegration_UCAPP033_AtomicOCCNoOpAndAudit(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	application := createVersionTestApplication(t, db, "archive-admin", "archive-app")
	repository := NewApplicationArchivalRepository(db)
	admin := shared.AuthID("archive-admin")

	initial, err := repository.GetArchiveState(t.Context(), application.ID(), admin)
	if err != nil || initial.ArchiveStatus != domain.ApplicationArchiveUnarchived || initial.ArchiveRevision != 1 || initial.PlatformAvailabilityStatus != domain.PlatformAvailabilityAvailable {
		t.Fatalf("initial=%#v error=%v", initial, err)
	}
	if _, err = repository.GetArchiveState(t.Context(), application.ID(), "other"); !errors.Is(err, domain.ErrApplicationArchiveNotFound) {
		t.Fatalf("non-admin error=%v", err)
	}
	at := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	factoryCalls := 0
	factory := func() (domain.ApplicationArchiveEventID, time.Time, error) {
		factoryCalls++
		return "0199b33c-d041-7abc-8abc-123456789012", at, nil
	}
	archived, err := repository.SetArchiveState(t.Context(), application.ID(), admin, domain.ApplicationArchiveArchived, 1, factory)
	if err != nil || archived.ArchiveStatus != domain.ApplicationArchiveArchived || archived.ArchiveRevision != 2 || factoryCalls != 1 {
		t.Fatalf("archived=%#v calls=%d error=%v", archived, factoryCalls, err)
	}
	if count, _ := db.Collection(applicationArchiveEventsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": application.ID().String(), "actorAuthId": admin.String()}); count != 1 {
		t.Fatalf("events=%d", count)
	}
	factoryCalls = 0
	noOp, err := repository.SetArchiveState(t.Context(), application.ID(), admin, domain.ApplicationArchiveArchived, 2, factory)
	if err != nil || noOp.ArchiveRevision != 2 || factoryCalls != 0 {
		t.Fatalf("no-op=%#v calls=%d error=%v", noOp, factoryCalls, err)
	}
	if _, err = repository.SetArchiveState(t.Context(), application.ID(), admin, domain.ApplicationArchiveUnarchived, 1, factory); !errors.Is(err, domain.ErrApplicationArchiveConflict) {
		t.Fatalf("stale error=%v", err)
	}
	if count, _ := db.Collection(applicationArchiveEventsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": application.ID().String()}); count != 1 {
		t.Fatalf("failed transaction leaked event: %d", count)
	}
}

func TestApplicationArchivalRepositoryIntegration_BR_APP_052_ConcurrentCommandsUseOCCAndOneAuditEvent(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	application := createVersionTestApplication(t, db, "archive-race-admin", "archive-race-app")
	repository := NewApplicationArchivalRepository(db)
	admin := shared.AuthID("archive-race-admin")
	at := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	ids := []domain.ApplicationArchiveEventID{"0199b33c-d04a-7abc-8abc-123456789012", "0199b33c-d04b-7abc-8abc-123456789012"}

	start := make(chan struct{})
	errorsByCommand := make(chan error, len(ids))
	var ready sync.WaitGroup
	ready.Add(len(ids))
	for _, id := range ids {
		id := id
		go func() {
			ready.Done()
			<-start
			_, err := repository.SetArchiveState(t.Context(), application.ID(), admin, domain.ApplicationArchiveArchived, 1, func() (domain.ApplicationArchiveEventID, time.Time, error) {
				return id, at, nil
			})
			errorsByCommand <- err
		}()
	}
	ready.Wait()
	close(start)

	succeeded, conflicted := 0, 0
	for range ids {
		err := <-errorsByCommand
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, domain.ErrApplicationArchiveConflict):
			conflicted++
		default:
			t.Fatalf("concurrent archive error=%v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent results: succeeded=%d conflicted=%d", succeeded, conflicted)
	}
	state, err := repository.GetArchiveState(t.Context(), application.ID(), admin)
	if err != nil || state.ArchiveStatus != domain.ApplicationArchiveArchived || state.ArchiveRevision != 2 {
		t.Fatalf("state=%#v error=%v", state, err)
	}
	if count, err := db.Collection(applicationArchiveEventsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": application.ID().String()}); err != nil || count != 1 {
		t.Fatalf("events=%d error=%v", count, err)
	}
}

func TestApplicationArchivalRepositoryIntegration_BR_APP_053_057_CorruptionFailsClosed(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	application := createVersionTestApplication(t, db, "archive-corrupt-admin", "archive-corrupt-app")
	repository := NewApplicationArchivalRepository(db)
	_, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": application.ID().String()}, bson.M{"$set": bson.M{"archiveRevision": int64(2), "lastArchiveEventId": "0199b33c-d042-7abc-8abc-123456789012"}}, options.UpdateOne().SetBypassDocumentValidation(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.GetArchiveState(t.Context(), application.ID(), "archive-corrupt-admin"); !errors.Is(err, domain.ErrApplicationArchiveInconsistent) {
		t.Fatalf("corrupt state error=%v", err)
	}
}

func TestApplicationArchivalRepositoryIntegration_BR_APP_051_055_OrthogonalPlatformGateAndUnarchive(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	application := createVersionTestApplication(t, db, "archive-orthogonal-admin", "archive-orthogonal-app")
	at := time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC)
	if _, err := NewApplicationOperationsRepository(db).Set(t.Context(), application.ID(), "platform-operator", domain.PlatformAvailabilitySuspended, 1, 1, "independent platform gate", "0199b33c-d046-7abc-8abc-123456789012", at); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationArchivalRepository(db)
	archived, err := repository.SetArchiveState(t.Context(), application.ID(), "archive-orthogonal-admin", domain.ApplicationArchiveArchived, 1, func() (domain.ApplicationArchiveEventID, time.Time, error) {
		return "0199b33c-d047-7abc-8abc-123456789012", at.Add(time.Second), nil
	})
	if err != nil || archived.PlatformAvailabilityStatus != domain.PlatformAvailabilitySuspended || archived.ArchiveStatus != domain.ApplicationArchiveArchived {
		t.Fatalf("archived=%#v error=%v", archived, err)
	}
	if _, err = NewApplicationOperationsRepository(db).Set(t.Context(), application.ID(), "platform-operator", domain.PlatformAvailabilityAvailable, 1, 2, "platform restored", "0199b33c-d048-7abc-8abc-123456789012", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	stillArchived, err := repository.GetArchiveState(t.Context(), application.ID(), "archive-orthogonal-admin")
	if err != nil || stillArchived.PlatformAvailabilityStatus != domain.PlatformAvailabilityAvailable || stillArchived.ArchiveStatus != domain.ApplicationArchiveArchived {
		t.Fatalf("after platform restore=%#v error=%v", stillArchived, err)
	}
	unarchived, err := repository.SetArchiveState(t.Context(), application.ID(), "archive-orthogonal-admin", domain.ApplicationArchiveUnarchived, 2, func() (domain.ApplicationArchiveEventID, time.Time, error) {
		return "0199b33c-d049-7abc-8abc-123456789013", at.Add(3 * time.Second), nil
	})
	if err != nil || unarchived.ArchiveStatus != domain.ApplicationArchiveUnarchived || unarchived.ArchiveRevision != 3 {
		t.Fatalf("unarchived=%#v error=%v", unarchived, err)
	}
}

func TestApplicationArchivalMigrationIntegration_BR_APP_057_StrictValidatorsAndIndexes(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	application := createVersionTestApplication(t, db, "archive-schema-admin", "archive-schema-app")
	if _, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": application.ID().String()}, bson.M{"$set": bson.M{"archiveStatus": "UNKNOWN"}}); err == nil {
		t.Fatal("application validator accepted unknown archive status")
	}
	if _, err := db.Collection(applicationArchiveEventsCollectionName).InsertOne(t.Context(), bson.M{"eventId": "0199b33c-d045-7abc-8abc-123456789012", "applicationId": application.ID().String(), "action": "BAD", "actorAuthId": "admin", "beforeStatus": "UNARCHIVED", "afterStatus": "ARCHIVED", "beforeRevision": int64(1), "afterRevision": int64(2), "occurredAt": time.Now().UTC()}); err == nil {
		t.Fatal("archive event validator accepted unknown action")
	}
	indexes, err := db.Collection(applicationArchiveEventsCollectionName).Indexes().List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer indexes.Close(t.Context())
	names := map[string]bool{}
	for indexes.Next(t.Context()) {
		var index bson.M
		if err := indexes.Decode(&index); err != nil {
			t.Fatal(err)
		}
		if name, ok := index["name"].(string); ok {
			names[name] = true
		}
	}
	if !names["uq_application_archive_event_id"] || !names["ix_application_archive_audit"] {
		t.Fatalf("archive indexes=%v", names)
	}
}
