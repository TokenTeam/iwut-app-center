package mongo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type ScriptQueryCursorKey [sha256.Size]byte

func DeriveScriptQueryCursorKey(material []byte) (ScriptQueryCursorKey, error) {
	if len(material) == 0 {
		return ScriptQueryCursorKey{}, errors.New("script query cursor key requires secret material")
	}
	mac := hmac.New(sha256.New, material)
	mac.Write([]byte("iwut-app-center/script-query-cursor/v1"))
	return ScriptQueryCursorKey(mac.Sum(nil)), nil
}

type ScriptQueryRepository struct {
	db  *dm.Database
	key ScriptQueryCursorKey
}

func NewScriptQueryRepository(db *dm.Database, key ScriptQueryCursorKey) *ScriptQueryRepository {
	return &ScriptQueryRepository{db: db, key: key}
}

var _ port.ScriptQueryRepository = (*ScriptQueryRepository)(nil)

type scriptCursorPosition struct {
	At time.Time `json:"at"`
	ID string    `json:"id"`
}

type scriptCursorScope struct {
	Reader   port.ScriptReadScope
	List     string
	Resource domain.ResourceID
	State    domain.VersionState
}

func (r *ScriptQueryRepository) cursorMAC(payload []byte, scope scriptCursorScope) []byte {
	context, _ := json.Marshal(scope)
	mac := hmac.New(sha256.New, r.key[:])
	mac.Write(context)
	mac.Write([]byte{0})
	mac.Write(payload)
	return mac.Sum(nil)
}

func (r *ScriptQueryRepository) encodeCursor(position scriptCursorPosition, scope scriptCursorScope) (string, error) {
	payload, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(r.cursorMAC(payload, scope), payload...)), nil
}

func (r *ScriptQueryRepository) applyCursor(token string, scope scriptCursorScope, filter bson.M, dateField, idField string) error {
	if token == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) <= sha256.Size || base64.RawURLEncoding.EncodeToString(raw) != token {
		return port.ErrInvalidScriptCursor
	}
	signature, payload := raw[:sha256.Size], raw[sha256.Size:]
	var cursor scriptCursorPosition
	if !hmac.Equal(signature, r.cursorMAC(payload, scope)) ||
		json.Unmarshal(payload, &cursor) != nil || !validScriptQueryTime(cursor.At) || !canonicalScriptID(cursor.ID) {
		return port.ErrInvalidScriptCursor
	}
	filter["$or"] = bson.A{
		bson.M{dateField: bson.M{"$lt": cursor.At}},
		bson.M{dateField: cursor.At, idField: bson.M{"$lt": cursor.ID}},
	}
	return nil
}

func findScriptPage[T any](ctx context.Context, coll *dm.Collection, filter bson.M, limit int, dateField, idField string, projection bson.M) ([]T, error) {
	opt := options.Find().SetSort(bson.D{{Key: dateField, Value: -1}, {Key: idField, Value: -1}}).
		SetLimit(int64(limit) + 1).SetCollation(&options.Collation{Locale: "simple"})
	if projection != nil {
		opt.SetProjection(projection)
	}
	cursor, err := coll.Find(ctx, filter, opt)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var docs []T
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

func projectScriptPage[T, U any](r *ScriptQueryRepository, docs []T, limit int, scope scriptCursorScope, project func(T) (U, scriptCursorPosition, error)) (port.ScriptPage[U], error) {
	var result port.ScriptPage[U]
	more := len(docs) > limit
	if more {
		docs = docs[:limit]
	}
	result.Items = make([]U, 0, len(docs))
	var last scriptCursorPosition
	for _, doc := range docs {
		item, position, err := project(doc)
		if err != nil {
			return port.ScriptPage[U]{}, err
		}
		last = position
		result.Items = append(result.Items, item)
	}
	if more {
		var err error
		result.NextCursor, err = r.encodeCursor(last, scope)
		if err != nil {
			return port.ScriptPage[U]{}, err
		}
	}
	return result, nil
}

func (r *ScriptQueryRepository) ListResources(ctx context.Context, reader port.ScriptReadScope, page port.ScriptPageRequest) (port.ScriptPage[domain.Resource], error) {
	var empty port.ScriptPage[domain.Resource]
	scope := scriptCursorScope{Reader: reader, List: "resources"}
	filter := bson.M{}
	if err := r.applyCursor(page.Cursor, scope, filter, "createdAt", "resourceId"); err != nil {
		return empty, err
	}
	docs, err := readScriptResourceRows[scriptResourceDocument](ctx, r.db, dm.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$sort", Value: bson.D{{Key: "createdAt", Value: -1}, {Key: "resourceId", Value: -1}}}},
		scriptResourceVersionLookup("visible", true),
		{{Key: "$match", Value: bson.M{"visible.0": bson.M{"$exists": true}}}},
		{{Key: "$limit", Value: int64(page.Limit) + 1}},
		{{Key: "$unset", Value: "visible"}},
	})
	if err != nil {
		return empty, err
	}
	return projectScriptPage(r, docs, page.Limit, scope, func(doc scriptResourceDocument) (domain.Resource, scriptCursorPosition, error) {
		resource := doc.resource()
		if resource.Validate() != nil {
			return domain.Resource{}, scriptCursorPosition{}, port.ErrScriptQueryInconsistent
		}
		return resource, scriptCursorPosition{At: resource.CreatedAt, ID: string(resource.ID)}, nil
	})
}

func (r *ScriptQueryRepository) GetResource(ctx context.Context, id domain.ResourceID) (domain.Resource, error) {
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.Resource, error) {
		return r.visibleResource(tx, id)
	})
}

func (r *ScriptQueryRepository) visibleResource(ctx context.Context, id domain.ResourceID) (domain.Resource, error) {
	resource, err := loadScriptResource(ctx, r.db, id)
	if err != nil {
		return domain.Resource{}, err
	}
	err = r.db.Collection(scriptVersionsCollection).FindOne(ctx,
		bson.M{"resourceId": id, "state": bson.M{"$in": submittedScriptStates}},
		options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, dm.ErrNoDocuments) {
		return domain.Resource{}, domain.ErrNotFound
	}
	return resource, err
}

func (r *ScriptQueryRepository) resourceForRead(ctx context.Context, reader port.ScriptReadScope, id domain.ResourceID) (domain.Resource, error) {
	if reader.Own {
		return loadOwnedScriptResource(ctx, r.db, id, reader.Actor)
	}
	return r.visibleResource(ctx, id)
}

func (r *ScriptQueryRepository) ListVersions(ctx context.Context, reader port.ScriptReadScope, resource domain.ResourceID, state domain.VersionState, page port.ScriptPageRequest) (port.ScriptPage[port.ScriptVersionSummary], error) {
	var empty port.ScriptPage[port.ScriptVersionSummary]
	scope := scriptCursorScope{Reader: reader, List: "versions", Resource: resource, State: state}
	filter := bson.M{"resourceId": resource}
	if err := r.applyCursor(page.Cursor, scope, filter, "createdAt", "versionId"); err != nil {
		return empty, err
	}
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.ScriptPage[port.ScriptVersionSummary], error) {
		parent, err := r.resourceForRead(tx, reader, resource)
		if err != nil {
			return empty, err
		}
		if state != "" {
			filter["state"] = state
		}
		if !reader.Own {
			if state == domain.VersionDraft {
				return port.ScriptPage[port.ScriptVersionSummary]{Items: []port.ScriptVersionSummary{}}, nil
			}
			if state == "" {
				filter["state"] = bson.M{"$in": submittedScriptStates}
			}
		}
		docs, err := findScriptPage[scriptVersionDocument](tx, r.db.Collection(scriptVersionsCollection), filter, page.Limit, "createdAt", "versionId", bson.M{"contentText": 0})
		if err != nil {
			return empty, err
		}
		reviews, err := r.ownedVersionReviews(tx, reader, resource, docs)
		if err != nil {
			return empty, err
		}
		return projectScriptPage(r, docs, page.Limit, scope, func(doc scriptVersionDocument) (port.ScriptVersionSummary, scriptCursorPosition, error) {
			summary, err := scriptVersionSummary(doc)
			if !doc.version().AuthoredBy(parent.OwnerAuthID) {
				return summary, scriptCursorPosition{}, port.ErrScriptQueryInconsistent
			}
			if reader.Own {
				summary.ReviewID = reviews[summary.ID]
			}
			return summary, scriptCursorPosition{At: summary.CreatedAt, ID: string(summary.ID)}, err
		})
	})
}

func (r *ScriptQueryRepository) GetVersion(ctx context.Context, reader port.ScriptReadScope, resource domain.ResourceID, versionID domain.VersionID) (port.ScriptVersionDetail, error) {
	var empty port.ScriptVersionDetail
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.ScriptVersionDetail, error) {
		parent, err := r.resourceForRead(tx, reader, resource)
		if err != nil {
			return empty, err
		}
		version, err := NewScriptVersionRepository(r.db).loadVersion(tx, resource, versionID)
		if err != nil {
			return empty, err
		}
		if version.State == domain.VersionDraft && !reader.Own {
			return empty, domain.ErrVersionNotFound
		}
		if version.Validate() != nil || !version.AuthoredBy(parent.OwnerAuthID) {
			return empty, port.ErrScriptQueryInconsistent
		}
		summary, err := scriptVersionSummary(toScriptVersionDocument(version))
		if err != nil {
			return empty, err
		}
		result := port.ScriptVersionDetail{Version: summary}
		if version.State != domain.VersionDraft {
			var doc scriptReviewDocument
			err := r.db.Collection(scriptReviewsCollection).FindOne(tx, bson.M{"snapshot.versionId": versionID}).Decode(&doc)
			if errors.Is(err, dm.ErrNoDocuments) {
				return empty, port.ErrScriptQueryInconsistent
			}
			if err != nil {
				return empty, err
			}
			if _, err := readScriptReview(doc, parent, version); err != nil {
				return empty, err
			}
			result.ReviewID = doc.ReviewID
			result.Version.ReviewID = doc.ReviewID
			if doc.Decision != nil {
				result.DecisionID = doc.Decision.ID
			}
		}
		if reader.Own || reader.Review {
			result.Text = &version.Content.Text
		}
		return result, nil
	})
}

func (r *ScriptQueryRepository) ListPendingReviews(ctx context.Context, reader port.ScriptReadScope, resource domain.ResourceID, page port.ScriptPageRequest) (port.ScriptPage[port.ScriptReviewSummary], error) {
	var empty port.ScriptPage[port.ScriptReviewSummary]
	scope := scriptCursorScope{Reader: reader, List: "pending", Resource: resource}
	filter := bson.M{"status": domain.ReviewPending}
	if resource != "" {
		filter["snapshot.resourceId"] = resource
	}
	if err := r.applyCursor(page.Cursor, scope, filter, "submittedAt", "reviewId"); err != nil {
		return empty, err
	}
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.ScriptPage[port.ScriptReviewSummary], error) {
		if resource != "" {
			if _, err := r.visibleResource(tx, resource); err != nil {
				return empty, err
			}
		}
		docs, err := findScriptPage[scriptReviewDocument](tx, r.db.Collection(scriptReviewsCollection), filter, page.Limit, "submittedAt", "reviewId",
			bson.M{"snapshot.contentText": 0, "snapshot.contributors": 0, "policyBinding": 0})
		if err != nil {
			return empty, err
		}
		resources, versions, err := r.pendingReviewParents(tx, docs[:min(len(docs), page.Limit)])
		if err != nil {
			return empty, err
		}
		return projectScriptPage(r, docs, page.Limit, scope, func(doc scriptReviewDocument) (port.ScriptReviewSummary, scriptCursorPosition, error) {
			resource, foundResource := resources[doc.Snapshot.ResourceID]
			version, foundVersion := versions[doc.Snapshot.VersionID]
			if !foundResource || !foundVersion || !pendingScriptReviewMatches(doc, resource, version) {
				return port.ScriptReviewSummary{}, scriptCursorPosition{}, port.ErrScriptQueryInconsistent
			}
			result := port.ScriptReviewSummary{ResourceID: doc.Snapshot.ResourceID, VersionID: doc.Snapshot.VersionID, ID: doc.ReviewID,
				Label: doc.Snapshot.VersionLabel, Digest: doc.Snapshot.Digest, SubmittedAt: doc.SubmittedAt.UTC()}
			return result, scriptCursorPosition{At: result.SubmittedAt, ID: string(result.ID)}, nil
		})
	})
}

func (r *ScriptQueryRepository) pendingReviewParents(ctx context.Context, docs []scriptReviewDocument) (map[domain.ResourceID]domain.Resource, map[domain.VersionID]domain.Version, error) {
	resources := make(map[domain.ResourceID]domain.Resource)
	versions := make(map[domain.VersionID]domain.Version)
	if len(docs) == 0 {
		return resources, versions, nil
	}
	resourceIDs, versionIDs := make([]domain.ResourceID, 0, len(docs)), make([]domain.VersionID, 0, len(docs))
	for _, doc := range docs {
		resourceIDs = append(resourceIDs, doc.Snapshot.ResourceID)
		versionIDs = append(versionIDs, doc.Snapshot.VersionID)
	}
	resourceDocs, err := findScriptPage[scriptResourceDocument](ctx, r.db.Collection(scriptResourcesCollectionName), bson.M{"resourceId": bson.M{"$in": resourceIDs}}, len(docs), "createdAt", "resourceId", nil)
	if err != nil {
		return nil, nil, err
	}
	for _, doc := range resourceDocs {
		resource := doc.resource()
		if resource.Validate() != nil {
			return nil, nil, port.ErrScriptQueryInconsistent
		}
		resources[resource.ID] = resource
	}
	versionDocs, err := findScriptPage[scriptVersionDocument](ctx, r.db.Collection(scriptVersionsCollection), bson.M{"versionId": bson.M{"$in": versionIDs}}, len(docs), "createdAt", "versionId", bson.M{"contentText": 0})
	if err != nil {
		return nil, nil, err
	}
	for _, doc := range versionDocs {
		if _, err := scriptVersionSummary(doc); err != nil {
			return nil, nil, err
		}
		versions[doc.VersionID] = doc.version()
	}
	return resources, versions, nil
}

func (r *ScriptQueryRepository) GetReview(ctx context.Context, resource domain.ResourceID, versionID domain.VersionID, reviewID domain.ReviewID) (port.ScriptReviewDetail, error) {
	var empty port.ScriptReviewDetail
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.ScriptReviewDetail, error) {
		var doc scriptReviewDocument
		err := r.db.Collection(scriptReviewsCollection).FindOne(tx, bson.M{"snapshot.resourceId": resource, "snapshot.versionId": versionID, "reviewId": reviewID}).Decode(&doc)
		if errors.Is(err, dm.ErrNoDocuments) {
			return empty, port.ErrScriptReviewNotFound
		}
		if err != nil {
			return empty, err
		}
		parent, err := loadScriptResource(tx, r.db, resource)
		if errors.Is(err, domain.ErrNotFound) {
			return empty, port.ErrScriptQueryInconsistent
		}
		if err != nil {
			return empty, err
		}
		version, err := NewScriptVersionRepository(r.db).loadVersion(tx, resource, versionID)
		if errors.Is(err, domain.ErrVersionNotFound) {
			return empty, port.ErrScriptQueryInconsistent
		}
		if err != nil {
			return empty, err
		}
		if version.Validate() != nil {
			return empty, port.ErrScriptQueryInconsistent
		}
		review, err := readScriptReview(doc, parent, version)
		if err != nil {
			return empty, err
		}
		summary, err := scriptVersionSummary(toScriptVersionDocument(version))
		if err != nil {
			return empty, err
		}
		progress, err := loadScriptCascadeProgress(tx, r.db, review)
		if err != nil {
			return empty, err
		}
		return port.ScriptReviewDetail{Review: review, Version: summary, Cascade: progress}, nil
	})
}

func scriptVersionSummary(doc scriptVersionDocument) (port.ScriptVersionSummary, error) {
	digest, err := hex.DecodeString(doc.Digest)
	if !canonicalScriptID(string(doc.ResourceID)) || !canonicalScriptID(string(doc.VersionID)) ||
		domain.ValidateVersionLabel(doc.VersionLabel) != nil || doc.Format != domain.JSONFormat ||
		err != nil || len(digest) != sha256.Size || doc.Digest != strings.ToLower(doc.Digest) ||
		doc.Revision < 1 || !validScriptQueryTime(doc.CreatedAt) || !validScriptQueryTime(doc.UpdatedAt) || doc.UpdatedAt.Before(doc.CreatedAt) {
		return port.ScriptVersionSummary{}, port.ErrScriptQueryInconsistent
	}
	switch doc.State {
	case domain.VersionDraft:
	case domain.VersionApproved, domain.VersionRejected:
		if doc.Revision < 3 {
			return port.ScriptVersionSummary{}, port.ErrScriptQueryInconsistent
		}
	case domain.VersionSubmitted:
		if doc.Revision < 2 {
			return port.ScriptVersionSummary{}, port.ErrScriptQueryInconsistent
		}
	default:
		return port.ScriptVersionSummary{}, port.ErrScriptQueryInconsistent
	}
	return port.ScriptVersionSummary{ResourceID: doc.ResourceID, ID: doc.VersionID, Label: doc.VersionLabel, Format: doc.Format, Digest: doc.Digest,
		State: doc.State, Revision: doc.Revision, CreatedAt: doc.CreatedAt.UTC(), UpdatedAt: doc.UpdatedAt.UTC()}, nil
}

func validScriptQueryTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999
}

func canonicalScriptID(id string) bool {
	return shared.IsUUIDv7(id) && id == strings.ToLower(id)
}

func pendingScriptReviewMatches(doc scriptReviewDocument, resource domain.Resource, version domain.Version) bool {
	snapshot := doc.Snapshot
	metadata, err := domain.NewMetadata(snapshot.ResourceName, snapshot.ResourcePurpose)
	return err == nil && metadata.Name == snapshot.ResourceName && canonicalScriptID(string(doc.ReviewID)) &&
		doc.Status == domain.ReviewPending && version.State == domain.VersionSubmitted &&
		resource.ID == version.ResourceID && snapshot.ResourceID == resource.ID && snapshot.VersionID == version.ID &&
		snapshot.VersionLabel == version.Label && snapshot.Format == version.Content.Format && snapshot.Digest == version.Content.Digest &&
		snapshot.SourceRevision > 0 && snapshot.SourceRevision == version.Revision-1 &&
		snapshot.ResourceRevision > 0 && snapshot.ResourceRevision <= resource.Revision &&
		(snapshot.ResourceRevision < resource.Revision || metadata == resource.Metadata) &&
		version.AuthoredBy(resource.OwnerAuthID) && doc.SubmittedBy == resource.OwnerAuthID && doc.SubmittedBy == version.UpdatedBy && validScriptQueryTime(doc.SubmittedAt) &&
		doc.SubmittedAt.Equal(version.UpdatedAt)
}

func readPendingScriptReview(doc scriptReviewDocument, resource domain.Resource, version domain.Version) (domain.Review, error) {
	policy, err := doc.PolicyBinding.policy()
	if err != nil || !pendingScriptReviewMatches(doc, resource, version) ||
		doc.Snapshot.Text != version.Content.Text || !slices.Equal(doc.Snapshot.Contributors, version.Contributors) {
		return domain.Review{}, port.ErrScriptQueryInconsistent
	}
	return domain.Review{ID: doc.ReviewID, Status: doc.Status, Policy: policy, SubmittedBy: doc.SubmittedBy, SubmittedAt: doc.SubmittedAt.UTC(),
		Snapshot: domain.ReviewSnapshot{ResourceID: resource.ID, VersionID: version.ID, Label: doc.Snapshot.VersionLabel, Content: version.Content,
			SourceRevision: doc.Snapshot.SourceRevision, Contributors: doc.Snapshot.Contributors,
			ResourceMetadata: domain.Metadata{Name: doc.Snapshot.ResourceName, Purpose: doc.Snapshot.ResourcePurpose},
			ResourceRevision: doc.Snapshot.ResourceRevision}}, nil
}
