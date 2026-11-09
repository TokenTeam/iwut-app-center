package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	ad "iwut-app-center/internal/application/domain"
	ap "iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type ManagementActivityRepository struct {
	db  *dm.Database
	key ManagementActivityCursorKey
}

func NewManagementActivityRepository(db *dm.Database, key ManagementActivityCursorKey) *ManagementActivityRepository {
	return &ManagementActivityRepository{db, key}
}

var _ ap.ManagementActivityRepository = (*ManagementActivityRepository)(nil)

func managementActivitySnapshot[T any](ctx context.Context, r *ManagementActivityRepository, fn func(context.Context) (T, error)) (T, error) {
	var empty T
	if r == nil || r.db == nil || !r.key.valid {
		return empty, ad.ErrManagementActivityUnavailable
	}
	session, err := r.db.Client().StartSession()
	if err != nil {
		return empty, ad.ErrManagementActivityUnavailable
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		for _, known := range []error{ad.ErrManagementActivityNotFound, ad.ErrManagementActivityStateInconsistent, ad.ErrInvalidManagementActivityPageToken, ad.ErrManagementActivityResponseTooLarge} {
			if errors.Is(err, known) {
				return empty, err
			}
		}
		return empty, fmt.Errorf("%w: %w", ad.ErrManagementActivityUnavailable, err)
	}
	result, ok := value.(T)
	if !ok {
		return empty, ad.ErrManagementActivityStateInconsistent
	}
	return result, nil
}
func (r *ManagementActivityRepository) find(ctx context.Context, collection string, filter bson.M, projection bson.M, sort bson.D, limit int64, hint string) ([]bson.Raw, error) {
	opts := options.Find().SetProjection(projection).SetLimit(limit)
	if len(sort) > 0 {
		opts.SetSort(sort)
	}
	if hint != "" {
		opts.SetHint(hint)
	}
	cur, err := r.db.Collection(collection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	rows := []bson.Raw{}
	for cur.Next(ctx) {
		if !profileManagementRawValid(cur.Current, false) {
			return nil, ad.ErrManagementActivityStateInconsistent
		}
		rows = append(rows, append(bson.Raw(nil), cur.Current...))
	}
	return rows, cur.Err()
}
func (r *ManagementActivityRepository) authorize(ctx context.Context, caller shared.AuthID, app shared.ApplicationID) (int64, error) {
	rows, err := r.find(ctx, applicationsCollectionName, bson.M{"id": app.String()}, bson.M{"_id": 0, "id": 1, "adminId": 1, "ownershipRevision": 1}, nil, 2, "")
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, ad.ErrManagementActivityNotFound
	}
	if len(rows) != 1 {
		return 0, ad.ErrManagementActivityStateInconsistent
	}
	raw := rows[0]
	id, ok := raw.Lookup("id").StringValueOK()
	owner, ownerOK := raw.Lookup("adminId").StringValueOK()
	rev, revOK := raw.Lookup("ownershipRevision").Int64OK()
	if !ok || id != app.String() || !ownerOK || owner == "" || !revOK || rev < 1 {
		return 0, ad.ErrManagementActivityStateInconsistent
	}
	if owner != caller.String() {
		return 0, ad.ErrManagementActivityNotFound
	}
	return rev, nil
}

type managementActivitySource struct {
	activity ad.ManagementActivity
	parent   string
	reason   *string
}

func activityFields(source int) (collection, id, parent, revision string) {
	if source == 0 {
		return applicationReviewsCollectionName, "reviewId", "versionId", "sourceVersionRevision"
	}
	return applicationProfileReviewsCollectionName, "profileReviewId", "profileRevisionId", "sourceRevision"
}
func activityProjection(source int) bson.M {
	_, id, parent, revision := activityFields(source)
	p := bson.M{"_id": 0, id: 1, parent: 1, revision: 1, "applicationId": 1, "attempt": 1, "status": 1, "submittedAt": 1, "policyBinding.version": 1, "decision.outcome": 1, "decision.decidedAt": 1, "decision.reason": 1}
	if source == 0 {
		p["snapshot.versionLabel"] = 1
		p["decision.reviewPolicyVersion"] = 1
	} else {
		p["decision.policyVersion"] = 1
	}
	return p
}
func activitySource(raw bson.Raw, app shared.ApplicationID, source, phase int) (managementActivitySource, error) {
	var empty managementActivitySource
	bad := ad.ErrManagementActivityStateInconsistent
	_, idField, parentField, revisionField := activityFields(source)
	id, idOK := raw.Lookup(idField).StringValueOK()
	parent, parentOK := raw.Lookup(parentField).StringValueOK()
	aid, appOK := raw.Lookup("applicationId").StringValueOK()
	attempt, attemptOK := raw.Lookup("attempt").Int32OK()
	revision, revisionOK := raw.Lookup(revisionField).Int64OK()
	state, stateOK := raw.Lookup("status").StringValueOK()
	at, atOK := raw.Lookup("submittedAt").DateTimeOK()
	policy, policyOK := raw.Lookup("policyBinding", "version").StringValueOK()
	locator := ad.ManagementActivityLocator{Source: source, Phase: phase, ReviewID: id}
	_, parentValid := shared.ParseApplicationID(parent)
	if !idOK || !locator.Valid() || !parentOK || !parentValid || !appOK || aid != app.String() || !attemptOK || attempt < 1 || !revisionOK || revision < 1 || !stateOK || !atOK || !policyOK || !activityPolicyVersionValid(policy) {
		return empty, bad
	}
	submitted := time.UnixMilli(at).UTC()
	occurred := submitted
	decision := raw.Lookup("decision")
	var reason *string
	kind := ad.VersionReviewSubmitted
	if source == 1 {
		kind = ad.ProfileReviewSubmitted
	}
	if state == "PENDING" {
		if (decision.Type != bson.TypeNull && decision.Type != 0) || phase == 0 {
			return empty, bad
		}
	} else {
		if state != "APPROVED" && state != "REJECTED" || decision.Type != bson.TypeEmbeddedDocument {
			return empty, bad
		}
		doc := decision.Document()
		outcome, outcomeOK := doc.Lookup("outcome").StringValueOK()
		decided, decidedOK := doc.Lookup("decidedAt").DateTimeOK()
		policyField := "reviewPolicyVersion"
		if source == 1 {
			policyField = "policyVersion"
		}
		bound, boundOK := doc.Lookup(policyField).StringValueOK()
		if !outcomeOK || outcome != state || !decidedOK || decided < at || !boundOK || bound != policy {
			return empty, bad
		}
		value := doc.Lookup("reason")
		if state == "REJECTED" {
			s, ok := value.StringValueOK()
			if !ok || strings.TrimSpace(s) == "" || !utf8.ValidString(s) {
				return empty, bad
			}
			reason = &s
		} else if value.Type != 0 && value.Type != bson.TypeNull && value.Type != bson.TypeString {
			return empty, bad
		}
		if phase == 0 {
			occurred = time.UnixMilli(decided).UTC()
			if source == 0 {
				if state == "APPROVED" {
					kind = ad.VersionReviewApproved
				} else {
					kind = ad.VersionReviewRejected
				}
			} else {
				if state == "APPROVED" {
					kind = ad.ProfileReviewApproved
				} else {
					kind = ad.ProfileReviewRejected
				}
			}
		}
	}
	a := ad.ManagementActivity{ActivityID: locator.String(), ApplicationID: app, Kind: kind, OccurredAt: occurred}
	if source == 0 {
		label, ok := raw.Lookup("snapshot", "versionLabel").StringValueOK()
		if !ok || label == "" {
			return empty, bad
		}
		a.VersionLabelAtSubmission = &label
		a.VersionReview = &ad.VersionReviewActivityTarget{VersionID: parent, ReviewID: id, Attempt: attempt}
	} else {
		a.ProfileReview = &ad.ProfileReviewActivityTarget{ProfileRevisionID: parent, ProfileReviewID: id, Attempt: attempt}
	}
	// Profile sequence comes from its parent in the same snapshot, after merging.
	if source == 0 && a.Validate() != nil {
		return empty, bad
	}
	if phase == 1 {
		reason = nil
	}
	return managementActivitySource{a, parent, reason}, nil
}
func (r *ManagementActivityRepository) parents(ctx context.Context, app shared.ApplicationID, rows []managementActivitySource) error {
	for source := 0; source < 2; source++ {
		ids := []string{}
		for _, row := range rows {
			if row.activity.Locator().Source == source && !slices.Contains(ids, row.parent) {
				ids = append(ids, row.parent)
			}
		}
		if len(ids) == 0 {
			continue
		}
		collection, idField := applicationVersionsCollectionName, "versionId"
		projection := bson.M{"_id": 0, "versionId": 1, "applicationId": 1}
		if source == 1 {
			collection, idField = applicationProfileRevisionsCollectionName, "profileRevisionId"
			projection = bson.M{"_id": 0, "profileRevisionId": 1, "applicationId": 1, "sequence": 1}
		}
		parents, err := r.find(ctx, collection, bson.M{idField: bson.M{"$in": ids}}, projection, nil, int64(len(ids))+1, "")
		if err != nil {
			return err
		}
		found := map[string]int64{}
		for _, raw := range parents {
			id, ok := raw.Lookup(idField).StringValueOK()
			aid, appOK := raw.Lookup("applicationId").StringValueOK()
			if !ok || !appOK || aid != app.String() {
				return ad.ErrManagementActivityStateInconsistent
			}
			if _, exists := found[id]; exists {
				return ad.ErrManagementActivityStateInconsistent
			}
			sequence := int64(0)
			if source == 1 {
				seq, ok := raw.Lookup("sequence").Int32OK()
				if !ok || seq < 1 {
					return ad.ErrManagementActivityStateInconsistent
				}
				sequence = int64(seq)
			}
			found[id] = sequence
		}
		for i := range rows {
			if rows[i].activity.Locator().Source != source {
				continue
			}
			seq, ok := found[rows[i].parent]
			if !ok {
				return ad.ErrManagementActivityStateInconsistent
			}
			rows[i].activity.ProfileRevisionSequence = seq
			if rows[i].activity.Validate() != nil {
				return ad.ErrManagementActivityStateInconsistent
			}
		}
	}
	return nil
}
func (r *ManagementActivityRepository) ListManagementActivity(ctx context.Context, caller shared.AuthID, app shared.ApplicationID, q ad.ManagementActivityQuery, at time.Time) (ad.ManagementActivityPage, error) {
	return managementActivitySnapshot(ctx, r, func(tx context.Context) (ad.ManagementActivityPage, error) {
		var empty ad.ManagementActivityPage
		ownership, err := r.authorize(tx, caller, app)
		if err != nil {
			return empty, err
		}
		binding := managementActivityBinding(caller, app, ownership, q)
		pos, err := r.decodeCursor(q.PageToken, binding)
		if err != nil {
			return empty, err
		}
		candidates := make([]managementActivitySource, 0, 4*(int(q.PageSize)+1))
		for source := 0; source < 2; source++ {
			for phase := 0; phase < 2; phase++ {
				collection, id, _, _ := activityFields(source)
				timeField := "submittedAt"
				hint := activitySubmissionIndexName
				submitted, approved, rejected := ad.VersionReviewSubmitted, ad.VersionReviewApproved, ad.VersionReviewRejected
				if source == 1 {
					submitted, approved, rejected = ad.ProfileReviewSubmitted, ad.ProfileReviewApproved, ad.ProfileReviewRejected
				}
				filter := bson.M{"applicationId": app.String()}
				if phase == 1 {
					if !q.Includes(submitted) {
						continue
					}
				} else {
					outcomes := []string{}
					if q.Includes(approved) {
						outcomes = append(outcomes, "APPROVED")
					}
					if q.Includes(rejected) {
						outcomes = append(outcomes, "REJECTED")
					}
					if len(outcomes) == 0 {
						continue
					}
					filter["decision.outcome"] = bson.M{"$in": outcomes}
					timeField = "decision.decidedAt"
					hint = activityDecisionIndexName
				}
				if after := managementActivityAfter(pos, source, phase, timeField, id); after != nil {
					filter["$and"] = bson.A{after}
				}
				rawRows, err := r.find(tx, collection, filter, activityProjection(source), bson.D{{Key: timeField, Value: -1}, {Key: id, Value: -1}}, int64(q.PageSize)+1, hint)
				if err != nil {
					return empty, err
				}
				for _, raw := range rawRows {
					row, err := activitySource(raw, app, source, phase)
					if err != nil {
						return empty, err
					}
					candidates = append(candidates, row)
				}
			}
		}
		slices.SortFunc(candidates, func(a, b managementActivitySource) int { return ad.CompareManagementActivity(a.activity, b.activity) })
		if len(candidates) > int(q.PageSize)+1 {
			candidates = candidates[:int(q.PageSize)+1]
		}
		// The lookahead is necessary data too. A corrupted duplicate source
		// must not become a second activity on the following page.
		seen := make(map[string]bool, len(candidates))
		for _, row := range candidates {
			if seen[row.activity.ActivityID] {
				return empty, ad.ErrManagementActivityStateInconsistent
			}
			seen[row.activity.ActivityID] = true
		}
		if err = r.parents(tx, app, candidates); err != nil {
			return empty, err
		}
		page := ad.ManagementActivityPage{Items: make([]ad.ManagementActivity, 0, q.PageSize), AsOf: at}
		for i, row := range candidates {
			if i == int(q.PageSize) {
				page.NextPageToken = r.encodeCursor(binding, page.Items[len(page.Items)-1])
				break
			}
			page.Items = append(page.Items, row.activity)
		}
		return page, page.Validate(app, q, at)
	})
}
func (r *ManagementActivityRepository) GetManagementActivity(ctx context.Context, caller shared.AuthID, app shared.ApplicationID, id ad.ManagementActivityLocator, at time.Time) (ad.ManagementActivityDetail, error) {
	return managementActivitySnapshot(ctx, r, func(tx context.Context) (ad.ManagementActivityDetail, error) {
		var empty ad.ManagementActivityDetail
		if _, err := r.authorize(tx, caller, app); err != nil {
			return empty, err
		}
		collection, idField, _, _ := activityFields(id.Source)
		rows, err := r.find(tx, collection, bson.M{"applicationId": app.String(), idField: id.ReviewID}, activityProjection(id.Source), nil, 2, "")
		if err != nil {
			return empty, err
		}
		if len(rows) == 0 {
			return empty, ad.ErrManagementActivityNotFound
		}
		if len(rows) != 1 {
			return empty, ad.ErrManagementActivityStateInconsistent
		}
		if id.Phase == 0 && rows[0].Lookup("status").Type == bson.TypeString && rows[0].Lookup("status").StringValue() == "PENDING" && (rows[0].Lookup("decision").Type == bson.TypeNull || rows[0].Lookup("decision").Type == 0) {
			return empty, ad.ErrManagementActivityNotFound
		}
		row, err := activitySource(rows[0], app, id.Source, id.Phase)
		if err != nil {
			return empty, err
		}
		selected := []managementActivitySource{row}
		if err = r.parents(tx, app, selected); err != nil {
			return empty, err
		}
		detail := ad.ManagementActivityDetail{Activity: selected[0].activity, RejectionReason: row.reason, AsOf: at}
		return detail, detail.Validate(app, id, at)
	})
}

func activityPolicyVersionValid(value string) bool {
	if len(value) < 1 || len(value) > 50 {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
