package mongo

import (
	"context"
	"errors"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	pd "iwut-app-center/internal/profile/domain"
	"slices"
)

const applicationProfileReviewDecisionMigrationID = "0013_application_profile_review_decision"
const profileReviewPoliciesCollectionName = "profile_review_policies"
const profileReviewPolicyVersionUniqueIndexName = "uq_profile_review_policies_version"

func (m *Migrator) applyApplicationProfileReviewDecisionMigration(ctx context.Context) error {
	for _, item := range []struct {
		name      string
		validator bson.D
	}{{applicationProfileReviewsCollectionName, profileReviewDecisionValidator()}, {applicationProfileRevisionsCollectionName, profileRevisionDecisionValidator()}, {profileReviewPoliciesCollectionName, profileReviewPolicyValidator()}} {
		if err := m.ensureValidatedCollection(ctx, item.name, item.validator); err != nil {
			return safeProfilePersistenceError(err)
		}
	}
	policies := m.database.Collection(profileReviewPoliciesCollectionName)
	if _, err := policies.Indexes().CreateOne(ctx, dm.IndexModel{Keys: bson.D{{Key: "version", Value: 1}}, Options: options.Index().SetName(profileReviewPolicyVersionUniqueIndexName).SetUnique(true)}); err != nil {
		return safeProfilePersistenceError(err)
	}
	raw, err := policies.FindOne(ctx, bson.M{"version": pd.InitialProfileReviewPolicyVersion}).Raw()
	if errors.Is(err, dm.ErrNoDocuments) {
		_, err = policies.InsertOne(ctx, bson.M{"version": pd.InitialProfileReviewPolicyVersion, "requiredChecks": pd.InitialProfileReviewChecks(), "status": "ACTIVE", "coordinationRevision": int64(0)})
		return err
	}
	if err != nil {
		return safeProfilePersistenceError(err)
	}
	policy, _, err := profilePolicyFromRaw(raw)
	if err != nil || !slices.Equal(policy.RequiredChecks, pd.InitialProfileReviewChecks()) {
		return fmt.Errorf("profile review policy definition conflicts with migration")
	}
	return nil
}
func profileReviewPolicyValidator() bson.D {
	return publicationObjectSchema(bson.A{"version", "requiredChecks", "status", "coordinationRevision"}, bson.D{
		{Key: "version", Value: profilePolicyVersionSchema()}, {Key: "requiredChecks", Value: bson.D{{Key: "bsonType", Value: "array"}, {Key: "minItems", Value: 1}, {Key: "uniqueItems", Value: true}, {Key: "items", Value: profilePolicyVersionSchema()}}}, {Key: "status", Value: bson.M{"enum": bson.A{"ACTIVE", "RETIRED"}}}, {Key: "coordinationRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(0)}}},
	})
}
func profilePolicyVersionSchema() bson.D {
	return bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 50}, {Key: "pattern", Value: `^[A-Za-z0-9._-]+$`}}
}

// Each branch retains every original structural and audit constraint, relaxing
// only content strings for rejected history.
func profileRevisionDecisionValidator() bson.D {
	strict := applicationProfileRevisionValidator()
	raw := applicationProfileRevisionValidator()
	schema := raw[0].Value.(bson.A)[0].(bson.D)[0].Value.(bson.D)
	replaceSchemaProperty(schema, "displayName", bson.D{{Key: "bsonType", Value: "string"}})
	replaceSchemaProperty(schema, "description", profileNullableSchema(bson.D{{Key: "bsonType", Value: "string"}}))
	replaceSchemaProperty(schema, "icon", profileNullableSchema(bson.D{{Key: "bsonType", Value: "string"}}))
	return bson.D{{Key: "$or", Value: bson.A{bson.M{"$and": bson.A{bson.M{"reviewStatus": bson.M{"$ne": "REJECTED"}}, strict}}, bson.M{"$and": bson.A{bson.M{"reviewStatus": "REJECTED"}, raw}}}}}
}
func replaceSchemaProperty(schema bson.D, key string, value any) {
	for _, e := range schema {
		if e.Key == "properties" {
			props := e.Value.(bson.D)
			for i := range props {
				if props[i].Key == key {
					props[i].Value = value
					return
				}
			}
		}
	}
}
func profileReviewDecisionValidator() bson.D {
	branches := bson.A{bson.M{"$and": bson.A{bson.M{"status": "PENDING"}, applicationProfileReviewValidator()}}}
	for _, status := range []string{"APPROVED", "REJECTED"} {
		schema := applicationProfileReviewValidator()
		obj := schema[0].Value.(bson.D)
		replaceSchemaProperty(obj, "status", bson.D{{Key: "enum", Value: bson.A{status}}})
		if status == "REJECTED" {
			replaceSchemaProperty(obj, "snapshot", bson.D{{Key: "bsonType", Value: "object"}, {Key: "required", Value: bson.A{"displayName", "description", "icon"}}, {Key: "additionalProperties", Value: false}, {Key: "properties", Value: bson.D{{Key: "displayName", Value: bson.M{"bsonType": "string"}}, {Key: "description", Value: profileNullableSchema(bson.D{{Key: "bsonType", Value: "string"}})}, {Key: "icon", Value: profileNullableSchema(bson.D{{Key: "bsonType", Value: "string"}})}}}})
		}
		reason := bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: 2000}, {Key: "pattern", Value: `^(?![\p{Z}\x{0009}-\x{000D}\x{0085}])(?!.*[\p{Z}\x{0009}-\x{000D}\x{0085}]$)[^\p{Cc}]+$`}}
		if status == "APPROVED" {
			reason = profileNullableSchema(reason)
		}
		checks := bson.D{{Key: "bsonType", Value: "array"}, {Key: "uniqueItems", Value: true}, {Key: "items", Value: profilePolicyVersionSchema()}}
		if status == "REJECTED" {
			checks = append(checks, bson.E{Key: "maxItems", Value: 0})
		} else {
			checks = append(checks, bson.E{Key: "minItems", Value: 1})
		}
		decision := bson.D{{Key: "bsonType", Value: "object"}, {Key: "required", Value: bson.A{"outcome", "policyVersion", "confirmedCheckIds", "reason", "decidedBy", "decidedAt"}}, {Key: "additionalProperties", Value: false}, {Key: "properties", Value: bson.D{{Key: "outcome", Value: bson.M{"enum": bson.A{status}}}, {Key: "policyVersion", Value: profilePolicyVersionSchema()}, {Key: "confirmedCheckIds", Value: checks}, {Key: "reason", Value: reason}, {Key: "decidedBy", Value: nonEmptyStringSchema()}, {Key: "decidedAt", Value: bson.M{"bsonType": "date"}}}}}
		replaceSchemaProperty(obj, "decision", decision)
		branches = append(branches, bson.M{"$and": bson.A{bson.M{"status": status}, schema}})
	}
	return bson.D{{Key: "$or", Value: branches}}
}
