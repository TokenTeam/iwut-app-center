package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	cd "iwut-app-center/internal/catalog/domain"
	"time"
)

type myTestArchiveEvent struct {
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

func (r *MyTestApplicationRepository) onlineApps(ctx context.Context, apps []applicationDocument) (map[string]bool, error) {
	result := map[string]bool{}
	operationIDs, archiveIDs := []string{}, []string{}
	seen := map[string]bool{}
	for _, d := range apps {
		if seen[d.ID] {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		seen[d.ID] = true
		if _, err := availabilityFromDocument(d); err != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		if _, err := archiveStateFromDocument(d); err != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		if d.LastOperationEventID != nil {
			operationIDs = append(operationIDs, *d.LastOperationEventID)
		}
		if d.LastArchiveEventID != nil {
			archiveIDs = append(archiveIDs, *d.LastArchiveEventID)
		}
	}
	operations := map[string]applicationOperationEventDocument{}
	archives := map[string]myTestArchiveEvent{}
	if len(operationIDs) > 0 {
		docs, err := pureTestDocuments[applicationOperationEventDocument](ctx, r.database, applicationOperationEventsCollectionName, bson.M{"eventId": bson.M{"$in": operationIDs}}, nil)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if _, ok := operations[d.EventID]; ok {
				return nil, cd.ErrMyTestApplicationStateInconsistent
			}
			operations[d.EventID] = d
		}
	}
	if len(archiveIDs) > 0 {
		docs, err := pureTestDocuments[myTestArchiveEvent](ctx, r.database, applicationArchiveEventsCollectionName, bson.M{"eventId": bson.M{"$in": archiveIDs}}, nil)
		if err != nil {
			return nil, err
		}
		for _, d := range docs {
			if _, ok := archives[d.EventID]; ok {
				return nil, cd.ErrMyTestApplicationStateInconsistent
			}
			archives[d.EventID] = d
		}
	}
	for _, d := range apps {
		if d.LastOperationEventID != nil {
			e, ok := operations[*d.LastOperationEventID]
			action, before := "RESTORE", "SUSPENDED"
			if d.PlatformAvailabilityStatus == "SUSPENDED" {
				action, before = "SUSPEND", "AVAILABLE"
			}
			if !ok || e.ApplicationID != d.ID || e.Action != action || e.BeforeStatus != before || e.AfterStatus != d.PlatformAvailabilityStatus || e.BeforeRevision != d.PlatformAvailabilityRevision-1 || e.AfterRevision != d.PlatformAvailabilityRevision {
				return nil, cd.ErrMyTestApplicationStateInconsistent
			}
		}
		if d.LastArchiveEventID != nil {
			e, ok := archives[*d.LastArchiveEventID]
			action, before := "UNARCHIVE", "ARCHIVED"
			if d.ArchiveStatus == "ARCHIVED" {
				action, before = "ARCHIVE", "UNARCHIVED"
			}
			if !ok || e.ActorAuthID == "" || e.ApplicationID != d.ID || e.Action != action || e.BeforeStatus != before || e.AfterStatus != d.ArchiveStatus || e.BeforeRevision != d.ArchiveRevision-1 || e.AfterRevision != d.ArchiveRevision {
				return nil, cd.ErrMyTestApplicationStateInconsistent
			}
		}
		result[d.ID] = d.LifecycleStatus == "ACTIVE" && d.PlatformAvailabilityStatus == "AVAILABLE" && d.ArchiveStatus == "UNARCHIVED"
	}
	return result, nil
}
