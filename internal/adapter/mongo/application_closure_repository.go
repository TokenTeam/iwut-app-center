package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type applicationClosureDocument struct {
	ClosureID               string     `bson:"closureId"`
	ApplicationID           string     `bson:"applicationId"`
	InitiatedBy             string     `bson:"initiatedBy"`
	SourceOwnershipRevision int64      `bson:"sourceOwnershipRevision"`
	SourceLifecycleRevision int64      `bson:"sourceLifecycleRevision"`
	Status                  string     `bson:"status"`
	HighRiskProofJTI        string     `bson:"highRiskProofJti"`
	ClosingStartedAt        time.Time  `bson:"closingStartedAt"`
	AuthRevocationState     string     `bson:"authRevocationState"`
	AuthReceiptID           *string    `bson:"authReceiptId"`
	AuthAppliedAt           *time.Time `bson:"authAppliedAt"`
	ClosedAt                *time.Time `bson:"closedAt"`
	NextAttemptAt           *time.Time `bson:"nextAttemptAt"`
	Attempt                 int64      `bson:"attempt"`
}

type ApplicationClosureRepository struct{ database *m.Database }

var _ port.ApplicationClosureRepository = (*ApplicationClosureRepository)(nil)

func NewApplicationClosureRepository(database *m.Database) *ApplicationClosureRepository {
	return &ApplicationClosureRepository{database: database}
}

func closureFromDocument(d applicationClosureDocument, lifecycleStatus string, lifecycleRevision int64) (domain.ApplicationClosure, error) {
	id, err := domain.ParseApplicationClosureID(d.ClosureID)
	app, ok := shared.ParseApplicationID(d.ApplicationID)
	by := shared.AuthID(d.InitiatedBy)
	if err != nil || !ok || !by.IsValid() || d.SourceOwnershipRevision < 1 || d.SourceLifecycleRevision < 1 || d.SourceLifecycleRevision > math.MaxInt64-2 || !shared.IsUUIDv7(d.HighRiskProofJTI) || d.ClosingStartedAt.IsZero() || d.Attempt < 0 {
		return domain.ApplicationClosure{}, domain.ErrApplicationClosureStateInconsistent
	}
	status, revocation := domain.ApplicationClosureStatus(d.Status), domain.AuthRevocationState(d.AuthRevocationState)
	closing := status == domain.ApplicationClosureClosing && revocation == domain.AuthRevocationPending && lifecycleStatus == string(domain.ApplicationLifecycleClosing) && lifecycleRevision == d.SourceLifecycleRevision+1 && d.AuthReceiptID == nil && d.AuthAppliedAt == nil && d.ClosedAt == nil && d.NextAttemptAt != nil
	closed := status == domain.ApplicationClosureClosed && revocation == domain.AuthRevocationApplied && lifecycleStatus == string(domain.ApplicationLifecycleClosed) && lifecycleRevision == d.SourceLifecycleRevision+2 && d.AuthReceiptID != nil && *d.AuthReceiptID != "" && d.AuthAppliedAt != nil && !d.AuthAppliedAt.IsZero() && d.ClosedAt != nil && !d.ClosedAt.IsZero() && d.AuthAppliedAt.Equal(*d.ClosedAt) && d.NextAttemptAt == nil
	if !closing && !closed {
		return domain.ApplicationClosure{}, domain.ErrApplicationClosureStateInconsistent
	}
	value := domain.ApplicationClosure{ClosureID: id, ApplicationID: app, InitiatedBy: by, SourceOwnershipRevision: d.SourceOwnershipRevision, SourceLifecycleRevision: d.SourceLifecycleRevision, Status: status, HighRiskProofJTI: d.HighRiskProofJTI, ClosingStartedAt: d.ClosingStartedAt.UTC(), AuthRevocationState: revocation, LifecycleRevision: lifecycleRevision}
	if d.AuthReceiptID != nil {
		value.AuthReceiptID = *d.AuthReceiptID
	}
	value.AuthAppliedAt, value.ClosedAt = d.AuthAppliedAt, d.ClosedAt
	return value, nil
}

func (r *ApplicationClosureRepository) Preview(ctx context.Context, appID shared.ApplicationID, actor shared.AuthID, now time.Time) (domain.ApplicationClosurePreview, error) {
	var app applicationDocument
	err := r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": appID.String(), "adminId": actor.String()}).Decode(&app)
	if errors.Is(err, m.ErrNoDocuments) {
		return domain.ApplicationClosurePreview{}, domain.ErrApplicationNotFound
	}
	if err != nil {
		return domain.ApplicationClosurePreview{}, err
	}
	preview := domain.ApplicationClosurePreview{ApplicationID: appID, OwnershipRevision: app.OwnershipRevision, LifecycleRevision: app.LifecycleRevision, LifecycleStatus: domain.ApplicationLifecycleStatus(app.LifecycleStatus)}
	if preview.OwnershipRevision < 1 || preview.LifecycleRevision < 1 {
		return preview, domain.ErrApplicationClosureStateInconsistent
	}
	transferCount, err := r.database.Collection(applicationAdminTransfersCollectionName).CountDocuments(ctx, bson.M{"applicationId": appID.String(), "status": "PENDING"}, options.Count().SetLimit(1))
	if err != nil {
		return preview, err
	}
	preview.HasPendingAdminTransfer = transferCount != 0
	counts := []struct {
		collection string
		filter     any
		target     *int32
	}{
		{applicationTesterMembershipsCollectionName, bson.M{"applicationId": appID.String(), "status": "ACTIVE"}, &preview.ActiveTesterCount},
		{applicationReviewsCollectionName, bson.M{"applicationId": appID.String(), "status": "PENDING"}, &preview.PendingReviewCount},
	}
	for _, c := range counts {
		n, countErr := r.database.Collection(c.collection).CountDocuments(ctx, c.filter)
		if countErr != nil {
			return preview, countErr
		}
		*c.target = int32(n)
	}
	profilePending, err := r.database.Collection(applicationProfileReviewsCollectionName).CountDocuments(ctx, bson.M{"applicationId": appID.String(), "status": "PENDING"})
	if err != nil {
		return preview, err
	}
	preview.PendingReviewCount += int32(profilePending)
	cursor, err := r.database.Collection(applicationPublicationsCollectionName).Find(ctx, bson.M{"applicationId": appID.String()})
	if err != nil {
		return preview, err
	}
	var publications []applicationPublicationDocument
	if err = cursor.All(ctx, &publications); err != nil {
		return preview, err
	}
	for _, p := range publications {
		if p.TestVersionID != nil {
			preview.ActivePublicationChannelCount++
		}
		if p.StableVersionID != nil {
			preview.ActivePublicationChannelCount++
		}
		if p.GreyRollout != nil {
			preview.ActivePublicationChannelCount++
		}
	}
	var registrations []applicationOAuthRegistrationDocument
	cursor, err = r.database.Collection(applicationOAuthRegistrationsCollectionName).Find(ctx, bson.M{"applicationId": appID.String()})
	if err != nil {
		return preview, err
	}
	if err = cursor.All(ctx, &registrations); err != nil {
		return preview, err
	}
	for _, registration := range registrations {
		if registration.PublicClient != nil && registration.PublicClient.Status == "ACTIVE" {
			preview.EnabledOAuthClientCount++
		}
		if registration.ConfidentialClient != nil && registration.ConfidentialClient.Status == "ACTIVE" {
			preview.EnabledOAuthClientCount++
		}
	}
	return preview, nil
}

func (r *ApplicationClosureRepository) Get(ctx context.Context, appID shared.ApplicationID, actor shared.AuthID) (domain.ApplicationClosure, error) {
	var app applicationDocument
	if err := r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": appID.String(), "adminId": actor.String()}).Decode(&app); errors.Is(err, m.ErrNoDocuments) {
		return domain.ApplicationClosure{}, domain.ErrApplicationNotFound
	} else if err != nil {
		return domain.ApplicationClosure{}, err
	}
	var d applicationClosureDocument
	if err := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"applicationId": appID.String()}).Decode(&d); errors.Is(err, m.ErrNoDocuments) {
		return domain.ApplicationClosure{}, domain.ErrApplicationClosureNotFound
	} else if err != nil {
		return domain.ApplicationClosure{}, err
	}
	return closureFromDocument(d, app.LifecycleStatus, app.LifecycleRevision)
}

func (r *ApplicationClosureRepository) transaction(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	return session.WithTransaction(ctx, fn, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
}

func (r *ApplicationClosureRepository) Start(ctx context.Context, appID shared.ApplicationID, actor shared.AuthID, expectedOwnership, expectedLifecycle int64, proof domain.ApplicationCloseProof, closureID domain.ApplicationClosureID, now time.Time) (domain.ApplicationClosure, error) {
	value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		var existing applicationClosureDocument
		existingErr := r.database.Collection(applicationClosuresCollectionName).FindOne(tx, bson.M{"applicationId": appID.String()}).Decode(&existing)
		if existingErr == nil {
			if existing.InitiatedBy != actor.String() || existing.HighRiskProofJTI != proof.JTI {
				return nil, domain.ErrApplicationLifecycleChanged
			}
			var existingApp applicationDocument
			if err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String(), "adminId": actor.String()}).Decode(&existingApp); err != nil {
				return nil, err
			}
			return closureFromDocument(existing, existingApp.LifecycleStatus, existingApp.LifecycleRevision)
		}
		if !errors.Is(existingErr, m.ErrNoDocuments) {
			return nil, existingErr
		}
		existingErr = r.database.Collection(applicationClosuresCollectionName).FindOne(tx, bson.M{"highRiskProofJti": proof.JTI}).Decode(&existing)
		if existingErr == nil {
			return nil, domain.ErrHighRiskProofReplayed
		}
		if !errors.Is(existingErr, m.ErrNoDocuments) {
			return nil, existingErr
		}
		var pre applicationDocument
		if findErr := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String()}).Decode(&pre); errors.Is(findErr, m.ErrNoDocuments) {
			return nil, domain.ErrApplicationNotFound
		} else if findErr != nil {
			return nil, findErr
		}
		var pending applicationAdminTransferDocument
		transferErr := r.database.Collection(applicationAdminTransfersCollectionName).FindOne(tx, bson.M{"applicationId": appID.String(), "status": "PENDING"}).Decode(&pending)
		authIDs := []shared.AuthID{shared.AuthID(pre.AdminID)}
		if transferErr == nil {
			authIDs = append(authIDs, shared.AuthID(pending.ToAdminID))
		} else if !errors.Is(transferErr, m.ErrNoDocuments) {
			return nil, transferErr
		}
		for _, id := range sortedAuthIDs(authIDs...) {
			fence, lockErr := lockOwnerFence(tx, r.database, id.String())
			if lockErr != nil {
				return nil, lockErr
			}
			if id == actor && (fence.SealedPurpose != 0 || fence.PendingPurpose != 0) {
				return nil, shared.ErrAccountExitBlocked
			}
		}
		if err := lockApplicationFence(tx, r.database, appID.String()); err != nil {
			return nil, err
		}
		var app applicationDocument
		if err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": appID.String()}).Decode(&app); err != nil {
			return nil, err
		}
		if app.AdminID != actor.String() {
			return nil, domain.ErrApplicationAdminRequired
		}
		if app.OwnershipRevision != expectedOwnership {
			return nil, domain.ErrApplicationOwnershipChanged
		}
		if app.LifecycleRevision != expectedLifecycle {
			return nil, domain.ErrApplicationLifecycleChanged
		}
		if app.LifecycleStatus != string(domain.ApplicationLifecycleActive) {
			return nil, domain.ErrApplicationNotActive
		}
		quotaResult, err := r.database.Collection(applicationCreationQuotasCollectionName).UpdateOne(tx, bson.M{"adminId": actor.String(), "usedCount": bson.M{"$gt": int32(0)}}, bson.M{"$inc": bson.M{"usedCount": int32(-1), "revision": int64(1)}, "$set": bson.M{"updatedAt": now}})
		if err != nil {
			return nil, err
		}
		if quotaResult.MatchedCount != 1 {
			return nil, domain.ErrApplicationClosureStateInconsistent
		}
		resolvedBy, cause := actor.String(), string(domain.ApplicationAdminTransferResolutionApplicationClosure)
		if transferErr == nil {
			result, updateErr := r.database.Collection(applicationAdminTransfersCollectionName).UpdateOne(tx, bson.M{"transferId": pending.TransferID, "status": "PENDING"}, bson.M{"$set": bson.M{"status": "CANCELLED", "resolvedAt": now, "resolvedBy": resolvedBy, "resolutionCause": cause}})
			if updateErr != nil || result.MatchedCount != 1 {
				if updateErr != nil {
					return nil, updateErr
				}
				return nil, domain.ErrApplicationClosureStateInconsistent
			}
		}
		_, err = r.database.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(tx, bson.M{"applicationId": appID.String(), "status": "ACTIVE"}, bson.M{"$set": bson.M{"status": "REVOKED", "revokedBy": actor.String(), "revokedAt": now, "revocationReason": "APPLICATION_CLOSURE", "replacedByJoinLinkId": nil}})
		if err != nil {
			return nil, err
		}
		var registrations []applicationOAuthRegistrationDocument
		cursor, err := r.database.Collection(applicationOAuthRegistrationsCollectionName).Find(tx, bson.M{"applicationId": appID.String()})
		if err != nil {
			return nil, err
		}
		if err = cursor.All(tx, &registrations); err != nil {
			return nil, err
		}
		for _, registration := range registrations {
			changed := false
			for _, client := range []*oauthClientIdentityDocument{registration.PublicClient, registration.ConfidentialClient} {
				if client != nil && client.Status == "ACTIVE" {
					if client.AuthorizationEpoch == math.MaxInt64 {
						return nil, domain.ErrApplicationClosureStateInconsistent
					}
					client.Status = "DISABLED"
					client.AuthorizationEpoch++
					client.StatusUpdatedBy = actor.String()
					client.StatusUpdatedAt = now
					changed = true
				}
			}
			if changed {
				if registration.RegistrationRevision == math.MaxInt64 {
					return nil, domain.ErrApplicationClosureStateInconsistent
				}
				registration.RegistrationRevision++
				registration.UpdatedAt = now
				result, replaceErr := r.database.Collection(applicationOAuthRegistrationsCollectionName).ReplaceOne(tx, bson.M{"applicationId": registration.ApplicationID, "channel": registration.Channel}, registration)
				if replaceErr != nil || result.MatchedCount != 1 {
					if replaceErr != nil {
						return nil, replaceErr
					}
					return nil, domain.ErrApplicationClosureStateInconsistent
				}
			}
		}
		result, err := r.database.Collection(applicationsCollectionName).UpdateOne(tx, bson.M{"id": appID.String(), "adminId": actor.String(), "ownershipRevision": expectedOwnership, "lifecycleRevision": expectedLifecycle, "lifecycleStatus": "ACTIVE"}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSING"}, "$inc": bson.M{"lifecycleRevision": int64(1)}})
		if err != nil || result.MatchedCount != 1 {
			if err != nil {
				return nil, err
			}
			return nil, domain.ErrApplicationLifecycleChanged
		}
		next := now.Add(time.Second)
		d := applicationClosureDocument{ClosureID: closureID.String(), ApplicationID: appID.String(), InitiatedBy: actor.String(), SourceOwnershipRevision: expectedOwnership, SourceLifecycleRevision: expectedLifecycle, Status: "CLOSING", HighRiskProofJTI: proof.JTI, ClosingStartedAt: now, AuthRevocationState: "PENDING", NextAttemptAt: &next, Attempt: 0}
		if _, err = r.database.Collection(applicationClosuresCollectionName).InsertOne(tx, d); err != nil {
			if m.IsDuplicateKeyError(err) {
				return nil, domain.ErrHighRiskProofReplayed
			}
			return nil, err
		}
		return closureFromDocument(d, string(domain.ApplicationLifecycleClosing), expectedLifecycle+1)
	})
	if err != nil {
		if errors.Is(err, domain.ErrHighRiskProofReplayed) {
			var existing applicationClosureDocument
			lookupErr := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"highRiskProofJti": proof.JTI}).Decode(&existing)
			if lookupErr == nil {
				if existing.ApplicationID == appID.String() && existing.InitiatedBy == actor.String() && existing.HighRiskProofJTI == proof.JTI {
					var app applicationDocument
					if lookupErr = r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": appID.String(), "adminId": actor.String()}).Decode(&app); lookupErr == nil {
						return closureFromDocument(existing, app.LifecycleStatus, app.LifecycleRevision)
					}
					return domain.ApplicationClosure{}, lookupErr
				}
				return domain.ApplicationClosure{}, domain.ErrHighRiskProofReplayed
			}
			if !errors.Is(lookupErr, m.ErrNoDocuments) {
				return domain.ApplicationClosure{}, lookupErr
			}
			appErr := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"applicationId": appID.String()}).Err()
			if appErr == nil {
				return domain.ApplicationClosure{}, domain.ErrApplicationLifecycleChanged
			}
			if !errors.Is(appErr, m.ErrNoDocuments) {
				return domain.ApplicationClosure{}, appErr
			}
			return domain.ApplicationClosure{}, domain.ErrApplicationClosureStateInconsistent
		}
		if errors.Is(err, domain.ErrApplicationLifecycleChanged) {
			var existing applicationClosureDocument
			lookupErr := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"applicationId": appID.String()}).Decode(&existing)
			if lookupErr == nil && existing.InitiatedBy == actor.String() && existing.HighRiskProofJTI == proof.JTI {
				var app applicationDocument
				if lookupErr = r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": appID.String(), "adminId": actor.String()}).Decode(&app); lookupErr == nil {
					return closureFromDocument(existing, app.LifecycleStatus, app.LifecycleRevision)
				}
			}
		}
		return domain.ApplicationClosure{}, err
	}
	return value.(domain.ApplicationClosure), nil
}

func (r *ApplicationClosureRepository) NextPending(ctx context.Context, now time.Time) (domain.ApplicationClosure, error) {
	var d applicationClosureDocument
	err := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"authRevocationState": "PENDING", "nextAttemptAt": bson.M{"$lte": now}}, options.FindOne().SetSort(bson.D{{Key: "nextAttemptAt", Value: 1}, {Key: "closureId", Value: 1}})).Decode(&d)
	if errors.Is(err, m.ErrNoDocuments) {
		return domain.ApplicationClosure{}, domain.ErrApplicationClosureNotFound
	}
	if err != nil {
		return domain.ApplicationClosure{}, err
	}
	var app applicationDocument
	if err = r.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": d.ApplicationID}).Decode(&app); err != nil {
		return domain.ApplicationClosure{}, err
	}
	return closureFromDocument(d, app.LifecycleStatus, app.LifecycleRevision)
}

func (r *ApplicationClosureRepository) ScheduleRetry(ctx context.Context, id domain.ApplicationClosureID, at time.Time) error {
	var d applicationClosureDocument
	if err := r.database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"closureId": id.String(), "authRevocationState": "PENDING"}).Decode(&d); err != nil {
		return err
	}
	if d.Attempt == math.MaxInt64 {
		return domain.ErrApplicationClosureStateInconsistent
	}
	delay := time.Second << min(d.Attempt, 6)
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	next := at.Add(delay)
	result, err := r.database.Collection(applicationClosuresCollectionName).UpdateOne(ctx, bson.M{"closureId": id.String(), "authRevocationState": "PENDING", "attempt": d.Attempt}, bson.M{"$set": bson.M{"nextAttemptAt": next}, "$inc": bson.M{"attempt": int64(1)}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return domain.ErrApplicationClosureStateInconsistent
	}
	return nil
}

func (r *ApplicationClosureRepository) Complete(ctx context.Context, id domain.ApplicationClosureID, receipt string, appliedAt time.Time) (domain.ApplicationClosure, error) {
	if receipt == "" || appliedAt.IsZero() {
		return domain.ApplicationClosure{}, domain.ErrApplicationClosureStateInconsistent
	}
	value, err := r.transaction(ctx, func(tx context.Context) (any, error) {
		var d applicationClosureDocument
		if err := r.database.Collection(applicationClosuresCollectionName).FindOne(tx, bson.M{"closureId": id.String()}).Decode(&d); err != nil {
			return nil, err
		}
		var app applicationDocument
		if err := r.database.Collection(applicationsCollectionName).FindOne(tx, bson.M{"id": d.ApplicationID}).Decode(&app); err != nil {
			return nil, err
		}
		if d.Status == "CLOSED" {
			if d.AuthReceiptID == nil || *d.AuthReceiptID != receipt || d.AuthAppliedAt == nil || !d.AuthAppliedAt.Equal(appliedAt) || d.ClosedAt == nil || !d.ClosedAt.Equal(appliedAt) || app.LifecycleStatus != "CLOSED" || app.LifecycleRevision != d.SourceLifecycleRevision+2 {
				return nil, domain.ErrApplicationClosureStateInconsistent
			}
			return closureFromDocument(d, app.LifecycleStatus, app.LifecycleRevision)
		}
		if d.Status != "CLOSING" || d.AuthRevocationState != "PENDING" || app.ID != d.ApplicationID || app.LifecycleStatus != "CLOSING" || app.LifecycleRevision != d.SourceLifecycleRevision+1 {
			return nil, domain.ErrApplicationClosureStateInconsistent
		}
		if err := lockApplicationFence(tx, r.database, d.ApplicationID); err != nil {
			return nil, err
		}
		result, err := r.database.Collection(applicationsCollectionName).UpdateOne(tx, bson.M{"id": d.ApplicationID, "lifecycleStatus": "CLOSING"}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSED"}, "$inc": bson.M{"lifecycleRevision": int64(1)}})
		if err != nil || result.MatchedCount != 1 {
			if err != nil {
				return nil, err
			}
			return nil, domain.ErrApplicationClosureStateInconsistent
		}
		closedAt := appliedAt.UTC()
		result, err = r.database.Collection(applicationClosuresCollectionName).UpdateOne(tx, bson.M{"closureId": id.String(), "status": "CLOSING", "authRevocationState": "PENDING"}, bson.M{"$set": bson.M{"status": "CLOSED", "authRevocationState": "APPLIED", "authReceiptId": receipt, "authAppliedAt": closedAt, "closedAt": closedAt, "nextAttemptAt": nil}})
		if err != nil || result.MatchedCount != 1 {
			if err != nil {
				return nil, err
			}
			return nil, domain.ErrApplicationClosureStateInconsistent
		}
		d.Status, d.AuthRevocationState, d.AuthReceiptID, d.AuthAppliedAt, d.ClosedAt, d.NextAttemptAt = "CLOSED", "APPLIED", &receipt, &closedAt, &closedAt, nil
		return closureFromDocument(d, string(domain.ApplicationLifecycleClosed), app.LifecycleRevision+1)
	})
	if err != nil {
		return domain.ApplicationClosure{}, fmt.Errorf("complete application closure: %w", err)
	}
	return value.(domain.ApplicationClosure), nil
}
