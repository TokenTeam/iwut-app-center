package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

type OAuthClientRepository struct{ database *drivermongo.Database }

func NewOAuthClientRepository(database *drivermongo.Database) *OAuthClientRepository {
	return &OAuthClientRepository{database}
}

func oauthTransactionOptions() *options.TransactionOptionsBuilder {
	return options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority())
}

func (r *OAuthClientRepository) inTransaction(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	if r == nil || r.database == nil {
		return nil, fmt.Errorf("oauth client repository is nil")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	return session.WithTransaction(ctx, fn, oauthTransactionOptions())
}

func (r *OAuthClientRepository) fenceApplication(ctx context.Context, appID shared.ApplicationID, adminID shared.AuthID) error {
	var app applicationDocument
	err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx,
		bson.D{{Key: "id", Value: appID.String()}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}},
	).Decode(&app)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return port.ErrApplicationNotFound
	}
	if err != nil {
		return err
	}
	if app.AdminID != adminID.String() {
		return port.ErrApplicationAdminRequired
	}
	return nil
}

func (r *OAuthClientRepository) Register(ctx context.Context, appID shared.ApplicationID, channel domain.Channel, typ domain.ClientType, expected *int64, clientID domain.ClientID, digest *domain.SecretDigest, adminID shared.AuthID, at time.Time) (*domain.RegisterResult, error) {
	result, err := r.inTransaction(ctx, func(tx context.Context) (any, error) {
		if err := r.fenceApplication(tx, appID, adminID); err != nil {
			return nil, err
		}
		collection := r.database.Collection(applicationOAuthRegistrationsCollectionName)
		var document applicationOAuthRegistrationDocument
		err := collection.FindOne(tx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "channel", Value: string(channel)}}).Decode(&document)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			if expected != nil {
				return nil, port.ErrRegistrationChanged
			}
			identity, createErr := domain.NewClientIdentity(clientID, typ, adminID, at)
			if createErr != nil {
				return nil, port.ErrStateInconsistent
			}
			document = applicationOAuthRegistrationDocument{ApplicationID: appID.String(), Channel: string(channel), ClientIDs: []string{clientID.String()}, RegistrationRevision: 1, CreatedAt: at, UpdatedAt: at}
			if typ == domain.ClientTypePublicPKCE {
				document.PublicClient = identityToDocument(identity)
			} else {
				document.ConfidentialClient = identityToDocument(identity)
			}
			if _, err = collection.InsertOne(tx, document); err != nil {
				if drivermongo.IsDuplicateKeyError(err) {
					return nil, port.ErrClientAlreadyExists
				}
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else {
			registration, restoreErr := registrationFromDocument(document)
			if restoreErr != nil {
				return nil, port.ErrStateInconsistent
			}
			if expected == nil || *expected != registration.Revision() {
				return nil, port.ErrRegistrationChanged
			}
			if registration.Client(typ) != nil {
				return nil, port.ErrClientAlreadyExists
			}
			if registration.Revision() == math.MaxInt64 {
				return nil, port.ErrStateInconsistent
			}
			identity, createErr := domain.NewClientIdentity(clientID, typ, adminID, at)
			if createErr != nil {
				return nil, port.ErrStateInconsistent
			}
			field := "publicClient"
			if typ == domain.ClientTypeConfidentialSecret {
				field = "confidentialClient"
			}
			update, updateErr := collection.UpdateOne(tx,
				bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "channel", Value: string(channel)}, {Key: "registrationRevision", Value: *expected}, {Key: field, Value: nil}},
				bson.D{{Key: "$set", Value: bson.D{{Key: field, Value: identityToDocument(identity)}, {Key: "updatedAt", Value: at}}}, {Key: "$addToSet", Value: bson.D{{Key: "clientIds", Value: clientID.String()}}}, {Key: "$inc", Value: bson.D{{Key: "registrationRevision", Value: int64(1)}}}},
			)
			if updateErr != nil {
				if drivermongo.IsDuplicateKeyError(updateErr) {
					return nil, port.ErrClientAlreadyExists
				}
				return nil, updateErr
			}
			if update.MatchedCount != 1 || update.ModifiedCount != 1 {
				return nil, port.ErrRegistrationChanged
			}
			document.RegistrationRevision++
			document.UpdatedAt = at
			if typ == domain.ClientTypePublicPKCE {
				document.PublicClient = identityToDocument(identity)
			} else {
				document.ConfidentialClient = identityToDocument(identity)
			}
			document.ClientIDs = append(document.ClientIDs, clientID.String())
		}
		var credential *domain.Credential
		if typ == domain.ClientTypeConfidentialSecret {
			if digest == nil {
				return nil, port.ErrStateInconsistent
			}
			bytes := digest.Bytes()
			credentialDoc := oauthClientCredentialDocument{clientID.String(), appID.String(), append([]byte{}, bytes[:]...), 1, adminID.String(), at}
			if _, err = r.database.Collection(oauthClientCredentialsCollectionName).InsertOne(tx, credentialDoc); err != nil {
				if drivermongo.IsDuplicateKeyError(err) {
					return nil, port.ErrClientAlreadyExists
				}
				return nil, err
			}
			credential, err = credentialFromDocument(credentialDoc)
			if err != nil {
				return nil, port.ErrStateInconsistent
			}
		} else if digest != nil {
			return nil, port.ErrStateInconsistent
		}
		registration, err := registrationFromDocument(document)
		if err != nil {
			return nil, port.ErrStateInconsistent
		}
		return &domain.RegisterResult{Registration: registration, Credential: credential}, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*domain.RegisterResult), nil
}

func (r *OAuthClientRepository) GetRegistration(ctx context.Context, appID shared.ApplicationID, channel domain.Channel, adminID shared.AuthID) (*domain.Registration, error) {
	result, err := r.inTransaction(ctx, func(tx context.Context) (any, error) {
		if err := r.fenceApplication(tx, appID, adminID); err != nil {
			return nil, err
		}
		var document applicationOAuthRegistrationDocument
		err := r.database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(tx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "channel", Value: string(channel)}}).Decode(&document)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, port.ErrRegistrationNotFound
		}
		if err != nil {
			return nil, err
		}
		registration, err := registrationFromDocument(document)
		if err != nil {
			return nil, port.ErrStateInconsistent
		}
		return registration, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*domain.Registration), nil
}

func (r *OAuthClientRepository) registrationByClient(ctx context.Context, clientID domain.ClientID) (applicationOAuthRegistrationDocument, error) {
	var document applicationOAuthRegistrationDocument
	err := r.database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(ctx, bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "publicClient.clientId", Value: clientID.String()}}, bson.D{{Key: "confidentialClient.clientId", Value: clientID.String()}}}}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return document, port.ErrClientNotFound
	}
	return document, err
}

func (r *OAuthClientRepository) SetStatus(ctx context.Context, clientID domain.ClientID, expected int64, status domain.ClientStatus, adminID shared.AuthID, at time.Time) (*domain.StatusResult, error) {
	result, err := r.inTransaction(ctx, func(tx context.Context) (any, error) {
		document, err := r.registrationByClient(tx, clientID)
		if err != nil {
			return nil, err
		}
		appID, ok := shared.ParseApplicationID(document.ApplicationID)
		if !ok {
			return nil, port.ErrStateInconsistent
		}
		if err = r.fenceApplication(tx, appID, adminID); err != nil {
			return nil, err
		}
		registration, restoreErr := registrationFromDocument(document)
		if restoreErr != nil {
			return nil, port.ErrStateInconsistent
		}
		if registration.Revision() != expected {
			return nil, port.ErrRegistrationChanged
		}
		identity := registration.ClientByID(clientID)
		if identity == nil {
			return nil, port.ErrStateInconsistent
		}
		if identity.Status() == status {
			return &domain.StatusResult{Registration: registration, Changed: false}, nil
		}
		if expected == math.MaxInt64 || identity.AuthorizationEpoch() == math.MaxInt64 {
			return nil, port.ErrStateInconsistent
		}
		field := "publicClient"
		if identity.Type() == domain.ClientTypeConfidentialSecret {
			field = "confidentialClient"
		}
		update, updateErr := r.database.Collection(applicationOAuthRegistrationsCollectionName).UpdateOne(tx,
			bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "channel", Value: document.Channel}, {Key: "registrationRevision", Value: expected}, {Key: field + ".clientId", Value: clientID.String()}, {Key: field + ".status", Value: string(identity.Status())}},
			bson.D{{Key: "$set", Value: bson.D{{Key: field + ".status", Value: string(status)}, {Key: field + ".statusUpdatedBy", Value: adminID.String()}, {Key: field + ".statusUpdatedAt", Value: at}, {Key: "updatedAt", Value: at}}}, {Key: "$inc", Value: bson.D{{Key: field + ".authorizationEpoch", Value: int64(1)}, {Key: "registrationRevision", Value: int64(1)}}}})
		if updateErr != nil {
			return nil, updateErr
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return nil, port.ErrRegistrationChanged
		}
		document.RegistrationRevision++
		document.UpdatedAt = at
		target := document.PublicClient
		if field == "confidentialClient" {
			target = document.ConfidentialClient
		}
		target.Status = string(status)
		target.StatusUpdatedBy = adminID.String()
		target.StatusUpdatedAt = at
		target.AuthorizationEpoch++
		registration, restoreErr = registrationFromDocument(document)
		if restoreErr != nil {
			return nil, port.ErrStateInconsistent
		}
		return &domain.StatusResult{Registration: registration, Changed: true}, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*domain.StatusResult), nil
}

func (r *OAuthClientRepository) GetCredential(ctx context.Context, clientID domain.ClientID, adminID shared.AuthID) (*domain.Credential, error) {
	result, err := r.inTransaction(ctx, func(tx context.Context) (any, error) {
		var document oauthClientCredentialDocument
		err := r.database.Collection(oauthClientCredentialsCollectionName).FindOne(tx, bson.D{{Key: "clientId", Value: clientID.String()}}).Decode(&document)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, port.ErrCredentialNotFound
		}
		if err != nil {
			return nil, err
		}
		appID, ok := shared.ParseApplicationID(document.ApplicationID)
		if !ok {
			return nil, port.ErrStateInconsistent
		}
		if err = r.fenceApplication(tx, appID, adminID); err != nil {
			return nil, err
		}
		registration, regErr := r.registrationByClient(tx, clientID)
		if regErr != nil || registration.ConfidentialClient == nil || registration.ConfidentialClient.ClientID != clientID.String() {
			return nil, port.ErrStateInconsistent
		}
		credential, mapErr := credentialFromDocument(document)
		if mapErr != nil {
			return nil, port.ErrStateInconsistent
		}
		return credential, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*domain.Credential), nil
}

func (r *OAuthClientRepository) RotateSecret(ctx context.Context, clientID domain.ClientID, expected int64, digest domain.SecretDigest, adminID shared.AuthID, at time.Time) (*domain.Credential, error) {
	result, err := r.inTransaction(ctx, func(tx context.Context) (any, error) {
		var document oauthClientCredentialDocument
		err := r.database.Collection(oauthClientCredentialsCollectionName).FindOne(tx, bson.D{{Key: "clientId", Value: clientID.String()}}).Decode(&document)
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, port.ErrCredentialNotFound
		}
		if err != nil {
			return nil, err
		}
		if document.CredentialRevision != expected {
			return nil, port.ErrCredentialChanged
		}
		appID, ok := shared.ParseApplicationID(document.ApplicationID)
		if !ok {
			return nil, port.ErrStateInconsistent
		}
		if err = r.fenceApplication(tx, appID, adminID); err != nil {
			return nil, err
		}
		registration, regErr := r.registrationByClient(tx, clientID)
		if regErr != nil || registration.ConfidentialClient == nil || registration.ConfidentialClient.ClientID != clientID.String() {
			return nil, port.ErrStateInconsistent
		}
		bytes := digest.Bytes()
		update, updateErr := r.database.Collection(oauthClientCredentialsCollectionName).UpdateOne(tx, bson.D{{Key: "clientId", Value: clientID.String()}, {Key: "credentialRevision", Value: expected}}, bson.D{{Key: "$set", Value: bson.D{{Key: "secretDigest", Value: append([]byte{}, bytes[:]...)}, {Key: "rotatedBy", Value: adminID.String()}, {Key: "rotatedAt", Value: at}}}, {Key: "$inc", Value: bson.D{{Key: "credentialRevision", Value: int64(1)}}}})
		if updateErr != nil {
			return nil, updateErr
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return nil, port.ErrCredentialChanged
		}
		document.SecretDigest = append([]byte{}, bytes[:]...)
		document.CredentialRevision++
		document.RotatedBy = adminID.String()
		document.RotatedAt = at
		credential, mapErr := credentialFromDocument(document)
		if mapErr != nil {
			return nil, port.ErrStateInconsistent
		}
		return credential, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*domain.Credential), nil
}
