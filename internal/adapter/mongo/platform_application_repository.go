package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

const platformApplicationScanBudget = 500

type PlatformApplicationQueryRepository struct {
	db  *dm.Database
	key PlatformApplicationCursorKey
}

func NewPlatformApplicationQueryRepository(db *dm.Database, key PlatformApplicationCursorKey) *PlatformApplicationQueryRepository {
	return &PlatformApplicationQueryRepository{db, key}
}

var _ port.PlatformApplicationQueryRepository = (*PlatformApplicationQueryRepository)(nil)

// Select only the contract's core facts and its internal comparison key. A
// malformed private child resource is neither loaded nor exposed by this query.
var platformApplicationProjection = bson.M{"_id": 0, "id": 1, "name": 1, "nameKey": 1, "adminId": 1, "createdAt": 1, "ownershipRevision": 1, "lifecycleStatus": 1, "lifecycleRevision": 1, "platformAvailabilityStatus": 1, "platformAvailabilityRevision": 1, "archiveStatus": 1, "archiveRevision": 1}

func platformApplicationCore(raw bson.Raw) (domain.PlatformApplicationSummary, error) {
	var empty domain.PlatformApplicationSummary
	for _, field := range []string{"id", "name", "nameKey", "adminId", "lifecycleStatus", "platformAvailabilityStatus", "archiveStatus"} {
		if raw.Lookup(field).Type != bson.TypeString {
			return empty, domain.ErrPlatformApplicationStateInconsistent
		}
	}
	for _, field := range []string{"ownershipRevision", "lifecycleRevision", "platformAvailabilityRevision", "archiveRevision"} {
		if raw.Lookup(field).Type != bson.TypeInt64 {
			return empty, domain.ErrPlatformApplicationStateInconsistent
		}
	}
	if raw.Lookup("createdAt").Type != bson.TypeDateTime {
		return empty, domain.ErrPlatformApplicationStateInconsistent
	}
	var d applicationDocument
	if bson.Unmarshal(raw, &d) != nil {
		return empty, domain.ErrPlatformApplicationStateInconsistent
	}
	n, err := domain.NewApplicationName(d.Name)
	if err != nil || n.Key() != d.NameKey {
		return empty, domain.ErrPlatformApplicationStateInconsistent
	}
	s := domain.PlatformApplicationSummary{ApplicationID: shared.ApplicationID(d.ID), Name: d.Name, AdminAuthID: shared.AuthID(d.AdminID), CreatedAt: d.CreatedAt, OwnershipRevision: d.OwnershipRevision, LifecycleStatus: domain.ApplicationLifecycleStatus(d.LifecycleStatus), LifecycleRevision: d.LifecycleRevision, PlatformAvailabilityStatus: domain.PlatformAvailabilityStatus(d.PlatformAvailabilityStatus), PlatformAvailabilityRevision: d.PlatformAvailabilityRevision, ArchiveStatus: domain.ApplicationArchiveStatus(d.ArchiveStatus), ArchiveRevision: d.ArchiveRevision}
	return s, s.Validate()
}

func platformQueryStorageError(err error) error {
	for _, known := range []error{domain.ErrPlatformApplicationNotFound, domain.ErrPlatformApplicationStateInconsistent} {
		if errors.Is(err, known) {
			return known
		}
	}
	return fmt.Errorf("%w: %w", domain.ErrPlatformApplicationUnavailable, err)
}
func platformQuerySnapshot[T any](ctx context.Context, r *PlatformApplicationQueryRepository, fn func(context.Context) (T, error)) (T, error) {
	var empty T
	if r == nil || r.db == nil {
		return empty, domain.ErrPlatformApplicationUnavailable
	}
	s, err := r.db.Client().StartSession()
	if err != nil {
		return empty, platformQueryStorageError(err)
	}
	defer s.EndSession(ctx)
	value, err := s.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return empty, platformQueryStorageError(err)
	}
	return value.(T), nil
}
func (r *PlatformApplicationQueryRepository) GetPlatformApplicationSummary(ctx context.Context, id shared.ApplicationID) (domain.PlatformApplicationSummary, error) {
	return platformQuerySnapshot(ctx, r, func(tx context.Context) (domain.PlatformApplicationSummary, error) {
		raw, err := r.db.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": id.String()}, options.FindOne().SetProjection(platformApplicationProjection)).Raw()
		if errors.Is(err, dm.ErrNoDocuments) {
			return domain.PlatformApplicationSummary{}, domain.ErrPlatformApplicationNotFound
		}
		if err != nil {
			return domain.PlatformApplicationSummary{}, err
		}
		return platformApplicationCore(raw)
	})
}
func (r *PlatformApplicationQueryRepository) SearchPlatformApplications(ctx context.Context, caller shared.AuthID, q domain.PlatformApplicationQuery, at time.Time) (domain.PlatformApplicationPage, error) {
	if r == nil || !r.key.valid {
		return domain.PlatformApplicationPage{}, domain.ErrPlatformApplicationUnavailable
	}
	pos, err := r.decodeCursor(q.PageToken, caller, q)
	if err != nil {
		return domain.PlatformApplicationPage{}, err
	}
	return platformQuerySnapshot(ctx, r, func(tx context.Context) (domain.PlatformApplicationPage, error) {
		var empty domain.PlatformApplicationPage
		filter := bson.M{}
		if q.ApplicationID != "" {
			filter["id"] = q.ApplicationID
		}
		if q.AdminAuthID != "" {
			filter["adminId"] = q.AdminAuthID
		}
		if q.NamePrefix != "" {
			upper := []byte(q.NamePrefix)
			upper[len(upper)-1]++
			filter["nameKey"] = bson.M{"$gte": q.NamePrefix, "$lt": string(upper)}
		}
		if pos != nil {
			filter["$or"] = bson.A{bson.M{"nameKey": bson.M{"$gt": pos.NameKey}}, bson.M{"nameKey": pos.NameKey, "id": bson.M{"$gt": pos.ID}}}
		}
		hint := platformApplicationQueryIndexName
		if q.AdminAuthID != "" {
			hint = platformApplicationAdminQueryIndexName
		}
		if q.ApplicationID != "" {
			hint = applicationIDUniqueIndexName
		}
		cur, err := r.db.Collection(applicationsCollectionName).Find(tx, filter, options.Find().SetHint(hint).SetProjection(platformApplicationProjection).SetSort(bson.D{{Key: "nameKey", Value: 1}, {Key: "id", Value: 1}}).SetLimit(platformApplicationScanBudget))
		if err != nil {
			return empty, err
		}
		defer cur.Close(tx)
		var rows []bson.Raw
		if err = cur.All(tx, &rows); err != nil {
			return empty, err
		}
		page := domain.PlatformApplicationPage{Items: make([]domain.PlatformApplicationSummary, 0, q.PageSize), AsOf: at}
		var last domain.PlatformApplicationSummary
		consumed := 0
		for _, raw := range rows {
			item, err := platformApplicationCore(raw)
			if err != nil {
				return empty, err
			}
			last = item
			consumed++
			// States are filtered after validating consumed core facts. The raw
			// budget bounds work and can produce an empty continuation page.
			if q.Matches(item) {
				page.Items = append(page.Items, item)
				if len(page.Items) == int(q.PageSize) {
					break
				}
			}
		}
		if consumed > 0 && (consumed < len(rows) || len(rows) == platformApplicationScanBudget) {
			page.NextPageToken, err = r.encodeCursor(caller, q, last)
			if err != nil {
				return empty, err
			}
		}
		return page, page.Validate(q, at)
	})
}
