package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	d "iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/shared"
	"math"
	"time"
)

const ownerFences = "account_owner_exit_fences"
const ownerOperations = "account_owner_exit_operations"

type ownerFence struct {
	AuthID             string    `bson:"authId"`
	Revision           int64     `bson:"revision"`
	SealedPurpose      d.Purpose `bson:"sealedPurpose"`
	SealedOperationID  string    `bson:"sealedOperationId"`
	PendingOperationID string    `bson:"pendingOperationId"`
	PendingPurpose     d.Purpose `bson:"pendingPurpose"`
}
type ownerOperation struct {
	AuthID        string     `bson:"authId"`
	OperationID   string     `bson:"operationId"`
	Purpose       d.Purpose  `bson:"purpose"`
	ReceiptID     string     `bson:"receiptId"`
	Decision      d.Decision `bson:"decision"`
	Cleanup       d.Cleanup  `bson:"cleanup"`
	Blocked       bool       `bson:"blocked"`
	Attempt       int        `bson:"attempt"`
	NextAttemptAt time.Time  `bson:"nextAttemptAt"`
}

func (o ownerOperation) status() d.Status {
	return d.Status{Prepare: d.Prepare{Key: d.Key{AuthID: o.AuthID, OperationID: o.OperationID}, Purpose: o.Purpose}, ReceiptID: o.ReceiptID, Decision: o.Decision, Cleanup: o.Cleanup}
}
func (o ownerOperation) valid() bool {
	if !o.status().Prepare.Valid() || o.NextAttemptAt.IsZero() || o.Attempt < 0 || o.Attempt > 7 {
		return false
	}
	validReceipt := (d.Finish{Prepare: o.status().Prepare, ReceiptID: o.ReceiptID, Decision: d.Committed}).Valid()
	switch o.Decision {
	case d.Pending:
		return !o.Blocked && validReceipt && o.Cleanup == d.NotRequired
	case d.Committed:
		return !o.Blocked && validReceipt && ((o.Purpose == d.Withdrawal && o.Cleanup == d.NotRequired) || (o.Purpose == d.Closure && (o.Cleanup == d.CleanupPending || o.Cleanup == d.Complete)))
	case d.Cancelled:
		return o.Cleanup == d.NotRequired && (o.ReceiptID == "" || validReceipt) && (!o.Blocked || o.ReceiptID == "")
	default:
		return false
	}
}

func ownerFilter(k d.Key) bson.D {
	return bson.D{{Key: "authId", Value: k.AuthID}, {Key: "operationId", Value: k.OperationID}}
}
func lockOwnerFence(ctx context.Context, db *m.Database, authID string) (ownerFence, error) {
	c := db.Collection(ownerFences)
	_, err := c.UpdateOne(ctx, bson.D{{Key: "authId", Value: authID}}, bson.D{{Key: "$setOnInsert", Value: ownerFence{AuthID: authID}}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return ownerFence{}, err
	}
	var f ownerFence
	err = c.FindOneAndUpdate(ctx, bson.D{{Key: "authId", Value: authID}, {Key: "revision", Value: bson.D{{Key: "$lt", Value: int64(math.MaxInt64)}}}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&f)
	if err != nil {
		return f, err
	}
	if f.AuthID != authID || f.Revision <= 0 || f.SealedPurpose < 0 || f.SealedPurpose > 2 || f.PendingPurpose < 0 || f.PendingPurpose > 2 || (f.PendingOperationID == "") != (f.PendingPurpose == 0) || (f.SealedOperationID == "") != (f.SealedPurpose == 0) {
		return f, errors.New("invalid owner fence")
	}
	return f, nil
}
func requireOwnerWritable(ctx context.Context, db *m.Database, authID string, personal bool) error {
	f, err := lockOwnerFence(ctx, db, authID)
	if err != nil {
		return err
	}
	if personal {
		if f.SealedPurpose == d.Closure || f.PendingPurpose == d.Closure {
			return shared.ErrAccountExitBlocked
		}
	} else if f.SealedPurpose != 0 || f.PendingPurpose != 0 {
		return shared.ErrAccountExitBlocked
	}
	return nil
}

type AccountOwnerExitRepository struct{ database *m.Database }

func NewAccountOwnerExitRepository(db *m.Database) *AccountOwnerExitRepository {
	return &AccountOwnerExitRepository{db}
}
func (r *AccountOwnerExitRepository) transaction(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	s, e := r.database.Client().StartSession()
	if e != nil {
		return nil, d.ErrUnavailable
	}
	defer s.EndSession(ctx)
	v, e := s.WithTransaction(ctx, fn, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return v, ownerError(e)
}
func ownerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, d.ErrConflict) || errors.Is(err, d.ErrNotFound) || errors.Is(err, d.ErrInvalid) {
		return err
	}
	return fmt.Errorf("%w: %w", d.ErrUnavailable, err)
}
func (r *AccountOwnerExitRepository) read(ctx context.Context, k d.Key) (ownerOperation, error) {
	var o ownerOperation
	err := r.database.Collection(ownerOperations).FindOne(ctx, ownerFilter(k)).Decode(&o)
	if errors.Is(err, m.ErrNoDocuments) {
		return o, d.ErrNotFound
	}
	if err != nil {
		return o, err
	}
	if !o.valid() {
		return o, errors.New("invalid owner exit operation")
	}
	return o, nil
}
func (r *AccountOwnerExitRepository) Prepare(ctx context.Context, p d.Prepare, receipt string, now time.Time) (d.Preparation, error) {
	v, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		f, e := lockOwnerFence(tx, r.database, p.AuthID)
		if e != nil {
			return nil, e
		}
		var existing ownerOperation
		e = r.database.Collection(ownerOperations).FindOne(tx, bson.D{{Key: "operationId", Value: p.OperationID}}).Decode(&existing)
		if e == nil {
			if !existing.valid() {
				return nil, d.ErrUnavailable
			}
			if existing.status().Prepare != p || existing.Decision == d.Cancelled && !existing.Blocked {
				return nil, d.ErrConflict
			}
			return d.Preparation{ReceiptID: existing.ReceiptID, Blocked: existing.Blocked}, nil
		}
		if !errors.Is(e, m.ErrNoDocuments) {
			return nil, e
		}
		if f.PendingOperationID != "" || f.SealedPurpose == d.Closure || f.SealedPurpose == d.Withdrawal && p.Purpose != d.Closure {
			return nil, d.ErrConflict
		}
		n, e := r.database.Collection(applicationsCollectionName).CountDocuments(tx, bson.D{{Key: "adminId", Value: p.AuthID}}, options.Count().SetLimit(1))
		if e != nil {
			return nil, e
		}
		o := ownerOperation{AuthID: p.AuthID, OperationID: p.OperationID, Purpose: p.Purpose, ReceiptID: receipt, Decision: d.Pending, Cleanup: d.NotRequired, NextAttemptAt: now.Add(time.Second)}
		if n > 0 {
			o.Blocked = true
			o.ReceiptID = ""
			o.Decision = d.Cancelled
		} else {
			_, e = r.database.Collection(ownerFences).UpdateOne(tx, bson.D{{Key: "authId", Value: p.AuthID}}, bson.D{{Key: "$set", Value: bson.D{{Key: "pendingOperationId", Value: p.OperationID}, {Key: "pendingPurpose", Value: p.Purpose}}}})
			if e != nil {
				return nil, e
			}
		}
		_, e = r.database.Collection(ownerOperations).InsertOne(tx, o)
		return d.Preparation{ReceiptID: o.ReceiptID, Blocked: o.Blocked}, e
	})
	if err != nil {
		return d.Preparation{}, err
	}
	return v.(d.Preparation), nil
}
func (r *AccountOwnerExitRepository) Finish(ctx context.Context, p d.Finish, now time.Time) (d.Status, error) {
	v, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		f, e := lockOwnerFence(tx, r.database, p.AuthID)
		if e != nil {
			return nil, e
		}
		var o ownerOperation
		e = r.database.Collection(ownerOperations).FindOne(tx, bson.D{{Key: "operationId", Value: p.OperationID}}).Decode(&o)
		if errors.Is(e, m.ErrNoDocuments) {
			if p.Decision != d.Cancelled || p.ReceiptID != "" {
				return nil, d.ErrConflict
			}
			o = ownerOperation{AuthID: p.AuthID, OperationID: p.OperationID, Purpose: p.Purpose, Decision: d.Cancelled, Cleanup: d.NotRequired, NextAttemptAt: now}
			_, e = r.database.Collection(ownerOperations).InsertOne(tx, o)
			return o.status(), e
		}
		if e != nil {
			return nil, e
		}
		if !o.valid() {
			return nil, d.ErrUnavailable
		}
		if o.status().Prepare != p.Prepare || (p.Decision != d.Cancelled || p.ReceiptID != "") && o.ReceiptID != p.ReceiptID {
			return nil, d.ErrConflict
		}
		if o.Decision != d.Pending {
			if o.Decision != p.Decision {
				return nil, d.ErrConflict
			}
			return o.status(), nil
		}
		if f.PendingOperationID != p.OperationID || f.PendingPurpose != p.Purpose {
			return nil, d.ErrConflict
		}
		o.Decision = p.Decision
		o.NextAttemptAt = now
		o.Attempt = 0
		set := bson.D{{Key: "pendingOperationId", Value: ""}, {Key: "pendingPurpose", Value: 0}}
		if p.Decision == d.Committed {
			set = append(set, bson.E{Key: "sealedPurpose", Value: p.Purpose}, bson.E{Key: "sealedOperationId", Value: p.OperationID})
			if p.Purpose == d.Closure {
				o.Cleanup = d.CleanupPending
			}
		}
		_, e = r.database.Collection(ownerFences).UpdateOne(tx, bson.D{{Key: "authId", Value: p.AuthID}}, bson.D{{Key: "$set", Value: set}})
		if e != nil {
			return nil, e
		}
		_, e = r.database.Collection(ownerOperations).ReplaceOne(tx, ownerFilter(p.Key), o)
		return o.status(), e
	})
	if err != nil {
		return d.Status{}, err
	}
	return v.(d.Status), nil
}
func (r *AccountOwnerExitRepository) Get(ctx context.Context, k d.Key) (d.Status, error) {
	o, e := r.read(ctx, k)
	return o.status(), ownerError(e)
}
func (r *AccountOwnerExitRepository) Due(ctx context.Context, now time.Time, limit int) ([]d.Work, error) {
	c, e := r.database.Collection(ownerOperations).Find(ctx, bson.D{{Key: "nextAttemptAt", Value: bson.D{{Key: "$lte", Value: now}}}, {Key: "$or", Value: bson.A{bson.D{{Key: "decision", Value: d.Pending}}, bson.D{{Key: "decision", Value: d.Committed}, {Key: "cleanup", Value: d.CleanupPending}}}}}, options.Find().SetSort(bson.D{{Key: "nextAttemptAt", Value: 1}, {Key: "operationId", Value: 1}}).SetLimit(int64(limit)))
	if e != nil {
		return nil, ownerError(e)
	}
	defer c.Close(ctx)
	var ops []ownerOperation
	if e = c.All(ctx, &ops); e != nil {
		return nil, ownerError(e)
	}
	jobs := make([]d.Work, 0, len(ops))
	for _, o := range ops {
		if !o.valid() {
			return nil, d.ErrUnavailable
		}
		jobs = append(jobs, d.Work{Status: o.status(), Attempt: o.Attempt, NextAttemptAt: o.NextAttemptAt})
	}
	return jobs, nil
}
func (r *AccountOwnerExitRepository) Retry(ctx context.Context, k d.Key, next time.Time, attempt int) error {
	_, e := r.database.Collection(ownerOperations).UpdateOne(ctx, ownerFilter(k), bson.D{{Key: "$set", Value: bson.D{{Key: "nextAttemptAt", Value: next}, {Key: "attempt", Value: attempt}}}})
	return ownerError(e)
}
func (r *AccountOwnerExitRepository) CleanupBatch(ctx context.Context, k d.Key, limit int) error {
	_, e := r.transaction(ctx, func(tx context.Context) (any, error) {
		f, err := lockOwnerFence(tx, r.database, k.AuthID)
		if err != nil {
			return nil, err
		}
		o, err := r.read(tx, k)
		if err != nil {
			return nil, err
		}
		if o.Decision != d.Committed || o.Purpose != d.Closure || f.SealedPurpose != d.Closure {
			return nil, d.ErrConflict
		}
		if o.Cleanup == d.Complete {
			return nil, nil
		}
		c, err := r.database.Collection(applicationTesterMembershipsCollectionName).Find(tx, bson.D{{Key: "testerAuthId", Value: k.AuthID}}, options.Find().SetLimit(int64(limit)).SetSort(bson.D{{Key: "applicationId", Value: 1}, {Key: "membershipId", Value: 1}}))
		if err != nil {
			return nil, err
		}
		var memberships []applicationTesterMembershipDocument
		err = c.All(tx, &memberships)
		if err != nil {
			return nil, err
		}
		for _, entry := range memberships {
			if _, err = testerMembershipFromDocument(entry); err != nil {
				return nil, err
			}
			result, err := r.database.Collection(applicationsCollectionName).UpdateOne(tx, bson.D{{Key: "id", Value: entry.ApplicationID}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}})
			if err != nil {
				return nil, err
			}
			if result.MatchedCount != 1 {
				return nil, d.ErrUnavailable
			}
			_, err = r.database.Collection(applicationTesterMembershipsCollectionName).DeleteOne(tx, bson.D{{Key: "membershipId", Value: entry.MembershipID}, {Key: "testerAuthId", Value: k.AuthID}})
			if err != nil {
				return nil, err
			}
		}
		if len(memberships) == 0 {
			n, err := r.database.Collection(applicationsCollectionName).CountDocuments(tx, bson.D{{Key: "adminId", Value: k.AuthID}}, options.Count().SetLimit(1))
			if err != nil {
				return nil, err
			}
			if n != 0 {
				return nil, d.ErrConflict
			}
			_, err = r.database.Collection(applicationCreationQuotasCollectionName).DeleteOne(tx, bson.D{{Key: "adminId", Value: k.AuthID}})
			if err != nil {
				return nil, err
			}
			_, err = r.database.Collection(ownerOperations).UpdateOne(tx, ownerFilter(k), bson.D{{Key: "$set", Value: bson.D{{Key: "cleanup", Value: d.Complete}}}})
			return nil, err
		}
		return nil, nil
	})
	return e
}
