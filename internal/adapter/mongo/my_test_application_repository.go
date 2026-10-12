package mongo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	pd "iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
)

type MyTestApplicationRepository struct {
	database  *m.Database
	cursorKey MyTestApplicationCursorKey
}

func NewMyTestApplicationRepository(db *m.Database, key MyTestApplicationCursorKey) *MyTestApplicationRepository {
	return &MyTestApplicationRepository{db, key}
}

var _ cp.MyTestApplicationRepository = (*MyTestApplicationRepository)(nil)

// Scan only qualification/sort keys. Full episode and source facts become
// necessary only after the Application's online gate has passed.
type myTestMembershipKey struct {
	MembershipID  string `bson:"membershipId"`
	ApplicationID string `bson:"applicationId"`
	TesterAuthID  string `bson:"testerAuthId"`
	Status        string `bson:"status"`
}

func myTestMembershipProjection() bson.M {
	return bson.M{"_id": 0, "membershipId": 1, "applicationId": 1, "testerAuthId": 1, "status": 1}
}

func myTestSnapshot[T any](r *MyTestApplicationRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
	var zero T
	if r == nil || r.database == nil || !r.cursorKey.valid {
		return zero, cd.ErrMyTestApplicationUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return zero, cd.ErrMyTestApplicationUnavailable
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx, time.Now().UTC()) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		for _, known := range []error{cd.ErrMyTestApplicationStateInconsistent, cd.ErrMyTestApplicationNotFound, cd.ErrInvalidMyTestApplicationPageToken} {
			if errors.Is(err, known) {
				return zero, known
			}
		}
		return zero, cd.ErrMyTestApplicationUnavailable
	}
	result, ok := value.(T)
	if !ok {
		return zero, cd.ErrMyTestApplicationStateInconsistent
	}
	return result, nil
}
func (r *MyTestApplicationRepository) ListMyTest(ctx context.Context, caller shared.AuthID, major int32, host []cd.CapabilityName, size int32, token string) (*cd.MyTestApplicationPage, error) {
	if r == nil {
		return nil, cd.ErrMyTestApplicationUnavailable
	}
	bind := myTestBinding(caller, major, host)
	after, err := r.decodeCursor(token, bind)
	if err != nil {
		return nil, err
	}
	return myTestSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*cd.MyTestApplicationPage, error) {
		filter := bson.M{"testerAuthId": caller.String(), "status": "ACTIVE"}
		if after != "" {
			filter["applicationId"] = bson.M{"$gt": after}
		}
		members, err := pureTestDocuments[myTestMembershipKey](tx, r.database, applicationTesterMembershipsCollectionName, filter, options.Find().SetProjection(myTestMembershipProjection()).SetSort(bson.D{{Key: "applicationId", Value: 1}, {Key: "membershipId", Value: 1}}).SetCollation(&options.Collation{Locale: "simple"}).SetLimit(catalogScanBudget+1))
		if err != nil {
			return nil, err
		}
		for i, d := range members {
			if d.TesterAuthID != caller.String() || d.Status != "ACTIVE" || (i > 0 && members[i-1].ApplicationID == d.ApplicationID) {
				return nil, cd.ErrMyTestApplicationStateInconsistent
			}
		}
		// The lookahead may itself have a second ACTIVE episode beyond the
		// bounded scan. Validate uniqueness for every read application key in
		// one batch, without consuming that lookahead or its private facts.
		keys := []string{}
		for _, d := range members {
			keys = append(keys, d.ApplicationID)
		}
		if len(keys) > 0 {
			seen := map[string]bool{}
			cursor, err := r.database.Collection(applicationTesterMembershipsCollectionName).Find(tx, bson.M{"testerAuthId": caller.String(), "status": "ACTIVE", "applicationId": bson.M{"$in": keys}}, options.Find().SetProjection(bson.M{"_id": 0, "applicationId": 1}))
			if err != nil {
				return nil, err
			}
			defer cursor.Close(tx)
			for cursor.Next(tx) {
				raw := cursor.Current
				if !pureTestRawValid(raw) || raw.Lookup("applicationId").Type != bson.TypeString {
					return nil, cd.ErrMyTestApplicationStateInconsistent
				}
				id := raw.Lookup("applicationId").StringValue()
				if seen[id] {
					return nil, cd.ErrMyTestApplicationStateInconsistent
				}
				seen[id] = true
			}
			if cursor.Err() != nil {
				return nil, cursor.Err()
			}
		}
		hasMore := len(members) > int(catalogScanBudget)
		if hasMore {
			members = members[:catalogScanBudget]
		}
		items, err := r.items(tx, members, caller, major, host, false)
		if err != nil {
			return nil, err
		}
		page := &cd.MyTestApplicationPage{Items: []*cd.MyTestApplicationItem{}, AsOf: asOf}
		last, bytesUsed := after, 1024
		for i, d := range members {
			item := items[d.ApplicationID]
			if item != nil {
				itemBytes := myTestItemBytes(item)
				if len(page.Items) > 0 && (len(page.Items) >= int(size) || bytesUsed+itemBytes > catalogResponseBudget) {
					hasMore = true
					break
				}
				page.Items = append(page.Items, item)
				bytesUsed += itemBytes
			}
			last = d.ApplicationID
			if len(page.Items) >= int(size) {
				hasMore = hasMore || i+1 < len(members)
				break
			}
		}
		if hasMore {
			bind.Last = last
			page.NextPageToken, err = r.encodeCursor(bind)
			if err != nil {
				return nil, err
			}
		}
		return page, nil
	})
}
func (r *MyTestApplicationRepository) GetMyTest(ctx context.Context, app shared.ApplicationID, caller shared.AuthID, major int32, host []cd.CapabilityName) (*cd.MyTestApplicationDetail, error) {
	return myTestSnapshot(r, ctx, func(tx context.Context, asOf time.Time) (*cd.MyTestApplicationDetail, error) {
		// Missing Application is a normal hidden detail, even if an orphan
		// membership exists. A list has already discovered that relationship.
		count, err := r.database.Collection(applicationsCollectionName).CountDocuments(tx, bson.M{"id": app.String()}, options.Count().SetLimit(1))
		if err != nil {
			return nil, err
		}
		if count == 0 {
			return nil, cd.ErrMyTestApplicationNotFound
		}
		members, err := pureTestDocuments[myTestMembershipKey](tx, r.database, applicationTesterMembershipsCollectionName, bson.M{"applicationId": app.String(), "testerAuthId": caller.String(), "status": "ACTIVE"}, options.Find().SetProjection(myTestMembershipProjection()))
		if err != nil {
			return nil, err
		}
		if len(members) == 0 {
			return nil, cd.ErrMyTestApplicationNotFound
		}
		if len(members) != 1 {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		items, err := r.items(tx, members, caller, major, host, true)
		if err != nil {
			return nil, err
		}
		item := items[app.String()]
		if item == nil {
			return nil, cd.ErrMyTestApplicationNotFound
		}
		return &cd.MyTestApplicationDetail{Item: item, AsOf: asOf}, nil
	})
}
func (r *MyTestApplicationRepository) items(ctx context.Context, members []myTestMembershipKey, caller shared.AuthID, major int32, host []cd.CapabilityName, detail bool) (map[string]*cd.MyTestApplicationItem, error) {
	result := map[string]*cd.MyTestApplicationItem{}
	if len(members) == 0 {
		return result, nil
	}
	appIDs := []string{}
	for _, d := range members {
		appIDs = append(appIDs, d.ApplicationID)
	}
	apps, err := pureTestDocuments[applicationDocument](ctx, r.database, applicationsCollectionName, bson.M{"id": bson.M{"$in": appIDs}}, nil)
	if err != nil {
		return nil, err
	}
	online, err := r.onlineApps(ctx, apps)
	if err != nil {
		return nil, err
	}
	if len(apps) != len(members) {
		if detail {
			return nil, cd.ErrMyTestApplicationNotFound
		}
		return nil, cd.ErrMyTestApplicationStateInconsistent
	}
	onlineKeys := map[string]myTestMembershipKey{}
	onlineMemberIDs := []string{}
	for _, d := range members {
		if !online[d.ApplicationID] {
			continue
		}
		onlineKeys[d.MembershipID] = d
		onlineMemberIDs = append(onlineMemberIDs, d.MembershipID)
	}
	if len(onlineMemberIDs) == 0 {
		return result, nil
	}
	onlineMembers, err := pureTestDocuments[applicationTesterMembershipDocument](ctx, r.database, applicationTesterMembershipsCollectionName, bson.M{"membershipId": bson.M{"$in": onlineMemberIDs}, "testerAuthId": caller.String(), "status": "ACTIVE"}, nil)
	if err != nil {
		return nil, err
	}
	if len(onlineMembers) != len(onlineMemberIDs) {
		return nil, cd.ErrMyTestApplicationStateInconsistent
	}
	sources := []string{}
	onlineIDs := []string{}
	seenMembers := map[string]bool{}
	for _, d := range onlineMembers {
		key, ok := onlineKeys[d.MembershipID]
		if !ok || seenMembers[d.MembershipID] || key.ApplicationID != d.ApplicationID {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		seenMembers[d.MembershipID] = true
		restored, err := testerMembershipFromDocument(d)
		if err != nil || restored.TesterAuthID() != caller || restored.Status() != td.MembershipStatusActive {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		sources = append(sources, d.JoinedViaJoinLinkID)
		onlineIDs = append(onlineIDs, d.ApplicationID)
	}
	links, err := pureTestDocuments[applicationTesterJoinLinkDocument](ctx, r.database, applicationTesterJoinLinksCollectionName, bson.M{"joinLinkId": bson.M{"$in": uniqueStrings(sources)}}, nil)
	if err != nil {
		return nil, err
	}
	linkMap := map[string]applicationTesterJoinLinkDocument{}
	for _, d := range links {
		if _, ok := linkMap[d.JoinLinkID]; ok {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		if _, err := testerJoinLinkFromDocument(d); err != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		linkMap[d.JoinLinkID] = d
	}
	for _, d := range onlineMembers {
		source, ok := linkMap[d.JoinedViaJoinLinkID]
		if !ok || source.ApplicationID != d.ApplicationID {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
	}
	profiles, err := r.publicProfiles(ctx, onlineIDs)
	if err != nil {
		return nil, err
	}
	readyIDs := []string{}
	for _, id := range onlineIDs {
		if profiles[id] != nil {
			readyIDs = append(readyIDs, id)
		}
	}
	facts, err := loadPureTestFacts(ctx, r.database, readyIDs, major)
	if err != nil {
		return nil, err
	}
	for _, d := range onlineMembers {
		app := shared.ApplicationID(d.ApplicationID)
		profile := profiles[d.ApplicationID]
		state := cd.MyTestProfileRequired
		var target *cd.TestLaunchDescriptor
		var missing []cd.CapabilityName
		if profile != nil {
			target, missing, err = resolvePureTest(app, major, host, facts)
			if err != nil {
				return nil, err
			}
			state = cd.MyTestTargetUnavailable
			if len(missing) > 0 {
				state = cd.MyTestCapabilitiesInsufficient
			}
			if target != nil {
				state = cd.MyTestReady
			}
		}
		item, err := cd.NewMyTestApplicationItem(app, cd.OwnTestMembership{MembershipID: cd.MyTestMembershipID(d.MembershipID), JoinedAt: d.JoinedAt}, profile, state, target, missing)
		if err != nil {
			return nil, err
		}
		result[d.ApplicationID] = item
	}
	return result, nil
}

type myTestProfilePointer struct {
	ApplicationID                     string  `bson:"applicationId"`
	CurrentPublishedProfileRevisionID *string `bson:"currentPublishedProfileRevisionId"`
}

func (r *MyTestApplicationRepository) publicProfiles(ctx context.Context, apps []string) (map[string]*cd.PublicApplicationProfile, error) {
	result := map[string]*cd.PublicApplicationProfile{}
	projections, err := pureTestDocuments[myTestProfilePointer](ctx, r.database, applicationProfilesCollectionName, bson.M{"applicationId": bson.M{"$in": apps}}, options.Find().SetProjection(bson.M{"_id": 0, "applicationId": 1, "currentPublishedProfileRevisionId": 1}))
	if err != nil {
		return nil, err
	}
	ids := []string{}
	ptrs := map[string]string{}
	seen := map[string]bool{}
	for _, d := range projections {
		if seen[d.ApplicationID] {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		seen[d.ApplicationID] = true
		if d.CurrentPublishedProfileRevisionID != nil {
			ids = append(ids, *d.CurrentPublishedProfileRevisionID)
			ptrs[d.ApplicationID] = *d.CurrentPublishedProfileRevisionID
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	revisions, err := pureTestDocuments[applicationProfileRevisionDocument](ctx, r.database, applicationProfileRevisionsCollectionName, bson.M{"profileRevisionId": bson.M{"$in": ids}}, nil)
	if err != nil {
		return nil, err
	}
	revs := map[string]*pd.ApplicationProfileRevision{}
	for _, d := range revisions {
		rev, err := profileRevisionFromDocument(d)
		if err != nil || revs[d.ProfileRevisionID] != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		revs[d.ProfileRevisionID] = rev
	}
	reviews := map[string]*pd.ApplicationProfileReview{}
	seenReviews := map[string]bool{}
	err = collectRaw(ctx, r.database.Collection(applicationProfileReviewsCollectionName), bson.M{"profileRevisionId": bson.M{"$in": ids}}, func(raw bson.Raw) error {
		if !pureTestRawValid(raw) || !pureTestShape(raw, reflect.TypeOf(applicationProfileReviewDocument{})) {
			return cd.ErrMyTestApplicationStateInconsistent
		}
		decision, ok := raw.Lookup("decision").DocumentOK()
		if !ok || !pureTestShape(decision, reflect.TypeOf(profileReviewDecisionDocument{})) {
			return cd.ErrMyTestApplicationStateInconsistent
		}
		review, err := profileReviewDecisionCandidate(raw)
		if err != nil {
			return cd.ErrMyTestApplicationStateInconsistent
		}
		key := review.ProfileRevisionID().String() + "\x00" + fmtInt(int64(review.Attempt()))
		if seenReviews[key] {
			return cd.ErrMyTestApplicationStateInconsistent
		}
		seenReviews[key] = true
		id := review.ProfileRevisionID().String()
		if old := reviews[id]; old == nil || review.Attempt() > old.Attempt() {
			reviews[id] = review
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for app, id := range ptrs {
		rev := revs[id]
		if rev == nil || rev.ApplicationID().String() != app || rev.ReviewStatus() != pd.ReviewStatusApproved || !approvedProfileSnapshotMatches(rev, reviews[id]) {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		profile, err := cd.NewPublicApplicationProfile(id, rev.DisplayName().String(), optionalDescription(rev.Description()), optionalIcon(rev.Icon()))
		if err != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		result[app] = profile
	}
	return result, nil
}

// Include escaped string lengths and a conservative per-item structural bound.
// Large Profile/descriptor content therefore cannot be hidden by private VO fields.
func myTestItemBytes(item *cd.MyTestApplicationItem) int {
	values := []any{item.ApplicationID.String(), item.Membership.MembershipID.String(), item.MissingCapabilities}
	if p := item.Profile; p != nil {
		values = append(values, p.RevisionID(), p.DisplayName(), p.Description(), p.Icon())
	}
	if d := item.LaunchTarget; d != nil {
		values = append(values, d.PublicationID(), d.VersionID(), d.VersionLabel(), d.LaunchURL(), d.RequiredCapabilities(), d.RequiredScopes(), d.OptionalScopes())
	}
	raw, _ := json.Marshal(values)
	return len(raw) + 2048
}
