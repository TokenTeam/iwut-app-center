package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
)

const stablePublicationMigrationID = "0016_stable_publication"

func (m *Migrator) applyStablePublicationMigration(ctx context.Context) error {
	for _, target := range []struct {
		name      string
		validator bson.D
	}{
		{applicationPublicationsCollectionName, stableApplicationPublicationValidator()},
		{applicationPublicationHistoryCollectionName, stableApplicationPublicationHistoryValidator()},
		{applicationOAuthRegistrationsCollectionName, oauthRegistrationValidatorWithChannels(bson.A{"TEST", "STABLE"})},
	} {
		if err := m.ensureValidatedCollection(ctx, target.name, target.validator); err != nil {
			return err
		}
	}
	return nil
}

func stableApplicationPublicationValidator() bson.D {
	nullableVersion := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, uuidV7Schema()}}}
	return bson.D{{Key: "$and", Value: bson.A{
		publicationObjectSchema(bson.A{"publicationId", "applicationId", "rpcApiMajor", "testVersionId", "stableVersionId", "revision", "createdBy", "createdAt", "updatedBy", "updatedAt"}, bson.D{
			{Key: "publicationId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
			{Key: "rpcApiMajor", Value: positiveIntSchema()}, {Key: "testVersionId", Value: nullableVersion}, {Key: "stableVersionId", Value: nullableVersion},
			{Key: "revision", Value: positiveLongSchema()}, {Key: "createdBy", Value: nonEmptyStringSchema()},
			{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}}, {Key: "updatedBy", Value: nonEmptyStringSchema()},
			{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}),
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "$gt", Value: bson.A{"$revision", int64(1)}}},
			bson.D{{Key: "$and", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}}, bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}}}}},
		}}}}},
	}}}
}

func stableApplicationPublicationHistoryValidator() bson.D {
	nullableVersion := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, uuidV7Schema()}}}
	nullableLong := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, positiveLongSchema()}}}
	nullablePolicy := bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[A-Za-z0-9._-]{1,50}$"}}}}}
	isClear := bson.D{{Key: "$eq", Value: bson.A{"$action", "CLEAR_STABLE_VERSION"}}}
	return bson.D{{Key: "$and", Value: bson.A{
		publicationObjectSchema(bson.A{"historyId", "publicationId", "applicationId", "rpcApiMajor", "publicationRevision", "action", "previousVersionId", "newVersionId", "approvedReviewId", "scopeCatalogRevision", "preflightPolicyVersion", "changedBy", "changedAt"}, bson.D{
			{Key: "historyId", Value: uuidV7Schema()}, {Key: "publicationId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()},
			{Key: "rpcApiMajor", Value: positiveIntSchema()}, {Key: "publicationRevision", Value: positiveLongSchema()},
			{Key: "action", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: bson.A{"SET_TEST_VERSION", "SET_STABLE_VERSION", "CLEAR_STABLE_VERSION"}}}},
			{Key: "previousVersionId", Value: nullableVersion}, {Key: "newVersionId", Value: nullableVersion}, {Key: "approvedReviewId", Value: nullableVersion},
			{Key: "scopeCatalogRevision", Value: nullableLong}, {Key: "preflightPolicyVersion", Value: nullablePolicy},
			{Key: "changedBy", Value: nonEmptyStringSchema()}, {Key: "changedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}),
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$cond", Value: bson.A{
			isClear,
			bson.D{{Key: "$and", Value: bson.A{
				bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$newVersionId", nil}}},
				bson.D{{Key: "$eq", Value: bson.A{"$approvedReviewId", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$scopeCatalogRevision", nil}}}, bson.D{{Key: "$eq", Value: bson.A{"$preflightPolicyVersion", nil}}},
			}}},
			bson.D{{Key: "$and", Value: bson.A{
				bson.D{{Key: "$ne", Value: bson.A{"$newVersionId", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$approvedReviewId", nil}}},
				bson.D{{Key: "$ne", Value: bson.A{"$scopeCatalogRevision", nil}}}, bson.D{{Key: "$ne", Value: bson.A{"$preflightPolicyVersion", nil}}},
				bson.D{{Key: "$ne", Value: bson.A{"$previousVersionId", "$newVersionId"}}},
				bson.D{{Key: "$cond", Value: bson.A{bson.D{{Key: "$eq", Value: bson.A{"$publicationRevision", int64(1)}}}, bson.D{{Key: "$eq", Value: bson.A{"$previousVersionId", nil}}}, true}}},
			}}},
		}}}}},
	}}}
}
