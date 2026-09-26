package mongo

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	applicationProfileRevisionMigrationID     = "0011_application_profile_revision"
	applicationProfileRevisionsCollectionName = "application_profile_revisions"
	applicationProfilesCollectionName         = "application_profiles"
	profileRevisionIDUniqueIndexName          = "uq_application_profile_revisions_id"
	profileRevisionSequenceUniqueIndexName    = "uq_application_profile_revisions_application_sequence"
	profileWorkingRevisionUniqueIndexName     = "uq_application_profile_revisions_working_application"
	profileApplicationUniqueIndexName         = "uq_application_profiles_application_id"
)

func (m *Migrator) applyApplicationProfileRevisionMigration(ctx context.Context) error {
	for _, item := range []struct {
		name      string
		validator bson.D
		indexes   []drivermongo.IndexModel
	}{
		{applicationProfileRevisionsCollectionName, applicationProfileRevisionValidator(), []drivermongo.IndexModel{
			{Keys: bson.D{{Key: "profileRevisionId", Value: 1}}, Options: options.Index().SetName(profileRevisionIDUniqueIndexName).SetUnique(true)},
			{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetName(profileRevisionSequenceUniqueIndexName).SetUnique(true)},
			{Keys: bson.D{{Key: "applicationId", Value: 1}}, Options: options.Index().SetName(profileWorkingRevisionUniqueIndexName).SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "reviewStatus", Value: bson.D{{Key: "$in", Value: bson.A{"DRAFT", "SUBMITTED"}}}}})},
		}},
		{applicationProfilesCollectionName, applicationProfileValidator(), []drivermongo.IndexModel{
			{Keys: bson.D{{Key: "applicationId", Value: 1}}, Options: options.Index().SetName(profileApplicationUniqueIndexName).SetUnique(true)},
		}},
	} {
		if err := m.ensureValidatedCollection(ctx, item.name, item.validator); err != nil {
			return safeProfilePersistenceError(err)
		}
		if _, err := m.database.Collection(item.name).Indexes().CreateMany(ctx, item.indexes); err != nil {
			return fmt.Errorf("create profile indexes: %w", safeProfilePersistenceError(err))
		}
	}
	return nil
}

func profileNullableSchema(schema bson.D) bson.D {
	return bson.D{{Key: "oneOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, schema}}}
}
func profileTextSchema(max int) bson.D {
	// MongoDB validates Unicode code-point length and prohibited categories. NFC
	// is enforced by domain construction and validated again on reconstruction.
	return bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}, {Key: "maxLength", Value: max}, {Key: "pattern", Value: `^(?![\p{Z}\x{0009}-\x{000D}\x{0085}])(?!.*[\p{Z}\x{0009}-\x{000D}\x{0085}]$)[^\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}]+$`}}
}
func applicationProfileRevisionValidator() bson.D {
	schema := publicationObjectSchema(bson.A{"profileRevisionId", "applicationId", "sequence", "displayName", "description", "icon", "reviewStatus", "createdBy", "createdAt", "revision", "updatedBy", "updatedAt"}, bson.D{
		{Key: "profileRevisionId", Value: uuidV7Schema()}, {Key: "applicationId", Value: uuidV7Schema()}, {Key: "sequence", Value: positiveIntSchema()},
		{Key: "displayName", Value: profileTextSchema(80)}, {Key: "description", Value: profileNullableSchema(profileTextSchema(1000))}, {Key: "icon", Value: profileNullableSchema(profileTextSchema(512))},
		{Key: "reviewStatus", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "enum", Value: bson.A{"DRAFT", "SUBMITTED", "APPROVED", "REJECTED"}}}},
		{Key: "createdBy", Value: nonEmptyStringSchema()}, {Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		{Key: "revision", Value: positiveLongSchema()}, {Key: "updatedBy", Value: nonEmptyStringSchema()}, {Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
	})
	initial := bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$eq", Value: bson.A{"$reviewStatus", "DRAFT"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$createdBy", "$updatedBy"}}},
		bson.D{{Key: "$eq", Value: bson.A{"$createdAt", "$updatedAt"}}},
	}}}
	audit := bson.D{{Key: "$expr", Value: bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "$gt", Value: bson.A{"$revision", int64(1)}}}, initial}}}}}
	return bson.D{{Key: "$and", Value: bson.A{schema, audit}}}
}
func applicationProfileValidator() bson.D {
	return publicationObjectSchema(bson.A{"applicationId", "workingProfileRevisionId", "currentPublishedProfileRevisionId"}, bson.D{
		{Key: "applicationId", Value: uuidV7Schema()}, {Key: "workingProfileRevisionId", Value: profileNullableSchema(uuidV7Schema())}, {Key: "currentPublishedProfileRevisionId", Value: profileNullableSchema(uuidV7Schema())},
	})
}
