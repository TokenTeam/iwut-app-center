package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const greyPublicationMigrationID = "0017_grey_rollout"

func (m *Migrator) applyGreyPublicationMigration(ctx context.Context) error {
	targets := []struct {
		name      string
		validator bson.D
	}{
		{applicationPublicationsCollectionName, greyApplicationPublicationValidator()},
		{applicationPublicationHistoryCollectionName, greyApplicationPublicationHistoryValidator()},
		{applicationOAuthRegistrationsCollectionName, oauthRegistrationValidatorWithChannels(bson.A{"TEST", "GREY", "STABLE"})},
	}
	for _, target := range targets {
		if err := m.ensureValidatedCollection(ctx, target.name, target.validator); err != nil {
			return err
		}
	}
	return nil
}

func greyApplicationPublicationValidator() bson.D {
	nullableVersion := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, uuidV7Schema()}}}
	grey := bson.D{{Key: "oneOf", Value: bson.A{
		bson.D{{Key: "bsonType", Value: "null"}},
		bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{"rolloutId", "versionId", "exposureBasisPoints", "cohortSeed"}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "rolloutId", Value: uuidV7Schema()},
				{Key: "versionId", Value: uuidV7Schema()},
				{Key: "exposureBasisPoints", Value: bson.D{{Key: "bsonType", Value: "int"}, {Key: "minimum", Value: int32(1)}, {Key: "maximum", Value: int32(10_000)}}},
				{Key: "cohortSeed", Value: bson.D{{Key: "bsonType", Value: "binData"}}},
			}},
		},
	}}}
	schema := publicationObjectSchema(
		bson.A{"publicationId", "applicationId", "rpcApiMajor", "testVersionId", "stableVersionId", "greyRollout", "revision", "createdBy", "createdAt", "updatedBy", "updatedAt"},
		bson.D{
			{Key: "publicationId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "rpcApiMajor", Value: positiveIntSchema()},
			{Key: "testVersionId", Value: nullableVersion}, {Key: "stableVersionId", Value: nullableVersion}, {Key: "greyRollout", Value: grey},
			{Key: "revision", Value: positiveLongSchema()}, {Key: "createdBy", Value: nonEmptyStringSchema()}, {Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "updatedBy", Value: nonEmptyStringSchema()}, {Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		},
	)
	expression := bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$greyRollout", nil}}},
			bson.D{{Key: "$ne", Value: bson.A{"$stableVersionId", nil}}},
		}}},
		bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$greyRollout", nil}}},
			true,
			bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$binarySize", Value: "$greyRollout.cohortSeed"}}, int32(32)}}},
		}}},
		bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{"$revision", int64(1)}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}}, bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}}}}},
		}}},
	}}}}}
	return bson.D{{Key: "$and", Value: bson.A{schema, expression}}}
}

func greyApplicationPublicationHistoryValidator() bson.D {
	nullableVersion := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, uuidV7Schema()}}}
	nullableLong := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, positiveLongSchema()}}}
	nullablePolicy := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"}}}}}
	nullableExposure := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "bsonType", Value: "int"}, {Key: "minimum", Value: int32(1)}, {Key: "maximum", Value: int32(10_000)}}}}}
	nullableSeed := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "bsonType", Value: "binData"}}}}}
	actions := bson.A{"SET_TEST_VERSION", "SET_STABLE_VERSION", "CLEAR_STABLE_VERSION", "SET_GREY_ROLLOUT", "INCREASE_GREY_EXPOSURE", "DECREASE_GREY_EXPOSURE", "REPLACE_GREY_VERSION", "CLEAR_GREY_ROLLOUT"}
	schema := publicationObjectSchema(
		bson.A{"historyId", "publicationId", "applicationId", "rpcApiMajor", "publicationRevision", "action", "previousVersionId", "newVersionId", "approvedReviewId", "scopeCatalogRevision", "preflightPolicyVersion", "greyRolloutId", "previousExposureBasisPoints", "newExposureBasisPoints", "cohortSeed", "changedBy", "changedAt"},
		bson.D{
			{Key: "historyId", Value: uuidV7Schema()}, {Key: "publicationId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
			{Key: "rpcApiMajor", Value: positiveIntSchema()}, {Key: "publicationRevision", Value: positiveLongSchema()}, {Key: "action", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: actions}}},
			{Key: "previousVersionId", Value: nullableVersion}, {Key: "newVersionId", Value: nullableVersion}, {Key: "approvedReviewId", Value: nullableVersion},
			{Key: "scopeCatalogRevision", Value: nullableLong}, {Key: "preflightPolicyVersion", Value: nullablePolicy}, {Key: "greyRolloutId", Value: nullableVersion},
			{Key: "previousExposureBasisPoints", Value: nullableExposure}, {Key: "newExposureBasisPoints", Value: nullableExposure}, {Key: "cohortSeed", Value: nullableSeed},
			{Key: "changedBy", Value: nonEmptyStringSchema()}, {Key: "changedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		},
	)
	legacy := bson.A{"SET_TEST_VERSION", "SET_STABLE_VERSION", "CLEAR_STABLE_VERSION"}
	expression := bson.D{{Key: "$expr", Value: bson.D{{Key: "$or", Value: bson.A{
		legacyHistoryExpression(legacy),
		greyHistoryExpression(),
	}}}}}
	return bson.D{{Key: "$and", Value: bson.A{schema, expression}}}
}

func legacyHistoryExpression(actions bson.A) bson.D {
	isClear := bson.D{{Key: "$eq", Value: bson.A{"$action", "CLEAR_STABLE_VERSION"}}}
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$in", Value: bson.A{"$action", actions}}},
		bson.D{{Key: "$eq", Value: bson.A{"$greyRolloutId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousExposureBasisPoints", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$newExposureBasisPoints", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$cohortSeed", nil}}},
		bson.D{{Key: "$cond", Value: bson.A{
			isClear,
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$newVersionId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$approvedReviewId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$scopeCatalogRevision", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$preflightPolicyVersion", nil}}}}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$ne", Value: bson.A{"$newVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$approvedReviewId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$scopeCatalogRevision", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$preflightPolicyVersion", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", "$newVersionId"}}}, bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$publicationRevision", int64(1)}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousVersionId", nil}}}, true}}}}}},
		}}},
	}}}
}

func greyHistoryExpression() bson.D {
	full := bson.A{"SET_GREY_ROLLOUT", "INCREASE_GREY_EXPOSURE", "REPLACE_GREY_VERSION"}
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{"$greyRolloutId", nil}}},
		bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$in", Value: bson.A{"$action", full}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$ne", Value: bson.A{"$approvedReviewId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$scopeCatalogRevision", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$preflightPolicyVersion", nil}}}}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$approvedReviewId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$scopeCatalogRevision", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$preflightPolicyVersion", nil}}}}}},
		}}},
		bson.D{{Key: "$cond", Value: bson.A{
			bson.D{{Key: "$eq", Value: bson.A{"$action", "SET_GREY_ROLLOUT"}}},
			bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$binarySize", Value: "$cohortSeed"}}, int32(32)}}},
			bson.D{{Key: "$eq", Value: bson.A{"$cohortSeed", nil}}},
		}}},
		bson.D{{Key: "$or", Value: bson.A{
			greyHistorySetExpression(), greyHistoryAdjustExpression("INCREASE_GREY_EXPOSURE", "$lt"), greyHistoryAdjustExpression("DECREASE_GREY_EXPOSURE", "$gt"), greyHistoryReplaceExpression(), greyHistoryClearExpression(),
		}}},
	}}}
}

func greyHistorySetExpression() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$action", "SET_GREY_ROLLOUT"}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousExposureBasisPoints", nil}}},
		bson.D{{Key: "$ne", Value: bson.A{"$newVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$newExposureBasisPoints", nil}}},
	}}}
}

func greyHistoryAdjustExpression(action, comparison string) bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$action", action}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousVersionId", "$newVersionId"}}},
		bson.D{{Key: "$ne", Value: bson.A{"$previousExposureBasisPoints", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$newExposureBasisPoints", nil}}}, bson.D{{Key: comparison, Value: bson.A{"$previousExposureBasisPoints", "$newExposureBasisPoints"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$cohortSeed", nil}}},
	}}}
}

func greyHistoryReplaceExpression() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$action", "REPLACE_GREY_VERSION"}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$newVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", "$newVersionId"}}},
		bson.D{{Key: "$ne", Value: bson.A{"$previousExposureBasisPoints", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$newExposureBasisPoints", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$cohortSeed", nil}}},
	}}}
}

func greyHistoryClearExpression() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$action", "CLEAR_GREY_ROLLOUT"}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$previousExposureBasisPoints", nil}}},
		bson.D{{Key: "$eq", Value: bson.A{"$newVersionId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$newExposureBasisPoints", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$cohortSeed", nil}}},
	}}}
}
