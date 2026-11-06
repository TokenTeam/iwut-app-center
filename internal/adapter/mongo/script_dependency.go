package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"

	reviewport "iwut-app-center/internal/review/port"
	script "iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

const scriptDependenciesCollection = "application_review_script_dependencies"

type scriptReferenceDocument struct {
	ResourceID string `bson:"scriptResourceId"`
	VersionID  string `bson:"scriptVersionId"`
}

type frozenScriptReferenceDocument struct {
	Reference scriptReferenceDocument `bson:",inline"`
	Format    string                  `bson:"format"`
	Digest    string                  `bson:"digest"`
	ReviewID  string                  `bson:"scriptReviewId"`
}

type scriptApprovalDocument struct {
	VersionID  string `bson:"scriptVersionId"`
	Digest     string `bson:"digest"`
	DecisionID string `bson:"scriptDecisionId"`
}

type scriptDependencyDocument struct {
	AppReviewID   string                        `bson:"appReviewId"`
	ApplicationID string                        `bson:"applicationId"`
	AppVersionID  string                        `bson:"appVersionId"`
	Fact          frozenScriptReferenceDocument `bson:",inline"`
}

type scriptReviewDecisionDocument struct {
	ID      string    `bson:"decisionId"`
	Outcome string    `bson:"outcome"`
	Actor   string    `bson:"decidedBy"`
	At      time.Time `bson:"decidedAt"`
}

func scriptReferencesToDocuments(refs []shared.ScriptReference) []scriptReferenceDocument {
	out := make([]scriptReferenceDocument, len(refs))
	for i, ref := range refs {
		out[i] = scriptReferenceDocument{ResourceID: ref.ResourceID, VersionID: ref.VersionID}
	}
	return out
}

func scriptReferencesFromDocuments(docs []scriptReferenceDocument) ([]shared.ScriptReference, error) {
	refs := make([]shared.ScriptReference, len(docs))
	for i, d := range docs {
		refs[i] = shared.ScriptReference{ResourceID: d.ResourceID, VersionID: d.VersionID}
	}
	normalized, err := shared.NormalizeScriptReferences(refs)
	if err != nil || !slices.Equal(refs, normalized) {
		return nil, shared.ErrInvalidScriptReferences
	}
	return refs, nil
}

func frozenScriptReferenceToDocument(fact shared.FrozenScriptReference) frozenScriptReferenceDocument {
	return frozenScriptReferenceDocument{Reference: scriptReferenceDocument{ResourceID: fact.ResourceID, VersionID: fact.VersionID},
		Format: fact.Format, Digest: fact.Digest, ReviewID: fact.ReviewID}
}

func frozenScriptReferencesFromDocuments(docs []frozenScriptReferenceDocument) ([]shared.ScriptReference, []shared.FrozenScriptReference, error) {
	keys := make([]scriptReferenceDocument, len(docs))
	for i, d := range docs {
		keys[i] = d.Reference
	}
	refs, err := scriptReferencesFromDocuments(keys)
	if err != nil {
		return nil, nil, err
	}
	facts := make([]shared.FrozenScriptReference, len(docs))
	for i, d := range docs {
		facts[i] = shared.FrozenScriptReference{ScriptReference: refs[i], Format: d.Format, Digest: d.Digest, ReviewID: d.ReviewID}
		if !facts[i].Valid() {
			return nil, nil, shared.ErrInvalidScriptReferences
		}
	}
	return refs, facts, nil
}

type loadedScriptDependency struct {
	version  scriptVersionDocument
	resource script.Resource
	review   scriptReviewDocument
}

func loadScriptDependency(ctx context.Context, db *dm.Database, ref shared.ScriptReference) (loadedScriptDependency, error) {
	var result loadedScriptDependency
	err := db.Collection(scriptVersionsCollection).FindOne(ctx, bson.M{"resourceId": ref.ResourceID, "versionId": ref.VersionID}).Decode(&result.version)
	if errors.Is(err, dm.ErrNoDocuments) {
		return result, shared.ErrInvalidScriptReferences
	}
	if err != nil {
		return result, err
	}
	version := result.version.version()
	if version.Validate() != nil || (result.version.DependencyFence != nil && *result.version.DependencyFence < 0) {
		return result, reviewport.ErrApplicationReviewDataInconsistent
	}
	result.resource, err = loadScriptResource(ctx, db, script.ResourceID(ref.ResourceID))
	if errors.Is(err, script.ErrNotFound) || errors.Is(err, script.ErrInternal) {
		return result, reviewport.ErrApplicationReviewDataInconsistent
	}
	if err != nil {
		return result, err
	}
	if version.State == script.VersionDraft {
		return result, nil
	}
	err = db.Collection(scriptReviewsCollection).FindOne(ctx, bson.M{"snapshot.versionId": ref.VersionID}).Decode(&result.review)
	if errors.Is(err, dm.ErrNoDocuments) {
		return result, reviewport.ErrApplicationReviewDataInconsistent
	}
	if err != nil {
		return result, err
	}
	if !scriptDependencyReviewMatches(result.review, result.resource, version) {
		return result, reviewport.ErrApplicationReviewDataInconsistent
	}
	return result, nil
}

func scriptDependencyReviewMatches(review scriptReviewDocument, resource script.Resource, version script.Version) bool {
	if version.State == script.VersionSubmitted {
		_, err := readPendingScriptReview(review, resource, version)
		return review.Decision == nil && err == nil
	}
	if version.State != script.VersionApproved && version.State != script.VersionRejected {
		return false
	}
	decision := review.Decision
	if decision == nil || !canonicalScriptID(decision.ID) || decision.Outcome != string(version.State) || review.Status != decision.Outcome ||
		decision.Actor == "" || !validScriptQueryTime(decision.At) || decision.At.Before(review.SubmittedAt) ||
		decision.Actor != version.UpdatedBy.String() || !decision.At.Equal(version.UpdatedAt) {
		return false
	}
	// The immutable snapshot must still match the Version as it was submitted.
	submitted := version
	submitted.State = script.VersionSubmitted
	submitted.Revision--
	submitted.UpdatedBy = review.SubmittedBy
	submitted.UpdatedAt = review.SubmittedAt
	review.Status = script.ReviewPending
	review.Decision = nil
	_, err := readPendingScriptReview(review, resource, submitted)
	return err == nil
}

func (loaded loadedScriptDependency) frozen(ref shared.ScriptReference) shared.FrozenScriptReference {
	return shared.FrozenScriptReference{ScriptReference: ref, Format: loaded.version.Format, Digest: loaded.version.Digest, ReviewID: string(loaded.review.ReviewID)}
}

func (loaded loadedScriptDependency) summary(ref shared.ScriptReference) shared.ScriptDependencySummary {
	result := shared.ScriptDependencySummary{ScriptReference: ref, Format: loaded.version.Format, Digest: loaded.version.Digest,
		State: string(loaded.version.State), ReviewID: string(loaded.review.ReviewID)}
	if loaded.review.Decision != nil {
		result.DecisionID = loaded.review.Decision.ID
	}
	return result
}

func verifyDraftScriptReferences(ctx context.Context, db *dm.Database, refs []shared.ScriptReference) error {
	canonical, err := shared.NormalizeScriptReferences(refs)
	if err != nil || !slices.Equal(canonical, refs) {
		return shared.ErrInvalidScriptReferences
	}
	for _, ref := range refs {
		dependency, err := loadScriptDependency(ctx, db, ref)
		if err != nil {
			return err
		}
		if dependency.version.State != script.VersionSubmitted && dependency.version.State != script.VersionApproved {
			return shared.ErrInvalidScriptReferences
		}
	}
	return nil
}

func freezeScriptDependencies(ctx context.Context, db *dm.Database, refs []shared.ScriptReference) ([]shared.FrozenScriptReference, error) {
	out := make([]shared.FrozenScriptReference, 0, len(refs))
	for _, ref := range refs {
		dependency, err := loadScriptDependency(ctx, db, ref)
		if errors.Is(err, shared.ErrInvalidScriptReferences) {
			return nil, reviewport.ErrApplicationReviewDataInconsistent
		}
		if err != nil {
			return nil, err
		}
		switch dependency.version.State {
		case script.VersionRejected:
			return nil, shared.ErrScriptDependencyRejected
		case script.VersionSubmitted, script.VersionApproved:
		default:
			return nil, reviewport.ErrApplicationReviewDataInconsistent
		}
		frozen := dependency.frozen(ref)
		filter := bson.M{"resourceId": ref.ResourceID, "versionId": ref.VersionID, "digest": frozen.Digest,
			"state": dependency.version.State, "revision": dependency.version.Revision,
			"$or": bson.A{bson.M{"dependencyFence": bson.M{"$exists": false}},
				bson.M{"dependencyFence": bson.M{"$gte": int64(0), "$lt": int64(math.MaxInt64)}}}}
		result, err := db.Collection(scriptVersionsCollection).UpdateOne(ctx, filter, bson.M{"$inc": bson.M{"dependencyFence": int64(1)}})
		if err != nil {
			return nil, err
		}
		if result.ModifiedCount != 1 {
			return nil, reviewport.ErrApplicationReviewDataInconsistent
		}
		out = append(out, frozen)
	}
	return out, nil
}

func registerScriptDependencies(ctx context.Context, db *dm.Database, reviewID, applicationID, versionID string, facts []shared.FrozenScriptReference) error {
	if len(facts) == 0 {
		return nil
	}
	docs := make([]any, len(facts))
	for i, fact := range facts {
		docs[i] = scriptDependencyDocument{AppReviewID: reviewID, ApplicationID: applicationID, AppVersionID: versionID,
			Fact: frozenScriptReferenceToDocument(fact)}
	}
	if _, err := db.Collection(scriptDependenciesCollection).InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("register application review script dependencies: %w", err)
	}
	return nil
}

func loadFrozenScriptDependency(ctx context.Context, db *dm.Database, reviewID, applicationID, versionID string, fact shared.FrozenScriptReference) (loadedScriptDependency, error) {
	var relation scriptDependencyDocument
	err := db.Collection(scriptDependenciesCollection).FindOne(ctx, bson.M{"appReviewId": reviewID, "scriptVersionId": fact.VersionID}).Decode(&relation)
	if errors.Is(err, dm.ErrNoDocuments) {
		return loadedScriptDependency{}, reviewport.ErrApplicationReviewDataInconsistent
	}
	if err != nil {
		return loadedScriptDependency{}, err
	}
	if relation.ApplicationID != applicationID || relation.AppVersionID != versionID ||
		relation.Fact != frozenScriptReferenceToDocument(fact) {
		return loadedScriptDependency{}, reviewport.ErrApplicationReviewDataInconsistent
	}
	dependency, err := loadScriptDependency(ctx, db, fact.ScriptReference)
	if errors.Is(err, shared.ErrInvalidScriptReferences) {
		return dependency, reviewport.ErrApplicationReviewDataInconsistent
	}
	if err != nil {
		return dependency, err
	}
	if dependency.frozen(fact.ScriptReference) != fact {
		return dependency, reviewport.ErrApplicationReviewDataInconsistent
	}
	return dependency, nil
}

func verifyScriptApprovals(ctx context.Context, db *dm.Database, reviewID, applicationID, versionID string, facts []shared.FrozenScriptReference) ([]shared.ScriptApprovalFact, error) {
	out := make([]shared.ScriptApprovalFact, 0, len(facts))
	pending, rejected := false, false
	for _, fact := range facts {
		dependency, err := loadFrozenScriptDependency(ctx, db, reviewID, applicationID, versionID, fact)
		if err != nil {
			return nil, err
		}
		switch dependency.version.State {
		case script.VersionSubmitted:
			pending = true
		case script.VersionRejected:
			rejected = true
		case script.VersionApproved:
			out = append(out, shared.ScriptApprovalFact{VersionID: fact.VersionID, Digest: fact.Digest, DecisionID: dependency.review.Decision.ID})
		default:
			return nil, reviewport.ErrApplicationReviewDataInconsistent
		}
	}
	if rejected {
		return nil, shared.ErrScriptDependencyRejected
	}
	if pending {
		return nil, shared.ErrScriptDependencyPending
	}
	return out, nil
}

func scriptDependencySummaries(ctx context.Context, db *dm.Database, reviewID, appID, versionID string, facts []shared.FrozenScriptReference) ([]shared.ScriptDependencySummary, error) {
	out := make([]shared.ScriptDependencySummary, 0, len(facts))
	for _, fact := range facts {
		dependency, err := loadFrozenScriptDependency(ctx, db, reviewID, appID, versionID, fact)
		if err != nil {
			return nil, err
		}
		out = append(out, dependency.summary(fact.ScriptReference))
	}
	return out, nil
}
