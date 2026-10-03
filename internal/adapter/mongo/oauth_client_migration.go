package mongo

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	oauthClientManagementMigrationID = "0014_oauth_client_management"
	oauthRegistrationKeyIndexName    = "uq_oauth_registrations_application_channel"
	oauthPublicClientIndexName       = "uq_oauth_registrations_public_client_id"
	oauthConfidentialClientIndexName = "uq_oauth_registrations_confidential_client_id"
	oauthAllClientIDsIndexName       = "uq_oauth_registrations_all_client_ids"
	oauthCredentialClientIndexName   = "uq_oauth_credentials_client_id"
)

func oauthIdentitySchema(expectedType string) bson.D {
	return bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{"clientId", "type", "status", "authorizationEpoch", "createdBy", "createdAt", "statusUpdatedBy", "statusUpdatedAt"}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "clientId", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "pattern", Value: "^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"}}},
			{Key: "type", Value: bson.D{{Key: "enum", Value: bson.A{expectedType}}}},
			{Key: "status", Value: bson.D{{Key: "enum", Value: bson.A{"ACTIVE", "DISABLED"}}}},
			{Key: "authorizationEpoch", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
			{Key: "createdBy", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}},
			{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "statusUpdatedBy", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}},
			{Key: "statusUpdatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}},
	}
}

func oauthRegistrationValidator() bson.D {
	return oauthRegistrationValidatorWithChannels(bson.A{"TEST"})
}

func oauthRegistrationValidatorWithChannels(channels bson.A) bson.D {
	return bson.D{{Key: "$jsonSchema", Value: bson.D{
		{Key: "bsonType", Value: "object"},
		{Key: "required", Value: bson.A{"applicationId", "channel", "publicClient", "confidentialClient", "clientIds", "registrationRevision", "createdAt", "updatedAt"}},
		{Key: "additionalProperties", Value: false},
		{Key: "properties", Value: bson.D{
			{Key: "_id", Value: bson.D{}},
			{Key: "applicationId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
			{Key: "channel", Value: bson.D{{Key: "enum", Value: channels}}},
			{Key: "publicClient", Value: bson.D{{Key: "anyOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, oauthIdentitySchema("PUBLIC_PKCE")}}}},
			{Key: "confidentialClient", Value: bson.D{{Key: "anyOf", Value: bson.A{bson.D{{Key: "bsonType", Value: "null"}}, oauthIdentitySchema("CONFIDENTIAL_SECRET")}}}},
			{Key: "clientIds", Value: bson.D{
				{Key: "bsonType", Value: "array"},
				{Key: "minItems", Value: 1},
				{Key: "maxItems", Value: 2},
				{Key: "uniqueItems", Value: true},
				{Key: "items", Value: bson.D{
					{Key: "bsonType", Value: "string"},
					{Key: "pattern", Value: "^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$"},
				}},
			}},
			{Key: "registrationRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
			{Key: "createdAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			{Key: "updatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
		}},
	}}}
}

func oauthCredentialValidator() bson.D {
	return bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$jsonSchema", Value: bson.D{
			{Key: "bsonType", Value: "object"},
			{Key: "required", Value: bson.A{"clientId", "applicationId", "secretDigest", "credentialRevision", "rotatedBy", "rotatedAt"}},
			{Key: "additionalProperties", Value: false},
			{Key: "properties", Value: bson.D{
				{Key: "_id", Value: bson.D{}},
				{Key: "clientId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
				{Key: "applicationId", Value: bson.D{{Key: "bsonType", Value: "string"}}},
				{Key: "secretDigest", Value: bson.D{{Key: "bsonType", Value: "binData"}}},
				{Key: "credentialRevision", Value: bson.D{{Key: "bsonType", Value: "long"}, {Key: "minimum", Value: int64(1)}}},
				{Key: "rotatedBy", Value: bson.D{{Key: "bsonType", Value: "string"}, {Key: "minLength", Value: 1}}},
				{Key: "rotatedAt", Value: bson.D{{Key: "bsonType", Value: "date"}}},
			}},
		}}},
		bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{bson.D{{Key: "$binarySize", Value: "$secretDigest"}}, int32(32)}}}}},
	}}}
}

func (m *Migrator) applyOAuthClientManagementMigration(ctx context.Context) error {
	if err := m.ensureValidatedCollection(ctx, applicationOAuthRegistrationsCollectionName, oauthRegistrationValidator()); err != nil {
		return err
	}
	if err := m.ensureValidatedCollection(ctx, oauthClientCredentialsCollectionName, oauthCredentialValidator()); err != nil {
		return err
	}
	registrations := m.database.Collection(applicationOAuthRegistrationsCollectionName)
	_, err := registrations.Indexes().CreateMany(ctx, []drivermongo.IndexModel{
		{Keys: bson.D{{Key: "applicationId", Value: 1}, {Key: "channel", Value: 1}}, Options: options.Index().SetName(oauthRegistrationKeyIndexName).SetUnique(true)},
		{Keys: bson.D{{Key: "publicClient.clientId", Value: 1}}, Options: options.Index().SetName(oauthPublicClientIndexName).SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "publicClient.clientId", Value: bson.D{{Key: "$type", Value: "string"}}}})},
		{Keys: bson.D{{Key: "confidentialClient.clientId", Value: 1}}, Options: options.Index().SetName(oauthConfidentialClientIndexName).SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "confidentialClient.clientId", Value: bson.D{{Key: "$type", Value: "string"}}}})},
		{Keys: bson.D{{Key: "clientIds", Value: 1}}, Options: options.Index().SetName(oauthAllClientIDsIndexName).SetUnique(true)},
	})
	if err != nil {
		return fmt.Errorf("create oauth registration indexes: %w", err)
	}
	_, err = m.database.Collection(oauthClientCredentialsCollectionName).Indexes().CreateOne(ctx, drivermongo.IndexModel{Keys: bson.D{{Key: "clientId", Value: 1}}, Options: options.Index().SetName(oauthCredentialClientIndexName).SetUnique(true)})
	if err != nil {
		return fmt.Errorf("create oauth credential index: %w", err)
	}
	return nil
}
