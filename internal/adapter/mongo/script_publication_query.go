package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

func (r *ScriptQueryRepository) GetPublication(ctx context.Context, resourceID domain.ResourceID) (domain.PublicationState, error) {
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (domain.PublicationState, error) {
		state, _, err := NewScriptPublicationRepository(r.db).loadState(tx, resourceID)
		return state, err
	})
}

func (r *ScriptQueryRepository) ListPublicationHistory(ctx context.Context, scope port.ScriptReadScope, resourceID domain.ResourceID, page port.ScriptPageRequest) (port.ScriptPage[port.PublicationHistoryItem], error) {
	var empty port.ScriptPage[port.PublicationHistoryItem]
	cursorScope := scriptCursorScope{Reader: scope, Resource: resourceID, List: "publication-history"}
	filter := bson.M{"resourceId": resourceID, "namespace": "PUBLICATION"}
	if err := r.applyCursor(page.Cursor, cursorScope, filter, "at", "eventId"); err != nil {
		return empty, err
	}
	return withScriptTransaction(ctx, r.db, func(tx context.Context) (port.ScriptPage[port.PublicationHistoryItem], error) {
		publication := NewScriptPublicationRepository(r.db)
		if _, _, err := publication.loadState(tx, resourceID); err != nil {
			return empty, err
		}
		docs, err := findScriptPage[scriptPublicationEventDocument](tx, r.db.Collection(scriptPublicationEventsCollection), filter, page.Limit, "at", "eventId", nil)
		if err != nil {
			return empty, err
		}
		return projectScriptPage(r, docs, page.Limit, cursorScope, func(doc scriptPublicationEventDocument) (port.PublicationHistoryItem, scriptCursorPosition, error) {
			var item port.PublicationHistoryItem
			if !canonicalScriptID(doc.EventID) || doc.ResourceID != string(resourceID) || doc.Namespace != "PUBLICATION" ||
				doc.Revision < 2 || !validScriptQueryTime(doc.At) || !shared.AuthID(doc.Actor).IsValid() || doc.Reason != "" {
				return item, scriptCursorPosition{}, domain.ErrPublicationInconsistent
			}
			item = port.PublicationHistoryItem{EventID: domain.PublicationEventID(doc.EventID), ResourceID: resourceID, Action: doc.Action, Revision: doc.Revision, At: doc.At.UTC()}
			switch doc.Action {
			case domain.PublishScript:
				if doc.Version == nil {
					return item, scriptCursorPosition{}, domain.ErrPublicationInconsistent
				}
				version, approvedAt, err := publication.approvedVersion(tx, resourceID, domain.VersionID(doc.Version.VersionID))
				if errors.Is(err, domain.ErrVersionNotFound) || errors.Is(err, domain.ErrScriptNotApproved) {
					return item, scriptCursorPosition{}, domain.ErrPublicationInconsistent
				}
				if err != nil {
					return item, scriptCursorPosition{}, err
				}
				fact, err := publication.publishedFact(tx, resourceID, version)
				if err != nil {
					return item, scriptCursorPosition{}, err
				}
				if fact == nil || *doc.Version != scriptPublishedVersion(version) || doc.At.Before(approvedAt) || doc.At.Before(fact.PublishedAt) {
					return item, scriptCursorPosition{}, domain.ErrPublicationInconsistent
				}
				item.Version = &version
			case domain.ClearScriptHead:
				if doc.Version != nil {
					return item, scriptCursorPosition{}, domain.ErrPublicationInconsistent
				}
			default:
				return item, scriptCursorPosition{}, domain.ErrPublicationInconsistent
			}
			return item, scriptCursorPosition{At: item.At, ID: doc.EventID}, nil
		})
	})
}
