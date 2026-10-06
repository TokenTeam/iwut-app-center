package mongo

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"

	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
	filterdomain "iwut-app-center/internal/filter/domain"
	profiledomain "iwut-app-center/internal/profile/domain"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
)

const catalogScanBudget int64 = 500
const catalogResponseBudget = 3 * 1024 * 1024

type PublicCatalogRepository struct{ database *drivermongo.Database }

func NewPublicCatalogRepository(database *drivermongo.Database) *PublicCatalogRepository {
	return &PublicCatalogRepository{database: database}
}

var _ catalogport.PublicApplicationCatalogRepository = (*PublicCatalogRepository)(nil)

type catalogCursor struct {
	Version     int    `json:"v"`
	Last        string `json:"last"`
	Fingerprint string `json:"q"`
}

func (r *PublicCatalogRepository) ListPublic(ctx context.Context, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName, pageSize int32, token string) (*catalogdomain.PublicApplicationCatalogPage, error) {
	if r == nil || r.database == nil || major < 1 || pageSize < 1 {
		return nil, fmt.Errorf("list public catalog: invalid repository input")
	}
	after, err := decodeCatalogCursor(token, major, host)
	if err != nil {
		return nil, catalogport.ErrInvalidPageToken
	}
	return r.withSnapshotPage(ctx, func(tx context.Context) (*catalogdomain.PublicApplicationCatalogPage, error) {
		filter := bson.M{"rpcApiMajor": major, "stableVersionId": bson.M{"$type": "string"}}
		if after != "" {
			filter["applicationId"] = bson.M{"$gt": after}
		}
		cursor, err := r.database.Collection(applicationPublicationsCollectionName).Find(tx, filter, options.Find().SetSort(bson.D{{Key: "applicationId", Value: 1}}).SetLimit(catalogScanBudget))
		if err != nil {
			return nil, err
		}
		defer cursor.Close(tx)
		var publications []applicationPublicationDocument
		if err = cursor.All(tx, &publications); err != nil {
			return nil, err
		}
		facts, err := r.loadCatalogFacts(tx, publications, authID)
		if err != nil {
			return nil, err
		}
		items := make([]*catalogdomain.PublicApplicationCatalogItem, 0, pageSize)
		lastScanned, responseBytes := after, 0
		stopped := false
		for index, publication := range publications {
			item, eligible, itemBytes, itemErr := r.catalogItem(publication, authID, major, host, facts)
			if itemErr != nil {
				return nil, itemErr
			}
			if !eligible {
				lastScanned = publication.ApplicationID
				continue
			}
			if len(items) > 0 && (len(items) >= int(pageSize) || responseBytes+itemBytes > catalogResponseBudget) {
				stopped = true
				break
			}
			items = append(items, item)
			responseBytes += itemBytes
			lastScanned = publication.ApplicationID
			if len(items) >= int(pageSize) {
				stopped = index+1 < len(publications) || len(publications) == int(catalogScanBudget)
				break
			}
		}
		next := ""
		if stopped || len(publications) == int(catalogScanBudget) {
			next, err = encodeCatalogCursor(lastScanned, major, host)
			if err != nil {
				return nil, err
			}
		}
		return catalogdomain.NewPublicApplicationCatalogPage(items, next)
	})
}

func (r *PublicCatalogRepository) GetPublic(ctx context.Context, applicationID shared.ApplicationID, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName) (*catalogdomain.PublicApplicationCatalogItem, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || major < 1 {
		return nil, fmt.Errorf("get public catalog: invalid repository input")
	}
	var result *catalogdomain.PublicApplicationCatalogItem
	_, err := r.withSnapshotPage(ctx, func(tx context.Context) (*catalogdomain.PublicApplicationCatalogPage, error) {
		var publication applicationPublicationDocument
		err := decodeUnifiedLaunchDocument(r.database.Collection(applicationPublicationsCollectionName).FindOne(tx, bson.M{"applicationId": applicationID.String(), "rpcApiMajor": major}), &publication)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, catalogport.ErrPublicApplicationNotFound
		}
		if err != nil {
			return nil, err
		}
		facts, err := r.loadCatalogFacts(tx, []applicationPublicationDocument{publication}, authID)
		if err != nil {
			return nil, err
		}
		item, eligible, _, err := r.catalogItem(publication, authID, major, host, facts)
		if err != nil {
			return nil, err
		}
		if !eligible {
			return nil, catalogport.ErrPublicApplicationNotFound
		}
		result = item
		return catalogdomain.NewPublicApplicationCatalogPage([]*catalogdomain.PublicApplicationCatalogItem{item}, "")
	})
	if err != nil {
		return nil, safeCatalogError(err)
	}
	return result, nil
}

func (r *PublicCatalogRepository) withSnapshotPage(ctx context.Context, fn func(context.Context) (*catalogdomain.PublicApplicationCatalogPage, error)) (*catalogdomain.PublicApplicationCatalogPage, error) {
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeCatalogError(err)
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return fn(tx) }, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, safeCatalogError(err)
	}
	page, ok := value.(*catalogdomain.PublicApplicationCatalogPage)
	if !ok || page == nil {
		return nil, safeCatalogError(errors.New("invalid catalog result"))
	}
	return page, nil
}

type catalogFacts struct {
	applications     map[string]bool
	profiles         map[string]applicationProfileDocument
	profileRevisions map[string]applicationProfileRevisionDocument
	profileReviews   map[string]*profiledomain.ApplicationProfileReview
	filters          map[string]applicationFilterDocument
	filterRevisions  map[string]applicationFilterRevisionDocument
	memberships      map[string]applicationTesterMembershipDocument
	histories        map[string]applicationPublicationHistoryDocument
	versions         map[string]applicationVersionDocument
	oauth            map[string]applicationVersionOAuthConfigDocument
	reviews          map[string]applicationReviewDocument
}

func (r *PublicCatalogRepository) loadCatalogFacts(ctx context.Context, publications []applicationPublicationDocument, authID shared.AuthID) (*catalogFacts, error) {
	f := &catalogFacts{applications: map[string]bool{}, profiles: map[string]applicationProfileDocument{}, profileRevisions: map[string]applicationProfileRevisionDocument{}, profileReviews: map[string]*profiledomain.ApplicationProfileReview{}, filters: map[string]applicationFilterDocument{}, filterRevisions: map[string]applicationFilterRevisionDocument{}, memberships: map[string]applicationTesterMembershipDocument{}, histories: map[string]applicationPublicationHistoryDocument{}, versions: map[string]applicationVersionDocument{}, oauth: map[string]applicationVersionOAuthConfigDocument{}, reviews: map[string]applicationReviewDocument{}}
	appIDs, pubIDs, versionIDs := []string{}, []string{}, []string{}
	for _, p := range publications {
		appIDs = append(appIDs, p.ApplicationID)
		pubIDs = append(pubIDs, p.PublicationID)
		for _, id := range []*string{p.TestVersionID, p.StableVersionID} {
			if id != nil {
				versionIDs = append(versionIDs, *id)
			}
		}
		if p.GreyRollout != nil {
			versionIDs = append(versionIDs, p.GreyRollout.VersionID)
		}
	}
	appIDs = uniqueStrings(appIDs)
	pubIDs = uniqueStrings(pubIDs)
	versionIDs = uniqueStrings(versionIDs)
	if err := collect(ctx, r.database.Collection(applicationsCollectionName), bson.M{"id": bson.M{"$in": appIDs}, "lifecycleStatus": "ACTIVE"}, func(d applicationDocument) { f.applications[d.ID] = true }); err != nil {
		return nil, err
	}
	if err := collect(ctx, r.database.Collection(applicationProfilesCollectionName), bson.M{"applicationId": bson.M{"$in": appIDs}}, func(d applicationProfileDocument) { f.profiles[d.ApplicationID] = d }); err != nil {
		return nil, err
	}
	profileIDs := []string{}
	for _, p := range f.profiles {
		if p.CurrentPublishedProfileRevisionID != nil {
			profileIDs = append(profileIDs, *p.CurrentPublishedProfileRevisionID)
		}
	}
	if err := collect(ctx, r.database.Collection(applicationProfileRevisionsCollectionName), bson.M{"profileRevisionId": bson.M{"$in": profileIDs}}, func(d applicationProfileRevisionDocument) { f.profileRevisions[d.ProfileRevisionID] = d }); err != nil {
		return nil, err
	}
	if err := collectRaw(ctx, r.database.Collection(applicationProfileReviewsCollectionName), bson.M{"profileRevisionId": bson.M{"$in": profileIDs}, "status": "APPROVED"}, func(raw bson.Raw) error {
		review, restoreErr := profileReviewDecisionCandidate(raw)
		if restoreErr != nil {
			return catalogport.ErrApplicationCatalogStateInconsistent
		}
		key := review.ProfileRevisionID().String()
		if old, exists := f.profileReviews[key]; !exists || review.Attempt() > old.Attempt() {
			f.profileReviews[key] = review
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := collect(ctx, r.database.Collection(applicationFiltersCollectionName), bson.M{"applicationId": bson.M{"$in": appIDs}}, func(d applicationFilterDocument) { f.filters[d.ApplicationID] = d }); err != nil {
		return nil, err
	}
	filterIDs := []string{}
	for _, d := range f.filters {
		filterIDs = append(filterIDs, d.CurrentFilterRevisionID)
	}
	if err := collect(ctx, r.database.Collection(applicationFilterRevisionsCollectionName), bson.M{"filterRevisionId": bson.M{"$in": filterIDs}}, func(d applicationFilterRevisionDocument) { f.filterRevisions[d.FilterRevisionID] = d }); err != nil {
		return nil, err
	}
	if authID.IsValid() {
		if err := collect(ctx, r.database.Collection(applicationTesterMembershipsCollectionName), bson.M{"applicationId": bson.M{"$in": appIDs}, "testerAuthId": authID.String(), "status": "ACTIVE"}, func(d applicationTesterMembershipDocument) { f.memberships[d.ApplicationID] = d }); err != nil {
			return nil, err
		}
	}
	if err := collect(ctx, r.database.Collection(applicationPublicationHistoryCollectionName), bson.M{"publicationId": bson.M{"$in": pubIDs}}, func(d applicationPublicationHistoryDocument) {
		key := historyMapKey(d.PublicationID, d.Action, valueOrEmpty(d.NewVersionID))
		if old, ok := f.histories[key]; !ok || d.PublicationRevision > old.PublicationRevision {
			f.histories[key] = d
		}
	}); err != nil {
		return nil, err
	}
	if err := collect(ctx, r.database.Collection(applicationVersionsCollectionName), bson.M{"versionId": bson.M{"$in": versionIDs}}, func(d applicationVersionDocument) { f.versions[d.VersionID] = d }); err != nil {
		return nil, err
	}
	if err := collect(ctx, r.database.Collection(applicationVersionOAuthConfigsCollectionName), bson.M{"applicationVersionId": bson.M{"$in": versionIDs}}, func(d applicationVersionOAuthConfigDocument) { f.oauth[d.ApplicationVersionID] = d }); err != nil {
		return nil, err
	}
	if err := collect(ctx, r.database.Collection(applicationReviewsCollectionName), bson.M{"versionId": bson.M{"$in": versionIDs}}, func(d applicationReviewDocument) {
		if old, ok := f.reviews[d.VersionID]; !ok || d.Attempt > old.Attempt {
			f.reviews[d.VersionID] = d
		}
	}); err != nil {
		return nil, err
	}
	return f, nil
}

func collect[T any](ctx context.Context, collection *drivermongo.Collection, filter any, add func(T)) error {
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var value T
		if err = cursor.Decode(&value); err != nil {
			return catalogport.ErrApplicationCatalogStateInconsistent
		}
		add(value)
	}
	return cursor.Err()
}

func collectRaw(ctx context.Context, collection *drivermongo.Collection, filter any, add func(bson.Raw) error) error {
	cursor, err := collection.Find(ctx, filter)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		if err = add(cursor.Current); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func (r *PublicCatalogRepository) catalogItem(publication applicationPublicationDocument, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName, f *catalogFacts) (*catalogdomain.PublicApplicationCatalogItem, bool, int, error) {
	applicationID, ok := shared.ParseApplicationID(publication.ApplicationID)
	if !ok {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	if !f.applications[publication.ApplicationID] {
		return nil, false, 0, nil
	}
	restored, err := applicationPublicationFromDocument(publication)
	if err != nil || restored.ApplicationID() != applicationID || restored.RPCAPIMajor() != major {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	if publication.StableVersionID == nil {
		return nil, false, 0, nil
	}
	_, stableCompatible, err := resolveCatalogCandidate(applicationID, publication, *publication.StableVersionID, major, catalogdomain.LaunchChannelStable, []string{"SET_STABLE_VERSION"}, host, f)
	if err != nil {
		return nil, false, 0, err
	}
	if !stableCompatible {
		return nil, false, 0, nil
	}
	profileProjection, ok := f.profiles[publication.ApplicationID]
	if !ok || profileProjection.CurrentPublishedProfileRevisionID == nil {
		return nil, false, 0, nil
	}
	profileDocument, ok := f.profileRevisions[*profileProjection.CurrentPublishedProfileRevisionID]
	if !ok {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	profileRevision, err := profileRevisionFromDocument(profileDocument)
	if err != nil || profileRevision.ApplicationID() != applicationID || profileRevision.ReviewStatus() != profiledomain.ReviewStatusApproved {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	profileReview, ok := f.profileReviews[profileRevision.ProfileRevisionID().String()]
	if !ok || !approvedProfileSnapshotMatches(profileRevision, profileReview) {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	profile, err := catalogdomain.NewPublicApplicationProfile(profileRevision.ProfileRevisionID().String(), profileRevision.DisplayName().String(), optionalDescription(profileRevision.Description()), optionalIcon(profileRevision.Icon()))
	if err != nil {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	activeTester := false
	if membership, ok := f.memberships[publication.ApplicationID]; ok {
		restored, restoreErr := testerMembershipFromDocument(membership)
		if restoreErr != nil || restored.ApplicationID() != applicationID || restored.TesterAuthID() != authID || restored.Status() != testerdomain.MembershipStatusActive {
			return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
		}
		activeTester = true
	}
	var target *catalogdomain.LaunchTargetDescriptor
	if activeTester && publication.TestVersionID != nil {
		target, _, err = resolveCatalogCandidate(applicationID, publication, *publication.TestVersionID, major, catalogdomain.LaunchChannelTest, []string{"SET_TEST_VERSION"}, host, f)
	}
	if err != nil {
		return nil, false, 0, err
	}
	if target == nil && authID.IsValid() {
		if rollout := restored.GreyRollout(); rollout != nil && rollout.Matches(authID) {
			target, _, err = resolveCatalogCandidate(applicationID, publication, rollout.VersionID().String(), major, catalogdomain.LaunchChannelGrey, []string{"SET_GREY_ROLLOUT", "INCREASE_GREY_EXPOSURE", "REPLACE_GREY_VERSION"}, host, f)
		}
	}
	if err != nil {
		return nil, false, 0, err
	}
	if target == nil {
		target, _, err = resolveCatalogCandidate(applicationID, publication, *publication.StableVersionID, major, catalogdomain.LaunchChannelStable, []string{"SET_STABLE_VERSION"}, host, f)
	}
	if err != nil || target == nil {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	filterProjection, filterBytes, err := catalogFilter(applicationID, f)
	if err != nil {
		return nil, false, 0, err
	}
	item, err := catalogdomain.NewPublicApplicationCatalogItem(applicationID, profile, target, filterProjection)
	if err != nil {
		return nil, false, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	return item, true, filterBytes + 4096, nil
}

func approvedProfileSnapshotMatches(revision *profiledomain.ApplicationProfileRevision, review *profiledomain.ApplicationProfileReview) bool {
	if revision == nil || review == nil || review.ApplicationID() != revision.ApplicationID() || review.ProfileRevisionID() != revision.ProfileRevisionID() || review.Status() != profiledomain.ProfileReviewStatusApproved || review.SourceRevision() > math.MaxInt64-2 || revision.Revision() != review.SourceRevision()+2 {
		return false
	}
	decision := review.Decision()
	if decision == nil || decision.Outcome != profiledomain.ProfileReviewStatusApproved || revision.UpdatedBy() != decision.DecidedBy || !revision.UpdatedAt().Equal(decision.DecidedAt) {
		return false
	}
	snapshot := review.Snapshot()
	if revision.DisplayName() != snapshot.DisplayName() {
		return false
	}
	return equalCatalogOptionalString(optionalDescription(revision.Description()), optionalDescription(snapshot.Description())) && equalCatalogOptionalString(optionalIcon(revision.Icon()), optionalIcon(snapshot.Icon()))
}

func equalCatalogOptionalString(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func resolveCatalogCandidate(applicationID shared.ApplicationID, publication applicationPublicationDocument, versionID string, major int32, channel catalogdomain.LaunchChannel, actions []string, host []catalogdomain.CapabilityName, f *catalogFacts) (*catalogdomain.LaunchTargetDescriptor, bool, error) {
	var history applicationPublicationHistoryDocument
	found := false
	for _, action := range actions {
		if candidate, ok := f.histories[historyMapKey(publication.PublicationID, action, versionID)]; ok && candidate.PublicationRevision <= publication.Revision && (!found || candidate.PublicationRevision > history.PublicationRevision) {
			history = candidate
			found = true
		}
	}
	if !found || history.ApplicationID != applicationID.String() || history.RPCAPIMajor != major || history.NewVersionID == nil || *history.NewVersionID != versionID || history.ApprovedReviewID == nil || !shared.IsUUIDv7(*history.ApprovedReviewID) {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	version, ok := f.versions[versionID]
	if !ok {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	oauth, ok := f.oauth[versionID]
	if !ok {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	if _, e := applicationVersionFromDocument(version, oauth); e != nil || version.ApplicationID != applicationID.String() || version.ReviewStatus != "APPROVED" || major < version.RPCApiMinVersion || major >= version.RPCApiMaxVersionExclusive {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	review, ok := f.reviews[versionID]
	if !ok {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	approved, e := applicationReviewFromDocument(review)
	if e != nil || review.ReviewID != *history.ApprovedReviewID || review.Status != "APPROVED" || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || version.Revision != review.SourceVersionRevision+2 {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	_, _, snapshot, e := applicationVersionDocumentToDecisionVersion(version, oauth)
	if e != nil || !snapshot.Equal(approved.Snapshot()) || version.UpdatedBy != review.Decision.DecidedBy || !version.UpdatedAt.Equal(review.Decision.DecidedAt) {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	required := make([]catalogdomain.CapabilityName, len(version.RequiredCapabilities))
	for i, name := range version.RequiredCapabilities {
		required[i] = catalogdomain.CapabilityName(name)
	}
	if len(catalogdomain.MissingCapabilities(required, host)) != 0 {
		return nil, false, nil
	}
	descriptor, e := catalogdomain.NewLaunchTargetDescriptor(applicationID, publication.PublicationID, publication.Revision, channel, major, version.VersionID, version.VersionLabel, version.LaunchURL, version.RPCApiMinVersion, version.RPCApiMaxVersionExclusive, required, version.RequiredScopes, version.OptionalScopes)
	if e != nil {
		return nil, false, catalogport.ErrApplicationCatalogStateInconsistent
	}
	return descriptor, true, nil
}

func catalogFilter(applicationID shared.ApplicationID, f *catalogFacts) (*catalogdomain.PublicApplicationFilter, int, error) {
	doc, ok := f.filters[applicationID.String()]
	if !ok {
		projection, e := catalogdomain.NewPublicApplicationFilter(0, "", "ALLOW_ALL", nil)
		return projection, 0, e
	}
	revisionDoc, ok := f.filterRevisions[doc.CurrentFilterRevisionID]
	if !ok {
		return nil, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	revision, e := filterRevisionFromDocument(revisionDoc)
	if e != nil {
		return nil, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	filter, e := filterdomain.RestoreApplicationFilter(applicationID, doc.Revision, doc.NextSequence, revision)
	if e != nil {
		return nil, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	var rule *catalogdomain.PublicFilterRule
	if source := filter.CurrentRevision().Rule(); source != nil {
		converted := catalogRule(*source)
		rule = &converted
	}
	projection, e := catalogdomain.NewPublicApplicationFilter(filter.Revision(), revision.ID().String(), string(filter.EffectiveMode()), rule)
	if e != nil {
		return nil, 0, catalogport.ErrApplicationCatalogStateInconsistent
	}
	raw, _ := bson.Marshal(revisionDoc)
	return projection, len(raw), nil
}

func catalogRule(rule filterdomain.Rule) catalogdomain.PublicFilterRule {
	if op, children, ok := rule.Group(); ok {
		mapped := make([]catalogdomain.PublicFilterRule, len(children))
		for i, child := range children {
			mapped[i] = catalogRule(child)
		}
		return catalogdomain.NewPublicFilterGroup(string(op), mapped)
	}
	field, op, value, _ := rule.Predicate()
	var scalar *catalogdomain.PublicFilterScalar
	if value != nil {
		v := catalogdomain.NewPublicFilterScalar(string(value.Kind()), value.StringValue(), value.IntegerValue(), value.BooleanValue())
		scalar = &v
	}
	return catalogdomain.NewPublicFilterPredicate(field, string(op), scalar)
}
func optionalDescription(value *profiledomain.ApplicationDescription) *string {
	if value == nil {
		return nil
	}
	v := value.String()
	return &v
}
func optionalIcon(value *profiledomain.ApplicationIcon) *string {
	if value == nil {
		return nil
	}
	v := value.String()
	return &v
}
func historyMapKey(publicationID, action, versionID string) string {
	return publicationID + "\x00" + action + "\x00" + versionID
}
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func uniqueStrings(values []string) []string { sort.Strings(values); return slicesCompact(values) }
func slicesCompact(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, v := range values[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
func catalogFingerprint(major int32, host []catalogdomain.CapabilityName) string {
	parts := make([]string, len(host))
	for i, v := range host {
		parts[i] = v.String()
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", major, strings.Join(parts, "\x00"))))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func encodeCatalogCursor(last string, major int32, host []catalogdomain.CapabilityName) (string, error) {
	if !shared.ApplicationID(last).IsValid() {
		return "", catalogport.ErrApplicationCatalogStateInconsistent
	}
	raw, e := json.Marshal(catalogCursor{1, last, catalogFingerprint(major, host)})
	if e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decodeCatalogCursor(token string, major int32, host []catalogdomain.CapabilityName) (string, error) {
	if token == "" {
		return "", nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(token)
	if e != nil {
		return "", e
	}
	var cursor catalogCursor
	if e = json.Unmarshal(raw, &cursor); e != nil || cursor.Version != 1 || !shared.ApplicationID(cursor.Last).IsValid() || cursor.Fingerprint != catalogFingerprint(major, host) {
		return "", catalogport.ErrInvalidPageToken
	}
	return cursor.Last, nil
}
func safeCatalogError(err error) error {
	for _, known := range []error{catalogport.ErrInvalidPageToken, catalogport.ErrPublicApplicationNotFound, catalogport.ErrApplicationCatalogStateInconsistent} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("public catalog persistence failed: canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("public catalog persistence failed: deadline exceeded")
	}
	return fmt.Errorf("public catalog persistence failed: storage failure")
}
