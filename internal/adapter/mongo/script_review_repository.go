package mongo

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
	"iwut-app-center/internal/shared"
)

const (
	scriptReviewsCollection        = "script_reviews"
	scriptReviewPoliciesCollection = "script_review_policies"
	scriptDraftSubmitted           = "DRAFT_SUBMITTED"
	scriptPolicyActive             = "ACTIVE"
	scriptPolicyRetired            = "RETIRED"
)

var _ port.ReviewSubmissionRepository = (*ScriptVersionRepository)(nil)

type scriptReviewCheckDocument struct {
	ID          string `bson:"id"`
	Title       string `bson:"title"`
	Description string `bson:"description"`
	Mandatory   *bool  `bson:"mandatory"`
}

type scriptReviewPolicyBindingDocument struct {
	Version string                      `bson:"version"`
	Checks  []scriptReviewCheckDocument `bson:"checks"`
}

func scriptReviewPolicyBinding(p domain.ReviewPolicy) scriptReviewPolicyBindingDocument {
	checks := make([]scriptReviewCheckDocument, 0, len(p.Checks))
	for _, c := range p.Checks {
		mandatory := c.Mandatory
		checks = append(checks, scriptReviewCheckDocument{ID: c.ID, Title: c.Title, Description: c.Description, Mandatory: &mandatory})
	}
	return scriptReviewPolicyBindingDocument{Version: p.Version, Checks: checks}
}

func (d scriptReviewPolicyBindingDocument) policy() (domain.ReviewPolicy, error) {
	p := domain.ReviewPolicy{Version: d.Version, Checks: make([]domain.ReviewCheck, 0, len(d.Checks))}
	for _, c := range d.Checks {
		if c.Mandatory == nil {
			return domain.ReviewPolicy{}, domain.ErrReviewPolicyUnavailable
		}
		p.Checks = append(p.Checks, domain.ReviewCheck{ID: c.ID, Title: c.Title, Description: c.Description, Mandatory: *c.Mandatory})
	}
	if err := p.Validate(); err != nil {
		return domain.ReviewPolicy{}, err
	}
	return p, nil
}

type scriptReviewPolicyDocument struct {
	Binding              scriptReviewPolicyBindingDocument `bson:",inline"`
	Status               string                            `bson:"status"`
	CoordinationRevision int64                             `bson:"coordinationRevision"`
}

type scriptReviewSnapshotDocument struct {
	ResourceID       domain.ResourceID `bson:"resourceId"`
	VersionID        domain.VersionID  `bson:"versionId"`
	VersionLabel     string            `bson:"versionLabel"`
	Format           string            `bson:"contentFormat"`
	Text             string            `bson:"contentText"`
	Digest           string            `bson:"digest"`
	SourceRevision   int64             `bson:"sourceRevision"`
	Contributors     []shared.AuthID   `bson:"contributors"`
	ResourceName     string            `bson:"resourceName"`
	ResourcePurpose  string            `bson:"resourcePurpose"`
	ResourceRevision int64             `bson:"resourceRevision"`
}

type scriptReviewDocument struct {
	ReviewID      domain.ReviewID                   `bson:"reviewId"`
	Status        string                            `bson:"status"`
	Snapshot      scriptReviewSnapshotDocument      `bson:"snapshot"`
	PolicyBinding scriptReviewPolicyBindingDocument `bson:"policyBinding"`
	SubmittedBy   shared.AuthID                     `bson:"submittedBy"`
	SubmittedAt   time.Time                         `bson:"submittedAt"`
}

func toScriptReviewDocument(r domain.Review) scriptReviewDocument {
	return scriptReviewDocument{
		ReviewID: r.ID, Status: r.Status, SubmittedBy: r.SubmittedBy, SubmittedAt: r.SubmittedAt,
		PolicyBinding: scriptReviewPolicyBinding(r.Policy),
		Snapshot: scriptReviewSnapshotDocument{
			ResourceID: r.Snapshot.ResourceID, VersionID: r.Snapshot.VersionID, VersionLabel: r.Snapshot.Label,
			Format: r.Snapshot.Content.Format, Text: r.Snapshot.Content.Text, Digest: r.Snapshot.Content.Digest,
			SourceRevision: r.Snapshot.SourceRevision, Contributors: r.Snapshot.Contributors,
			ResourceName: r.Snapshot.ResourceMetadata.Name, ResourcePurpose: r.Snapshot.ResourceMetadata.Purpose,
			ResourceRevision: r.Snapshot.ResourceRevision,
		},
	}
}

func (r *ScriptVersionRepository) SubmitDraft(ctx context.Context, cmd port.SubmitDraft) (domain.Submission, error) {
	at := cmd.At.UTC().Truncate(time.Millisecond)
	return withScriptTransaction(ctx, r.database, func(tx context.Context) (domain.Submission, error) {
		resource, err := loadScriptResource(tx, r.database, cmd.ResourceID)
		if err != nil {
			return domain.Submission{}, err
		}
		current, err := r.loadVersion(tx, cmd.ResourceID, cmd.VersionID)
		if err != nil {
			return domain.Submission{}, err
		}
		if err := current.CheckDraftRevision(cmd.ExpectedRevision); err != nil {
			return domain.Submission{}, err
		}
		policy, err := selectScriptReviewPolicy(tx, r.database)
		if err != nil {
			return domain.Submission{}, err
		}
		next, review, err := current.Submit(cmd.ExpectedRevision, resource, cmd.ReviewID, policy, cmd.Actor, at)
		if err != nil {
			return domain.Submission{}, err
		}
		if err := r.writeDraftTransition(tx, current, bson.D{
			{Key: "state", Value: next.State}, {Key: "revision", Value: next.Revision},
			{Key: "updatedBy", Value: next.UpdatedBy}, {Key: "updatedAt", Value: next.UpdatedAt},
		}); err != nil {
			return domain.Submission{}, err
		}
		if _, err := r.database.Collection(scriptReviewsCollection).InsertOne(tx, toScriptReviewDocument(review)); err != nil {
			return domain.Submission{}, fmt.Errorf("insert script review snapshot: %w", err)
		}
		if err := r.insertAudit(tx, next, scriptDraftSubmitted); err != nil {
			return domain.Submission{}, err
		}
		return domain.Submission{
			ReviewID: review.ID, State: next.State, Revision: next.Revision,
			SubmittedAt: review.SubmittedAt, PolicyVersion: review.Policy.Version,
		}, nil
	})
}

func selectScriptReviewPolicy(ctx context.Context, db *drivermongo.Database) (domain.ReviewPolicy, error) {
	collection := db.Collection(scriptReviewPoliciesCollection)
	cursor, err := collection.Find(ctx, bson.M{"status": scriptPolicyActive}, options.Find().SetLimit(2))
	if err != nil {
		return domain.ReviewPolicy{}, fmt.Errorf("select active script review policy: %w", err)
	}
	defer cursor.Close(ctx)
	var active []scriptReviewPolicyDocument
	if err := cursor.All(ctx, &active); err != nil {
		return domain.ReviewPolicy{}, fmt.Errorf("decode active script review policy: %w", err)
	}
	if len(active) != 1 || active[0].CoordinationRevision < 1 || active[0].CoordinationRevision == math.MaxInt64 {
		return domain.ReviewPolicy{}, domain.ErrReviewPolicyUnavailable
	}
	selected := active[0]
	policy, err := selected.Binding.policy()
	if err != nil {
		return domain.ReviewPolicy{}, err
	}
	// Policy activation changes the same document so a submission cannot
	// commit a stale selection after a competing activation has won.
	result, err := collection.UpdateOne(ctx, bson.M{
		"version": selected.Binding.Version, "status": scriptPolicyActive, "coordinationRevision": selected.CoordinationRevision,
	}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return domain.ReviewPolicy{}, fmt.Errorf("fence script review policy binding: %w", err)
	}
	if result.MatchedCount != 1 {
		return domain.ReviewPolicy{}, domain.ErrReviewPolicyUnavailable
	}
	return policy, nil
}
