package mongo

import (
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func TestApplicationOperationsRepositoryIntegration_UCAPP028_TransactionalStateAndOutbox(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	application := createVersionTestApplication(t, db, "operations-admin", "operations-app")
	repository := NewApplicationOperationsRepository(db)

	initial, err := repository.Get(t.Context(), application.ID())
	if err != nil || initial.Status != domain.PlatformAvailabilityAvailable || initial.Revision != 1 || initial.LifecycleStatus != domain.ApplicationLifecycleActive {
		t.Fatalf("initial=%#v error=%v", initial, err)
	}
	at := time.Date(2026, 10, 6, 8, 0, 0, 0, time.UTC)
	suspendID := domain.ApplicationOperationEventID("0199b33c-d030-7abc-8abc-123456789012")
	suspended, err := repository.Set(t.Context(), application.ID(), shared.AuthID("platform-operator"), domain.PlatformAvailabilitySuspended, 1, 1, "security incident", suspendID, at)
	if err != nil || suspended.Status != domain.PlatformAvailabilitySuspended || suspended.Revision != 2 || suspended.LastOperationEventID == nil || *suspended.LastOperationEventID != suspendID || suspended.SuspendedAt == nil || !suspended.SuspendedAt.Equal(at) {
		t.Fatalf("suspended=%#v error=%v", suspended, err)
	}
	if count, _ := db.Collection(applicationOperationEventsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": application.ID().String()}); count != 1 {
		t.Fatalf("events=%d", count)
	}
	if count, _ := db.Collection(applicationOperationAlertOutboxCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": application.ID().String(), "status": "PENDING", "severity": "WARNING"}); count != 1 {
		t.Fatalf("alerts=%d", count)
	}

	staleID := domain.ApplicationOperationEventID("0199b33c-d031-7abc-8abc-123456789012")
	if _, err = repository.Set(t.Context(), application.ID(), "platform-operator", domain.PlatformAvailabilityAvailable, 1, 1, "stale restore", staleID, at.Add(time.Minute)); !errors.Is(err, domain.ErrApplicationAvailabilityConflict) {
		t.Fatalf("stale restore error=%v", err)
	}
	if count, _ := db.Collection(applicationOperationEventsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": application.ID().String()}); count != 1 {
		t.Fatalf("stale transaction leaked event: %d", count)
	}

	restoreID := domain.ApplicationOperationEventID("0199b33c-d032-7abc-8abc-123456789012")
	restored, err := repository.Set(t.Context(), application.ID(), "platform-operator", domain.PlatformAvailabilityAvailable, 1, 2, "incident resolved", restoreID, at.Add(2*time.Minute))
	if err != nil || restored.Status != domain.PlatformAvailabilityAvailable || restored.Revision != 3 || restored.RestoredAt == nil {
		t.Fatalf("restored=%#v error=%v", restored, err)
	}
}
