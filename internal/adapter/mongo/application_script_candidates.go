package mongo

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	script "iwut-app-center/internal/script/domain"
	scriptport "iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

func (r *ApplicationVersionManagementRepository) ListScriptCandidates(ctx context.Context, actor shared.AuthID, applicationID shared.ApplicationID, limit int32, cursor string) (*versiondomain.ApplicationScriptCandidatePage, error) {
	key, err := DeriveScriptQueryCursorKey(r.cursorKey.value[:])
	if err != nil || !r.cursorKey.valid {
		return nil, versionport.ErrManagementUnavailable
	}
	query := NewScriptQueryRepository(r.database, key)
	scope := scriptCursorScope{Reader: scriptport.ScriptReadScope{Actor: actor}, List: "app-script-candidates:" + applicationID.String()}
	filter := bson.M{"state": bson.M{"$in": bson.A{script.VersionSubmitted, script.VersionApproved}}}
	if err := query.applyCursor(cursor, scope, filter, "createdAt", "versionId"); err != nil {
		return nil, versionport.ErrInvalidManagementPageToken
	}
	return withApplicationVersionManagementSnapshot(r, ctx, func(tx context.Context, _ time.Time) (*versiondomain.ApplicationScriptCandidatePage, error) {
		if err := r.requireVersionManagementAdmin(tx, actor, applicationID); err != nil {
			return nil, err
		}
		docs, err := findScriptPage[scriptVersionDocument](tx, r.database.Collection(scriptVersionsCollection), filter, int(limit), "createdAt", "versionId",
			bson.M{"contentText": 0, "contributors": 0, "createdBy": 0, "updatedBy": 0})
		if err != nil {
			return nil, versionport.ErrManagementUnavailable
		}
		page, err := projectScriptPage(query, docs, int(limit), scope, func(doc scriptVersionDocument) (versiondomain.ApplicationScriptCandidate, scriptCursorPosition, error) {
			ref := shared.ScriptReference{ResourceID: string(doc.ResourceID), VersionID: string(doc.VersionID)}
			dependency, err := loadScriptDependency(tx, r.database, ref)
			if err != nil || (dependency.version.State != script.VersionApproved && dependency.version.State != script.VersionSubmitted) {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return versiondomain.ApplicationScriptCandidate{}, scriptCursorPosition{}, err
				}
				return versiondomain.ApplicationScriptCandidate{}, scriptCursorPosition{}, versionport.ErrManagementStateInconsistent
			}
			summary := dependency.summary(ref)
			summary.ReviewID, summary.DecisionID = "", ""
			return versiondomain.ApplicationScriptCandidate{Dependency: summary, ResourceName: dependency.resource.Metadata.Name,
					ResourcePurpose: dependency.resource.Metadata.Purpose, VersionLabel: doc.VersionLabel},
				scriptCursorPosition{At: doc.CreatedAt, ID: string(doc.VersionID)}, nil
		})
		if err != nil {
			return nil, err
		}
		return &versiondomain.ApplicationScriptCandidatePage{Items: page.Items, NextCursor: page.NextCursor}, nil
	})
}
