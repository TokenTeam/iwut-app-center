package mongo

import (
	"context"
	"math"
	"strconv"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

// Check duplicate keys and UTF-8 recursively before typed decoding can erase
// damaged source facts. BSON datetimes must fit the public timestamp range.
func profileManagementRawValid(raw bson.Raw, array bool) bool {
	if raw.Validate() != nil {
		return false
	}
	elements, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for i, e := range elements {
		key := e.Key()
		if seen[key] || !utf8.ValidString(key) || array && key != strconv.Itoa(i) {
			return false
		}
		seen[key] = true
		v := e.Value()
		switch v.Type {
		case bson.TypeString:
			if !utf8.ValidString(v.StringValue()) {
				return false
			}
		case bson.TypeEmbeddedDocument:
			if !profileManagementRawValid(v.Document(), false) {
				return false
			}
		case bson.TypeArray:
			if !profileManagementRawValid(bson.Raw(v.Array()), true) {
				return false
			}
		case bson.TypeDateTime:
			if v.DateTime() < -62135596800000 || v.DateTime() > 253402300799999 {
				return false
			}
		}
	}
	return true
}
func (r *ProfileManagementRepository) findRaw(ctx context.Context, collection string, filter bson.M, sort bson.D, limit int64) ([]bson.Raw, error) {
	opts := options.Find()
	if len(sort) > 0 {
		opts.SetSort(sort)
	}
	if limit > 0 {
		opts.SetLimit(limit)
	}
	c, err := r.database.Collection(collection).Find(ctx, filter, opts)
	if err != nil {
		return nil, pp.ErrProfileManagementUnavailable
	}
	defer c.Close(ctx)
	rows := []bson.Raw{}
	for c.Next(ctx) {
		if !profileManagementRawValid(c.Current, false) {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		rows = append(rows, append(bson.Raw(nil), c.Current...))
	}
	if c.Err() != nil {
		return nil, pp.ErrProfileManagementUnavailable
	}
	return rows, nil
}
func (r *ProfileManagementRepository) authorize(ctx context.Context, caller shared.AuthID, app shared.ApplicationID) (int64, error) {
	rows, err := r.findRaw(ctx, applicationsCollectionName, bson.M{"id": app.String(), "adminId": caller.String()}, nil, 2)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, pp.ErrProfileManagementNotFound
	}
	if len(rows) != 1 {
		return 0, pp.ErrProfileManagementStateInconsistent
	}
	raw := rows[0]
	if raw.Lookup("createdAt").Type != bson.TypeDateTime {
		return 0, pp.ErrProfileManagementStateInconsistent
	}
	for _, key := range []string{"id", "adminId", "lifecycleStatus", "platformAvailabilityStatus", "archiveStatus"} {
		if raw.Lookup(key).Type != bson.TypeString {
			return 0, pp.ErrProfileManagementStateInconsistent
		}
	}
	for _, key := range []string{"ownershipRevision", "lifecycleRevision", "platformAvailabilityRevision", "archiveRevision"} {
		if raw.Lookup(key).Type != bson.TypeInt64 || raw.Lookup(key).Int64() < 1 {
			return 0, pp.ErrProfileManagementStateInconsistent
		}
	}
	var doc applicationDocument
	if bson.Unmarshal(raw, &doc) != nil || doc.ID != app.String() || doc.AdminID != caller.String() || !canonicalTesterManagementID(doc.ID) {
		return 0, pp.ErrProfileManagementStateInconsistent
	}
	if _, err := managementCore(doc); err != nil {
		return 0, pp.ErrProfileManagementStateInconsistent
	}
	// Only after proving a caller-owned candidate, reject a colliding global
	// Application ID even when its second copy names a different administrator.
	count, err := r.database.Collection(applicationsCollectionName).CountDocuments(ctx, bson.M{"id": app.String()}, options.Count().SetLimit(2))
	if err != nil {
		return 0, pp.ErrProfileManagementUnavailable
	}
	if count != 1 {
		return 0, pp.ErrProfileManagementStateInconsistent
	}
	return doc.OwnershipRevision, nil
}
func profileManagementRevision(raw bson.Raw, app string) (applicationProfileRevisionDocument, error) {
	doc, err := profileRevisionDocumentFromRaw(raw)
	if err != nil || doc.ApplicationID != app || !canonicalTesterManagementID(doc.ProfileRevisionID) || !canonicalTesterManagementID(doc.ApplicationID) || !profileManagementRawValid(raw, false) {
		return doc, pp.ErrProfileManagementStateInconsistent
	}
	if _, err := profileRevisionFromDocument(doc); err != nil || doc.UpdatedAt.Before(doc.CreatedAt) {
		return doc, pp.ErrProfileManagementStateInconsistent
	}
	return doc, nil
}
func (r *ProfileManagementRepository) loadProfile(ctx context.Context, app string) (pd.ManagedProfileState, error) {
	state := pd.ManagedProfileState{}
	rows, err := r.findRaw(ctx, applicationProfilesCollectionName, bson.M{"applicationId": app}, nil, 2)
	if err != nil {
		return state, err
	}
	if len(rows) == 0 {
		for _, collection := range []string{applicationProfileRevisionsCollectionName, applicationProfileReviewsCollectionName} {
			row, err := r.findRaw(ctx, collection, bson.M{"applicationId": app}, nil, 1)
			if err != nil {
				return state, err
			}
			if len(row) > 0 {
				return state, pp.ErrProfileManagementStateInconsistent
			}
		}
		return state, nil
	}
	if len(rows) != 1 {
		return state, pp.ErrProfileManagementStateInconsistent
	}
	doc, err := profileFromRaw(rows[0])
	if err != nil || doc.ApplicationID != app {
		return state, pp.ErrProfileManagementStateInconsistent
	}
	state = pd.ManagedProfileState{WorkingProfileRevisionID: doc.WorkingProfileRevisionID, CurrentPublishedProfileRevisionID: doc.CurrentPublishedProfileRevisionID}
	for _, id := range []*string{state.WorkingProfileRevisionID, state.CurrentPublishedProfileRevisionID} {
		if id != nil && !canonicalTesterManagementID(*id) {
			return state, pp.ErrProfileManagementStateInconsistent
		}
	}
	// Check the unique working fact as well as the pointer; a missing/extra
	// working revision must not become an apparently empty management state.
	working, err := r.findRaw(ctx, applicationProfileRevisionsCollectionName, bson.M{"applicationId": app, "reviewStatus": bson.M{"$in": []string{"DRAFT", "SUBMITTED"}}}, nil, 2)
	if err != nil {
		return state, err
	}
	if len(working) > 1 || (len(working) == 0) != (state.WorkingProfileRevisionID == nil) {
		return state, pp.ErrProfileManagementStateInconsistent
	}
	ids := []string{}
	for _, id := range []*string{state.WorkingProfileRevisionID, state.CurrentPublishedProfileRevisionID} {
		if id != nil {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 2 && ids[0] == ids[1] {
		return state, pp.ErrProfileManagementStateInconsistent
	}
	if len(ids) == 0 {
		return state, nil
	}
	facts, err := r.findRaw(ctx, applicationProfileRevisionsCollectionName, bson.M{"profileRevisionId": bson.M{"$in": ids}}, nil, 0)
	if err != nil {
		return state, err
	}
	docs := []applicationProfileRevisionDocument{}
	for _, raw := range facts {
		d, err := profileManagementRevision(raw, app)
		if err != nil {
			return state, err
		}
		docs = append(docs, d)
	}
	if len(docs) != len(ids) {
		return state, pp.ErrProfileManagementStateInconsistent
	}
	projected, err := r.projectRevisions(ctx, app, state, docs)
	if err != nil {
		return state, err
	}
	for _, p := range projected {
		if p.IsCurrentWorkingRevision && p.ReviewStatus != "DRAFT" && p.ReviewStatus != "SUBMITTED" || p.IsCurrentPublished && p.ReviewStatus != "APPROVED" {
			return state, pp.ErrProfileManagementStateInconsistent
		}
	}
	return state, nil
}
func (r *ProfileManagementRepository) latestReviews(ctx context.Context, ids []string) (map[string]*pd.ApplicationProfileReview, error) {
	result := map[string]*pd.ApplicationProfileReview{}
	if len(ids) == 0 {
		return result, nil
	}
	// One batched aggregation loads the top two attempts per required revision.
	// The indexed lookup bounds history memory and retains duplicate lookahead.
	pipeline := m.Pipeline{
		{{Key: "$match", Value: bson.M{"profileRevisionId": bson.M{"$in": ids}}}},
		{{Key: "$lookup", Value: bson.M{"from": applicationProfileReviewsCollectionName, "localField": "profileRevisionId", "foreignField": "profileRevisionId", "pipeline": m.Pipeline{
			{{Key: "$sort", Value: bson.D{{Key: "attempt", Value: -1}, {Key: "profileReviewId", Value: -1}}}},
			{{Key: "$limit", Value: 2}},
		}, "as": "reviews"}}},
		{{Key: "$unwind", Value: "$reviews"}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$reviews"}}},
	}
	c, err := r.database.Collection(applicationProfileRevisionsCollectionName).Aggregate(ctx, pipeline)
	if err != nil {
		return nil, pp.ErrProfileManagementUnavailable
	}
	defer c.Close(ctx)
	seenIDs := map[string]bool{}
	facts := []profileManagementReviewKey{}
	for c.Next(ctx) {
		raw := c.Current
		if !profileManagementRawValid(raw, false) {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		review, err := profileReviewDecisionCandidate(raw)
		if err != nil || !canonicalTesterManagementID(review.ProfileReviewID().String()) || !canonicalTesterManagementID(review.ProfileRevisionID().String()) {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		id := review.ProfileRevisionID().String()
		if seenIDs[review.ProfileReviewID().String()] {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		seenIDs[review.ProfileReviewID().String()] = true
		facts = append(facts, profileManagementReviewKey{review.ProfileRevisionID().String(), review.ProfileReviewID().String(), review.Attempt(), review.SourceRevision()})
		if previous := result[id]; previous != nil {
			if previous.ApplicationID() != review.ApplicationID() || previous.Attempt() <= review.Attempt() || previous.ProfileReviewID() == review.ProfileReviewID() || previous.SourceRevision() <= review.SourceRevision() || previous.SubmittedAt().Before(review.SubmittedAt()) || review.Decision() == nil || review.Decision().DecidedAt.Before(review.SubmittedAt()) || review.Decision().DecidedAt.After(previous.SubmittedAt()) {
				return nil, pp.ErrProfileManagementStateInconsistent
			}
			if review.Attempt() > previous.Attempt() {
				result[id] = review
			}
		} else {
			result[id] = review
		}
	}
	if c.Err() != nil {
		return nil, pp.ErrProfileManagementUnavailable
	}
	if err := r.uniqueReviewKeys(ctx, facts); err != nil {
		return nil, err
	}
	return result, nil
}

// Detect collisions involving a required history row even when the second
// copy sorts just outside this page. These queries are bounded to page keys.
func (r *ProfileManagementRepository) uniqueReviewPage(ctx context.Context, parent string, reviews []pd.ManagedProfileReview) error {
	facts := make([]profileManagementReviewKey, len(reviews))
	for i, v := range reviews {
		facts[i] = profileManagementReviewKey{parent, v.ProfileReviewID, v.Attempt, v.SourceRevision}
	}
	return r.uniqueReviewKeys(ctx, facts)
}

type profileManagementReviewKey struct {
	parent, id string
	attempt    int32
	source     int64
}

func (r *ProfileManagementRepository) uniqueReviewKeys(ctx context.Context, facts []profileManagementReviewKey) error {
	if len(facts) == 0 {
		return nil
	}
	ids, attempts, sources := make([]string, len(facts)), make(bson.A, 0, len(facts)), make(bson.A, 0, len(facts))
	for i, v := range facts {
		ids[i] = v.id
		attempts = append(attempts, bson.M{"profileRevisionId": v.parent, "attempt": v.attempt})
		sources = append(sources, bson.M{"profileRevisionId": v.parent, "sourceRevision": v.source})
	}
	for _, fact := range []struct {
		field  string
		filter bson.M
	}{
		{"profileReviewId", bson.M{"profileReviewId": bson.M{"$in": ids}}},
		{"attempt", bson.M{"$or": attempts}},
		{"sourceRevision", bson.M{"$or": sources}},
	} {
		var group any = "$" + fact.field
		if fact.field != "profileReviewId" {
			group = bson.M{"parent": "$profileRevisionId", "key": "$" + fact.field}
		}
		c, err := r.database.Collection(applicationProfileReviewsCollectionName).Aggregate(ctx, m.Pipeline{
			{{Key: "$match", Value: fact.filter}}, {{Key: "$group", Value: bson.M{"_id": group, "count": bson.M{"$sum": 1}}}},
		})
		if err != nil {
			return pp.ErrProfileManagementUnavailable
		}
		for c.Next(ctx) {
			n, ok := c.Current.Lookup("count").Int32OK()
			if !ok || n != 1 {
				c.Close(ctx)
				return pp.ErrProfileManagementStateInconsistent
			}
		}
		err = c.Err()
		c.Close(ctx)
		if err != nil {
			return pp.ErrProfileManagementUnavailable
		}
	}
	return nil
}
func (r *ProfileManagementRepository) projectRevisions(ctx context.Context, app string, state pd.ManagedProfileState, docs []applicationProfileRevisionDocument) ([]pd.ManagedProfileRevision, error) {
	ids := make([]string, len(docs))
	sequences := map[int32]bool{}
	seen := map[string]bool{}
	for i, d := range docs {
		if seen[d.ProfileRevisionID] || sequences[d.Sequence] {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		seen[d.ProfileRevisionID] = true
		sequences[d.Sequence] = true
		ids[i] = d.ProfileRevisionID
	}
	if len(docs) > 0 {
		ordinals := make([]int32, len(docs))
		for i, d := range docs {
			ordinals[i] = d.Sequence
		}
		for _, fact := range []struct {
			field  string
			filter bson.M
		}{
			{"sequence", bson.M{"applicationId": app, "sequence": bson.M{"$in": ordinals}}},
			{"profileRevisionId", bson.M{"profileRevisionId": bson.M{"$in": ids}}},
		} {
			c, err := r.database.Collection(applicationProfileRevisionsCollectionName).Aggregate(ctx, m.Pipeline{
				{{Key: "$match", Value: fact.filter}},
				{{Key: "$group", Value: bson.M{"_id": "$" + fact.field, "count": bson.M{"$sum": 1}}}},
			})
			if err != nil {
				return nil, pp.ErrProfileManagementUnavailable
			}
			for c.Next(ctx) {
				n, ok := c.Current.Lookup("count").Int32OK()
				if !ok || n != 1 {
					c.Close(ctx)
					return nil, pp.ErrProfileManagementStateInconsistent
				}
			}
			err = c.Err()
			c.Close(ctx)
			if err != nil {
				return nil, pp.ErrProfileManagementUnavailable
			}
		}
	}
	reviews, err := r.latestReviews(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]pd.ManagedProfileRevision, 0, len(docs))
	for _, d := range docs {
		latest := reviews[d.ProfileRevisionID]
		if err := profileManagementReviewMatches(d, latest); err != nil {
			return nil, err
		}
		p := pd.ManagedProfileRevision{ApplicationID: app, ProfileRevisionID: d.ProfileRevisionID, Sequence: d.Sequence, ReviewStatus: d.ReviewStatus, Revision: d.Revision, DisplayName: d.DisplayName, Description: d.Description, Icon: d.Icon, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, LatestReview: projectManagedProfileReview(latest)}
		p.IsCurrentWorkingRevision = state.WorkingProfileRevisionID != nil && *state.WorkingProfileRevisionID == d.ProfileRevisionID
		p.IsCurrentPublished = state.CurrentPublishedProfileRevisionID != nil && *state.CurrentPublishedProfileRevisionID == d.ProfileRevisionID
		if (d.ReviewStatus == "DRAFT" || d.ReviewStatus == "SUBMITTED") != p.IsCurrentWorkingRevision || p.IsCurrentPublished && d.ReviewStatus != "APPROVED" {
			return nil, pp.ErrProfileManagementStateInconsistent
		}
		result = append(result, p)
	}
	return result, nil
}
func profileManagementReviewMatches(d applicationProfileRevisionDocument, review *pd.ApplicationProfileReview) error {
	bad := pp.ErrProfileManagementStateInconsistent
	if review == nil {
		if d.ReviewStatus == "DRAFT" {
			return nil
		}
		return bad
	}
	if review.ApplicationID().String() != d.ApplicationID || review.ProfileRevisionID().String() != d.ProfileRevisionID || review.SubmittedAt().Before(d.CreatedAt) {
		return bad
	}
	snapshot := review.Snapshot()
	if d.DisplayName != snapshot.DisplayName().String() || !equalProfileQueryText(d.Description, snapshot.Description()) || !equalProfileQueryIcon(d.Icon, snapshot.Icon()) {
		return bad
	}
	switch d.ReviewStatus {
	case "SUBMITTED":
		if review.SourceRevision() > math.MaxInt64-1 || !pendingProfileReviewMatchesRevision(review, d) || d.UpdatedBy != review.SubmittedBy().String() || !d.UpdatedAt.Equal(review.SubmittedAt()) {
			return bad
		}
	case "APPROVED", "REJECTED":
		decision := review.Decision()
		if review.SourceRevision() > math.MaxInt64-2 || string(review.Status()) != d.ReviewStatus || decision == nil || string(decision.Outcome) != d.ReviewStatus || d.Revision != review.SourceRevision()+2 || d.UpdatedBy != decision.DecidedBy.String() || !d.UpdatedAt.Equal(decision.DecidedAt) || decision.DecidedAt.Before(review.SubmittedAt()) {
			return bad
		}
	default:
		return bad
	}
	return nil
}
func projectManagedProfileReview(review *pd.ApplicationProfileReview) *pd.ManagedProfileReview {
	if review == nil {
		return nil
	}
	p := &pd.ManagedProfileReview{ProfileReviewID: review.ProfileReviewID().String(), Attempt: review.Attempt(), Status: string(review.Status()), SourceRevision: review.SourceRevision(), SubmittedAt: review.SubmittedAt()}
	if d := review.Decision(); d != nil {
		p.Decision = &pd.ManagedProfileDecision{Outcome: string(d.Outcome), PolicyVersion: d.PolicyVersion, Reason: d.Reason, DecidedAt: d.DecidedAt}
	}
	return p
}
