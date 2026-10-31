package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"iwut-app-center/internal/adapter/generator"
	mongoadapter "iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

type applicationOperationTestClock struct{ at time.Time }

func (c *applicationOperationTestClock) Now() time.Time { return c.at }

func TestIntegration_UCAPP028_BR_APP_036_OperationTimeReadback(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	_, db := e2eIsolatedDatabase(t, ctx, true)
	appID, _ := shared.ParseApplicationID("0199b33c-d030-7abc-8abc-123456789012")
	name, err := domain.NewApplicationName("Operation_Time_Readback")
	if err != nil {
		t.Fatal(err)
	}
	application, err := domain.NewApplication(appID, name, "time-test-owner", time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := mongoadapter.NewApplicationRepository(db).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	clock := &applicationOperationTestClock{}
	handlers := usecase.NewApplicationOperationsHandlers(mongoadapter.NewApplicationOperationsRepository(db), generator.NewApplicationOperationUUIDv7Generator(), clock)
	identity := shared.TrustedIdentity{AuthID: "time-test-operator", Permissions: []string{usecase.PermissionApplicationSuspend, usecase.PermissionApplicationRestore}}
	var suspendedAt *time.Time
	for i, tc := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"suspend", time.Date(2026, 10, 30, 1, 1, 0, 123456789, time.UTC), time.Date(2026, 10, 30, 1, 1, 0, 123000000, time.UTC)},
		{"restore", time.Date(2026, 10, 30, 1, 2, 0, 987654321, time.UTC), time.Date(2026, 10, 30, 1, 2, 0, 987000000, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock.at = tc.now
			command := usecase.ApplicationOperationCommand{ApplicationID: appID, ExpectedLifecycleRevision: 1, ExpectedPlatformAvailabilityRevision: int64(i + 1), Reason: "time precision regression"}
			change := handlers.Suspend
			wantStatus := domain.PlatformAvailabilitySuspended
			if i == 1 {
				change, wantStatus = handlers.Restore, domain.PlatformAvailabilityAvailable
			}
			ack, err := change(ctx, identity, command)
			if err != nil {
				t.Fatal(err)
			}
			if ack.ApplicationID != appID || ack.Status != wantStatus || ack.Revision != int64(i+2) || ack.LifecycleRevision != 1 || ack.LastOperationEventID == nil {
				t.Fatal("command returned an unexpected operation identity or revision")
			}
			operationAt := ack.SuspendedAt
			if i == 1 {
				operationAt = ack.RestoredAt
				if suspendedAt == nil || ack.SuspendedAt == nil || !ack.SuspendedAt.Equal(*suspendedAt) {
					t.Error("restore changed the previous suspension time")
				}
			} else {
				suspendedAt = ack.SuspendedAt
			}
			if operationAt == nil || !operationAt.Equal(tc.want) {
				t.Error("command ACK did not use the exact persisted millisecond time")
			}
			readback, err := handlers.Get(ctx, identity, appID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(ack, readback) {
				t.Error("same-event/revision GET differs from the complete command ACK")
			}
			var event struct {
				OccurredAt    time.Time `bson:"occurredAt"`
				AfterRevision int64     `bson:"afterRevision"`
				Action        string    `bson:"action"`
			}
			filter := bson.M{"applicationId": appID.String(), "eventId": ack.LastOperationEventID.String()}
			if err := db.Collection("application_operation_events").FindOne(ctx, filter).Decode(&event); err != nil {
				t.Fatal(err)
			}
			var alert struct {
				CreatedAt time.Time `bson:"createdAt"`
				Action    string    `bson:"action"`
			}
			if err := db.Collection("application_operation_alert_outbox").FindOne(ctx, filter).Decode(&alert); err != nil {
				t.Fatal(err)
			}
			if event.AfterRevision != ack.Revision || event.Action != alert.Action || !event.OccurredAt.Equal(tc.want) || !alert.CreatedAt.Equal(tc.want) || operationAt == nil || !event.OccurredAt.Equal(*operationAt) {
				t.Error("ACK, event and outbox do not share one persisted operation time/revision")
			}
			// A replay with the old revision must neither change state nor append
			// another event/outbox, even when the Clock has advanced.
			clock.at = tc.now.Add(time.Second)
			if _, err := change(ctx, identity, command); !errors.Is(err, domain.ErrApplicationAvailabilityConflict) {
				t.Errorf("stale command error = %v", err)
			}
			for _, collection := range []string{"application_operation_events", "application_operation_alert_outbox"} {
				count, err := db.Collection(collection).CountDocuments(ctx, bson.M{"applicationId": appID.String()})
				if err != nil || count != int64(i+1) {
					t.Errorf("%s unexpected operation count: count=%d error=%v", collection, count, err)
				}
			}
			afterReplay, err := handlers.Get(ctx, identity, appID)
			if err != nil || !reflect.DeepEqual(afterReplay, readback) {
				t.Error("stale command changed the persisted state")
			}
		})
	}
}
