package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

func (r *ApplicationTesterJoinLinkRepository) LoadRevocationCandidate(ctx context.Context, appID shared.ApplicationID, linkID testerdomain.ApplicationTesterJoinLinkID, adminID shared.AuthID) (*testerdomain.TesterJoinLinkRevocationCandidate, error) {
	if r == nil || r.database == nil || !appID.IsValid() || !linkID.IsValid() || !adminID.IsValid() {
		return nil, fmt.Errorf("load tester join link revocation: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	defer session.EndSession(ctx)
	// A REVOKED candidate can finish the command without reading Clock. It must
	// therefore acquire the same write fence as the final transition so an old
	// administrator cannot return an idempotent success after a transfer.
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadTesterRevocationCandidate(tx, appID, linkID, adminID)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	return result.(*testerdomain.TesterJoinLinkRevocationCandidate), nil
}

func (r *ApplicationTesterJoinLinkRepository) Revoke(ctx context.Context, appID shared.ApplicationID, linkID testerdomain.ApplicationTesterJoinLinkID, adminID shared.AuthID, revokedAt time.Time) (*testerdomain.RevokeTesterJoinLinkResult, error) {
	if r == nil || r.database == nil || !appID.IsValid() || !linkID.IsValid() || !adminID.IsValid() {
		return nil, fmt.Errorf("revoke tester join link: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.revokeTesterJoinLinkTransaction(tx, appID, linkID, adminID, revokedAt.UTC().Truncate(time.Millisecond))
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	return result.(*testerdomain.RevokeTesterJoinLinkResult), nil
}

func (r *ApplicationTesterJoinLinkRepository) loadTesterRevocationCandidate(ctx context.Context, appID shared.ApplicationID, linkID testerdomain.ApplicationTesterJoinLinkID, adminID shared.AuthID) (*testerdomain.TesterJoinLinkRevocationCandidate, error) {
	var app applicationDocument
	err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx,
		bson.D{{Key: "id", Value: appID.String()}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}},
	).Decode(&app)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrApplicationTesterJoinLinkNotFound
	}
	if err != nil {
		return nil, err
	}
	if app.AdminID != adminID.String() {
		return nil, testerport.ErrApplicationAdminRequired
	}
	stored := r.database.Collection(applicationTesterJoinLinksCollectionName).FindOne(ctx,
		bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "joinLinkId", Value: linkID.String()}},
	)
	err = stored.Err()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrApplicationTesterJoinLinkNotFound
	}
	if err != nil {
		return nil, err
	}
	var document applicationTesterJoinLinkDocument
	if err := stored.Decode(&document); err != nil {
		// A successful read with an invalid BSON shape is a stored invariant
		// failure, distinct from an unavailable database. Discard decode details.
		return nil, testerport.ErrApplicationTesterJoinLinkStateInconsistent
	}
	link, err := testerJoinLinkFromDocument(document)
	if err != nil {
		return nil, testerport.ErrApplicationTesterJoinLinkStateInconsistent
	}
	return testerdomain.NewTesterJoinLinkRevocationCandidate(link)
}

func (r *ApplicationTesterJoinLinkRepository) revokeTesterJoinLinkTransaction(ctx context.Context, appID shared.ApplicationID, linkID testerdomain.ApplicationTesterJoinLinkID, adminID shared.AuthID, revokedAt time.Time) (*testerdomain.RevokeTesterJoinLinkResult, error) {
	candidate, err := r.loadTesterRevocationCandidate(ctx, appID, linkID, adminID)
	if err != nil {
		return nil, err
	}
	link := candidate.JoinLink()
	if link.Status() == testerdomain.JoinLinkStatusRevoked {
		return testerdomain.NewRevokeTesterJoinLinkResult(link, false)
	}
	revoked, err := link.Revoke(adminID, revokedAt)
	if err != nil {
		return nil, err
	}
	result, err := r.database.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(ctx,
		bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "joinLinkId", Value: linkID.String()}, {Key: "status", Value: "ACTIVE"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REVOKED"}, {Key: "revokedBy", Value: adminID.String()}, {Key: "revokedAt", Value: revokedAt}, {Key: "revocationReason", Value: "MANUAL"}, {Key: "replacedByJoinLinkId", Value: nil}}}},
	)
	if err != nil {
		return nil, err
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return nil, testerport.ErrApplicationTesterJoinLinkStateInconsistent
	}
	// Neither credential/creation audit nor Membership is touched. The shared
	// Application fence orders this terminal transition with joins and rotation.
	return testerdomain.NewRevokeTesterJoinLinkResult(revoked, true)
}
