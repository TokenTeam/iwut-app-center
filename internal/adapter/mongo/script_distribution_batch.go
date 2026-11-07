package mongo

import (
	"context"
	"errors"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	reviewdomain "iwut-app-center/internal/review/domain"
	script "iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

// A chunk holds at most sixteen version/review text pairs. Pages retain only
// compact proof results after validating each chunk, never all script bodies.
const scriptDistributionBatchSize = 16

type preparedDistributedScript struct {
	frozen   shared.FrozenScriptReference
	decision string
	label    string
	value    distributedScript
	err      error
}

func scriptDistributionDocuments[T any](ctx context.Context, db *dm.Database, collection string, filter any) ([]T, error) {
	out := []T{}
	err := collect(ctx, db.Collection(collection), filter, func(d T) { out = append(out, d) })
	return out, scriptDistributionError(err)
}

func prepareDistributedScripts(ctx context.Context, db *dm.Database, refs []shared.ScriptReference) (map[string]preparedDistributedScript, error) {
	result := make(map[string]preparedDistributedScript, len(refs))
	for start := 0; start < len(refs); start += scriptDistributionBatchSize {
		chunk := refs[start:min(start+scriptDistributionBatchSize, len(refs))]
		ids, resources, pairs := []string{}, []string{}, bson.A{}
		for _, ref := range chunk {
			ids, resources = append(ids, ref.VersionID), append(resources, ref.ResourceID)
			pairs = append(pairs, bson.M{"resourceId": ref.ResourceID, "version.versionId": ref.VersionID})
		}
		versions, err := scriptDistributionDocuments[scriptVersionDocument](ctx, db, scriptVersionsCollection, bson.M{"versionId": bson.M{"$in": ids}})
		if err != nil {
			return nil, err
		}
		versionMap := map[string]scriptVersionDocument{}
		for _, d := range versions {
			if _, exists := versionMap[string(d.VersionID)]; exists {
				return nil, script.ErrDistributionStateInconsistent
			}
			versionMap[string(d.VersionID)] = d
		}
		resourceDocs, err := scriptDistributionDocuments[scriptResourceDocument](ctx, db, scriptResourcesCollectionName, bson.M{"resourceId": bson.M{"$in": resources}})
		if err != nil {
			return nil, err
		}
		resourceMap := map[string]script.Resource{}
		for _, d := range resourceDocs {
			if _, exists := resourceMap[d.ResourceID]; exists {
				return nil, script.ErrDistributionStateInconsistent
			}
			resourceMap[d.ResourceID] = d.resource()
		}
		reviews, err := scriptDistributionDocuments[scriptReviewDocument](ctx, db, scriptReviewsCollection, bson.M{"snapshot.versionId": bson.M{"$in": ids}})
		if err != nil {
			return nil, err
		}
		reviewMap := map[string]scriptReviewDocument{}
		for _, d := range reviews {
			key := string(d.Snapshot.VersionID)
			if _, exists := reviewMap[key]; exists {
				return nil, script.ErrDistributionStateInconsistent
			}
			reviewMap[key] = d
		}
		facts, err := scriptDistributionDocuments[scriptPublishedFactDocument](ctx, db, scriptPublishedVersionsCollection, bson.M{"$or": pairs})
		if err != nil {
			return nil, err
		}
		factMap, eventIDs := map[string]scriptPublishedFactDocument{}, []string{}
		for _, d := range facts {
			if _, exists := factMap[d.Version.VersionID]; exists {
				return nil, script.ErrDistributionStateInconsistent
			}
			factMap[d.Version.VersionID] = d
			eventIDs = append(eventIDs, d.FirstEventID)
		}
		eventMap := map[string]scriptPublicationEventDocument{}
		if len(eventIDs) > 0 {
			events, err := scriptDistributionDocuments[scriptPublicationEventDocument](ctx, db, scriptPublicationEventsCollection, bson.M{"eventId": bson.M{"$in": eventIDs}})
			if err != nil {
				return nil, err
			}
			for _, d := range events {
				if _, exists := eventMap[d.EventID]; exists {
					return nil, script.ErrDistributionStateInconsistent
				}
				eventMap[d.EventID] = d
			}
		}
		gateDocs, err := scriptDistributionDocuments[scriptAvailabilityDocument](ctx, db, scriptAvailabilityCollection, bson.M{"resourceId": bson.M{"$in": resources}})
		if err != nil {
			return nil, err
		}
		gates := map[string]scriptAvailabilityDocument{}
		for _, d := range gateDocs {
			if _, exists := gates[d.ResourceID]; exists {
				return nil, script.ErrDistributionStateInconsistent
			}
			gates[d.ResourceID] = d
		}
		missingPairs := bson.A{}
		for _, ref := range chunk {
			if _, exists := factMap[ref.VersionID]; !exists {
				missingPairs = append(missingPairs, bson.M{"resourceId": ref.ResourceID, "version.versionId": ref.VersionID})
			}
		}
		historical := map[string]bool{}
		if len(missingPairs) > 0 {
			// Group in Mongo so arbitrarily long publication history cannot
			// expand the page memory budget when permanent proof is missing.
			cursor, err := db.Collection(scriptPublicationEventsCollection).Aggregate(ctx, dm.Pipeline{
				{{Key: "$match", Value: bson.M{"action": script.PublishScript, "$or": missingPairs}}},
				{{Key: "$group", Value: bson.M{"_id": "$version.versionId"}}},
			})
			if err != nil {
				return nil, scriptDistributionError(err)
			}
			var rows []struct {
				VersionID string `bson:"_id"`
			}
			err = cursor.All(ctx, &rows)
			_ = cursor.Close(ctx)
			if err != nil {
				return nil, scriptDistributionError(err)
			}
			for _, row := range rows {
				historical[row.VersionID] = true
			}
		}
		for _, ref := range chunk {
			loaded := loadedScriptDependency{version: versionMap[ref.VersionID], resource: resourceMap[ref.ResourceID], review: reviewMap[ref.VersionID]}
			prepared := preparedDistributedScript{err: script.ErrDistributionStateInconsistent}
			if loaded.version.ResourceID != script.ResourceID(ref.ResourceID) || loaded.version.VersionID != script.VersionID(ref.VersionID) ||
				loaded.version.version().Validate() != nil || loaded.resource.Validate() != nil ||
				(loaded.version.DependencyFence != nil && *loaded.version.DependencyFence < 0) ||
				loaded.version.State != script.VersionApproved || !scriptDependencyReviewMatches(loaded.review, loaded.resource, loaded.version.version()) || loaded.review.Decision == nil {
				result[ref.VersionID] = prepared
				continue
			}
			prepared.frozen, prepared.decision, prepared.label = loaded.frozen(ref), loaded.review.Decision.ID, loaded.version.VersionLabel
			fact, published := factMap[ref.VersionID]
			if !published {
				if !historical[ref.VersionID] {
					prepared.err = script.ErrScriptContentUnavailable
				}
				result[ref.VersionID] = prepared
				continue
			}
			version := script.PublishedScript{VersionID: loaded.version.VersionID, ReviewID: loaded.review.ReviewID, DecisionID: loaded.review.Decision.ID,
				Label: loaded.version.VersionLabel, Format: loaded.version.Format, Digest: loaded.version.Digest, ByteLength: int64(len(loaded.version.Text))}
			first, exists := eventMap[fact.FirstEventID]
			if version.Validate() != nil || !exists || validateScriptPublicationProof(script.ResourceID(ref.ResourceID), version, fact, first) != nil {
				result[ref.VersionID] = prepared
				continue
			}
			prepared.value, prepared.err = distributedScriptFromProof(loaded, version, &fact, gates[ref.ResourceID])
			prepared.value.text = ""
			result[ref.VersionID] = prepared
		}
	}
	return result, nil
}

func prepareApplicationScriptGates(ctx context.Context, db *dm.Database, reviews map[string]*reviewdomain.ApplicationReview) (map[string]error, error) {
	result := make(map[string]error, len(reviews))
	refsByID, relationPairs := map[string]shared.ScriptReference{}, bson.A{}
	for _, review := range reviews {
		if review == nil || review.Decision() == nil || review.Decision().ApprovalValidation() == nil || review.Decision().Outcome() != reviewdomain.ReviewDecisionApproved {
			return nil, script.ErrDistributionStateInconsistent
		}
		for _, fact := range review.ScriptDependencies() {
			relationPairs = append(relationPairs, bson.M{"appReviewId": review.ReviewID().String(), "scriptVersionId": fact.VersionID})
			if old, exists := refsByID[fact.VersionID]; exists && old != fact.ScriptReference {
				return nil, script.ErrDistributionStateInconsistent
			}
			refsByID[fact.VersionID] = fact.ScriptReference
		}
	}
	if len(refsByID) == 0 {
		for id := range reviews {
			result[id] = nil
		}
		return result, nil
	}
	refs := make([]shared.ScriptReference, 0, len(refsByID))
	for _, ref := range refsByID {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b shared.ScriptReference) int { return strings.Compare(a.VersionID, b.VersionID) })
	prepared, err := prepareDistributedScripts(ctx, db, refs)
	if err != nil {
		return nil, err
	}
	bound := map[string]scriptDependencyDocument{}
	for start := 0; start < len(relationPairs); start += 256 {
		pairs := relationPairs[start:min(start+256, len(relationPairs))]
		relations, err := scriptDistributionDocuments[scriptDependencyDocument](ctx, db, scriptDependenciesCollection, bson.M{"$or": pairs})
		if err != nil {
			return nil, err
		}
		for _, d := range relations {
			key := d.AppReviewID + "/" + d.Fact.Reference.VersionID
			if _, exists := bound[key]; exists {
				return nil, script.ErrDistributionStateInconsistent
			}
			bound[key] = d
		}
	}
	for id, review := range reviews {
		facts, approvals := review.ScriptDependencies(), review.Decision().ApprovalValidation().ScriptApprovals()
		if len(facts) != len(approvals) {
			return nil, script.ErrDistributionStateInconsistent
		}
		var unavailable error
		for i, fact := range facts {
			relation, exists := bound[review.ReviewID().String()+"/"+fact.VersionID]
			loaded, loadedOK := prepared[fact.VersionID]
			if !exists || relation.ApplicationID != review.ApplicationID().String() || relation.AppVersionID != review.VersionID().String() ||
				relation.Fact != frozenScriptReferenceToDocument(fact) || !loadedOK || loaded.frozen != fact ||
				approvals[i] != (shared.ScriptApprovalFact{VersionID: fact.VersionID, Digest: fact.Digest, DecisionID: loaded.decision}) {
				unavailable = script.ErrDistributionStateInconsistent
				break
			}
			if loaded.err != nil {
				if !errors.Is(loaded.err, script.ErrScriptContentUnavailable) {
					unavailable = loaded.err
					break
				}
				unavailable = loaded.err
			}
		}
		result[id] = unavailable
	}
	return result, nil
}
