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

type IncomingApplicationAdminTransferRepository struct {
	db  *dm.Database
	key IncomingTransferCursorKey
}

func NewIncomingApplicationAdminTransferRepository(db *dm.Database, key IncomingTransferCursorKey) *IncomingApplicationAdminTransferRepository {
	return &IncomingApplicationAdminTransferRepository{db, key}
}

var _ port.IncomingApplicationAdminTransferRepository = (*IncomingApplicationAdminTransferRepository)(nil)

func (r *IncomingApplicationAdminTransferRepository) ListIncoming(ctx context.Context, caller shared.AuthID, q domain.IncomingTransferQuery, at time.Time) (domain.IncomingApplicationAdminTransferPage, error) {
	var empty domain.IncomingApplicationAdminTransferPage
	if r.db == nil || !r.key.valid {
		return empty, domain.ErrIncomingTransferUnavailable
	}
	position, err := r.decodeCursor(q.PageToken, caller)
	if err != nil {
		return empty, err
	}
	session, err := r.db.Client().StartSession()
	if err != nil {
		return empty, incomingTransferStorageError(err)
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return r.listSnapshot(tx, caller, q, at, position) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return empty, incomingTransferStorageError(err)
	}
	return value.(domain.IncomingApplicationAdminTransferPage), nil
}
func incomingTransferStorageError(err error) error {
	if errors.Is(err, domain.ErrApplicationAdminTransferStateInconsistent) {
		return err
	}
	return fmt.Errorf("%w: %w", domain.ErrIncomingTransferUnavailable, err)
}
func (r *IncomingApplicationAdminTransferRepository) listSnapshot(ctx context.Context, caller shared.AuthID, q domain.IncomingTransferQuery, at time.Time, pos *incomingTransferPosition) (domain.IncomingApplicationAdminTransferPage, error) {
	var empty domain.IncomingApplicationAdminTransferPage
	filter := bson.M{"toAdminId": caller.String(), "status": "PENDING", "expiresAt": bson.M{"$gt": at}}
	if pos != nil {
		lastAt := time.UnixMilli(pos.Millis)
		filter["$or"] = bson.A{bson.M{"requestedAt": bson.M{"$lt": lastAt}}, bson.M{"requestedAt": lastAt, "transferId": bson.M{"$lt": pos.ID}}}
	}
	cursor, err := r.db.Collection(applicationAdminTransfersCollectionName).Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "requestedAt", Value: -1}, {Key: "transferId", Value: -1}}).SetLimit(int64(q.PageSize)+1))
	if err != nil {
		return empty, err
	}
	defer cursor.Close(ctx)
	docs := make([]applicationAdminTransferDocument, 0, int(q.PageSize)+1)
	for cursor.Next(ctx) {
		var d applicationAdminTransferDocument
		if cursor.Decode(&d) != nil {
			return empty, domain.ErrApplicationAdminTransferStateInconsistent
		}
		docs = append(docs, d)
	}
	if err = cursor.Err(); err != nil {
		return empty, err
	}
	more := len(docs) > int(q.PageSize)
	if more {
		docs = docs[:q.PageSize]
	}
	result := domain.IncomingApplicationAdminTransferPage{Items: make([]domain.IncomingApplicationAdminTransfer, 0, len(docs)), AsOf: at}
	if len(docs) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ApplicationID)
	}
	appsCursor, err := r.db.Collection(applicationsCollectionName).Find(ctx, bson.M{"id": bson.M{"$in": ids}})
	if err != nil {
		return empty, err
	}
	defer appsCursor.Close(ctx)
	apps := make(map[string]applicationDocument, len(ids))
	for appsCursor.Next(ctx) {
		var d applicationDocument
		if appsCursor.Decode(&d) != nil {
			return empty, domain.ErrApplicationAdminTransferStateInconsistent
		}
		if _, ok := apps[d.ID]; ok {
			return empty, domain.ErrApplicationAdminTransferStateInconsistent
		}
		apps[d.ID] = d
	}
	if err = appsCursor.Err(); err != nil {
		return empty, err
	}
	for _, d := range docs {
		transfer, err := transferFromDocument(d)
		if err != nil {
			return empty, domain.ErrApplicationAdminTransferStateInconsistent
		}
		app, ok := apps[d.ApplicationID]
		if !ok {
			return empty, domain.ErrApplicationAdminTransferStateInconsistent
		}
		if _, err = applicationFromDocument(app); err != nil || app.ID != d.ApplicationID || app.AdminID != d.FromAdminID || app.OwnershipRevision != d.SourceOwnershipRevision || app.LifecycleStatus != "ACTIVE" || app.CreatedAt.After(d.RequestedAt) {
			return empty, domain.ErrApplicationAdminTransferStateInconsistent
		}
		item := domain.IncomingApplicationAdminTransfer{TransferID: transfer.TransferID, ApplicationID: transfer.ApplicationID, ApplicationName: app.Name, FromAuthID: transfer.FromAdminID, ToAuthID: transfer.ToAdminID, Status: transfer.Status, RequestedAt: transfer.RequestedAt, ExpiresAt: transfer.ExpiresAt}
		if err = item.Validate(caller, at); err != nil {
			return empty, err
		}
		result.Items = append(result.Items, item)
	}
	if more {
		result.NextPageToken, err = r.encodeCursor(caller, result.Items[len(result.Items)-1])
		if err != nil {
			return empty, err
		}
	}
	return result, result.Validate(caller, q, at)
}
