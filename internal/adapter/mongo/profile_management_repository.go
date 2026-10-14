package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

type ProfileManagementRepository struct {
	database  *m.Database
	cursorKey ProfileManagementCursorKey
	sizer     pp.ManagementResponseSizer
}

func NewProfileManagementRepository(db *m.Database, key ProfileManagementCursorKey, sizer pp.ManagementResponseSizer) *ProfileManagementRepository {
	return &ProfileManagementRepository{database: db, cursorKey: key, sizer: sizer}
}

var _ pp.ManagementQueryRepository = (*ProfileManagementRepository)(nil)

func profileManagementSnapshot[T any](r *ProfileManagementRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	if r == nil || r.database == nil || !r.cursorKey.valid || r.sizer == nil {
		return zero, pp.ErrProfileManagementUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return zero, pp.ErrProfileManagementUnavailable
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx, time.Now().UTC()) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		for _, expected := range []error{pp.ErrProfileManagementNotFound, pp.ErrProfileManagementStateInconsistent, pp.ErrInvalidProfileManagementPageToken, pp.ErrProfileManagementResponseTooLarge, pp.ErrProfileManagementUnavailable} {
			if errors.Is(err, expected) {
				return zero, err
			}
		}
		return zero, pp.ErrProfileManagementUnavailable
	}
	value, ok := result.(T)
	if !ok {
		return zero, pp.ErrProfileManagementStateInconsistent
	}
	return value, nil
}
func (r *ProfileManagementRepository) List(ctx context.Context, caller shared.AuthID, app shared.ApplicationID, status *pd.ReviewStatus, size int32, token string) (*pd.ManagedProfilePage, error) {
	return profileManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*pd.ManagedProfilePage, error) {
		ownership, err := r.authorize(tx, caller, app)
		if err != nil {
			return nil, err
		}
		statusValue := ""
		if status != nil {
			statusValue = string(*status)
		}
		binding := profileManagementBinding(caller.String(), app.String(), "REVISIONS", "", statusValue, ownership)
		position, err := r.decodeCursor(token, binding)
		if err != nil {
			return nil, err
		}
		if err = r.checkCursorAnchor(tx, app.String(), "", position); err != nil {
			return nil, err
		}
		state, err := r.loadProfile(tx, app.String())
		if err != nil {
			return nil, err
		}
		filter := bson.M{"applicationId": app.String()}
		if status != nil {
			filter["reviewStatus"] = statusValue
		}
		if position != nil {
			filter["$or"] = bson.A{bson.M{"sequence": bson.M{"$lt": position.Ordinal}}, bson.M{"sequence": position.Ordinal, "profileRevisionId": bson.M{"$lt": position.ID}}}
		}
		rows, err := r.findRaw(tx, applicationProfileRevisionsCollectionName, filter, bson.D{{Key: "sequence", Value: -1}, {Key: "profileRevisionId", Value: -1}}, int64(size)+1)
		if err != nil {
			return nil, err
		}
		docs := make([]applicationProfileRevisionDocument, 0, len(rows))
		for _, raw := range rows {
			d, err := profileManagementRevision(raw, app.String())
			if err != nil {
				return nil, err
			}
			docs = append(docs, d)
		}
		items, err := r.projectRevisions(tx, app.String(), state, docs)
		if err != nil {
			return nil, err
		}
		page := &pd.ManagedProfilePage{Items: []pd.ManagedProfileRevision{}, ProfileState: state, AsOf: asOf}
		for i, item := range items {
			if i >= int(size) {
				break
			}
			page.Items = append(page.Items, item)
			page.NextPageToken = ""
			if i+1 < len(items) {
				page.NextPageToken, err = r.encodeCursor(binding, item.Sequence, item.ProfileRevisionID)
				if err != nil {
					return nil, err
				}
			}
			bytes, err := r.sizer.ListSize(page)
			if err != nil {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			if bytes > pd.ProfileManagementResponseBudget {
				page.Items = page.Items[:len(page.Items)-1]
				if len(page.Items) == 0 {
					return nil, pp.ErrProfileManagementResponseTooLarge
				}
				last := page.Items[len(page.Items)-1]
				page.NextPageToken, err = r.encodeCursor(binding, last.Sequence, last.ProfileRevisionID)
				if err != nil {
					return nil, err
				}
				break
			}
		}
		if len(page.Items) == 0 {
			n, err := r.sizer.ListSize(page)
			if err != nil {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			if n > pd.ProfileManagementResponseBudget {
				return nil, pp.ErrProfileManagementResponseTooLarge
			}
		}
		return page, nil
	})
}
func (r *ProfileManagementRepository) Get(ctx context.Context, caller shared.AuthID, app shared.ApplicationID, id pd.ApplicationProfileRevisionID, size int32, token string) (*pd.ManagedProfileDetail, error) {
	return profileManagementSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*pd.ManagedProfileDetail, error) {
		ownership, err := r.authorize(tx, caller, app)
		if err != nil {
			return nil, err
		}
		rows, err := r.findRaw(tx, applicationProfileRevisionsCollectionName, bson.M{"applicationId": app.String(), "profileRevisionId": id.String()}, nil, 2)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			return nil, pp.ErrProfileManagementNotFound
		}
		if len(rows) != 1 {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		binding := profileManagementBinding(caller.String(), app.String(), "REVIEWS", id.String(), "", ownership)
		position, err := r.decodeCursor(token, binding)
		if err != nil {
			return nil, err
		}
		if err = r.checkCursorAnchor(tx, app.String(), id.String(), position); err != nil {
			return nil, err
		}
		state, err := r.loadProfile(tx, app.String())
		if err != nil {
			return nil, err
		}
		doc, err := profileManagementRevision(rows[0], app.String())
		if err != nil {
			return nil, err
		}
		items, err := r.projectRevisions(tx, app.String(), state, []applicationProfileRevisionDocument{doc})
		if err != nil {
			return nil, err
		}
		filter := bson.M{"profileRevisionId": id.String()}
		if position != nil {
			filter["$or"] = bson.A{bson.M{"attempt": bson.M{"$gt": position.Ordinal}}, bson.M{"attempt": position.Ordinal, "profileReviewId": bson.M{"$gt": position.ID}}}
		}
		reviewRows, err := r.findRaw(tx, applicationProfileReviewsCollectionName, filter, bson.D{{Key: "attempt", Value: 1}, {Key: "profileReviewId", Value: 1}}, int64(size)+1)
		if err != nil {
			return nil, err
		}
		reviews := make([]pd.ManagedProfileReview, 0, len(reviewRows))
		seen := map[string]bool{}
		attempts := map[int32]bool{}
		sources := map[int64]bool{}
		for _, raw := range reviewRows {
			review, err := profileReviewDecisionCandidate(raw)
			if err != nil || review.ApplicationID() != app || review.ProfileRevisionID() != id || !canonicalTesterManagementID(review.ProfileReviewID().String()) || review.SubmittedAt().Before(doc.CreatedAt) || review.SourceRevision() >= doc.Revision || seen[review.ProfileReviewID().String()] || attempts[review.Attempt()] || sources[review.SourceRevision()] {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			seen[review.ProfileReviewID().String()] = true
			attempts[review.Attempt()] = true
			sources[review.SourceRevision()] = true
			if review.Status() == pd.ProfileReviewStatusPending && (items[0].LatestReview == nil || review.ProfileReviewID().String() != items[0].LatestReview.ProfileReviewID) {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			if d := review.Decision(); d != nil && (d.DecidedAt.Before(review.SubmittedAt()) || d.DecidedAt.After(doc.UpdatedAt)) {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			reviews = append(reviews, *projectManagedProfileReview(review))
		}
		if err = r.uniqueReviewPage(tx, id.String(), reviews); err != nil {
			return nil, err
		}
		for i := 1; i < len(reviews); i++ {
			previous, current := reviews[i-1], reviews[i]
			if previous.Attempt >= current.Attempt || previous.SourceRevision >= current.SourceRevision || previous.SubmittedAt.After(current.SubmittedAt) || previous.Decision == nil || previous.Decision.DecidedAt.After(current.SubmittedAt) {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
		}
		detail := &pd.ManagedProfileDetail{Profile: items[0], ProfileState: state, Reviews: []pd.ManagedProfileReview{}, AsOf: asOf}
		n, err := r.sizer.DetailSize(detail)
		if err != nil {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		if n > pd.ProfileManagementResponseBudget {
			return nil, pp.ErrProfileManagementResponseTooLarge
		}
		for i, review := range reviews {
			if i >= int(size) {
				break
			}
			detail.Reviews = append(detail.Reviews, review)
			detail.NextReviewPageToken = ""
			if i+1 < len(reviews) {
				detail.NextReviewPageToken, err = r.encodeCursor(binding, review.Attempt, review.ProfileReviewID)
				if err != nil {
					return nil, err
				}
			}
			n, err := r.sizer.DetailSize(detail)
			if err != nil {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			if n > pd.ProfileManagementResponseBudget {
				detail.Reviews = detail.Reviews[:len(detail.Reviews)-1]
				if len(detail.Reviews) == 0 {
					return nil, pp.ErrProfileManagementResponseTooLarge
				}
				last := detail.Reviews[len(detail.Reviews)-1]
				detail.NextReviewPageToken, err = r.encodeCursor(binding, last.Attempt, last.ProfileReviewID)
				if err != nil {
					return nil, err
				}
				break
			}
		}
		return detail, nil
	})
}
func (r *ProfileManagementRepository) checkCursorAnchor(ctx context.Context, app, parent string, p *profileManagementPosition) error {
	if p == nil {
		return nil
	}
	collection, idKey, ordinalKey := applicationProfileRevisionsCollectionName, "profileRevisionId", "sequence"
	if parent != "" {
		collection, idKey, ordinalKey = applicationProfileReviewsCollectionName, "profileReviewId", "attempt"
	}
	rows, err := r.findRaw(ctx, collection, bson.M{idKey: p.ID}, nil, 2)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0].Lookup("applicationId").Type != bson.TypeString || rows[0].Lookup("applicationId").StringValue() != app || rows[0].Lookup(ordinalKey).Type != bson.TypeInt32 || rows[0].Lookup(ordinalKey).Int32() != p.Ordinal {
		return pp.ErrProfileManagementStateInconsistent
	}
	if parent != "" {
		if rows[0].Lookup("profileRevisionId").Type != bson.TypeString || rows[0].Lookup("profileRevisionId").StringValue() != parent {
			return pp.ErrProfileManagementStateInconsistent
		}
		if _, err := profileReviewDecisionCandidate(rows[0]); err != nil {
			return pp.ErrProfileManagementStateInconsistent
		}
	} else {
		if _, err := profileManagementRevision(rows[0], app); err != nil {
			return err
		}
	}
	return nil
}
