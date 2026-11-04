package mongo

import (
	"context"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	pp "iwut-app-center/internal/profile/port"
)

// The caller owns the submission transaction. Retirement writes the same
// ACTIVE policy document, so an old snapshot cannot commit a new binding.
func selectProfileReviewPolicyForSubmission(ctx context.Context, db *dm.Database) (pd.ProfileReviewPolicy, error) {
	coll := db.Collection(profileReviewPoliciesCollectionName)
	cur, err := coll.Find(ctx, bson.M{"status": "ACTIVE"}, options.Find().SetLimit(2))
	if err != nil {
		return pd.ProfileReviewPolicy{}, err
	}
	defer cur.Close(ctx)
	var docs []bson.Raw
	if err = cur.All(ctx, &docs); err != nil {
		return pd.ProfileReviewPolicy{}, err
	}
	if len(docs) != 1 {
		return pd.ProfileReviewPolicy{}, pp.ErrApplicationProfileStateInconsistent
	}
	policy, coord, err := profilePolicyFromRaw(docs[0])
	if err != nil || coord == math.MaxInt64 {
		return pd.ProfileReviewPolicy{}, pp.ErrApplicationProfileStateInconsistent
	}
	update, err := coll.UpdateOne(ctx, bson.M{"version": policy.Version, "status": "ACTIVE", "coordinationRevision": coord}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return pd.ProfileReviewPolicy{}, err
	}
	if update.MatchedCount != 1 {
		return pd.ProfileReviewPolicy{}, pp.ErrApplicationProfileStateInconsistent
	}
	return policy, nil
}
