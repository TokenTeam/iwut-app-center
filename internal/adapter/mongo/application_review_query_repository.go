package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

const reviewQueryScanBudget int64 = 500

type ApplicationReviewQueryRepository struct{ database *m.Database }

func NewApplicationReviewQueryRepository(database *m.Database) *ApplicationReviewQueryRepository {
	return &ApplicationReviewQueryRepository{database: database}
}

var _ reviewport.ReviewQueryRepository = (*ApplicationReviewQueryRepository)(nil)

type reviewQueryCursor struct {
	Version               int `json:"v"`
	Reviewer, Fingerprint string
	SubmittedAt           time.Time `json:"submittedAt"`
	ReviewID              string    `json:"reviewId"`
}

func (r *ApplicationReviewQueryRepository) ListPending(ctx context.Context, reviewer shared.AuthID, filterApp *shared.ApplicationID, pageSize int32, token string) (*reviewdomain.PendingReviewPage, error) {
	if r == nil || r.database == nil || !reviewer.IsValid() || pageSize < 1 {
		return nil, fmt.Errorf("list review query: invalid input")
	}
	cursor, err := decodeReviewQueryCursor(token, reviewer, filterApp)
	if err != nil {
		return nil, reviewport.ErrInvalidReviewPageToken
	}
	return withReviewQuerySnapshot(r, ctx, func(tx context.Context, now time.Time) (*reviewdomain.PendingReviewPage, error) {
		filter := bson.M{"status": "PENDING"}
		if filterApp != nil {
			filter["applicationId"] = filterApp.String()
		}
		if cursor != nil {
			filter["$or"] = bson.A{bson.M{"submittedAt": bson.M{"$gt": cursor.SubmittedAt}}, bson.M{"submittedAt": cursor.SubmittedAt, "reviewId": bson.M{"$gt": cursor.ReviewID}}}
		}
		cur, err := r.database.Collection(applicationReviewsCollectionName).Find(tx, filter, options.Find().SetSort(bson.D{{Key: "submittedAt", Value: 1}, {Key: "reviewId", Value: 1}}).SetLimit(reviewQueryScanBudget))
		if err != nil {
			return nil, err
		}
		var docs []applicationReviewDocument
		if err = cur.All(tx, &docs); err != nil {
			return nil, err
		}
		if len(docs) == 0 {
			return &reviewdomain.PendingReviewPage{Items: []reviewdomain.PendingReviewSummary{}, AsOf: now}, nil
		}
		appIDs, versionIDs := []string{}, []string{}
		for _, d := range docs {
			appIDs = append(appIDs, d.ApplicationID)
			versionIDs = append(versionIDs, d.VersionID)
		}
		apps, err := r.reviewQueryApplications(tx, appIDs)
		if err != nil {
			return nil, err
		}
		versions, err := r.reviewQueryVersions(tx, versionIDs)
		if err != nil {
			return nil, err
		}
		oauthConfigs, err := r.reviewQueryOAuthConfigs(tx, versionIDs)
		if err != nil {
			return nil, err
		}
		items := make([]reviewdomain.PendingReviewSummary, 0, pageSize+1)
		var lastScanned applicationReviewDocument
		for _, d := range docs {
			lastScanned = d
			app, ok := apps[d.ApplicationID]
			if !ok {
				return nil, reviewport.ErrReviewQueryStateInconsistent
			}
			version, ok := versions[d.VersionID]
			if !ok || version.ApplicationID != d.ApplicationID {
				return nil, reviewport.ErrReviewQueryStateInconsistent
			}
			review, err := applicationReviewFromDocument(d)
			if err != nil {
				return nil, reviewport.ErrReviewQueryStateInconsistent
			}
			oauth, ok := oauthConfigs[d.VersionID]
			if !ok || !pendingVersionReviewMatchesVersion(review, version, oauth) {
				return nil, reviewport.ErrReviewQueryStateInconsistent
			}
			if app.LifecycleStatus != "ACTIVE" {
				if app.LifecycleStatus != "CLOSING" && app.LifecycleStatus != "CLOSED" {
					return nil, reviewport.ErrReviewQueryStateInconsistent
				}
				continue
			}
			eligibility := versionReviewEligibility(reviewer, app.AdminID, version.CreatedBy, d.SubmittedBy)
			items = append(items, reviewdomain.PendingReviewSummary{ApplicationID: d.ApplicationID, ApplicationName: app.Name, VersionID: d.VersionID, VersionLabel: review.Snapshot().VersionLabel(), ReviewID: d.ReviewID, Attempt: d.Attempt, SubmittedBy: d.SubmittedBy, SubmittedAt: d.SubmittedAt, DecisionEligibility: eligibility})
			if len(items) > int(pageSize) {
				break
			}
		}
		hasMore := len(items) > int(pageSize)
		var next string
		if hasMore {
			items = items[:pageSize]
			last := items[len(items)-1]
			next, err = encodeReviewQueryCursor(reviewer, filterApp, last.SubmittedAt, last.ReviewID)
		} else if len(docs) == int(reviewQueryScanBudget) {
			next, err = encodeReviewQueryCursor(reviewer, filterApp, lastScanned.SubmittedAt, lastScanned.ReviewID)
		}
		if err != nil {
			return nil, err
		}
		return &reviewdomain.PendingReviewPage{Items: items, NextPageToken: next, AsOf: now}, nil
	})
}
func (r *ApplicationReviewQueryRepository) Get(ctx context.Context, reviewer shared.AuthID, applicationID shared.ApplicationID, versionID reviewdomain.ApplicationVersionID, reviewID reviewdomain.ApplicationReviewID) (*reviewdomain.ReviewDetail, error) {
	return withReviewQuerySnapshot(r, ctx, func(tx context.Context, now time.Time) (*reviewdomain.ReviewDetail, error) {
		var d applicationReviewDocument
		err := r.database.Collection(applicationReviewsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "versionId": versionID.String(), "reviewId": reviewID.String()}).Decode(&d)
		if errors.Is(err, m.ErrNoDocuments) {
			return nil, reviewport.ErrReviewQueryNotFound
		}
		if err != nil {
			return nil, err
		}
		review, err := applicationReviewFromDocument(d)
		if err != nil {
			return nil, reviewport.ErrReviewQueryStateInconsistent
		}
		var app applicationDocument
		if err = r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": applicationID.String()}).Decode(&app); err != nil {
			return nil, reviewport.ErrReviewQueryStateInconsistent
		}
		var version applicationVersionDocument
		if err = r.database.Collection(applicationVersionsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "versionId": versionID.String()}).Decode(&version); err != nil {
			return nil, reviewport.ErrReviewQueryStateInconsistent
		}
		if review.Status() == reviewdomain.ReviewStatusPending {
			var oauth applicationVersionOAuthConfigDocument
			if err = r.database.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "applicationVersionId": versionID.String()}).Decode(&oauth); err != nil || !pendingVersionReviewMatchesVersion(review, version, oauth) {
				return nil, reviewport.ErrReviewQueryStateInconsistent
			}
		}
		policy, err := r.loadCurrentReviewPolicy(tx)
		if err != nil {
			return nil, err
		}
		core, err := managementCore(app)
		if err != nil {
			return nil, reviewport.ErrReviewQueryStateInconsistent
		}
		return &reviewdomain.ReviewDetail{Application: reviewdomain.ReviewApplicationContext{ApplicationID: app.ID, Name: app.Name, AdminID: app.AdminID, LifecycleStatus: core.LifecycleStatus, PlatformAvailabilityStatus: core.PlatformAvailabilityStatus}, Version: reviewdomain.ReviewVersionContext{VersionID: version.VersionID, Sequence: version.Sequence, ReviewStatus: version.ReviewStatus, Revision: version.Revision, CreatedBy: version.CreatedBy}, Review: review, CurrentPolicy: policy, DecisionEligibility: versionReviewEligibility(reviewer, app.AdminID, version.CreatedBy, d.SubmittedBy), AsOf: now}, nil
	})
}
func withReviewQuerySnapshot[T any](r *ApplicationReviewQueryRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	session, err := r.database.Client().StartSession()
	if err != nil {
		return zero, err
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx, time.Now().UTC()) }, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return zero, err
	}
	result, ok := value.(T)
	if !ok {
		return zero, errors.New("invalid review query result")
	}
	return result, nil
}
func (r *ApplicationReviewQueryRepository) reviewQueryApplications(ctx context.Context, ids []string) (map[string]applicationDocument, error) {
	cur, err := r.database.Collection(applicationsCollectionName).Find(ctx, bson.M{"id": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var docs []applicationDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := map[string]applicationDocument{}
	for _, d := range docs {
		out[d.ID] = d
	}
	return out, nil
}
func (r *ApplicationReviewQueryRepository) reviewQueryVersions(ctx context.Context, ids []string) (map[string]applicationVersionDocument, error) {
	cur, err := r.database.Collection(applicationVersionsCollectionName).Find(ctx, bson.M{"versionId": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var docs []applicationVersionDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := map[string]applicationVersionDocument{}
	for _, d := range docs {
		out[d.VersionID] = d
	}
	return out, nil
}

func (r *ApplicationReviewQueryRepository) reviewQueryOAuthConfigs(ctx context.Context, ids []string) (map[string]applicationVersionOAuthConfigDocument, error) {
	cur, err := r.database.Collection(applicationVersionOAuthConfigsCollectionName).Find(ctx, bson.M{"applicationVersionId": bson.M{"$in": ids}})
	if err != nil {
		return nil, err
	}
	var docs []applicationVersionOAuthConfigDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make(map[string]applicationVersionOAuthConfigDocument, len(docs))
	for _, doc := range docs {
		if _, duplicate := out[doc.ApplicationVersionID]; duplicate {
			return nil, reviewport.ErrReviewQueryStateInconsistent
		}
		out[doc.ApplicationVersionID] = doc
	}
	return out, nil
}

func pendingVersionReviewMatchesVersion(review *reviewdomain.ApplicationReview, version applicationVersionDocument, oauth applicationVersionOAuthConfigDocument) bool {
	if review == nil || review.Status() != reviewdomain.ReviewStatusPending || version.ReviewStatus != "SUBMITTED" || version.Revision != review.SourceVersionRevision()+1 || oauth.ApplicationVersionID != version.VersionID || oauth.ApplicationID != version.ApplicationID {
		return false
	}
	document, err := applicationReviewToDocument(review)
	if err != nil {
		return false
	}
	snapshot := document.Snapshot
	return snapshot.VersionLabel == version.VersionLabel && snapshot.LaunchURL == version.LaunchURL && snapshot.RPCApiMinVersion == version.RPCApiMinVersion && snapshot.RPCApiMaxVersionExclusive == version.RPCApiMaxVersionExclusive &&
		slices.Equal(snapshot.RequiredCapabilities, version.RequiredCapabilities) && slices.Equal(snapshot.RequiredScopes, version.RequiredScopes) && slices.Equal(snapshot.OptionalScopes, version.OptionalScopes) &&
		slices.Equal(snapshot.OAuthRedirects.PKCERedirectURIs, oauth.OAuthRedirects.PKCERedirectURIs) && slices.Equal(snapshot.OAuthRedirects.ConfidentialRedirectURIs, oauth.OAuthRedirects.ConfidentialRedirectURIs)
}
func (r *ApplicationReviewQueryRepository) loadCurrentReviewPolicy(ctx context.Context) (reviewdomain.ReviewPolicyContext, error) {
	var d versionReviewPolicyDocument
	err := r.database.Collection(versionReviewPoliciesCollectionName).FindOne(ctx, bson.M{"status": "ACTIVE"}).Decode(&d)
	if err != nil {
		return reviewdomain.ReviewPolicyContext{}, reviewport.ErrReviewQueryStateInconsistent
	}
	if d.Version == "" || len(d.RequiredChecks) == 0 {
		return reviewdomain.ReviewPolicyContext{}, reviewport.ErrReviewQueryStateInconsistent
	}
	return reviewdomain.ReviewPolicyContext{Version: d.Version, RequiredCheckIDs: append([]string(nil), d.RequiredChecks...)}, nil
}
func versionReviewEligibility(reviewer shared.AuthID, admin, creator, submitter string) reviewdomain.DecisionEligibility {
	conflicts := []reviewdomain.ReviewConflict{}
	if reviewer.String() == admin {
		conflicts = append(conflicts, reviewdomain.ConflictCurrentAdmin)
	}
	if reviewer.String() == creator {
		conflicts = append(conflicts, reviewdomain.ConflictVersionCreator)
	}
	if reviewer.String() == submitter {
		conflicts = append(conflicts, reviewdomain.ConflictReviewSubmitter)
	}
	return reviewdomain.DecisionEligibility{Eligible: len(conflicts) == 0, Conflicts: conflicts}
}
func reviewQueryFingerprint(filter *shared.ApplicationID) string {
	value := "*"
	if filter != nil {
		value = filter.String()
	}
	sum := sha256.Sum256([]byte(value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func encodeReviewQueryCursor(reviewer shared.AuthID, filter *shared.ApplicationID, at time.Time, id string) (string, error) {
	raw, err := json.Marshal(reviewQueryCursor{Version: 1, Reviewer: reviewer.String(), Fingerprint: reviewQueryFingerprint(filter), SubmittedAt: at.UTC(), ReviewID: id})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decodeReviewQueryCursor(token string, reviewer shared.AuthID, filter *shared.ApplicationID) (*reviewQueryCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, reviewport.ErrInvalidReviewPageToken
	}
	var c reviewQueryCursor
	if json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Reviewer != reviewer.String() || c.Fingerprint != reviewQueryFingerprint(filter) || c.SubmittedAt.IsZero() || !reviewdomain.ApplicationReviewID(c.ReviewID).IsValid() {
		return nil, reviewport.ErrInvalidReviewPageToken
	}
	return &c, nil
}
