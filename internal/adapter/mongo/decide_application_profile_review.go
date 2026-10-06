package mongo

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"math"
	"slices"
	"time"
)

var _ pp.ApplicationProfileReviewDecisionRepository = (*ApplicationProfileRevisionRepository)(nil)

func (r *ApplicationProfileRevisionRepository) DecideReview(ctx context.Context, in pp.ProfileReviewDecisionInput) (*pd.ApplicationProfileDecisionResult, error) {
	if r == nil || r.database == nil {
		return nil, pd.NewInternalError(nil)
	}
	if !in.ApplicationID.IsValid() || !in.ProfileRevisionID.IsValid() || !in.ProfileReviewID.IsValid() || in.ExpectedRevision < 1 || !pd.ValidProfileReviewPolicyVersion(in.PolicyVersion) || in.DecidedAt.IsZero() {
		return nil, pd.ErrInvalidApplicationProfileReviewDecision
	}
	if !in.ReviewerID.IsValid() {
		return nil, pd.ErrReviewerIdentityRequired
	}
	if !slices.Contains(in.Permissions, pd.ProfileReviewPermission) {
		return nil, pd.ErrApplicationProfileReviewPermissionRequired
	}
	in.DecidedAt = in.DecidedAt.UTC().Truncate(time.Millisecond)
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeProfilePersistenceError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return r.decideProfileReviewTransaction(tx, in) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		var business *pd.Error
		if errors.As(err, &business) {
			return nil, business
		}
		return nil, safeProfilePersistenceError(err)
	}
	return result.(*pd.ApplicationProfileDecisionResult), nil
}
func (r *ApplicationProfileRevisionRepository) decideProfileReviewTransaction(ctx context.Context, in pp.ProfileReviewDecisionInput) (*pd.ApplicationProfileDecisionResult, error) {
	apps := r.database.Collection(applicationsCollectionName)
	raw, err := apps.FindOne(ctx, bson.M{"id": in.ApplicationID.String(), "lifecycleStatus": "ACTIVE"}).Raw()
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, pd.ErrApplicationProfileReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	admin, ok := raw.Lookup("adminId").StringValueOK()
	coord, valid := raw.Lookup("coordinationRevision").Int64OK()
	if !ok || !shared.AuthID(admin).IsValid() || !valid || coord < 0 || coord == math.MaxInt64 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	fence, err := apps.UpdateOne(ctx, bson.M{"id": in.ApplicationID.String(), "coordinationRevision": coord, "lifecycleStatus": "ACTIVE"}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return nil, err
	}
	if fence.MatchedCount != 1 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	revisions := r.database.Collection(applicationProfileRevisionsCollectionName)
	reviews := r.database.Collection(applicationProfileReviewsCollectionName)
	revFilter := bson.M{"applicationId": in.ApplicationID.String(), "profileRevisionId": in.ProfileRevisionID.String()}
	raw, err = revisions.FindOne(ctx, revFilter).Raw()
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, pd.ErrApplicationProfileReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	revision, err := profileRevisionDecisionCandidate(raw)
	if err != nil {
		return nil, err
	}
	reviewFilter := bson.M{"applicationId": in.ApplicationID.String(), "profileRevisionId": in.ProfileRevisionID.String(), "profileReviewId": in.ProfileReviewID.String()}
	raw, err = reviews.FindOne(ctx, reviewFilter).Raw()
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, pd.ErrApplicationProfileReviewNotFound
	}
	if err != nil {
		return nil, err
	}
	review, err := profileReviewDecisionCandidate(raw)
	if err != nil {
		return nil, err
	}
	if in.ReviewerID == shared.AuthID(admin) || in.ReviewerID == revision.CreatedBy() || in.ReviewerID == review.SubmittedBy() {
		return nil, pd.ErrApplicationProfileReviewConflictOfInterest
	}
	if review.Status() != pd.ProfileReviewStatusPending {
		return nil, pd.ErrApplicationProfileReviewAlreadyDecided
	}
	if revision.ReviewStatus() != pd.ReviewStatusSubmitted {
		return nil, pd.ErrApplicationProfileReviewStateConflict
	}
	if revision.Revision() != in.ExpectedRevision {
		return nil, pd.ErrApplicationProfileRevisionConflict
	}
	// Read both sides of the work slot without imposing approval content rules.
	raw, err = r.database.Collection(applicationProfilesCollectionName).FindOne(ctx, bson.M{"applicationId": in.ApplicationID.String()}).Raw()
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	if err != nil {
		return nil, err
	}
	projection, err := profileFromRaw(raw)
	if err != nil || projection.WorkingProfileRevisionID == nil || *projection.WorkingProfileRevisionID != in.ProfileRevisionID.String() {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	count, err := revisions.CountDocuments(ctx, bson.M{"applicationId": in.ApplicationID.String(), "reviewStatus": bson.M{"$in": bson.A{"DRAFT", "SUBMITTED"}}})
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	latest, err := reviews.FindOne(ctx, bson.M{"profileRevisionId": in.ProfileRevisionID.String()}, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})).Raw()
	if err != nil {
		return nil, err
	}
	latestID, ok := latest.Lookup("profileReviewId").StringValueOK()
	if !ok || latestID != in.ProfileReviewID.String() {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	count, err = reviews.CountDocuments(ctx, bson.M{"profileRevisionId": in.ProfileRevisionID.String(), "status": "PENDING"})
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	if projection.CurrentPublishedProfileRevisionID != nil {
		raw, err = revisions.FindOne(ctx, bson.M{"applicationId": in.ApplicationID.String(), "profileRevisionId": *projection.CurrentPublishedProfileRevisionID}).Raw()
		if errors.Is(err, dm.ErrNoDocuments) {
			return nil, pd.ErrApplicationProfileReviewStateInconsistent
		}
		if err != nil {
			return nil, err
		}
		published, e := profileRevisionFromRaw(raw)
		if e != nil || published.ReviewStatus() != pd.ReviewStatusApproved {
			return nil, pd.ErrApplicationProfileReviewStateInconsistent
		}
	}
	if in.Outcome == "APPROVE" && !equalProfilePublication(projection.CurrentPublishedProfileRevisionID, in.ExpectedPublishedID) {
		return nil, pd.ErrApplicationProfilePublicationConflict
	}
	policies := r.database.Collection(profileReviewPoliciesCollectionName)
	raw, err = policies.FindOne(ctx, bson.M{"version": in.PolicyVersion}).Raw()
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, pd.ErrProfileReviewPolicyUnavailable
	}
	if err != nil {
		return nil, err
	}
	policy, coord, err := profilePolicyFromRaw(raw)
	if err != nil {
		return nil, err
	}
	if policy.Status != "ACTIVE" {
		return nil, pd.ErrProfileReviewPolicyUnavailable
	}
	// A genuine write forces retirement and decisions to serialize on this policy.
	fence, err = policies.UpdateOne(ctx, bson.M{"version": in.PolicyVersion, "status": "ACTIVE", "coordinationRevision": coord}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return nil, err
	}
	if fence.MatchedCount != 1 {
		return nil, pd.ErrProfileReviewPolicyUnavailable
	}
	result, err := revision.DecideReview(review, in.ReviewerID, shared.AuthID(admin), in.Permissions, in.ExpectedRevision, in.Outcome, policy, in.ConfirmedCheckIDs, in.Reason, in.DecidedAt)
	if err != nil {
		return nil, err
	}
	reviewFilter["status"] = "PENDING"
	reviewFilter["decision"] = nil
	changed, err := reviews.UpdateOne(ctx, reviewFilter, bson.M{"$set": bson.M{"status": string(result.Review.Status()), "decision": profileDecisionToDocument(result.Review.Decision())}})
	if err != nil {
		return nil, err
	}
	if changed.MatchedCount != 1 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	revFilter["revision"] = in.ExpectedRevision
	revFilter["reviewStatus"] = "SUBMITTED"
	changed, err = revisions.UpdateOne(ctx, revFilter, bson.M{"$set": bson.M{"reviewStatus": string(result.ProfileRevision.ReviewStatus()), "revision": result.ProfileRevision.Revision(), "updatedBy": in.ReviewerID.String(), "updatedAt": in.DecidedAt}})
	if err != nil {
		return nil, err
	}
	if changed.MatchedCount != 1 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	set := bson.M{"workingProfileRevisionId": nil}
	if in.Outcome == "APPROVE" {
		id := in.ProfileRevisionID.String()
		projection.CurrentPublishedProfileRevisionID = &id
		set["currentPublishedProfileRevisionId"] = id
	}
	changed, err = r.database.Collection(applicationProfilesCollectionName).UpdateOne(ctx, bson.M{"applicationId": in.ApplicationID.String(), "workingProfileRevisionId": in.ProfileRevisionID.String()}, bson.M{"$set": set})
	if err != nil {
		return nil, err
	}
	if changed.MatchedCount != 1 {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	if projection.CurrentPublishedProfileRevisionID != nil {
		id := pd.ApplicationProfileRevisionID(*projection.CurrentPublishedProfileRevisionID)
		result.CurrentPublishedProfileRevisionID = &id
	}
	return result, nil
}
func equalProfilePublication(a *string, b *pd.ApplicationProfileRevisionID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == b.String()
}
func profileRevisionDecisionCandidate(raw bson.Raw) (*pd.ApplicationProfileRevision, error) {
	d, err := profileRevisionDocumentFromRaw(raw)
	if err != nil {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	r, err := pd.RestoreProfileRevisionDecisionCandidate(pd.ApplicationProfileRevisionState{ProfileRevisionID: pd.ApplicationProfileRevisionID(d.ProfileRevisionID), ApplicationID: shared.ApplicationID(d.ApplicationID), Sequence: pd.ProfileSequence(d.Sequence), DisplayName: d.DisplayName, Description: d.Description, Icon: d.Icon, ReviewStatus: pd.ReviewStatus(d.ReviewStatus), CreatedBy: shared.AuthID(d.CreatedBy), CreatedAt: d.CreatedAt, Revision: d.Revision, UpdatedBy: shared.AuthID(d.UpdatedBy), UpdatedAt: d.UpdatedAt})
	if err != nil {
		return nil, pd.ErrApplicationProfileReviewStateInconsistent
	}
	return r, nil
}

type profileReviewDecisionDocument struct {
	Outcome           string    `bson:"outcome"`
	PolicyVersion     string    `bson:"policyVersion"`
	ConfirmedCheckIDs []string  `bson:"confirmedCheckIds"`
	Reason            *string   `bson:"reason"`
	DecidedBy         string    `bson:"decidedBy"`
	DecidedAt         time.Time `bson:"decidedAt"`
}

func profileDecisionToDocument(d *pd.ApplicationProfileReviewDecision) any {
	if d == nil {
		return nil
	}
	checks := append([]string{}, d.ConfirmedCheckIDs...)
	return profileReviewDecisionDocument{string(d.Outcome), d.PolicyVersion, checks, d.Reason, d.DecidedBy.String(), d.DecidedAt}
}
func profileReviewDecisionCandidate(raw bson.Raw) (*pd.ApplicationProfileReview, error) {
	bad := pd.ErrApplicationProfileReviewStateInconsistent
	for _, key := range []string{"profileReviewId", "applicationId", "profileRevisionId", "status", "submittedBy"} {
		if raw.Lookup(key).Type != bson.TypeString {
			return nil, bad
		}
	}
	if raw.Lookup("attempt").Type != bson.TypeInt32 || raw.Lookup("sourceRevision").Type != bson.TypeInt64 || raw.Lookup("submittedAt").Type != bson.TypeDateTime {
		return nil, bad
	}
	sn, ok := raw.Lookup("snapshot").DocumentOK()
	if !ok || sn.Lookup("displayName").Type != bson.TypeString {
		return nil, bad
	}
	for _, key := range []string{"description", "icon"} {
		kind := sn.Lookup(key).Type
		if kind != bson.TypeString && kind != bson.TypeNull {
			return nil, bad
		}
	}
	var doc applicationProfileReviewDocument
	if bson.Unmarshal(raw, &doc) != nil {
		return nil, bad
	}
	var decision *pd.ApplicationProfileReviewDecision
	if raw.Lookup("decision").Type != bson.TypeNull {
		d, ok := raw.Lookup("decision").DocumentOK()
		if !ok {
			return nil, bad
		}
		for _, key := range []string{"outcome", "policyVersion", "decidedBy"} {
			if d.Lookup(key).Type != bson.TypeString {
				return nil, bad
			}
		}
		if d.Lookup("decidedAt").Type != bson.TypeDateTime || d.Lookup("confirmedCheckIds").Type != bson.TypeArray {
			return nil, bad
		}
		kind := d.Lookup("reason").Type
		if kind != bson.TypeNull && kind != bson.TypeString {
			return nil, bad
		}
		var dd profileReviewDecisionDocument
		if bson.Unmarshal(d, &dd) != nil {
			return nil, bad
		}
		decision = &pd.ApplicationProfileReviewDecision{Outcome: pd.ProfileReviewStatus(dd.Outcome), PolicyVersion: dd.PolicyVersion, ConfirmedCheckIDs: dd.ConfirmedCheckIDs, Reason: dd.Reason, DecidedBy: shared.AuthID(dd.DecidedBy), DecidedAt: dd.DecidedAt}
	}
	return pd.RestoreProfileReviewDecisionCandidate(pd.ApplicationProfileReviewState{ProfileReviewID: pd.ApplicationProfileReviewID(doc.ProfileReviewID), ApplicationID: shared.ApplicationID(doc.ApplicationID), ProfileRevisionID: pd.ApplicationProfileRevisionID(doc.ProfileRevisionID), Attempt: doc.Attempt, SourceRevision: doc.SourceRevision, Status: pd.ProfileReviewStatus(doc.Status), DisplayName: doc.Snapshot.DisplayName, Description: doc.Snapshot.Description, Icon: doc.Snapshot.Icon, SubmittedBy: shared.AuthID(doc.SubmittedBy), SubmittedAt: doc.SubmittedAt, Decision: decision})
}
func profilePolicyFromRaw(raw bson.Raw) (pd.ProfileReviewPolicy, int64, error) {
	var d struct {
		Version        string   `bson:"version"`
		RequiredChecks []string `bson:"requiredChecks"`
		Status         string   `bson:"status"`
	}
	coord, ok := raw.Lookup("coordinationRevision").Int64OK()
	if !ok || coord < 0 || coord == math.MaxInt64 || bson.Unmarshal(raw, &d) != nil || !pd.ValidProfileReviewPolicyVersion(d.Version) || (d.Status != "ACTIVE" && d.Status != "RETIRED") || len(d.RequiredChecks) == 0 {
		return pd.ProfileReviewPolicy{}, 0, pd.ErrApplicationProfileReviewStateInconsistent
	}
	checks := slices.Clone(d.RequiredChecks)
	slices.Sort(checks)
	if len(slices.Compact(slices.Clone(checks))) != len(checks) {
		return pd.ProfileReviewPolicy{}, 0, pd.ErrApplicationProfileReviewStateInconsistent
	}
	for _, id := range checks {
		if !pd.ValidProfileReviewPolicyVersion(id) {
			return pd.ProfileReviewPolicy{}, 0, pd.ErrApplicationProfileReviewStateInconsistent
		}
	}
	return pd.ProfileReviewPolicy{Version: d.Version, RequiredChecks: checks, Status: d.Status}, coord, nil
}
