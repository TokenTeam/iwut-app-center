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

type ApplicationArchivalRepository struct{ database *m.Database }

var _ port.ApplicationArchivalRepository = (*ApplicationArchivalRepository)(nil)

func NewApplicationArchivalRepository(database *m.Database) *ApplicationArchivalRepository {
	return &ApplicationArchivalRepository{database: database}
}

func archiveStateFromDocument(d applicationDocument) (domain.ApplicationArchiveState, error) {
	appID, ok := shared.ParseApplicationID(d.ID)
	lifecycle := domain.ApplicationLifecycleStatus(d.LifecycleStatus)
	availability := domain.PlatformAvailabilityStatus(d.PlatformAvailabilityStatus)
	archive := domain.ApplicationArchiveStatus(d.ArchiveStatus)
	if !ok || d.ArchiveRevision < 1 ||
		(lifecycle != domain.ApplicationLifecycleActive && lifecycle != domain.ApplicationLifecycleClosing && lifecycle != domain.ApplicationLifecycleClosed) ||
		(availability != domain.PlatformAvailabilityAvailable && availability != domain.PlatformAvailabilitySuspended) ||
		(archive != domain.ApplicationArchiveUnarchived && archive != domain.ApplicationArchiveArchived) ||
		(d.ArchiveRevision == 1 && d.LastArchiveEventID != nil) || (d.ArchiveRevision > 1 && d.LastArchiveEventID == nil) {
		return domain.ApplicationArchiveState{}, domain.ErrApplicationArchiveInconsistent
	}
	if d.LastArchiveEventID != nil && !shared.IsUUIDv7(*d.LastArchiveEventID) {
		return domain.ApplicationArchiveState{}, domain.ErrApplicationArchiveInconsistent
	}
	return domain.ApplicationArchiveState{ApplicationID: appID, LifecycleStatus: lifecycle, PlatformAvailabilityStatus: availability, ArchiveStatus: archive, ArchiveRevision: d.ArchiveRevision}, nil
}

func (r *ApplicationArchivalRepository) validateLatestArchiveEvent(ctx context.Context, d applicationDocument, current domain.ApplicationArchiveState) error {
	if current.ArchiveRevision == 1 {
		return nil
	}
	var event struct {
		EventID        string    `bson:"eventId"`
		ApplicationID  string    `bson:"applicationId"`
		Action         string    `bson:"action"`
		ActorAuthID    string    `bson:"actorAuthId"`
		BeforeStatus   string    `bson:"beforeStatus"`
		AfterStatus    string    `bson:"afterStatus"`
		BeforeRevision int64     `bson:"beforeRevision"`
		AfterRevision  int64     `bson:"afterRevision"`
		OccurredAt     time.Time `bson:"occurredAt"`
	}
	err := r.database.Collection(applicationArchiveEventsCollectionName).FindOne(ctx, bson.M{"eventId": *d.LastArchiveEventID}).Decode(&event)
	if err != nil {
		if errors.Is(err, m.ErrNoDocuments) {
			return domain.ErrApplicationArchiveInconsistent
		}
		return err
	}
	wantAction, wantBefore := "UNARCHIVE", string(domain.ApplicationArchiveArchived)
	if current.ArchiveStatus == domain.ApplicationArchiveArchived {
		wantAction, wantBefore = "ARCHIVE", string(domain.ApplicationArchiveUnarchived)
	}
	if event.EventID != *d.LastArchiveEventID || event.ApplicationID != d.ID || event.Action != wantAction || event.ActorAuthID == "" || event.BeforeStatus != wantBefore || event.AfterStatus != string(current.ArchiveStatus) || event.BeforeRevision != current.ArchiveRevision-1 || event.AfterRevision != current.ArchiveRevision || event.OccurredAt.IsZero() {
		return domain.ErrApplicationArchiveInconsistent
	}
	return nil
}

func (r *ApplicationArchivalRepository) validateArchiveState(ctx context.Context, d applicationDocument, current domain.ApplicationArchiveState) error {
	availability, err := availabilityFromDocument(d)
	if err != nil {
		return domain.ErrApplicationArchiveInconsistent
	}
	if err = (&ApplicationOperationsRepository{database: r.database}).validateLatestEvent(ctx, d, availability); err != nil {
		if errors.Is(err, domain.ErrApplicationOperationStateInconsistent) {
			return domain.ErrApplicationArchiveInconsistent
		}
		return err
	}
	if availability.LifecycleStatus != current.LifecycleStatus || availability.Status != current.PlatformAvailabilityStatus {
		return domain.ErrApplicationArchiveInconsistent
	}
	return r.validateLatestArchiveEvent(ctx, d, current)
}

func (r *ApplicationArchivalRepository) GetArchiveState(ctx context.Context, appID shared.ApplicationID, actor shared.AuthID) (domain.ApplicationArchiveState, error) {
	if r == nil || r.database == nil {
		return domain.ApplicationArchiveState{}, domain.ErrApplicationArchiveUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return domain.ApplicationArchiveState{}, archivePersistenceError(err)
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var d applicationDocument
		err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String(), "adminId": actor.String()}).Decode(&d)
		if errors.Is(err, m.ErrNoDocuments) {
			return nil, domain.ErrApplicationArchiveNotFound
		}
		if err != nil {
			return nil, err
		}
		state, err := archiveStateFromDocument(d)
		if err != nil {
			return nil, err
		}
		if err = r.validateArchiveState(tx, d, state); err != nil {
			return nil, err
		}
		return state, nil
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return domain.ApplicationArchiveState{}, archivePersistenceError(err)
	}
	return value.(domain.ApplicationArchiveState), nil
}

func (r *ApplicationArchivalRepository) SetArchiveState(ctx context.Context, appID shared.ApplicationID, actor shared.AuthID, target domain.ApplicationArchiveStatus, expected int64, eventFactory port.ApplicationArchiveEventFactory) (domain.ApplicationArchiveState, error) {
	if r == nil || r.database == nil {
		return domain.ApplicationArchiveState{}, domain.ErrApplicationArchiveUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return domain.ApplicationArchiveState{}, archivePersistenceError(err)
	}
	defer session.EndSession(ctx)
	var result domain.ApplicationArchiveState
	_, err = session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		var d applicationDocument
		err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String()}).Decode(&d)
		if errors.Is(err, m.ErrNoDocuments) {
			return nil, domain.ErrApplicationArchiveNotFound
		}
		if err != nil {
			return nil, err
		}
		if d.AdminID != actor.String() {
			return nil, domain.ErrApplicationArchiveNotFound
		}
		current, err := archiveStateFromDocument(d)
		if err != nil {
			return nil, err
		}
		if err = r.validateArchiveState(tx, d, current); err != nil {
			return nil, err
		}
		if current.LifecycleStatus != domain.ApplicationLifecycleActive {
			return nil, domain.ErrApplicationNotActive
		}
		if current.ArchiveRevision != expected {
			return nil, domain.ErrApplicationArchiveConflict
		}
		if current.ArchiveStatus == target {
			result = current
			return nil, nil
		}
		if eventFactory == nil {
			return nil, domain.ErrApplicationArchiveUnavailable
		}
		eventID, occurredAt, err := eventFactory()
		if err != nil || !eventID.IsValid() || occurredAt.IsZero() {
			return nil, domain.ErrApplicationArchiveUnavailable
		}
		action := "UNARCHIVE"
		if target == domain.ApplicationArchiveArchived {
			action = "ARCHIVE"
		}
		newRevision := current.ArchiveRevision + 1
		event := bson.M{"eventId": eventID.String(), "applicationId": appID.String(), "action": action, "actorAuthId": actor.String(), "beforeStatus": string(current.ArchiveStatus), "afterStatus": string(target), "beforeRevision": current.ArchiveRevision, "afterRevision": newRevision, "occurredAt": occurredAt}
		if _, err = r.database.Collection(applicationArchiveEventsCollectionName).InsertOne(tx, event); err != nil {
			return nil, err
		}
		update, err := r.database.Collection(applicationsCollectionName).UpdateOne(tx,
			bson.M{"id": d.ID, "adminId": actor.String(), "lifecycleStatus": string(domain.ApplicationLifecycleActive), "archiveStatus": string(current.ArchiveStatus), "archiveRevision": expected, "coordinationRevision": d.CoordinationRevision},
			bson.M{"$set": bson.M{"archiveStatus": string(target), "archiveRevision": newRevision, "lastArchiveEventId": eventID.String()}, "$inc": bson.M{"coordinationRevision": int64(1)}})
		if err != nil {
			return nil, err
		}
		if update.MatchedCount != 1 {
			return nil, domain.ErrApplicationArchiveConflict
		}
		d.ArchiveStatus, d.ArchiveRevision, d.LastArchiveEventID = string(target), newRevision, ptrString(eventID.String())
		result, err = archiveStateFromDocument(d)
		return nil, err
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return domain.ApplicationArchiveState{}, archivePersistenceError(err)
	}
	return result, nil
}

func archivePersistenceError(err error) error {
	for _, business := range []error{domain.ErrApplicationArchiveNotFound, domain.ErrApplicationArchiveConflict, domain.ErrApplicationNotActive, domain.ErrApplicationArchiveInconsistent, domain.ErrApplicationArchiveUnavailable} {
		if errors.Is(err, business) {
			return business
		}
	}
	return fmt.Errorf("%w: persistence failure", domain.ErrApplicationArchiveUnavailable)
}
