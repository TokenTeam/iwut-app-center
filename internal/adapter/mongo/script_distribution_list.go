package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const scriptDistributionScanBudget = 500
const scriptDistributionPageBytes = 2 * 1024 * 1024

func (r *ScriptDistributionRepository) ListPublishedScripts(ctx context.Context, caller shared.AuthID, page port.ScriptPageRequest, at time.Time) (domain.PublishedScriptPage, error) {
	var empty domain.PublishedScriptPage
	scope := scriptCursorScope{Reader: port.ScriptReadScope{Actor: caller}, List: "published-script-distribution/v1"}
	filter := bson.M{}
	if err := r.cursors.applyCursor(page.Cursor, scope, filter, "createdAt", "resourceId"); err != nil {
		return empty, domain.ErrInvalidDistributionCursor
	}
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.PublishedScriptPage, error) {
		docs, err := findScriptPage[scriptResourceDocument](tx, r.db.Collection(scriptResourcesCollectionName), filter, scriptDistributionScanBudget, "createdAt", "resourceId", nil)
		if err != nil {
			return empty, scriptDistributionError(err)
		}
		more := len(docs) > scriptDistributionScanBudget
		if more {
			docs = docs[:scriptDistributionScanBudget]
		}
		ids := make([]string, 0, len(docs))
		for _, doc := range docs {
			if doc.resource().Validate() != nil {
				return empty, domain.ErrDistributionStateInconsistent
			}
			ids = append(ids, doc.ResourceID)
		}
		result := domain.PublishedScriptPage{Items: make([]domain.PublishedScriptItem, 0, page.Limit), AsOf: at}
		if len(ids) == 0 {
			return result, nil
		}
		publications, err := scriptDistributionDocuments[scriptPublicationDocument](tx, r.db, scriptPublicationsCollection, bson.M{"resourceId": bson.M{"$in": ids}})
		if err != nil {
			return empty, err
		}
		pubs := map[string]scriptPublicationDocument{}
		for _, p := range publications {
			if _, exists := pubs[p.ResourceID]; exists {
				return empty, domain.ErrDistributionStateInconsistent
			}
			pubs[p.ResourceID] = p
		}
		gateDocs, err := scriptDistributionDocuments[scriptAvailabilityDocument](tx, r.db, scriptAvailabilityCollection, bson.M{"resourceId": bson.M{"$in": ids}})
		if err != nil {
			return empty, err
		}
		gates := map[string]scriptAvailabilityDocument{}
		for _, g := range gateDocs {
			if _, exists := gates[g.ResourceID]; exists {
				return empty, domain.ErrDistributionStateInconsistent
			}
			gates[g.ResourceID] = g
		}
		var last scriptCursorPosition
		responseBytes := 128
		for start := 0; start < len(docs); start += scriptDistributionBatchSize {
			chunk := docs[start:min(start+scriptDistributionBatchSize, len(docs))]
			refs := []shared.ScriptReference{}
			for _, doc := range chunk {
				p, present := pubs[doc.ResourceID]
				g, gatePresent := gates[doc.ResourceID]
				state := domain.PublicationState{ResourceID: domain.ResourceID(doc.ResourceID), PublicationRevision: p.Revision, Availability: g.State, AvailabilityRevision: g.Revision,
					PublicationUpdatedBy: shared.AuthID(p.UpdatedBy), PublicationUpdatedAt: p.UpdatedAt, AvailabilityUpdatedBy: shared.AuthID(g.UpdatedBy), AvailabilityUpdatedAt: g.UpdatedAt}
				if !present || !gatePresent || state.Validate() != nil || g.CoordinationRevision < 1 ||
					p.UpdatedAt.Before(doc.CreatedAt) || g.UpdatedAt.Before(doc.CreatedAt) ||
					(p.Head != nil && (!canonicalScriptID(*p.Head) || p.Revision == 1)) {
					return empty, domain.ErrDistributionStateInconsistent
				}
				if g.State == domain.DistributionAvailable && p.Head != nil {
					refs = append(refs, shared.ScriptReference{ResourceID: doc.ResourceID, VersionID: *p.Head})
				}
			}
			prepared, err := prepareDistributedScripts(tx, r.db, refs)
			if err != nil {
				return empty, err
			}
			for offset, doc := range chunk {
				position := scriptCursorPosition{At: doc.CreatedAt, ID: doc.ResourceID}
				p, g := pubs[doc.ResourceID], gates[doc.ResourceID]
				if g.State != domain.DistributionAvailable || p.Head == nil {
					last = position
					continue
				}
				proof, exists := prepared[*p.Head]
				if !exists || errors.Is(proof.err, domain.ErrScriptContentUnavailable) {
					return empty, domain.ErrDistributionStateInconsistent
				}
				if proof.err != nil {
					return empty, proof.err
				}
				version := proof.value.descriptor
				item := domain.PublishedScriptItem{ResourceID: domain.ResourceID(doc.ResourceID), Name: doc.Name, Purpose: doc.Purpose, VersionLabel: "", Head: version}
				// The head label is part of the immutable published descriptor.
				item.VersionLabel = proof.label
				if item.Validate() != nil {
					return empty, domain.ErrDistributionStateInconsistent
				}
				bytes := 6*(len(item.Name)+len(item.Purpose)+len(item.VersionLabel)) + 1024
				if len(result.Items) > 0 && (len(result.Items) >= page.Limit || responseBytes+bytes > scriptDistributionPageBytes) {
					result.NextCursor, err = r.cursors.encodeCursor(last, scope)
					return result, err
				}
				result.Items = append(result.Items, item)
				responseBytes += bytes
				last = position
				if len(result.Items) == page.Limit {
					if more || start+offset+1 < len(docs) {
						result.NextCursor, err = r.cursors.encodeCursor(last, scope)
					}
					return result, err
				}
			}
		}
		if more {
			result.NextCursor, err = r.cursors.encodeCursor(last, scope)
		}
		return result, err
	})
}
