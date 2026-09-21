package mongo

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
)

type versionReviewPolicyDocument struct {
	Version        string   `bson:"version"`
	RequiredChecks []string `bson:"requiredChecks"`
	Status         string   `bson:"status"`
}

func equalVersionReviewPolicyDocuments(left, right versionReviewPolicyDocument) bool {
	return left.Version == right.Version && left.Status == right.Status && slices.Equal(left.RequiredChecks, right.RequiredChecks)
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

func (repository *VersionReviewPolicyRepository) RequireUsable(
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
		return nil, reviewport.ErrReviewPolicyChanged
	}
	if err != nil {
		return nil, fmt.Errorf("read version review policy: %w", err)
	}
	version, err := reviewdomain.NewReviewPolicyVersion(document.Version)
	if err != nil {
		return nil, fmt.Errorf("decode version review policy version: %w", err)
	}
	checks := make([]reviewdomain.ReviewCheckDefinition, 0, len(document.RequiredChecks))
	for _, value := range document.RequiredChecks {
		id, err := reviewdomain.NewReviewCheckID(value)
		if err != nil {
			return nil, fmt.Errorf("decode version review policy check: %w", err)
		}
		definition, err := reviewdomain.NewReviewCheckDefinition(id)
		if err != nil {
			return nil, fmt.Errorf("decode version review policy definition: %w", err)
		}
		checks = append(checks, definition)
	}
	policy, err := reviewdomain.NewVersionReviewPolicy(version, checks, reviewdomain.ReviewPolicyStatus(document.Status))
	if err != nil {
		return nil, fmt.Errorf("decode version review policy: %w", err)
	}
	if !policy.Usable() {
		return nil, reviewport.ErrReviewPolicyChanged
	}
	return policy, nil
}

var _ reviewport.ReviewPolicyProvider = (*VersionReviewPolicyRepository)(nil)
