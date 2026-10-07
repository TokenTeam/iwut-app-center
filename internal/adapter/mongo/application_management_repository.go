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

	managementdomain "iwut-app-center/internal/management/domain"
	managementport "iwut-app-center/internal/management/port"
	"iwut-app-center/internal/shared"
)

const applicationManagementMigrationID = "0024_application_management_query"
const applicationManagementIndexName = "ix_applications_admin_lifecycle_created_id"

type ApplicationManagementRepository struct{ database *m.Database }

func NewApplicationManagementRepository(database *m.Database) *ApplicationManagementRepository {
	return &ApplicationManagementRepository{database: database}
}

var _ managementport.Repository = (*ApplicationManagementRepository)(nil)

type applicationManagementCursor struct {
	Version              int `json:"v"`
	AdminID, Fingerprint string
	CreatedAt            time.Time `json:"createdAt"`
	ApplicationID        string    `json:"applicationId"`
}

func (r *ApplicationManagementRepository) List(ctx context.Context, adminID shared.AuthID, lifecycle, availability []string, pageSize int32, token string) (*managementdomain.Page, error) {
	if r == nil || r.database == nil || !adminID.IsValid() || pageSize < 1 {
		return nil, fmt.Errorf("list application management: invalid input")
	}
	cursor, err := decodeApplicationManagementCursor(token, adminID.String(), lifecycle, availability)
	if err != nil {
		return nil, managementport.ErrInvalidPageToken
	}
	return withManagementSnapshot(r, ctx, func(tx context.Context, now time.Time) (*managementdomain.Page, error) {
		filter := bson.M{"adminId": adminID.String(), "lifecycleStatus": bson.M{"$in": lifecycle}}
		if len(availability) > 0 {
			filter["platformAvailabilityStatus"] = bson.M{"$in": availability}
		}
		if cursor != nil {
			filter["$or"] = bson.A{bson.M{"createdAt": bson.M{"$lt": cursor.CreatedAt}}, bson.M{"createdAt": cursor.CreatedAt, "id": bson.M{"$lt": cursor.ApplicationID}}}
		}
		find, err := r.database.Collection(applicationsCollectionName).Find(tx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}, {Key: "id", Value: -1}}).SetLimit(int64(pageSize)+1))
		if err != nil {
			return nil, err
		}
		defer find.Close(tx)
		var docs []applicationDocument
		if err = find.All(tx, &docs); err != nil {
			return nil, err
		}
		hasMore := len(docs) > int(pageSize)
		if hasMore {
			docs = docs[:pageSize]
		}
		ids := make([]string, len(docs))
		for i := range docs {
			ids[i] = docs[i].ID
		}
		counts, err := r.loadManagementCounts(tx, ids)
		if err != nil {
			return nil, err
		}
		items := make([]managementdomain.Summary, len(docs))
		for i, doc := range docs {
			core, err := managementCore(doc)
			if err != nil {
				return nil, err
			}
			items[i] = managementdomain.Summary{Application: core, Counts: counts[doc.ID]}
		}
		next := ""
		if hasMore && len(docs) > 0 {
			next, err = encodeApplicationManagementCursor(adminID.String(), lifecycle, availability, docs[len(docs)-1])
			if err != nil {
				return nil, err
			}
		}
		return &managementdomain.Page{Items: items, NextPageToken: next, AsOf: now}, nil
	})
}

func (r *ApplicationManagementRepository) Get(ctx context.Context, adminID shared.AuthID, applicationID shared.ApplicationID) (*managementdomain.Detail, error) {
	if r == nil || r.database == nil || !adminID.IsValid() || !applicationID.IsValid() {
		return nil, fmt.Errorf("get application management: invalid input")
	}
	return withManagementSnapshot(r, ctx, func(tx context.Context, now time.Time) (*managementdomain.Detail, error) {
		var app applicationDocument
		err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": applicationID.String(), "adminId": adminID.String()}).Decode(&app)
		if errors.Is(err, m.ErrNoDocuments) {
			return nil, managementport.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		core, err := managementCore(app)
		if err != nil {
			return nil, err
		}
		counts, err := r.loadManagementCounts(tx, []string{app.ID})
		if err != nil {
			return nil, err
		}
		detail := &managementdomain.Detail{Application: core, Counts: counts[app.ID], FilterState: managementdomain.FilterState{Mode: "ALLOW_ALL", SchemaVersion: "profile-filter-v1"}, AsOf: now}
		if err = r.loadManagementProfile(tx, app.ID, &detail.ProfileState); err != nil {
			return nil, err
		}
		if detail.Publications, err = r.loadManagementPublications(tx, app.ID); err != nil {
			return nil, err
		}
		if detail.FilterState, err = r.loadManagementFilter(tx, app.ID); err != nil {
			return nil, err
		}
		if detail.OAuthRegistrations, err = r.loadManagementOAuth(tx, app.ID); err != nil {
			return nil, err
		}
		if detail.PendingTransfer, err = r.loadManagementTransfer(tx, app.ID, now); err != nil {
			return nil, err
		}
		if detail.Closure, err = r.loadManagementClosure(tx, app); err != nil {
			return nil, err
		}
		return detail, nil
	})
}

func withManagementSnapshot[T any](r *ApplicationManagementRepository, ctx context.Context, fn func(context.Context, time.Time) (T, error)) (T, error) {
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
		return zero, errors.New("invalid management snapshot result")
	}
	return result, nil
}

func managementCore(doc applicationDocument) (managementdomain.ApplicationCore, error) {
	if _, ok := shared.ParseApplicationID(doc.ID); !ok || doc.Name == "" || doc.AdminID == "" || doc.CreatedAt.IsZero() || doc.OwnershipRevision < 1 || doc.LifecycleRevision < 1 || doc.PlatformAvailabilityRevision < 1 || !slices.Contains([]string{"ACTIVE", "CLOSING", "CLOSED"}, doc.LifecycleStatus) || !slices.Contains([]string{"AVAILABLE", "SUSPENDED"}, doc.PlatformAvailabilityStatus) {
		return managementdomain.ApplicationCore{}, managementport.ErrStateInconsistent
	}
	return managementdomain.ApplicationCore{ApplicationID: doc.ID, Name: doc.Name, AdminID: doc.AdminID, CreatedAt: doc.CreatedAt, OwnershipRevision: doc.OwnershipRevision, LifecycleStatus: doc.LifecycleStatus, LifecycleRevision: doc.LifecycleRevision, PlatformAvailabilityStatus: doc.PlatformAvailabilityStatus, PlatformAvailabilityRevision: doc.PlatformAvailabilityRevision}, nil
}

func (r *ApplicationManagementRepository) loadManagementCounts(ctx context.Context, appIDs []string) (map[string]managementdomain.Counts, error) {
	result := make(map[string]managementdomain.Counts, len(appIDs))
	if len(appIDs) == 0 {
		return result, nil
	}
	for _, id := range appIDs {
		result[id] = managementdomain.Counts{}
	}
	var versions []applicationVersionDocument
	cur, err := r.database.Collection(applicationVersionsCollectionName).Find(ctx, bson.M{"applicationId": bson.M{"$in": appIDs}})
	if err != nil {
		return nil, err
	}
	if err = cur.All(ctx, &versions); err != nil {
		return nil, err
	}
	for _, v := range versions {
		c := result[v.ApplicationID]
		c.VersionCount++
		switch v.ReviewStatus {
		case "DRAFT":
			c.DraftVersionCount++
		case "SUBMITTED":
			c.SubmittedVersionCount++
		case "APPROVED":
			c.ApprovedVersionCount++
		case "REJECTED":
			c.RejectedVersionCount++
		default:
			return nil, managementport.ErrStateInconsistent
		}
		result[v.ApplicationID] = c
	}
	for _, spec := range []struct {
		collection, status string
		apply              func(*managementdomain.Counts)
	}{
		{applicationReviewsCollectionName, "PENDING", func(c *managementdomain.Counts) { c.PendingVersionReviewCount++ }},
		{applicationProfileReviewsCollectionName, "PENDING", func(c *managementdomain.Counts) { c.PendingProfileReviewCount++ }},
		{applicationTesterMembershipsCollectionName, "ACTIVE", func(c *managementdomain.Counts) { c.ActiveTesterCount++ }},
	} {
		cur, err = r.database.Collection(spec.collection).Find(ctx, bson.M{"applicationId": bson.M{"$in": appIDs}, "status": spec.status}, options.Find().SetProjection(bson.M{"applicationId": 1}))
		if err != nil {
			return nil, err
		}
		var rows []struct {
			ApplicationID string `bson:"applicationId"`
		}
		if err = cur.All(ctx, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			c, ok := result[row.ApplicationID]
			if !ok {
				return nil, managementport.ErrStateInconsistent
			}
			spec.apply(&c)
			result[row.ApplicationID] = c
		}
	}
	return result, nil
}

func (r *ApplicationManagementRepository) loadManagementProfile(ctx context.Context, appID string, out *managementdomain.ProfileState) error {
	var profile applicationProfileDocument
	err := r.database.Collection(applicationProfilesCollectionName).FindOne(ctx, bson.M{"applicationId": appID}).Decode(&profile)
	if errors.Is(err, m.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}
	*out = managementdomain.ProfileState{WorkingProfileRevisionID: profile.WorkingProfileRevisionID, CurrentPublishedProfileRevisionID: profile.CurrentPublishedProfileRevisionID}
	ids := []string{}
	for _, id := range []*string{profile.WorkingProfileRevisionID, profile.CurrentPublishedProfileRevisionID} {
		if id != nil && !slices.Contains(ids, *id) {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	count, err := r.database.Collection(applicationProfileRevisionsCollectionName).CountDocuments(ctx, bson.M{"applicationId": appID, "profileRevisionId": bson.M{"$in": ids}})
	if err != nil {
		return err
	}
	if count != int64(len(ids)) {
		return managementport.ErrStateInconsistent
	}
	return nil
}
func (r *ApplicationManagementRepository) loadManagementPublications(ctx context.Context, appID string) ([]managementdomain.Publication, error) {
	cur, err := r.database.Collection(applicationPublicationsCollectionName).Find(ctx, bson.M{"applicationId": appID}, options.Find().SetSort(bson.D{{Key: "rpcApiMajor", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []applicationPublicationDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]managementdomain.Publication, len(docs))
	for i, d := range docs {
		p, e := applicationPublicationFromDocument(d)
		if e != nil {
			return nil, managementport.ErrStateInconsistent
		}
		var greyID *string
		var percent int32
		if g := p.GreyRollout(); g != nil {
			v := g.VersionID().String()
			greyID = &v
			percent = g.ExposureBasisPoints().Int32() / 100
		}
		out[i] = managementdomain.Publication{RPCAPIMajor: p.RPCAPIMajor(), Revision: p.Revision(), TestVersionID: d.TestVersionID, GreyVersionID: greyID, GreyRolloutPercent: percent, StableVersionID: d.StableVersionID}
	}
	return out, nil
}
func (r *ApplicationManagementRepository) loadManagementFilter(ctx context.Context, appID string) (managementdomain.FilterState, error) {
	def := managementdomain.FilterState{Mode: "ALLOW_ALL", SchemaVersion: "profile-filter-v1"}
	var f applicationFilterDocument
	err := r.database.Collection(applicationFiltersCollectionName).FindOne(ctx, bson.M{"applicationId": appID}).Decode(&f)
	if errors.Is(err, m.ErrNoDocuments) {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	var rev applicationFilterRevisionDocument
	err = r.database.Collection(applicationFilterRevisionsCollectionName).FindOne(ctx, bson.M{"filterRevisionId": f.CurrentFilterRevisionID, "applicationId": appID}).Decode(&rev)
	if err != nil {
		return def, managementport.ErrStateInconsistent
	}
	if f.Revision < 1 || rev.Sequence < 1 || rev.SchemaVersion != "profile-filter-v1" || (rev.Mode != "ALLOW_ALL" && rev.Mode != "RULE") {
		return def, managementport.ErrStateInconsistent
	}
	id := rev.FilterRevisionID
	return managementdomain.FilterState{Revision: f.Revision, FilterRevisionID: &id, Sequence: rev.Sequence, SchemaVersion: rev.SchemaVersion, Mode: rev.Mode}, nil
}
func (r *ApplicationManagementRepository) loadManagementOAuth(ctx context.Context, appID string) ([]managementdomain.OAuthRegistration, error) {
	cur, err := r.database.Collection(applicationOAuthRegistrationsCollectionName).Find(ctx, bson.M{"applicationId": appID}, options.Find().SetSort(bson.D{{Key: "channel", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []applicationOAuthRegistrationDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	creds := map[string]oauthClientCredentialDocument{}
	ccur, err := r.database.Collection(oauthClientCredentialsCollectionName).Find(ctx, bson.M{"applicationId": appID})
	if err != nil {
		return nil, err
	}
	var cd []oauthClientCredentialDocument
	if err = ccur.All(ctx, &cd); err != nil {
		return nil, err
	}
	for _, c := range cd {
		creds[c.ClientID] = c
	}
	out := make([]managementdomain.OAuthRegistration, len(docs))
	for i, d := range docs {
		if _, e := registrationFromDocument(d); e != nil {
			return nil, managementport.ErrStateInconsistent
		}
		out[i] = managementdomain.OAuthRegistration{Channel: d.Channel, RegistrationRevision: d.RegistrationRevision, PublicClient: managementOAuthClient(d.PublicClient, nil), ConfidentialClient: managementOAuthClient(d.ConfidentialClient, creds)}
	}
	return out, nil
}
func managementOAuthClient(id *oauthClientIdentityDocument, creds any) *managementdomain.OAuthClient {
	if id == nil {
		return nil
	}
	out := &managementdomain.OAuthClient{ClientID: id.ClientID, Status: id.Status, AuthorizationEpoch: id.AuthorizationEpoch}
	if values, ok := creds.(map[string]oauthClientCredentialDocument); ok {
		if c, found := values[id.ClientID]; found {
			rev := c.CredentialRevision
			at := c.RotatedAt
			out.CredentialRevision = &rev
			out.CredentialRotatedAt = &at
		}
	}
	return out
}
func (r *ApplicationManagementRepository) loadManagementTransfer(ctx context.Context, appID string, now time.Time) (*managementdomain.PendingTransfer, error) {
	var d applicationAdminTransferDocument
	err := r.database.Collection(applicationAdminTransfersCollectionName).FindOne(ctx, bson.M{"applicationId": appID, "status": "PENDING"}).Decode(&d)
	if errors.Is(err, m.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !d.ExpiresAt.After(now) {
		return nil, nil
	}
	return &managementdomain.PendingTransfer{TransferID: d.TransferID, ToAdminID: d.ToAdminID, RequestedAt: d.RequestedAt, ExpiresAt: d.ExpiresAt}, nil
}
func (r *ApplicationManagementRepository) loadManagementClosure(ctx context.Context, app applicationDocument) (*managementdomain.Closure, error) {
	var d applicationClosureDocument
	err := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"applicationId": app.ID}).Decode(&d)
	if errors.Is(err, m.ErrNoDocuments) {
		if app.LifecycleStatus != "ACTIVE" {
			return nil, managementport.ErrStateInconsistent
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if app.LifecycleStatus == "ACTIVE" {
		return nil, managementport.ErrStateInconsistent
	}
	return &managementdomain.Closure{ClosureID: d.ClosureID, Status: d.Status, AuthRevocationState: d.AuthRevocationState, ClosingStartedAt: d.ClosingStartedAt, ClosedAt: d.ClosedAt}, nil
}

func managementFingerprint(lifecycle, availability []string) string {
	raw := append(append([]string(nil), lifecycle...), "|")
	raw = append(raw, availability...)
	sum := sha256.Sum256([]byte(fmt.Sprint(raw)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func encodeApplicationManagementCursor(admin string, lifecycle, availability []string, doc applicationDocument) (string, error) {
	raw, err := json.Marshal(applicationManagementCursor{Version: 1, AdminID: admin, Fingerprint: managementFingerprint(lifecycle, availability), CreatedAt: doc.CreatedAt.UTC(), ApplicationID: doc.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func decodeApplicationManagementCursor(token, admin string, lifecycle, availability []string) (*applicationManagementCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, managementport.ErrInvalidPageToken
	}
	var c applicationManagementCursor
	if json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.AdminID != admin || c.Fingerprint != managementFingerprint(lifecycle, availability) || c.CreatedAt.IsZero() {
		return nil, managementport.ErrInvalidPageToken
	}
	if _, ok := shared.ParseApplicationID(c.ApplicationID); !ok {
		return nil, managementport.ErrInvalidPageToken
	}
	return &c, nil
}

func (migrator *Migrator) applyApplicationManagementMigration(ctx context.Context) error {
	_, err := migrator.database.Collection(applicationsCollectionName).Indexes().CreateOne(ctx, m.IndexModel{Keys: bson.D{{Key: "adminId", Value: 1}, {Key: "lifecycleStatus", Value: 1}, {Key: "createdAt", Value: -1}, {Key: "id", Value: -1}}, Options: options.Index().SetName(applicationManagementIndexName)})
	return err
}
