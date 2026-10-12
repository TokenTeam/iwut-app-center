package mongo

import (
	"context"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	cd "iwut-app-center/internal/catalog/domain"
	pd "iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
)

// This reader deliberately checks stored BSON before Go decoding: the driver
// otherwise accepts numeric coercions and duplicate fields. It is local to the
// two pure-Test queries, and never normalizes or repairs persisted identity.
func pureTestRawValid(raw bson.Raw) bool {
	elements, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, e := range elements {
		if seen[e.Key()] {
			return false
		}
		seen[e.Key()] = true
		v := e.Value()
		if v.Type == bson.TypeEmbeddedDocument && !pureTestRawValid(v.Document()) {
			return false
		}
		if v.Type == bson.TypeArray {
			values, err := v.Array().Values()
			if err != nil {
				return false
			}
			for _, a := range values {
				if a.Type == bson.TypeEmbeddedDocument && !pureTestRawValid(a.Document()) {
					return false
				}
			}
		}
		if v.Type == bson.TypeDateTime && !testerManagementTime(time.UnixMilli(v.DateTime()).UTC()) {
			return false
		}
		if pureTestUUIDField(e.Key()) && v.Type != bson.TypeNull && (v.Type != bson.TypeString || !canonicalTesterManagementID(v.StringValue())) {
			return false
		}
	}
	return true
}
func pureTestUUIDField(key string) bool {
	switch key {
	case "id", "applicationId", "membershipId", "joinedViaJoinLinkId", "joinLinkId", "replacedByJoinLinkId", "publicationId", "testVersionId", "historyId", "previousVersionId", "newVersionId", "approvedReviewId", "versionId", "applicationVersionId", "reviewId", "profileRevisionId", "profileReviewId", "workingProfileRevisionId", "currentPublishedProfileRevisionId", "eventId", "lastPlatformOperationEventId", "lastArchiveEventId":
		return true
	}
	return false
}

var pureTestTimeType = reflect.TypeOf(time.Time{})

func pureTestShape(raw bson.Raw, typ reflect.Type) bool {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := strings.Split(field.Tag.Get("bson"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		v := raw.Lookup(key)
		if !pureTestValueShape(v, field.Type) {
			return false
		}
	}
	return true
}
func pureTestValueShape(v bson.RawValue, typ reflect.Type) bool {
	if typ == reflect.TypeOf(bson.Binary{}) {
		return v.Type == bson.TypeBinary
	}
	if typ.Kind() == reflect.Pointer {
		if v.Type == bson.TypeNull {
			return true
		}
		return pureTestValueShape(v, typ.Elem())
	}
	if typ == pureTestTimeType {
		return v.Type == bson.TypeDateTime && testerManagementTime(time.UnixMilli(v.DateTime()).UTC())
	}
	switch typ.Kind() {
	case reflect.String:
		return v.Type == bson.TypeString
	case reflect.Int32:
		return v.Type == bson.TypeInt32
	case reflect.Int64:
		return v.Type == bson.TypeInt64
	case reflect.Struct:
		return v.Type == bson.TypeEmbeddedDocument && pureTestShape(v.Document(), typ)
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return v.Type == bson.TypeBinary || v.Type == bson.TypeNull
		}
		if v.Type != bson.TypeArray {
			return false
		}
		values, err := v.Array().Values()
		if err != nil {
			return false
		}
		for _, value := range values {
			if !pureTestValueShape(value, typ.Elem()) {
				return false
			}
		}
		return true
	case reflect.Interface:
		return v.Type == bson.TypeEmbeddedDocument || v.Type == bson.TypeNull
	}
	return false
}
func pureTestDocuments[T any](ctx context.Context, db *m.Database, collection string, filter any, opts *options.FindOptionsBuilder) ([]T, error) {
	if opts == nil {
		opts = options.Find()
	}
	cur, err := db.Collection(collection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	result := []T{}
	for cur.Next(ctx) {
		var d T
		raw := cur.Current
		if !pureTestRawValid(raw) || !pureTestShape(raw, reflect.TypeOf(d)) || bson.Unmarshal(raw, &d) != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		result = append(result, d)
	}
	return result, cur.Err()
}

// A projection keeps Grey/Stable facts outside the pure-Test read boundary.
type pureTestPublication struct {
	PublicationID string    `bson:"publicationId"`
	ApplicationID string    `bson:"applicationId"`
	RPCAPIMajor   int32     `bson:"rpcApiMajor"`
	TestVersionID *string   `bson:"testVersionId"`
	Revision      int64     `bson:"revision"`
	CreatedBy     string    `bson:"createdBy"`
	CreatedAt     time.Time `bson:"createdAt"`
	UpdatedBy     string    `bson:"updatedBy"`
	UpdatedAt     time.Time `bson:"updatedAt"`
}

func (p pureTestPublication) document() applicationPublicationDocument {
	return applicationPublicationDocument{PublicationID: p.PublicationID, ApplicationID: p.ApplicationID, RPCAPIMajor: p.RPCAPIMajor, TestVersionID: p.TestVersionID, Revision: p.Revision, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt, UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt}
}

type pureTestFacts struct {
	publications map[string]applicationPublicationDocument
	history      map[string]applicationPublicationHistoryDocument
	versions     map[string]applicationVersionDocument
	oauth        map[string]applicationVersionOAuthConfigDocument
	reviews      map[string]applicationReviewDocument
}

func loadPureTestFacts(ctx context.Context, db *m.Database, appIDs []string, major int32) (*pureTestFacts, error) {
	f := &pureTestFacts{map[string]applicationPublicationDocument{}, map[string]applicationPublicationHistoryDocument{}, map[string]applicationVersionDocument{}, map[string]applicationVersionOAuthConfigDocument{}, map[string]applicationReviewDocument{}}
	if len(appIDs) == 0 {
		return f, nil
	}
	projection := bson.M{"_id": 0, "publicationId": 1, "applicationId": 1, "rpcApiMajor": 1, "testVersionId": 1, "revision": 1, "createdBy": 1, "createdAt": 1, "updatedBy": 1, "updatedAt": 1}
	pubs, err := pureTestDocuments[pureTestPublication](ctx, db, applicationPublicationsCollectionName, bson.M{"applicationId": bson.M{"$in": appIDs}, "rpcApiMajor": major}, options.Find().SetProjection(projection))
	if err != nil {
		return nil, err
	}
	pubIDs, versionIDs := []string{}, []string{}
	for _, p := range pubs {
		d := p.document()
		if _, err := applicationPublicationFromDocument(d); err != nil || p.RPCAPIMajor != major {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		if _, exists := f.publications[p.ApplicationID]; exists {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		f.publications[p.ApplicationID] = d
		pubIDs = append(pubIDs, p.PublicationID)
		if p.TestVersionID != nil {
			versionIDs = append(versionIDs, *p.TestVersionID)
		}
	}
	histories, err := pureTestDocuments[applicationPublicationHistoryDocument](ctx, db, applicationPublicationHistoryCollectionName, bson.M{"publicationId": bson.M{"$in": pubIDs}, "action": bson.M{"$in": bson.A{"SET_TEST_VERSION", "CLEAR_TEST_VERSION"}}}, nil)
	if err != nil {
		return nil, err
	}
	seenHistory := map[string]bool{}
	for _, h := range histories {
		key := h.PublicationID + "\x00" + fmtInt(h.PublicationRevision)
		if seenHistory[key] {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		seenHistory[key] = true
		p, ok := f.publications[h.ApplicationID]
		if !ok || p.PublicationID != h.PublicationID || h.PublicationRevision > p.Revision {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		// Validate each read Test action against its own target; the newest
		// action is subsequently checked against the actual current pointer.
		shadow := p
		shadow.TestVersionID = h.NewVersionID
		if h.Action == "CLEAR_TEST_VERSION" {
			shadow.TestVersionID = nil
		}
		if checkLatestTestHistory(shadow, &h) != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		if h.Action == "SET_TEST_VERSION" {
			if _, err := pd.NewPreflightPolicyVersion(*h.PreflightPolicyVersion); err != nil {
				return nil, cd.ErrMyTestApplicationStateInconsistent
			}
		}
		if old, ok := f.history[h.PublicationID]; !ok || h.PublicationRevision > old.PublicationRevision {
			f.history[h.PublicationID] = h
		}
	}
	for _, p := range f.publications {
		var latest *applicationPublicationHistoryDocument
		if h, ok := f.history[p.PublicationID]; ok {
			latest = &h
		}
		if checkLatestTestHistory(p, latest) != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
	}
	versionIDs = uniqueStrings(versionIDs)
	if len(versionIDs) == 0 {
		return f, nil
	}
	versions, err := pureTestDocuments[applicationVersionDocument](ctx, db, applicationVersionsCollectionName, bson.M{"versionId": bson.M{"$in": versionIDs}}, nil)
	if err != nil {
		return nil, err
	}
	for _, d := range versions {
		if _, ok := f.versions[d.VersionID]; ok {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		f.versions[d.VersionID] = d
	}
	oauth, err := pureTestDocuments[applicationVersionOAuthConfigDocument](ctx, db, applicationVersionOAuthConfigsCollectionName, bson.M{"applicationVersionId": bson.M{"$in": versionIDs}}, nil)
	if err != nil {
		return nil, err
	}
	for _, d := range oauth {
		if _, ok := f.oauth[d.ApplicationVersionID]; ok {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		f.oauth[d.ApplicationVersionID] = d
	}
	reviews, err := pureTestDocuments[applicationReviewDocument](ctx, db, applicationReviewsCollectionName, bson.M{"versionId": bson.M{"$in": versionIDs}}, nil)
	if err != nil {
		return nil, err
	}
	seenReviews := map[string]bool{}
	for _, d := range reviews {
		if _, err := applicationReviewFromDocument(d); err != nil {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		if version, ok := f.versions[d.VersionID]; !ok || version.ApplicationID != d.ApplicationID {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		key := d.VersionID + "\x00" + fmtInt(int64(d.Attempt))
		if seenReviews[key] {
			return nil, cd.ErrMyTestApplicationStateInconsistent
		}
		seenReviews[key] = true
		if old, ok := f.reviews[d.VersionID]; !ok || d.Attempt > old.Attempt {
			f.reviews[d.VersionID] = d
		}
	}
	return f, nil
}
func fmtInt(v int64) string { return strconv.FormatInt(v, 10) }

// Both UC012 and UC038 use this pure resolver. Absence and missing capabilities
// remain distinct from corruption; only the consumer selects RPC vs card form.
func resolvePureTest(app shared.ApplicationID, major int32, host []cd.CapabilityName, f *pureTestFacts) (*cd.TestLaunchDescriptor, []cd.CapabilityName, error) {
	bad := cd.ErrMyTestApplicationStateInconsistent
	p, ok := f.publications[app.String()]
	if !ok || p.TestVersionID == nil {
		return nil, nil, nil
	}
	h, ok := f.history[p.PublicationID]
	if !ok || h.ApprovedReviewID == nil {
		return nil, nil, bad
	}
	v, ok := f.versions[*p.TestVersionID]
	if !ok {
		return nil, nil, bad
	}
	o, ok := f.oauth[v.VersionID]
	if !ok {
		return nil, nil, bad
	}
	if _, err := applicationVersionFromDocument(v, o); err != nil || v.ApplicationID != app.String() || o.ApplicationID != app.String() || v.ReviewStatus != "APPROVED" || major < v.RPCApiMinVersion || major >= v.RPCApiMaxVersionExclusive {
		return nil, nil, bad
	}
	review, ok := f.reviews[v.VersionID]
	if !ok {
		return nil, nil, bad
	}
	approved, err := applicationReviewFromDocument(review)
	if err != nil || review.ApplicationID != app.String() || review.ReviewID != *h.ApprovedReviewID || review.Status != "APPROVED" || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || v.Revision != review.SourceVersionRevision+2 {
		return nil, nil, bad
	}
	_, _, snapshot, err := applicationVersionDocumentToDecisionVersion(v, o)
	if err != nil || !snapshot.Equal(approved.Snapshot()) || v.UpdatedBy != review.Decision.DecidedBy || !v.UpdatedAt.Equal(review.Decision.DecidedAt) {
		return nil, nil, bad
	}
	required := make([]cd.CapabilityName, len(v.RequiredCapabilities))
	for i, n := range v.RequiredCapabilities {
		required[i] = cd.CapabilityName(n)
	}
	if missing := cd.MissingCapabilities(required, host); len(missing) > 0 {
		return nil, missing, nil
	}
	d, err := cd.NewTestLaunchDescriptor(app, p.PublicationID, p.Revision, major, v.VersionID, v.VersionLabel, v.LaunchURL, v.RPCApiMinVersion, v.RPCApiMaxVersionExclusive, required, v.RequiredScopes, v.OptionalScopes)
	if err != nil {
		return nil, nil, bad
	}
	return d, nil, nil
}
