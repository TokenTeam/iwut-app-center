package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type ownedScriptRow struct {
	Resource scriptResourceDocument  `bson:",inline"`
	Latest   []scriptVersionDocument `bson:"latest"`
}

func scriptResourceVersionLookup(as string, visibleOnly bool) bson.D {
	pipeline := dm.Pipeline{}
	if visibleOnly {
		pipeline = append(pipeline, bson.D{{Key: "$match", Value: bson.M{"state": bson.M{"$in": submittedScriptStates}}}})
	} else {
		pipeline = append(pipeline, bson.D{{Key: "$sort", Value: bson.D{{Key: "createdAt", Value: -1}, {Key: "versionId", Value: -1}}}})
	}
	pipeline = append(pipeline, bson.D{{Key: "$limit", Value: 1}}, bson.D{{Key: "$project", Value: bson.M{"contentText": 0}}})
	return bson.D{{Key: "$lookup", Value: bson.M{
		"from": scriptVersionsCollection, "localField": "resourceId", "foreignField": "resourceId", "pipeline": pipeline, "as": as,
	}}}
}

var submittedScriptStates = bson.A{domain.VersionSubmitted, domain.VersionApproved, domain.VersionRejected}

func (r *ScriptQueryRepository) ListOwnResources(ctx context.Context, actor shared.AuthID, page port.ScriptPageRequest) (port.ScriptPage[port.OwnedScriptResource], error) {
	return r.ownedResources(ctx, actor, "", page)
}

func (r *ScriptQueryRepository) GetOwnResource(ctx context.Context, actor shared.AuthID, id domain.ResourceID) (port.OwnedScriptResource, error) {
	page, err := r.ownedResources(ctx, actor, id, port.ScriptPageRequest{Limit: 1})
	if err != nil {
		return port.OwnedScriptResource{}, err
	}
	if len(page.Items) == 0 {
		return port.OwnedScriptResource{}, domain.ErrNotFound
	}
	return page.Items[0], nil
}

func (r *ScriptQueryRepository) ownedResources(ctx context.Context, actor shared.AuthID, id domain.ResourceID, page port.ScriptPageRequest) (port.ScriptPage[port.OwnedScriptResource], error) {
	var empty port.ScriptPage[port.OwnedScriptResource]
	scope := scriptCursorScope{Reader: port.ScriptReadScope{Actor: actor, Own: true}, List: "resources", Resource: id}
	filter := bson.M{"ownerAuthId": actor}
	if id != "" {
		filter["resourceId"] = id
	}
	if err := r.applyCursor(page.Cursor, scope, filter, "createdAt", "resourceId"); err != nil {
		return empty, err
	}
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.ScriptPage[port.OwnedScriptResource], error) {
		rows, err := readScriptResourceRows[ownedScriptRow](tx, r.db, dm.Pipeline{
			{{Key: "$match", Value: filter}},
			{{Key: "$sort", Value: bson.D{{Key: "createdAt", Value: -1}, {Key: "resourceId", Value: -1}}}},
			{{Key: "$limit", Value: int64(page.Limit) + 1}},
			scriptResourceVersionLookup("latest", false),
		})
		if err != nil {
			return empty, err
		}
		return projectScriptPage(r, rows, page.Limit, scope, func(row ownedScriptRow) (port.OwnedScriptResource, scriptCursorPosition, error) {
			resource := row.Resource.resource()
			item := port.OwnedScriptResource{Resource: resource}
			position := scriptCursorPosition{At: resource.CreatedAt, ID: string(resource.ID)}
			if resource.Validate() != nil || resource.OwnerAuthID != actor {
				return item, position, port.ErrScriptQueryInconsistent
			}
			if len(row.Latest) != 0 {
				version := row.Latest[0]
				summary, err := scriptVersionSummary(version)
				if err != nil || !version.version().AuthoredBy(actor) || summary.ResourceID != resource.ID {
					return item, position, port.ErrScriptQueryInconsistent
				}
				item.LatestVersion = &summary
			}
			return item, position, nil
		})
	})
}

func readScriptResourceRows[T any](ctx context.Context, db *dm.Database, pipeline dm.Pipeline) ([]T, error) {
	cursor, err := db.Collection(scriptResourcesCollectionName).Aggregate(ctx, pipeline, options.Aggregate().SetCollation(&options.Collation{Locale: "simple"}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []T
	err = cursor.All(ctx, &rows)
	return rows, err
}

func (r *ScriptQueryRepository) GetOwnReview(ctx context.Context, actor shared.AuthID, resource domain.ResourceID, versionID domain.VersionID, reviewID domain.ReviewID) (port.OwnedScriptReviewResult, error) {
	var empty port.OwnedScriptReviewResult
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.OwnedScriptReviewResult, error) {
		parent, err := loadOwnedScriptResource(tx, r.db, resource, actor)
		if err != nil {
			return empty, err
		}
		var doc scriptReviewDocument
		err = r.db.Collection(scriptReviewsCollection).FindOne(tx, bson.M{"snapshot.resourceId": resource, "snapshot.versionId": versionID, "reviewId": reviewID}).Decode(&doc)
		if errors.Is(err, dm.ErrNoDocuments) {
			return empty, domain.ErrNotFound
		}
		if err != nil {
			return empty, err
		}
		version, err := NewScriptVersionRepository(r.db).loadVersion(tx, resource, versionID)
		if errors.Is(err, domain.ErrVersionNotFound) || (err == nil && version.Validate() != nil) {
			return empty, port.ErrScriptQueryInconsistent
		}
		if err != nil {
			return empty, err
		}
		review, err := readScriptReview(doc, parent, version)
		if err != nil {
			return empty, err
		}
		snapshot := review.Snapshot
		result := port.OwnedScriptReviewResult{
			ResourceID: resource, VersionID: versionID, ID: review.ID, Status: review.Status,
			SubmittedAt: review.SubmittedAt, Label: snapshot.Label, Format: snapshot.Content.Format,
			Digest: snapshot.Content.Digest, PolicyVersion: review.Policy.Version,
			SourceRevision: snapshot.SourceRevision, ResourceRevision: snapshot.ResourceRevision, Metadata: snapshot.ResourceMetadata,
		}
		if review.Decision != nil {
			result.DecisionID = review.Decision.ID
			result.Outcome = string(review.Decision.Outcome)
			result.Reason = review.Decision.Reason
			result.DecidedAt = review.Decision.At
		}
		return result, nil
	})
}

func (r *ScriptQueryRepository) ownedVersionReviews(ctx context.Context, reader port.ScriptReadScope, resource domain.ResourceID, versions []scriptVersionDocument) (map[domain.VersionID]domain.ReviewID, error) {
	if !reader.Own {
		return nil, nil
	}
	expected := make(map[domain.VersionID]string)
	ids := make([]domain.VersionID, 0, len(versions))
	for _, v := range versions {
		if v.State == domain.VersionDraft {
			continue
		}
		state := string(v.State)
		if v.State == domain.VersionSubmitted {
			state = domain.ReviewPending
		}
		expected[v.VersionID] = state
		ids = append(ids, v.VersionID)
	}
	found := make(map[domain.VersionID]domain.ReviewID, len(ids))
	if len(ids) == 0 {
		return found, nil
	}
	cursor, err := r.db.Collection(scriptReviewsCollection).Find(ctx,
		bson.M{"snapshot.resourceId": resource, "snapshot.versionId": bson.M{"$in": ids}},
		options.Find().SetLimit(int64(len(ids)+1)).SetProjection(bson.M{"reviewId": 1, "snapshot.versionId": 1, "status": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var doc scriptReviewDocument
		if err := cursor.Decode(&doc); err != nil {
			return nil, err
		}
		version := doc.Snapshot.VersionID
		if expected[version] != doc.Status || found[version] != "" || !canonicalScriptID(string(doc.ReviewID)) {
			return nil, port.ErrScriptQueryInconsistent
		}
		found[version] = doc.ReviewID
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	if len(found) != len(expected) {
		return nil, port.ErrScriptQueryInconsistent
	}
	return found, nil
}
