package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"math"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
)

type versionReviewCheckDocument struct {
	ID          string `bson:"id"`
	Title       string `bson:"title"`
	Description string `bson:"description"`
}

type versionReviewPolicyDocument struct {
	Version              string                       `bson:"version"`
	RequiredChecks       []string                     `bson:"requiredChecks"`
	Status               string                       `bson:"status"`
	Definitions          []versionReviewCheckDocument `bson:"definitions,omitempty"`
	CoordinationRevision int64                        `bson:"coordinationRevision,omitempty"`
}

func equalVersionReviewPolicyDocuments(left, right versionReviewPolicyDocument) bool {
	return left.Version == right.Version && left.Status == right.Status && slices.Equal(left.RequiredChecks, right.RequiredChecks) && slices.Equal(left.Definitions, right.Definitions)
}

// VersionReviewPolicyRepository reads immutable App Center-owned policy
// versions. Writes are deployment migrations, not runtime CRUD.
type VersionReviewPolicyRepository struct {
	collection *drivermongo.Collection
}

func NewVersionReviewPolicyRepository(database *drivermongo.Database) *VersionReviewPolicyRepository {
	if database == nil {
		return &VersionReviewPolicyRepository{}
	}
	return &VersionReviewPolicyRepository{collection: database.Collection(versionReviewPoliciesCollectionName)}
}

func (repository *VersionReviewPolicyRepository) LoadBoundDefinition(
	ctx context.Context,
	expectedVersion reviewdomain.ReviewPolicyVersion,
	_ reviewdomain.ApplicationVersionReviewSnapshot,
) (*reviewdomain.VersionReviewPolicy, error) {
	if repository == nil || repository.collection == nil {
		return nil, errors.New("version review policy repository is unavailable")
	}
	var document versionReviewPolicyDocument
	err := repository.collection.FindOne(ctx, bson.D{{Key: "version", Value: expectedVersion.String()}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, reviewport.ErrApplicationReviewDataInconsistent
	}
	if err != nil {
		return nil, fmt.Errorf("read version review policy: %w", err)
	}
	policy, err := versionReviewPolicyFromDocument(document)
	if err != nil {
		return nil, reviewport.ErrApplicationReviewDataInconsistent
	}
	return policy, nil
}

func versionReviewPolicyFromDocument(document versionReviewPolicyDocument) (*reviewdomain.VersionReviewPolicy, error) {
	if document.CoordinationRevision < 1 {
		return nil, errors.New("invalid policy coordination revision")
	}
	version, err := reviewdomain.NewReviewPolicyVersion(document.Version)
	if err != nil {
		return nil, fmt.Errorf("decode version review policy version: %w", err)
	}
	checks := make([]reviewdomain.ReviewCheckDefinition, 0, len(document.RequiredChecks))
	for index, value := range document.RequiredChecks {
		id, err := reviewdomain.NewReviewCheckID(value)
		if err != nil {
			return nil, fmt.Errorf("decode version review policy check: %w", err)
		}
		if index >= len(document.Definitions) || document.Definitions[index].ID != value {
			return nil, errors.New("invalid policy definition identifiers")
		}
		stored := document.Definitions[index]
		definition, err := reviewdomain.NewFormalReviewCheckDefinition(id, stored.Title, stored.Description)
		if err != nil {
			return nil, err
		}
		checks = append(checks, definition)
	}
	if len(document.Definitions) == 0 || len(document.Definitions) != len(document.RequiredChecks) {
		return nil, errors.New("invalid policy definition count")
	}
	if document.Version == "app-version-review-v3" && (len(checks) != 4 || len(document.Definitions) != 4 || !slices.Equal(document.RequiredChecks, formalVersionReviewPolicy().RequiredChecks)) {
		return nil, errors.New("incomplete formal review policy")
	}
	policy, err := reviewdomain.NewVersionReviewPolicy(version, checks, reviewdomain.ReviewPolicyStatus(document.Status))
	if err != nil {
		return nil, fmt.Errorf("decode version review policy: %w", err)
	}
	return policy, nil
}

var _ reviewport.ReviewPolicyProvider = (*VersionReviewPolicyRepository)(nil)

// Selection and binding happen inside the caller's submission transaction.
func selectVersionReviewPolicyForSubmission(ctx context.Context, db *drivermongo.Database) (*reviewdomain.VersionReviewPolicy, error) {
	coll := db.Collection(versionReviewPoliciesCollectionName)
	cur, err := coll.Find(ctx, bson.M{"status": "ACTIVE"}, options.Find().SetLimit(2))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []versionReviewPolicyDocument
	if err = cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	if len(docs) != 1 {
		return nil, reviewport.ErrApplicationReviewDataInconsistent
	}
	d := docs[0]
	policy, err := versionReviewPolicyFromDocument(d)
	if err != nil || d.CoordinationRevision < 1 || d.CoordinationRevision == math.MaxInt64 {
		return nil, reviewport.ErrApplicationReviewDataInconsistent
	}
	update, err := coll.UpdateOne(ctx, bson.M{"version": d.Version, "status": "ACTIVE", "coordinationRevision": d.CoordinationRevision}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return nil, err
	}
	if update.MatchedCount != 1 {
		return nil, reviewport.ErrApplicationReviewDataInconsistent
	}
	return policy, nil
}
