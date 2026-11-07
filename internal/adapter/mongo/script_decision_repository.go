package mongo

import (
	"context"
	"errors"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

var _ port.ReviewDecisionRepository = (*ScriptVersionRepository)(nil)

func (r *ScriptVersionRepository) DecideReview(ctx context.Context, command port.DecideReview) (domain.DecisionResult, error) {
	at := command.At.UTC().Truncate(time.Millisecond)
	return withScriptTransaction(ctx, r.database, func(tx context.Context) (domain.DecisionResult, error) {
		var empty domain.DecisionResult
		loaded, err := loadScriptDependency(tx, r.database, shared.ScriptReference{ResourceID: string(command.ResourceID), VersionID: string(command.VersionID)})
		if errors.Is(err, shared.ErrInvalidScriptReferences) {
			return empty, port.ErrScriptReviewNotFound
		}
		if err != nil {
			return empty, err
		}
		if loaded.review.ReviewID != command.ReviewID {
			return empty, port.ErrScriptReviewNotFound
		}
		if loaded.review.Status != domain.ReviewPending {
			return empty, domain.ErrReviewDecided
		}
		current := loaded.version.version()
		review, err := readPendingScriptReview(loaded.review, loaded.resource, current)
		if err != nil {
			return empty, err
		}
		next, decision, err := review.Decide(current, command.Input, command.DecisionID, command.Actor, at)
		if err != nil {
			return empty, err
		}
		versionResult, err := r.database.Collection(scriptVersionsCollection).UpdateOne(tx,
			bson.M{"versionId": current.ID, "resourceId": current.ResourceID, "revision": current.Revision, "state": domain.VersionSubmitted},
			bson.M{"$set": bson.M{"state": next.State, "revision": next.Revision, "updatedBy": next.UpdatedBy, "updatedAt": next.UpdatedAt}})
		if err != nil {
			return empty, err
		}
		if versionResult.ModifiedCount != 1 {
			return empty, domain.ErrRevisionConflict
		}
		doc := scriptDecisionDocument(decision)
		result, err := r.database.Collection(scriptReviewsCollection).UpdateOne(tx,
			bson.M{"reviewId": review.ID, "status": domain.ReviewPending, "decision": nil},
			bson.M{"$set": bson.M{"status": string(next.State), "decision": doc}})
		if err != nil {
			return empty, err
		}
		if result.ModifiedCount != 1 {
			return empty, domain.ErrReviewDecided
		}
		if err := r.insertAudit(tx, next, "REVIEW_"+string(next.State)); err != nil {
			return empty, err
		}
		out := domain.DecisionResult{ReviewID: review.ID, Revision: next.Revision, Decision: decision}
		if next.State == domain.VersionRejected {
			if !canonicalScriptID(command.TaskID) {
				return empty, domain.ErrInternal
			}
			total, err := r.database.Collection(scriptDependenciesCollection).CountDocuments(tx, bson.M{"scriptVersionId": current.ID})
			if err != nil {
				return empty, err
			}
			task := scriptCascadeTaskDocument{TaskID: command.TaskID, ResourceID: string(current.ResourceID), VersionID: string(current.ID),
				Digest: current.Content.Digest, ReviewID: string(review.ID), DecisionID: decision.ID, TriggeredBy: decision.Actor.String(),
				DecidedAt: decision.At, Status: "PENDING", Phase: "DEPENDENCIES", Total: total, LeaseUntil: at, NextAttemptAt: at}
			if _, err := r.database.Collection(scriptCascadeTasksCollection).InsertOne(tx, task); err != nil {
				return empty, err
			}
			out.Cascade = &domain.CascadeProgress{TaskID: task.TaskID, Status: task.Status, Total: task.Total, ObservedAt: at}
		}
		return out, nil
	})
}

func scriptDecisionDocument(d domain.Decision) *scriptReviewDecisionDocument {
	checks := append([]string{}, d.ConfirmedCheckIDs...)
	return &scriptReviewDecisionDocument{ID: d.ID, Outcome: string(d.Outcome), Actor: d.Actor.String(), At: d.At,
		PolicyVersion: d.PolicyVersion, ConfirmedCheckIDs: checks, Reason: d.Reason}
}

func readScriptReview(doc scriptReviewDocument, resource domain.Resource, version domain.Version) (domain.Review, error) {
	if doc.Status == domain.ReviewPending {
		if doc.Decision != nil {
			return domain.Review{}, port.ErrScriptQueryInconsistent
		}
		return readPendingScriptReview(doc, resource, version)
	}
	d := doc.Decision
	if d == nil || !canonicalScriptID(d.ID) || (doc.Status != string(domain.VersionApproved) && doc.Status != string(domain.VersionRejected)) ||
		d.Outcome != doc.Status || string(version.State) != doc.Status || d.Actor != version.UpdatedBy.String() ||
		!d.At.Equal(version.UpdatedAt) || version.Revision < 3 || d.ConfirmedCheckIDs == nil {
		return domain.Review{}, port.ErrScriptQueryInconsistent
	}
	submitted := version
	submitted.State, submitted.Revision = domain.VersionSubmitted, version.Revision-1
	submitted.UpdatedBy, submitted.UpdatedAt = doc.SubmittedBy, doc.SubmittedAt
	pending := doc
	pending.Status, pending.Decision = domain.ReviewPending, nil
	review, err := readPendingScriptReview(pending, resource, submitted)
	if err != nil {
		return domain.Review{}, err
	}
	_, decision, err := review.Decide(submitted, domain.DecisionInput{ExpectedRevision: submitted.Revision, PolicyVersion: d.PolicyVersion,
		Action:            map[domain.VersionState]string{domain.VersionApproved: "APPROVE", domain.VersionRejected: "REJECT"}[version.State],
		ConfirmedCheckIDs: d.ConfirmedCheckIDs, Reason: d.Reason}, d.ID, version.UpdatedBy, d.At)
	if err != nil || !slices.Equal(decision.ConfirmedCheckIDs, d.ConfirmedCheckIDs) || decision.Reason != d.Reason {
		return domain.Review{}, port.ErrScriptQueryInconsistent
	}
	review.Status, review.Decision = doc.Status, &decision
	return review, nil
}

func loadScriptCascadeProgress(ctx context.Context, db *dm.Database, review domain.Review) (*domain.CascadeProgress, error) {
	if review.Status != string(domain.VersionRejected) {
		return nil, nil
	}
	var task scriptCascadeTaskDocument
	err := db.Collection(scriptCascadeTasksCollection).FindOne(ctx, bson.M{"decisionId": review.Decision.ID}).Decode(&task)
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, port.ErrScriptQueryInconsistent
	}
	if err != nil {
		return nil, err
	}
	if err := validateScriptCascadeSource(ctx, db, task); err != nil {
		return nil, err
	}
	return &domain.CascadeProgress{TaskID: task.TaskID, Status: task.Status, Processed: task.Processed, Total: task.Total, LastFailure: task.LastFailure,
		Rejected: task.Rejected, AlreadyTerminal: task.AlreadyTerminal, Closed: task.Closed}, nil
}
