package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type ApplicationOperationsRepository struct{ database *m.Database }

type applicationOperationEventDocument struct {
	EventID        string    `bson:"eventId"`
	ApplicationID  string    `bson:"applicationId"`
	Action         string    `bson:"action"`
	BeforeStatus   string    `bson:"beforeStatus"`
	AfterStatus    string    `bson:"afterStatus"`
	BeforeRevision int64     `bson:"beforeRevision"`
	AfterRevision  int64     `bson:"afterRevision"`
	OccurredAt     time.Time `bson:"occurredAt"`
}

var _ port.ApplicationOperationsRepository = (*ApplicationOperationsRepository)(nil)

func NewApplicationOperationsRepository(database *m.Database) *ApplicationOperationsRepository {
	return &ApplicationOperationsRepository{database: database}
}

func availabilityFromDocument(d applicationDocument) (domain.ApplicationPlatformAvailability, error) {
	appID, ok := shared.ParseApplicationID(d.ID)
	status := domain.PlatformAvailabilityStatus(d.PlatformAvailabilityStatus)
	lifecycle := domain.ApplicationLifecycleStatus(d.LifecycleStatus)
	if !ok || d.LifecycleRevision < 1 || d.PlatformAvailabilityRevision < 1 ||
		(lifecycle != domain.ApplicationLifecycleActive && lifecycle != domain.ApplicationLifecycleClosing && lifecycle != domain.ApplicationLifecycleClosed) ||
		(status != domain.PlatformAvailabilityAvailable && status != domain.PlatformAvailabilitySuspended) {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationStateInconsistent
	}
	value := domain.ApplicationPlatformAvailability{ApplicationID: appID, LifecycleStatus: lifecycle, LifecycleRevision: d.LifecycleRevision, Status: status, Revision: d.PlatformAvailabilityRevision, SuspendedAt: d.SuspendedAt, RestoredAt: d.RestoredAt}
	if d.LastOperationEventID != nil {
		id := domain.ApplicationOperationEventID(*d.LastOperationEventID)
		if !id.IsValid() {
			return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationStateInconsistent
		}
		value.LastOperationEventID = &id
	}
	if status == domain.PlatformAvailabilitySuspended && (d.SuspendedAt == nil || d.SuspendedAt.IsZero()) {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationStateInconsistent
	}
	if d.PlatformAvailabilityRevision == 1 && (d.LastOperationEventID != nil || d.SuspendedAt != nil || d.RestoredAt != nil) {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationStateInconsistent
	}
	if d.PlatformAvailabilityRevision > 1 && d.LastOperationEventID == nil {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationStateInconsistent
	}
	if status == domain.PlatformAvailabilityAvailable && d.PlatformAvailabilityRevision > 1 && (d.RestoredAt == nil || d.RestoredAt.IsZero()) {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationStateInconsistent
	}
	return value, nil
}

func (r *ApplicationOperationsRepository) validateLatestEvent(ctx context.Context, d applicationDocument, current domain.ApplicationPlatformAvailability) error {
	if current.Revision == 1 {
		return nil
	}
	var event applicationOperationEventDocument
	err := r.database.Collection(applicationOperationEventsCollectionName).FindOne(ctx, bson.M{"eventId": *d.LastOperationEventID}).Decode(&event)
	if errors.Is(err, m.ErrNoDocuments) {
		return domain.ErrApplicationOperationStateInconsistent
	}
	if err != nil {
		return err
	}
	wantAction := "RESTORE"
	wantBefore := domain.PlatformAvailabilitySuspended
	if current.Status == domain.PlatformAvailabilitySuspended {
		wantAction, wantBefore = "SUSPEND", domain.PlatformAvailabilityAvailable
	}
	if event.EventID != *d.LastOperationEventID || event.ApplicationID != d.ID || event.Action != wantAction || event.BeforeStatus != string(wantBefore) || event.AfterStatus != string(current.Status) || event.BeforeRevision != current.Revision-1 || event.AfterRevision != current.Revision || event.OccurredAt.IsZero() {
		return domain.ErrApplicationOperationStateInconsistent
	}
	return nil
}

func (r *ApplicationOperationsRepository) Get(ctx context.Context, appID shared.ApplicationID) (domain.ApplicationPlatformAvailability, error) {
	if r == nil || r.database == nil {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return domain.ApplicationPlatformAvailability{}, operationPersistenceError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var d applicationDocument
		err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String()}).Decode(&d)
		if errors.Is(err, m.ErrNoDocuments) {
			return nil, domain.ErrApplicationNotFound
		}
		if err != nil {
			return nil, err
		}
		current, err := availabilityFromDocument(d)
		if err != nil {
			return nil, err
		}
		if err := r.validateLatestEvent(tx, d, current); err != nil {
			return nil, err
		}
		return current, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return domain.ApplicationPlatformAvailability{}, operationPersistenceError(err)
	}
	return result.(domain.ApplicationPlatformAvailability), nil
}

func (r *ApplicationOperationsRepository) Set(ctx context.Context, appID shared.ApplicationID, actor shared.AuthID, target domain.PlatformAvailabilityStatus, expectedLifecycle, expectedAvailability int64, reason string, eventID domain.ApplicationOperationEventID, now time.Time) (domain.ApplicationPlatformAvailability, error) {
	if r == nil || r.database == nil {
		return domain.ApplicationPlatformAvailability{}, domain.ErrApplicationOperationUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return domain.ApplicationPlatformAvailability{}, operationPersistenceError(err)
	}
	defer session.EndSession(ctx)
	var result domain.ApplicationPlatformAvailability
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var d applicationDocument
		err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String()}).Decode(&d)
		if errors.Is(err, m.ErrNoDocuments) {
			return nil, domain.ErrApplicationNotFound
		}
		if err != nil {
			return nil, err
		}
		current, err := availabilityFromDocument(d)
		if err != nil {
			return nil, err
		}
		if err := r.validateLatestEvent(tx, d, current); err != nil {
			return nil, err
		}
		if current.LifecycleStatus != domain.ApplicationLifecycleActive {
			return nil, domain.ErrApplicationNotActive
		}
		if current.LifecycleRevision != expectedLifecycle || current.Revision != expectedAvailability {
			return nil, domain.ErrApplicationAvailabilityConflict
		}
		if current.Status == target {
			return nil, domain.ErrApplicationAvailabilityConflict
		}
		newRevision := current.Revision + 1
		action, severity := "RESTORE", "INFO"
		if target == domain.PlatformAvailabilitySuspended {
			action, severity = "SUSPEND", "WARNING"
		}
		event := bson.M{"eventId": eventID.String(), "applicationId": appID.String(), "actorAuthId": actor.String(), "action": action, "reason": reason, "sourceLifecycleRevision": current.LifecycleRevision, "beforeStatus": string(current.Status), "afterStatus": string(target), "beforeRevision": current.Revision, "afterRevision": newRevision, "occurredAt": now}
		if _, err := r.database.Collection(applicationOperationEventsCollectionName).InsertOne(tx, event); err != nil {
			return nil, err
		}
		alert := bson.M{"eventId": eventID.String(), "applicationId": appID.String(), "action": action, "severity": severity, "status": "PENDING", "createdAt": now}
		if _, err := r.database.Collection(applicationOperationAlertOutboxCollectionName).InsertOne(tx, alert); err != nil {
			return nil, err
		}
		set := bson.M{"platformAvailabilityStatus": string(target), "platformAvailabilityRevision": newRevision, "lastPlatformOperationEventId": eventID.String()}
		if target == domain.PlatformAvailabilitySuspended {
			set["suspendedAt"] = now
		} else {
			set["restoredAt"] = now
		}
		update, err := r.database.Collection(applicationsCollectionName).UpdateOne(tx,
			bson.M{"id": d.ID, "lifecycleStatus": string(domain.ApplicationLifecycleActive), "lifecycleRevision": expectedLifecycle, "platformAvailabilityStatus": string(current.Status), "platformAvailabilityRevision": expectedAvailability, "coordinationRevision": d.CoordinationRevision},
			bson.M{"$set": set, "$inc": bson.M{"coordinationRevision": int64(1)}})
		if err != nil {
			return nil, err
		}
		if update.MatchedCount != 1 {
			return nil, domain.ErrApplicationAvailabilityConflict
		}
		d.PlatformAvailabilityStatus, d.PlatformAvailabilityRevision, d.LastOperationEventID = string(target), newRevision, ptrString(eventID.String())
		if target == domain.PlatformAvailabilitySuspended {
			d.SuspendedAt = &now
		} else {
			d.RestoredAt = &now
		}
		result, err = availabilityFromDocument(d)
		return nil, err
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return domain.ApplicationPlatformAvailability{}, operationPersistenceError(err)
	}
	return result, nil
}

func ptrString(value string) *string { return &value }

func operationPersistenceError(err error) error {
	for _, business := range []error{domain.ErrApplicationNotFound, domain.ErrApplicationAvailabilityConflict, domain.ErrApplicationNotActive, domain.ErrApplicationOperationStateInconsistent} {
		if errors.Is(err, business) {
			return business
		}
	}
	return fmt.Errorf("%w: persistence failure", domain.ErrApplicationOperationUnavailable)
}
