package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"

	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

const profileReviewQueryScanBudget int64 = 500

type ApplicationProfileReviewQueryRepository struct{ database *drivermongo.Database }

func NewApplicationProfileReviewQueryRepository(database *drivermongo.Database) *ApplicationProfileReviewQueryRepository {
	return &ApplicationProfileReviewQueryRepository{database: database}
}

var _ profileport.ProfileReviewQueryRepository = (*ApplicationProfileReviewQueryRepository)(nil)

type profileReviewQueryCursor struct {
	Version     int       `json:"v"`
	Reviewer    string    `json:"reviewer"`
	Fingerprint string    `json:"fingerprint"`
	SubmittedAt time.Time `json:"submittedAt"`
	ReviewID    string    `json:"profileReviewId"`
}

func (r *ApplicationProfileReviewQueryRepository) ListPending(ctx context.Context, reviewer shared.AuthID, filterApp *shared.ApplicationID, pageSize int32, token string) (*profiledomain.PendingProfileReviewPage, error) {
	if r == nil || r.database == nil || !reviewer.IsValid() || pageSize < 1 {
		return nil, fmt.Errorf("list profile review query: invalid input")
	}
	cursor, err := decodeProfileReviewQueryCursor(token, reviewer, filterApp)
	if err != nil {
		return nil, profileport.ErrInvalidProfileReviewPageToken
	}
	return withProfileReviewQuerySnapshot(r, ctx, func(tx context.Context, now time.Time) (*profiledomain.PendingProfileReviewPage, error) {
		filter := bson.M{"status": "PENDING"}
		if filterApp != nil {
			filter["applicationId"] = filterApp.String()
		}
		if cursor != nil {
			filter["$or"] = bson.A{
				bson.M{"submittedAt": bson.M{"$gt": cursor.SubmittedAt}},
				bson.M{"submittedAt": cursor.SubmittedAt, "profileReviewId": bson.M{"$gt": cursor.ReviewID}},
			}
		}
		findOptions := options.Find().SetSort(bson.D{{Key: "submittedAt", Value: 1}, {Key: "profileReviewId", Value: 1}}).SetLimit(profileReviewQueryScanBudget)
		cur, err := r.database.Collection(applicationProfileReviewsCollectionName).Find(tx, filter, findOptions)
		if err != nil {
			return nil, err
		}
		var raws []bson.Raw
		if err = cur.All(tx, &raws); err != nil {
			return nil, err
		}
		if len(raws) == 0 {
			return &profiledomain.PendingProfileReviewPage{Items: []profiledomain.PendingProfileReviewSummary{}, AsOf: now}, nil
		}

		appIDs := make([]string, 0, len(raws))
		revisionIDs := make([]string, 0, len(raws))
		for _, raw := range raws {
			appID, appOK := raw.Lookup("applicationId").StringValueOK()
			revisionID, revisionOK := raw.Lookup("profileRevisionId").StringValueOK()
			if !appOK || !revisionOK {
				return nil, profileport.ErrProfileReviewQueryStateInconsistent
			}
			appIDs = append(appIDs, appID)
			revisionIDs = append(revisionIDs, revisionID)
		}
		apps, err := r.profileReviewQueryApplications(tx, appIDs)
		if err != nil {
			return nil, err
		}
		revisions, err := r.profileReviewQueryRevisions(tx, revisionIDs)
		if err != nil {
			return nil, err
		}

		items := make([]profiledomain.PendingProfileReviewSummary, 0, pageSize+1)
		var lastScannedAt time.Time
		var lastScannedID string
		for _, raw := range raws {
			review, restoreErr := profileReviewDecisionCandidate(raw)
			if restoreErr != nil || review.Status() != profiledomain.ProfileReviewStatusPending {
				return nil, profileport.ErrProfileReviewQueryStateInconsistent
			}
			lastScannedAt, lastScannedID = review.SubmittedAt(), review.ProfileReviewID().String()
			app, ok := apps[review.ApplicationID().String()]
			if !ok {
				return nil, profileport.ErrProfileReviewQueryStateInconsistent
			}
			revision, ok := revisions[review.ProfileRevisionID().String()]
			if !ok || revision.ApplicationID != review.ApplicationID().String() {
				return nil, profileport.ErrProfileReviewQueryStateInconsistent
			}
			if !pendingProfileReviewMatchesRevision(review, revision) {
				return nil, profileport.ErrProfileReviewQueryStateInconsistent
			}
			if app.LifecycleStatus != "ACTIVE" {
				if app.LifecycleStatus != "CLOSING" && app.LifecycleStatus != "CLOSED" {
					return nil, profileport.ErrProfileReviewQueryStateInconsistent
				}
				continue
			}
			snapshot := review.Snapshot()
			items = append(items, profiledomain.PendingProfileReviewSummary{
				ApplicationID: review.ApplicationID().String(), ApplicationName: app.Name,
				ProfileRevisionID: review.ProfileRevisionID().String(), Sequence: revision.Sequence,
				DisplayName: snapshot.DisplayName().String(), ProfileReviewID: review.ProfileReviewID().String(),
				Attempt: review.Attempt(), SubmittedAt: review.SubmittedAt(),
				DecisionEligibility: profileReviewEligibility(reviewer, app.AdminID, revision.CreatedBy, review.SubmittedBy().String()),
			})
			if len(items) > int(pageSize) {
				break
			}
		}

		hasMore := len(items) > int(pageSize)
		var next string
		if hasMore {
			items = items[:pageSize]
			last := items[len(items)-1]
			next, err = encodeProfileReviewQueryCursor(reviewer, filterApp, last.SubmittedAt, last.ProfileReviewID)
		} else if len(raws) == int(profileReviewQueryScanBudget) {
			next, err = encodeProfileReviewQueryCursor(reviewer, filterApp, lastScannedAt, lastScannedID)
		}
		if err != nil {
			return nil, err
		}
		return &profiledomain.PendingProfileReviewPage{Items: items, NextPageToken: next, AsOf: now}, nil
	})
}

func (r *ApplicationProfileReviewQueryRepository) Get(ctx context.Context, reviewer shared.AuthID, applicationID shared.ApplicationID, revisionID profiledomain.ApplicationProfileRevisionID, reviewID profiledomain.ApplicationProfileReviewID) (*profiledomain.ProfileReviewDetail, error) {
	if r == nil || r.database == nil || !reviewer.IsValid() || !applicationID.IsValid() || !revisionID.IsValid() || !reviewID.IsValid() {
		return nil, fmt.Errorf("get profile review query: invalid input")
	}
	return withProfileReviewQuerySnapshot(r, ctx, func(tx context.Context, now time.Time) (*profiledomain.ProfileReviewDetail, error) {
		raw, err := r.database.Collection(applicationProfileReviewsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "profileRevisionId": revisionID.String(), "profileReviewId": reviewID.String()}).Raw()
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, profileport.ErrProfileReviewQueryNotFound
		}
		if err != nil {
			return nil, err
		}
		review, err := profileReviewDecisionCandidate(raw)
		if err != nil {
			return nil, profileport.ErrProfileReviewQueryStateInconsistent
		}
		var app applicationDocument
		if err = r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": applicationID.String()}).Decode(&app); err != nil {
			return nil, profileport.ErrProfileReviewQueryStateInconsistent
		}
		var revision applicationProfileRevisionDocument
		if err = r.database.Collection(applicationProfileRevisionsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "profileRevisionId": revisionID.String()}).Decode(&revision); err != nil {
			return nil, profileport.ErrProfileReviewQueryStateInconsistent
		}
		if review.Status() == profiledomain.ProfileReviewStatusPending && !pendingProfileReviewMatchesRevision(review, revision) {
			return nil, profileport.ErrProfileReviewQueryStateInconsistent
		}
		policy, err := r.loadCurrentProfileReviewPolicy(tx)
		if err != nil {
			return nil, err
		}
		core, err := managementCore(app)
		if err != nil {
			return nil, profileport.ErrProfileReviewQueryStateInconsistent
		}
		return &profiledomain.ProfileReviewDetail{
			Application:     profiledomain.ProfileReviewApplicationContext{ApplicationID: app.ID, Name: app.Name, AdminID: app.AdminID, LifecycleStatus: core.LifecycleStatus, PlatformAvailabilityStatus: core.PlatformAvailabilityStatus},
			ProfileRevision: profiledomain.ProfileRevisionReviewContext{ProfileRevisionID: revision.ProfileRevisionID, Sequence: revision.Sequence, ReviewStatus: revision.ReviewStatus, Revision: revision.Revision, CreatedBy: revision.CreatedBy},
			Review:          review, CurrentPolicy: policy,
			DecisionEligibility: profileReviewEligibility(reviewer, app.AdminID, revision.CreatedBy, review.SubmittedBy().String()), AsOf: now,
		}, nil
	})
}

func pendingProfileReviewMatchesRevision(review *profiledomain.ApplicationProfileReview, revision applicationProfileRevisionDocument) bool {
	if review == nil || review.Status() != profiledomain.ProfileReviewStatusPending ||
		revision.ReviewStatus != string(profiledomain.ReviewStatusSubmitted) ||
		revision.Revision != review.SourceRevision()+1 {
		return false
	}
	snapshot := review.Snapshot()
	if revision.DisplayName != snapshot.DisplayName().String() {
		return false
	}
	if !equalProfileQueryText(revision.Description, snapshot.Description()) || !equalProfileQueryIcon(revision.Icon, snapshot.Icon()) {
		return false
	}
	return true
}

func equalProfileQueryText(stored *string, value *profiledomain.ApplicationDescription) bool {
	if stored == nil || value == nil {
		return stored == nil && value == nil
	}
	return *stored == value.String()
}

func equalProfileQueryIcon(stored *string, value *profiledomain.ApplicationIcon) bool {
	if stored == nil || value == nil {
		return stored == nil && value == nil
	}
	return *stored == value.String()
}

func withProfileReviewQuerySnapshot[T any](r *ApplicationProfileReviewQueryRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	session, err := r.database.Client().StartSession()
	if err != nil {
		return zero, err
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return fn(tx, time.Now().UTC())
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return zero, err
	}
	result, ok := value.(T)
	if !ok {
		return zero, errors.New("invalid profile review query result")
	}
	return result, nil
}

func (r *ApplicationProfileReviewQueryRepository) profileReviewQueryApplications(ctx context.Context, ids []string) (map[string]applicationDocument, error) {
	cur, err := r.database.Collection(applicationsCollectionName).Find(ctx, bson.M{"id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var docs []applicationDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make(map[string]applicationDocument, len(docs))
	for _, doc := range docs {
		out[doc.ID] = doc
	}
	return out, nil
}

func (r *ApplicationProfileReviewQueryRepository) profileReviewQueryRevisions(ctx context.Context, ids []string) (map[string]applicationProfileRevisionDocument, error) {
	cur, err := r.database.Collection(applicationProfileRevisionsCollectionName).Find(ctx, bson.M{"profileRevisionId": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var docs []applicationProfileRevisionDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make(map[string]applicationProfileRevisionDocument, len(docs))
	for _, doc := range docs {
		out[doc.ProfileRevisionID] = doc
	}
	return out, nil
}

func (r *ApplicationProfileReviewQueryRepository) loadCurrentProfileReviewPolicy(ctx context.Context) (profiledomain.ProfileReviewPolicyContext, error) {
	raw, err := r.database.Collection(profileReviewPoliciesCollectionName).FindOne(ctx, bson.M{"status": "ACTIVE"}).Raw()
	if err != nil {
		return profiledomain.ProfileReviewPolicyContext{}, profileport.ErrProfileReviewQueryStateInconsistent
	}
	policy, _, err := profilePolicyFromRaw(raw)
	if err != nil {
		return profiledomain.ProfileReviewPolicyContext{}, profileport.ErrProfileReviewQueryStateInconsistent
	}
	return profiledomain.ProfileReviewPolicyContext{Version: policy.Version, RequiredCheckIDs: append([]string(nil), policy.RequiredChecks...)}, nil
}

func profileReviewEligibility(reviewer shared.AuthID, admin, creator, submitter string) profiledomain.ProfileDecisionEligibility {
	conflicts := []profiledomain.ProfileReviewConflict{}
	if reviewer.String() == admin {
		conflicts = append(conflicts, profiledomain.ProfileConflictCurrentAdmin)
	}
	if reviewer.String() == creator {
		conflicts = append(conflicts, profiledomain.ProfileConflictRevisionCreator)
	}
	if reviewer.String() == submitter {
		conflicts = append(conflicts, profiledomain.ProfileConflictReviewSubmitter)
	}
	return profiledomain.ProfileDecisionEligibility{Eligible: len(conflicts) == 0, Conflicts: conflicts}
}

func profileReviewQueryFingerprint(filter *shared.ApplicationID) string {
	value := "*"
	if filter != nil {
		value = filter.String()
	}
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func encodeProfileReviewQueryCursor(reviewer shared.AuthID, filter *shared.ApplicationID, at time.Time, id string) (string, error) {
	raw, err := json.Marshal(profileReviewQueryCursor{Version: 1, Reviewer: reviewer.String(), Fingerprint: profileReviewQueryFingerprint(filter), SubmittedAt: at.UTC(), ReviewID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeProfileReviewQueryCursor(token string, reviewer shared.AuthID, filter *shared.ApplicationID) (*profileReviewQueryCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, profileport.ErrInvalidProfileReviewPageToken
	}
	var cursor profileReviewQueryCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || cursor.Reviewer != reviewer.String() || cursor.Fingerprint != profileReviewQueryFingerprint(filter) || cursor.SubmittedAt.IsZero() || !profiledomain.ApplicationProfileReviewID(cursor.ReviewID).IsValid() {
		return nil, profileport.ErrInvalidProfileReviewPageToken
	}
	return &cursor, nil
}
