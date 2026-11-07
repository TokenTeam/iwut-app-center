package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	publicationport "iwut-app-center/internal/publication/port"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	script "iwut-app-center/internal/script/domain"
	scriptport "iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

type distributedScript struct {
	descriptor script.ScriptDescriptor
	text       string
	gate       scriptAvailabilityDocument
}

// qualifyDistributedScript never follows head. Its caller owns the snapshot.
func qualifyDistributedScript(ctx context.Context, db *dm.Database, loaded loadedScriptDependency) (distributedScript, error) {
	var out distributedScript
	if loaded.version.State != script.VersionApproved {
		return out, script.ErrScriptContentUnavailable
	}
	if loaded.review.Decision == nil {
		return out, script.ErrDistributionStateInconsistent
	}
	version := script.PublishedScript{VersionID: loaded.version.VersionID, ReviewID: loaded.review.ReviewID,
		DecisionID: loaded.review.Decision.ID, Label: loaded.version.VersionLabel, Format: loaded.version.Format,
		Digest: loaded.version.Digest, ByteLength: int64(len(loaded.version.Text))}
	if version.Validate() != nil {
		return out, script.ErrDistributionStateInconsistent
	}
	resourceID := loaded.resource.ID
	fact, err := NewScriptPublicationRepository(db).publishedFact(ctx, resourceID, version)
	if err != nil {
		return out, scriptDistributionError(err)
	}
	if fact == nil {
		// A missing permanent fact after a publication event is damaged proof,
		// rather than an unpublished version. Never reconstruct it on reads.
		err = db.Collection(scriptPublicationEventsCollection).FindOne(ctx,
			bson.M{"resourceId": resourceID, "action": script.PublishScript, "version.versionId": version.VersionID}).Err()
		if err == nil {
			return out, script.ErrDistributionStateInconsistent
		}
		if !errors.Is(err, dm.ErrNoDocuments) {
			return out, scriptDistributionError(err)
		}
		return out, script.ErrScriptContentUnavailable
	}
	var gate scriptAvailabilityDocument
	err = db.Collection(scriptAvailabilityCollection).FindOne(ctx, bson.M{"resourceId": resourceID}).Decode(&gate)
	if errors.Is(err, dm.ErrNoDocuments) {
		return out, script.ErrDistributionStateInconsistent
	}
	if err != nil {
		return out, scriptDistributionError(err)
	}
	return distributedScriptFromProof(loaded, version, fact, gate)
}

func distributedScriptFromProof(loaded loadedScriptDependency, version script.PublishedScript, fact *scriptPublishedFactDocument, gate scriptAvailabilityDocument) (distributedScript, error) {
	var out distributedScript
	resourceID := loaded.resource.ID
	if fact == nil || fact.PublishedAt.Before(loaded.review.Decision.At) {
		return out, script.ErrDistributionStateInconsistent
	}
	if gate.ResourceID != string(resourceID) || gate.Revision < 1 || gate.CoordinationRevision < 1 ||
		!shared.AuthID(gate.UpdatedBy).IsValid() || !validScriptQueryTime(gate.UpdatedAt) || gate.UpdatedAt.Before(loaded.resource.CreatedAt) ||
		(gate.State != script.DistributionAvailable && gate.State != script.DistributionSuspended) ||
		(gate.Revision == 1 && gate.State != script.DistributionAvailable) {
		return out, script.ErrDistributionStateInconsistent
	}
	if gate.State == script.DistributionSuspended {
		return out, script.ErrScriptContentUnavailable
	}
	return distributedScript{descriptor: script.ScriptDescriptor{ResourceID: resourceID, VersionID: version.VersionID,
		Format: version.Format, Digest: version.Digest, ByteLength: version.ByteLength}, text: loaded.version.Text, gate: gate}, nil
}

func scriptDistributionError(err error) error {
	if err == nil {
		return nil
	}
	for _, inconsistent := range []error{script.ErrPublicationInconsistent, reviewport.ErrApplicationReviewDataInconsistent,
		scriptport.ErrScriptQueryInconsistent, script.ErrInternal} {
		if errors.Is(err, inconsistent) {
			return fmt.Errorf("%w: %w", script.ErrDistributionStateInconsistent, err)
		}
	}
	for _, known := range []error{script.ErrDistributionStateInconsistent, script.ErrScriptContentUnavailable, script.ErrDistributionUnavailable} {
		if errors.Is(err, known) {
			return err
		}
	}
	return fmt.Errorf("%w: %w", script.ErrDistributionUnavailable, err)
}

// applicationDistributedScripts consumes the exact review binding and approval
// proofs. A publication writes all unique resource gates in resource ID order.
func applicationDistributedScripts(ctx context.Context, db *dm.Database, review *reviewdomain.ApplicationReview, lock bool) ([]distributedScript, error) {
	if review == nil || review.Decision() == nil || review.Decision().Outcome() != reviewdomain.ReviewDecisionApproved || review.Decision().ApprovalValidation() == nil {
		return nil, script.ErrDistributionStateInconsistent
	}
	facts := review.ScriptDependencies()
	approvals := review.Decision().ApprovalValidation().ScriptApprovals()
	if len(facts) != len(approvals) {
		return nil, script.ErrDistributionStateInconsistent
	}
	out := make([]distributedScript, 0, len(facts))
	for i, fact := range facts {
		loaded, err := loadFrozenScriptDependency(ctx, db, review.ReviewID().String(), review.ApplicationID().String(), review.VersionID().String(), fact)
		if err != nil {
			return nil, scriptDistributionError(err)
		}
		if loaded.version.State != script.VersionApproved || loaded.review.Decision == nil ||
			approvals[i] != (shared.ScriptApprovalFact{VersionID: fact.VersionID, Digest: fact.Digest, DecisionID: loaded.review.Decision.ID}) {
			return nil, script.ErrDistributionStateInconsistent
		}
		value, err := qualifyDistributedScript(ctx, db, loaded)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if lock {
		ordered := slices.Clone(out)
		slices.SortFunc(ordered, func(a, b distributedScript) int {
			return strings.Compare(string(a.descriptor.ResourceID), string(b.descriptor.ResourceID))
		})
		for _, item := range ordered {
			gate := item.gate
			if gate.CoordinationRevision == math.MaxInt64 {
				return nil, script.ErrDistributionStateInconsistent
			}
			result, err := db.Collection(scriptAvailabilityCollection).UpdateOne(ctx,
				bson.M{"resourceId": gate.ResourceID, "state": script.DistributionAvailable, "coordinationRevision": gate.CoordinationRevision},
				bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
			if err != nil {
				return nil, scriptDistributionError(err)
			}
			if result.ModifiedCount != 1 {
				return nil, script.ErrDistributionStateInconsistent
			}
		}
	}
	return out, nil
}

// The reduction path has no external preflight, but needs the same local
// approved source and dependency facts as a new placement.
func loadApplicationScriptReview(ctx context.Context, db *dm.Database, appID, versionID string) (*reviewdomain.ApplicationReview, error) {
	var version applicationVersionDocument
	if err := db.Collection(applicationVersionsCollectionName).FindOne(ctx, bson.M{"applicationId": appID, "versionId": versionID}).Decode(&version); err != nil {
		if errors.Is(err, dm.ErrNoDocuments) {
			return nil, script.ErrDistributionStateInconsistent
		}
		return nil, scriptDistributionError(err)
	}
	var document applicationReviewDocument
	err := db.Collection(applicationReviewsCollectionName).FindOne(ctx, bson.M{"applicationId": appID, "versionId": versionID},
		options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})).Decode(&document)
	if errors.Is(err, dm.ErrNoDocuments) {
		return nil, script.ErrDistributionStateInconsistent
	}
	if err != nil {
		return nil, scriptDistributionError(err)
	}
	review, err := applicationReviewFromDocument(document)
	if err != nil || version.ReviewStatus != "APPROVED" || review.Decision() == nil || review.Decision().Outcome() != reviewdomain.ReviewDecisionApproved ||
		document.SourceVersionRevision > math.MaxInt64-2 || version.Revision != document.SourceVersionRevision+2 ||
		version.UpdatedBy != document.Decision.DecidedBy || !version.UpdatedAt.Equal(document.Decision.DecidedAt) {
		return nil, script.ErrDistributionStateInconsistent
	}
	var config applicationVersionOAuthConfigDocument
	if err := db.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(ctx, bson.M{"applicationId": appID, "applicationVersionId": versionID}).Decode(&config); err != nil {
		if errors.Is(err, dm.ErrNoDocuments) {
			return nil, script.ErrDistributionStateInconsistent
		}
		return nil, scriptDistributionError(err)
	}
	_, _, snapshot, err := applicationVersionDocumentToDecisionVersion(version, config)
	if err != nil || !snapshot.Equal(review.Snapshot()) {
		return nil, script.ErrDistributionStateInconsistent
	}
	return review, nil
}

func publicationScriptGateError(err error) error {
	switch {
	case errors.Is(err, script.ErrScriptContentUnavailable):
		return publicationport.ErrApplicationScriptDependencyUnavailable
	case errors.Is(err, script.ErrDistributionStateInconsistent):
		return fmt.Errorf("%w: %w", publicationport.ErrApplicationPublicationStateInconsistent, err)
	default:
		return fmt.Errorf("%w: %w", publicationport.ErrApplicationPublicationUnavailable, err)
	}
}
