package mongo

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	applicationdomain "iwut-app-center/internal/application/domain"
	applicationport "iwut-app-center/internal/application/port"
	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	oauthport "iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

const applicationAdminTransfersCollectionName = "application_admin_transfers"

type applicationAdminTransferDocument struct {
	TransferID                     string     `bson:"transferId"`
	ApplicationID                  string     `bson:"applicationId"`
	FromAdminID                    string     `bson:"fromAdminId"`
	ToAdminID                      string     `bson:"toAdminId"`
	SourceOwnershipRevision        int64      `bson:"sourceOwnershipRevision"`
	Status                         string     `bson:"status"`
	RequestedAt                    time.Time  `bson:"requestedAt"`
	ExpiresAt                      time.Time  `bson:"expiresAt"`
	ResolvedAt                     *time.Time `bson:"resolvedAt"`
	ResolvedBy                     *string    `bson:"resolvedBy"`
	ResolutionCause                *string    `bson:"resolutionCause"`
	ConfidentialCredentialHandling *string    `bson:"confidentialCredentialHandling"`
}

func transferFromDocument(document applicationAdminTransferDocument) (applicationdomain.ApplicationAdminTransfer, error) {
	id, err := applicationdomain.ParseApplicationAdminTransferID(document.TransferID)
	appID, ok := shared.ParseApplicationID(document.ApplicationID)
	from, to := shared.AuthID(document.FromAdminID), shared.AuthID(document.ToAdminID)
	status := applicationdomain.ApplicationAdminTransferStatus(document.Status)
	if err != nil || !ok || !from.IsValid() || !to.IsValid() || from == to || document.SourceOwnershipRevision < 1 || document.RequestedAt.IsZero() || document.ExpiresAt.IsZero() || !document.RequestedAt.Before(document.ExpiresAt) {
		return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
	}
	if status != applicationdomain.ApplicationAdminTransferPending && status != applicationdomain.ApplicationAdminTransferAccepted && status != applicationdomain.ApplicationAdminTransferRejected && status != applicationdomain.ApplicationAdminTransferCancelled && status != applicationdomain.ApplicationAdminTransferExpired {
		return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
	}
	var resolvedBy *shared.AuthID
	if document.ResolvedBy != nil {
		value := shared.AuthID(*document.ResolvedBy)
		if !value.IsValid() {
			return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		resolvedBy = &value
	}
	var handling *applicationdomain.ConfidentialCredentialHandling
	if document.ConfidentialCredentialHandling != nil {
		value, parseErr := applicationdomain.ParseConfidentialCredentialHandling(*document.ConfidentialCredentialHandling)
		if parseErr != nil {
			return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		handling = &value
	}
	var cause *applicationdomain.ApplicationAdminTransferResolutionCause
	if document.ResolutionCause != nil {
		value := applicationdomain.ApplicationAdminTransferResolutionCause(*document.ResolutionCause)
		if value != applicationdomain.ApplicationAdminTransferResolutionExplicit && value != applicationdomain.ApplicationAdminTransferResolutionApplicationClosure {
			return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		cause = &value
	}
	if !document.ExpiresAt.Equal(document.RequestedAt.Add(applicationdomain.ApplicationAdminTransferLifetime)) ||
		status == applicationdomain.ApplicationAdminTransferPending && (document.ResolvedAt != nil || resolvedBy != nil || cause != nil || handling != nil) ||
		status == applicationdomain.ApplicationAdminTransferExpired && (document.ResolvedAt == nil || resolvedBy != nil || cause != nil || handling != nil || document.ResolvedAt.Before(document.ExpiresAt)) ||
		(status == applicationdomain.ApplicationAdminTransferAccepted || status == applicationdomain.ApplicationAdminTransferRejected || status == applicationdomain.ApplicationAdminTransferCancelled) && (document.ResolvedAt == nil || resolvedBy == nil || !document.ResolvedAt.Before(document.ExpiresAt)) ||
		status == applicationdomain.ApplicationAdminTransferAccepted && (handling == nil || *resolvedBy != to) ||
		status == applicationdomain.ApplicationAdminTransferRejected && (*resolvedBy != to || cause != nil) ||
		status == applicationdomain.ApplicationAdminTransferCancelled && (*resolvedBy != from || cause == nil) ||
		status != applicationdomain.ApplicationAdminTransferAccepted && handling != nil ||
		status != applicationdomain.ApplicationAdminTransferCancelled && cause != nil {
		return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
	}
	return applicationdomain.ApplicationAdminTransfer{TransferID: id, ApplicationID: appID, FromAdminID: from, ToAdminID: to, SourceOwnershipRevision: document.SourceOwnershipRevision, Status: status, RequestedAt: document.RequestedAt.UTC(), ExpiresAt: document.ExpiresAt.UTC(), ResolvedAt: document.ResolvedAt, ResolvedBy: resolvedBy, ResolutionCause: cause, ConfidentialCredentialHandling: handling}, nil
}

type ApplicationAdminTransferRepository struct {
	database          *m.Database
	secrets           oauthport.SecretFactory
	initialQuotaLimit int32
}

type transferSecretMaterial struct {
	plain  string
	digest oauthdomain.SecretDigest
}

type needTransferSecrets struct{ clientIDs []oauthdomain.ClientID }

func (e *needTransferSecrets) Error() string { return "transfer secret material required" }

type acceptTransferTransactionResult struct {
	result  applicationdomain.AcceptApplicationAdminTransferResult
	expired bool
}

type resolveTransferTransactionResult struct {
	transfer applicationdomain.ApplicationAdminTransfer
	expired  bool
}

var _ applicationport.ApplicationAdminTransferRepository = (*ApplicationAdminTransferRepository)(nil)

func NewApplicationAdminTransferRepository(database *m.Database, secrets oauthport.SecretFactory, initialQuotaLimit int32) *ApplicationAdminTransferRepository {
	return &ApplicationAdminTransferRepository{database: database, secrets: secrets, initialQuotaLimit: initialQuotaLimit}
}

func (r *ApplicationAdminTransferRepository) transaction(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, r.mapError(err)
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, fn, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	return value, r.mapError(err)
}

func (r *ApplicationAdminTransferRepository) mapError(err error) error {
	if err == nil {
		return nil
	}
	known := []error{applicationdomain.ErrApplicationNotFound, applicationdomain.ErrApplicationAdminTransferNotFound, applicationdomain.ErrApplicationAdminRequired, applicationdomain.ErrApplicationAdminTransferParticipantRequired, applicationdomain.ErrApplicationAdminTransferAlreadyPending, applicationdomain.ErrApplicationNameConflict, applicationdomain.ErrApplicationQuotaExceeded, applicationdomain.ErrApplicationAdminTransferExpired, applicationdomain.ErrApplicationAdminTransferNotPending, applicationdomain.ErrApplicationOwnershipChanged, applicationdomain.ErrApplicationAdminTransferStateInconsistent, shared.ErrAccountExitBlocked}
	for _, target := range known {
		if errors.Is(err, target) {
			return target
		}
	}
	if m.IsDuplicateKeyError(err) {
		return applicationdomain.ErrApplicationAdminTransferAlreadyPending
	}
	var need *needTransferSecrets
	if errors.As(err, &need) {
		return need
	}
	return fmt.Errorf("%w: %v", applicationdomain.ErrApplicationAdminTransferStateInconsistent, err)
}

func sortedAuthIDs(values ...shared.AuthID) []shared.AuthID {
	result := append([]shared.AuthID(nil), values...)
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func lockTransferFences(ctx context.Context, db *m.Database, values ...shared.AuthID) error {
	for _, authID := range sortedAuthIDs(values...) {
		if err := requireOwnerWritable(ctx, db, authID.String(), false); err != nil {
			return err
		}
	}
	return nil
}

func (r *ApplicationAdminTransferRepository) readTransfer(ctx context.Context, transferID applicationdomain.ApplicationAdminTransferID) (applicationAdminTransferDocument, applicationdomain.ApplicationAdminTransfer, error) {
	var document applicationAdminTransferDocument
	err := r.database.Collection(applicationAdminTransfersCollectionName).FindOne(ctx, bson.D{{Key: "transferId", Value: transferID.String()}}).Decode(&document)
	if errors.Is(err, m.ErrNoDocuments) {
		return document, applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferNotFound
	}
	if err != nil {
		return document, applicationdomain.ApplicationAdminTransfer{}, err
	}
	transfer, err := transferFromDocument(document)
	return document, transfer, err
}

func (r *ApplicationAdminTransferRepository) expireLocked(ctx context.Context, document *applicationAdminTransferDocument, now time.Time) error {
	if document.Status != string(applicationdomain.ApplicationAdminTransferPending) || now.Before(document.ExpiresAt) {
		return nil
	}
	result, err := r.database.Collection(applicationAdminTransfersCollectionName).UpdateOne(ctx,
		bson.D{{Key: "transferId", Value: document.TransferID}, {Key: "status", Value: string(applicationdomain.ApplicationAdminTransferPending)}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: string(applicationdomain.ApplicationAdminTransferExpired)}, {Key: "resolvedAt", Value: now}, {Key: "resolvedBy", Value: nil}}}},
	)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return applicationdomain.ErrApplicationAdminTransferNotPending
	}
	document.Status = string(applicationdomain.ApplicationAdminTransferExpired)
	document.ResolvedAt = &now
	document.ResolvedBy = nil
	return nil
}

func lockApplicationFence(ctx context.Context, database *m.Database, applicationID string) error {
	result, err := database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.D{{Key: "id", Value: applicationID}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return applicationdomain.ErrApplicationNotFound
	}
	return nil
}

func (r *ApplicationAdminTransferRepository) GetOwnership(ctx context.Context, applicationID shared.ApplicationID, adminID shared.AuthID, now time.Time) (applicationdomain.ApplicationOwnership, error) {
	value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		var app applicationDocument
		if err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.D{{Key: "id", Value: applicationID.String()}}).Decode(&app); errors.Is(err, m.ErrNoDocuments) {
			return nil, applicationdomain.ErrApplicationNotFound
		} else if err != nil {
			return nil, err
		}
		if app.AdminID != adminID.String() {
			return nil, applicationdomain.ErrApplicationAdminRequired
		}
		if app.OwnershipRevision < 1 {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		var document applicationAdminTransferDocument
		err := r.database.Collection(applicationAdminTransfersCollectionName).FindOne(tx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "status", Value: string(applicationdomain.ApplicationAdminTransferPending)}}).Decode(&document)
		if err != nil && !errors.Is(err, m.ErrNoDocuments) {
			return nil, err
		}
		ownership := applicationdomain.ApplicationOwnership{ApplicationID: applicationID, OwnershipRevision: app.OwnershipRevision}
		if err == nil {
			if !now.Before(document.ExpiresAt) {
				if err = lockTransferFences(tx, r.database, shared.AuthID(document.FromAdminID), shared.AuthID(document.ToAdminID)); err != nil {
					return nil, err
				}
				if err = lockApplicationFence(tx, r.database, document.ApplicationID); err != nil {
					return nil, err
				}
				if err = r.expireLocked(tx, &document, now); err != nil {
					return nil, err
				}
			}
			if document.Status == string(applicationdomain.ApplicationAdminTransferPending) {
				id, _ := applicationdomain.ParseApplicationAdminTransferID(document.TransferID)
				expires := document.ExpiresAt.UTC()
				ownership.PendingTransferID, ownership.PendingExpiresAt = &id, &expires
			}
		}
		return ownership, nil
	})
	if err != nil {
		return applicationdomain.ApplicationOwnership{}, err
	}
	return value.(applicationdomain.ApplicationOwnership), nil
}

func (r *ApplicationAdminTransferRepository) Initiate(ctx context.Context, applicationID shared.ApplicationID, from, to shared.AuthID, expected int64, transferID applicationdomain.ApplicationAdminTransferID, now time.Time) (applicationdomain.ApplicationAdminTransfer, error) {
	value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		if err := lockTransferFences(tx, r.database, from, to); err != nil {
			return nil, err
		}
		var app applicationDocument
		if err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(tx,
			bson.D{{Key: "id", Value: applicationID.String()}, {Key: "adminId", Value: from.String()}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&app); errors.Is(err, m.ErrNoDocuments) {
			var count int64
			count, _ = r.database.Collection(applicationsCollectionName).CountDocuments(tx, bson.D{{Key: "id", Value: applicationID.String()}})
			if count == 0 {
				return nil, applicationdomain.ErrApplicationNotFound
			}
			return nil, applicationdomain.ErrApplicationAdminRequired
		} else if err != nil {
			return nil, err
		}
		if app.OwnershipRevision != expected {
			return nil, applicationdomain.ErrApplicationOwnershipChanged
		}
		var existing applicationAdminTransferDocument
		err := r.database.Collection(applicationAdminTransfersCollectionName).FindOne(tx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "status", Value: string(applicationdomain.ApplicationAdminTransferPending)}}).Decode(&existing)
		if err == nil {
			if err = r.expireLocked(tx, &existing, now); err != nil {
				return nil, err
			}
			if existing.Status == string(applicationdomain.ApplicationAdminTransferPending) {
				transfer, mapErr := transferFromDocument(existing)
				if mapErr != nil {
					return nil, mapErr
				}
				if transfer.FromAdminID == from && transfer.ToAdminID == to {
					return transfer, nil
				}
				return nil, applicationdomain.ErrApplicationAdminTransferAlreadyPending
			}
		} else if !errors.Is(err, m.ErrNoDocuments) {
			return nil, err
		}
		document := applicationAdminTransferDocument{TransferID: transferID.String(), ApplicationID: applicationID.String(), FromAdminID: from.String(), ToAdminID: to.String(), SourceOwnershipRevision: expected, Status: string(applicationdomain.ApplicationAdminTransferPending), RequestedAt: now, ExpiresAt: now.Add(applicationdomain.ApplicationAdminTransferLifetime)}
		if _, err = r.database.Collection(applicationAdminTransfersCollectionName).InsertOne(tx, document); err != nil {
			return nil, err
		}
		return transferFromDocument(document)
	})
	if err != nil {
		return applicationdomain.ApplicationAdminTransfer{}, err
	}
	return value.(applicationdomain.ApplicationAdminTransfer), nil
}

func (r *ApplicationAdminTransferRepository) Get(ctx context.Context, transferID applicationdomain.ApplicationAdminTransferID, caller shared.AuthID, now time.Time) (applicationdomain.ApplicationAdminTransfer, error) {
	value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		document, transfer, err := r.readTransfer(tx, transferID)
		if err != nil {
			return nil, err
		}
		if !transfer.IsParticipant(caller) {
			return nil, applicationdomain.ErrApplicationAdminTransferParticipantRequired
		}
		if !now.Before(document.ExpiresAt) {
			if err = lockTransferFences(tx, r.database, transfer.FromAdminID, transfer.ToAdminID); err != nil {
				return nil, err
			}
			if err = lockApplicationFence(tx, r.database, transfer.ApplicationID.String()); err != nil {
				return nil, err
			}
			if err = r.expireLocked(tx, &document, now); err != nil {
				return nil, err
			}
		}
		return transferFromDocument(document)
	})
	if err != nil {
		return applicationdomain.ApplicationAdminTransfer{}, err
	}
	return value.(applicationdomain.ApplicationAdminTransfer), nil
}

func (r *ApplicationAdminTransferRepository) Accept(ctx context.Context, transferID applicationdomain.ApplicationAdminTransferID, caller shared.AuthID, handling applicationdomain.ConfidentialCredentialHandling, now time.Time) (applicationdomain.AcceptApplicationAdminTransferResult, error) {
	materials := make(map[oauthdomain.ClientID]transferSecretMaterial)
	for {
		value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
			document, transfer, err := r.readTransfer(tx, transferID)
			if err != nil {
				return nil, err
			}
			if caller != transfer.ToAdminID {
				return nil, applicationdomain.ErrApplicationAdminTransferParticipantRequired
			}
			if transfer.Status == applicationdomain.ApplicationAdminTransferAccepted {
				return acceptTransferTransactionResult{result: applicationdomain.AcceptApplicationAdminTransferResult{Transfer: transfer}}, nil
			}
			if err = lockTransferFences(tx, r.database, transfer.FromAdminID, transfer.ToAdminID); err != nil {
				return nil, err
			}
			if !now.Before(document.ExpiresAt) {
				if err = lockApplicationFence(tx, r.database, transfer.ApplicationID.String()); err != nil {
					return nil, err
				}
				if err = r.expireLocked(tx, &document, now); err != nil {
					return nil, err
				}
				return acceptTransferTransactionResult{expired: true}, nil
			}
			if document.Status != string(applicationdomain.ApplicationAdminTransferPending) {
				return nil, applicationdomain.ErrApplicationAdminTransferNotPending
			}
			var app applicationDocument
			err = r.database.Collection(applicationsCollectionName).FindOneAndUpdate(tx,
				bson.D{{Key: "id", Value: transfer.ApplicationID.String()}, {Key: "adminId", Value: transfer.FromAdminID.String()}, {Key: "ownershipRevision", Value: transfer.SourceOwnershipRevision}},
				bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: transfer.ToAdminID.String()}}}, {Key: "$inc", Value: bson.D{{Key: "ownershipRevision", Value: int64(1)}, {Key: "coordinationRevision", Value: int64(1)}}}}, options.FindOneAndUpdate().SetReturnDocument(options.Before)).Decode(&app)
			if errors.Is(err, m.ErrNoDocuments) {
				return nil, applicationdomain.ErrApplicationOwnershipChanged
			}
			if err != nil {
				if m.IsDuplicateKeyError(err) {
					return nil, applicationdomain.ErrApplicationNameConflict
				}
				return nil, err
			}
			if app.OwnershipRevision < 1 {
				return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
			}
			if n, e := r.database.Collection(applicationsCollectionName).CountDocuments(tx, bson.D{{Key: "adminId", Value: transfer.ToAdminID.String()}, {Key: "nameKey", Value: app.NameKey}, {Key: "id", Value: bson.D{{Key: "$ne", Value: app.ID}}}}, options.Count().SetLimit(1)); e != nil {
				return nil, e
			} else if n != 0 {
				return nil, applicationdomain.ErrApplicationNameConflict
			}
			if err = r.moveQuota(tx, transfer.FromAdminID, transfer.ToAdminID, now); err != nil {
				return nil, err
			}
			rotated := []applicationdomain.RotatedCredential{}
			if handling == applicationdomain.ConfidentialCredentialRotate {
				rotated, err = r.rotateConfidentialCredentials(tx, transfer.ApplicationID, transfer.ToAdminID, now, materials)
				if err != nil {
					return nil, err
				}
			}
			_, err = r.database.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(tx,
				bson.D{{Key: "applicationId", Value: transfer.ApplicationID.String()}, {Key: "status", Value: "ACTIVE"}},
				bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REVOKED"}, {Key: "revokedBy", Value: transfer.ToAdminID.String()}, {Key: "revokedAt", Value: now}, {Key: "revocationReason", Value: "ADMIN_TRANSFER"}, {Key: "replacedByJoinLinkId", Value: nil}}}})
			if err != nil {
				return nil, err
			}
			handlingString, resolvedBy := string(handling), transfer.ToAdminID.String()
			result, err := r.database.Collection(applicationAdminTransfersCollectionName).UpdateOne(tx,
				bson.D{{Key: "transferId", Value: transferID.String()}, {Key: "status", Value: string(applicationdomain.ApplicationAdminTransferPending)}},
				bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: string(applicationdomain.ApplicationAdminTransferAccepted)}, {Key: "resolvedAt", Value: now}, {Key: "resolvedBy", Value: resolvedBy}, {Key: "confidentialCredentialHandling", Value: handlingString}}}})
			if err != nil {
				return nil, err
			}
			if result.MatchedCount != 1 {
				return nil, applicationdomain.ErrApplicationAdminTransferNotPending
			}
			document.Status = string(applicationdomain.ApplicationAdminTransferAccepted)
			document.ResolvedAt = &now
			document.ResolvedBy = &resolvedBy
			document.ConfidentialCredentialHandling = &handlingString
			resolved, err := transferFromDocument(document)
			if err != nil {
				return nil, err
			}
			return acceptTransferTransactionResult{result: applicationdomain.AcceptApplicationAdminTransferResult{Transfer: resolved, RotatedCredentials: rotated, SecretsDisclosed: len(rotated) > 0}}, nil
		})
		if err != nil {
			var need *needTransferSecrets
			if errors.As(err, &need) {
				for _, clientID := range need.clientIDs {
					if _, exists := materials[clientID]; exists {
						continue
					}
					plain, digest, createErr := r.secrets.NewSecret(clientID)
					if createErr != nil || plain == "" {
						return applicationdomain.AcceptApplicationAdminTransferResult{}, applicationdomain.ErrApplicationAdminTransferStateInconsistent
					}
					materials[clientID] = transferSecretMaterial{plain: plain, digest: digest}
				}
				continue
			}
			return applicationdomain.AcceptApplicationAdminTransferResult{}, err
		}
		outcome := value.(acceptTransferTransactionResult)
		if outcome.expired {
			return applicationdomain.AcceptApplicationAdminTransferResult{}, applicationdomain.ErrApplicationAdminTransferExpired
		}
		return outcome.result, nil
	}
}

func (r *ApplicationAdminTransferRepository) moveQuota(ctx context.Context, from, to shared.AuthID, now time.Time) error {
	source := r.database.Collection(applicationCreationQuotasCollectionName)
	result, err := source.UpdateOne(ctx, bson.D{{Key: "adminId", Value: from.String()}, {Key: "usedCount", Value: bson.D{{Key: "$gt", Value: int32(0)}}}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "usedCount", Value: int32(-1)}, {Key: "revision", Value: int64(1)}}}, {Key: "$set", Value: bson.D{{Key: "updatedAt", Value: now}}}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return applicationdomain.ErrApplicationAdminTransferStateInconsistent
	}
	_, err = source.UpdateOne(ctx, bson.D{{Key: "adminId", Value: to.String()}}, bson.D{{Key: "$setOnInsert", Value: bson.D{{Key: "adminId", Value: to.String()}, {Key: "limit", Value: r.initialQuotaLimit}, {Key: "usedCount", Value: int32(0)}, {Key: "revision", Value: int64(0)}, {Key: "updatedAt", Value: now}}}}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}
	result, err = source.UpdateOne(ctx, bson.D{{Key: "adminId", Value: to.String()}, {Key: "$expr", Value: bson.D{{Key: "$lt", Value: bson.A{"$usedCount", "$limit"}}}}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "usedCount", Value: int32(1)}, {Key: "revision", Value: int64(1)}}}, {Key: "$set", Value: bson.D{{Key: "updatedAt", Value: now}}}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return applicationdomain.ErrApplicationQuotaExceeded
	}
	return nil
}

func (r *ApplicationAdminTransferRepository) rotateConfidentialCredentials(ctx context.Context, applicationID shared.ApplicationID, by shared.AuthID, now time.Time, materials map[oauthdomain.ClientID]transferSecretMaterial) ([]applicationdomain.RotatedCredential, error) {
	cursor, err := r.database.Collection(applicationOAuthRegistrationsCollectionName).Find(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "confidentialClient", Value: bson.D{{Key: "$ne", Value: nil}}}}, options.Find().SetSort(bson.D{{Key: "channel", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var registrations []applicationOAuthRegistrationDocument
	if err = cursor.All(ctx, &registrations); err != nil {
		return nil, err
	}
	order := map[string]int{"TEST": 0, "GREY": 1, "STABLE": 2}
	sort.Slice(registrations, func(i, j int) bool { return order[registrations[i].Channel] < order[registrations[j].Channel] })
	result := make([]applicationdomain.RotatedCredential, 0, len(registrations))
	missing := make([]oauthdomain.ClientID, 0)
	for _, registration := range registrations {
		if registration.ConfidentialClient == nil {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		clientID, parseErr := oauthdomain.ParseClientID(registration.ConfidentialClient.ClientID)
		if parseErr != nil {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		if _, exists := materials[clientID]; !exists {
			missing = append(missing, clientID)
		}
	}
	if len(missing) != 0 {
		return nil, &needTransferSecrets{clientIDs: missing}
	}
	for _, registration := range registrations {
		if _, err = registrationFromDocument(registration); err != nil || registration.ConfidentialClient == nil {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		clientID, err := oauthdomain.ParseClientID(registration.ConfidentialClient.ClientID)
		if err != nil {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		material := materials[clientID]
		plain, digest := material.plain, material.digest
		bytes := digest.Bytes()
		update := r.database.Collection(oauthClientCredentialsCollectionName).FindOneAndUpdate(ctx,
			bson.D{{Key: "clientId", Value: clientID.String()}, {Key: "applicationId", Value: applicationID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "secretDigest", Value: bytes[:]}, {Key: "rotatedBy", Value: by.String()}, {Key: "rotatedAt", Value: now}}}, {Key: "$inc", Value: bson.D{{Key: "credentialRevision", Value: int64(1)}}}}, options.FindOneAndUpdate().SetReturnDocument(options.After))
		var credential oauthClientCredentialDocument
		if err = update.Decode(&credential); errors.Is(err, m.ErrNoDocuments) {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		} else if err != nil {
			return nil, err
		}
		if _, err = credentialFromDocument(credential); err != nil {
			return nil, applicationdomain.ErrApplicationAdminTransferStateInconsistent
		}
		result = append(result, applicationdomain.RotatedCredential{Channel: registration.Channel, ClientID: clientID.String(), CredentialRevision: credential.CredentialRevision, ClientSecret: plain})
	}
	return result, nil
}

func (r *ApplicationAdminTransferRepository) resolve(ctx context.Context, transferID applicationdomain.ApplicationAdminTransferID, caller shared.AuthID, target applicationdomain.ApplicationAdminTransferStatus, now time.Time) (applicationdomain.ApplicationAdminTransfer, error) {
	value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		document, transfer, err := r.readTransfer(tx, transferID)
		if err != nil {
			return nil, err
		}
		allowed := target == applicationdomain.ApplicationAdminTransferRejected && caller == transfer.ToAdminID || target == applicationdomain.ApplicationAdminTransferCancelled && caller == transfer.FromAdminID
		if !allowed {
			return nil, applicationdomain.ErrApplicationAdminTransferParticipantRequired
		}
		if transfer.Status == target {
			return resolveTransferTransactionResult{transfer: transfer}, nil
		}
		if err = lockTransferFences(tx, r.database, transfer.FromAdminID, transfer.ToAdminID); err != nil {
			return nil, err
		}
		if !now.Before(document.ExpiresAt) {
			if err = lockApplicationFence(tx, r.database, transfer.ApplicationID.String()); err != nil {
				return nil, err
			}
			if err = r.expireLocked(tx, &document, now); err != nil {
				return nil, err
			}
			expired, mapErr := transferFromDocument(document)
			if mapErr != nil {
				return nil, mapErr
			}
			return resolveTransferTransactionResult{transfer: expired, expired: true}, nil
		}
		if document.Status != string(applicationdomain.ApplicationAdminTransferPending) {
			return nil, applicationdomain.ErrApplicationAdminTransferNotPending
		}
		var app applicationDocument
		if err = r.database.Collection(applicationsCollectionName).FindOneAndUpdate(tx, bson.D{{Key: "id", Value: transfer.ApplicationID.String()}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}, options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&app); err != nil {
			return nil, err
		}
		if target == applicationdomain.ApplicationAdminTransferCancelled && app.AdminID != caller.String() {
			return nil, applicationdomain.ErrApplicationAdminRequired
		}
		resolvedBy := caller.String()
		set := bson.D{{Key: "status", Value: string(target)}, {Key: "resolvedAt", Value: now}, {Key: "resolvedBy", Value: resolvedBy}}
		if target == applicationdomain.ApplicationAdminTransferCancelled {
			set = append(set, bson.E{Key: "resolutionCause", Value: "EXPLICIT"})
		}
		result, err := r.database.Collection(applicationAdminTransfersCollectionName).UpdateOne(tx, bson.D{{Key: "transferId", Value: transferID.String()}, {Key: "status", Value: string(applicationdomain.ApplicationAdminTransferPending)}}, bson.D{{Key: "$set", Value: set}})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount != 1 {
			return nil, applicationdomain.ErrApplicationAdminTransferNotPending
		}
		document.Status = string(target)
		document.ResolvedAt = &now
		document.ResolvedBy = &resolvedBy
		if target == applicationdomain.ApplicationAdminTransferCancelled {
			cause := "EXPLICIT"
			document.ResolutionCause = &cause
		}
		resolved, mapErr := transferFromDocument(document)
		return resolveTransferTransactionResult{transfer: resolved}, mapErr
	})
	if err != nil {
		return applicationdomain.ApplicationAdminTransfer{}, err
	}
	outcome := value.(resolveTransferTransactionResult)
	if outcome.expired {
		return applicationdomain.ApplicationAdminTransfer{}, applicationdomain.ErrApplicationAdminTransferExpired
	}
	return outcome.transfer, nil
}

func (r *ApplicationAdminTransferRepository) Reject(ctx context.Context, id applicationdomain.ApplicationAdminTransferID, caller shared.AuthID, now time.Time) (applicationdomain.ApplicationAdminTransfer, error) {
	return r.resolve(ctx, id, caller, applicationdomain.ApplicationAdminTransferRejected, now)
}
func (r *ApplicationAdminTransferRepository) Cancel(ctx context.Context, id applicationdomain.ApplicationAdminTransferID, caller shared.AuthID, now time.Time) (applicationdomain.ApplicationAdminTransfer, error) {
	return r.resolve(ctx, id, caller, applicationdomain.ApplicationAdminTransferCancelled, now)
}
