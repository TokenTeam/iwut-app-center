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

func (r *ApplicationTesterMembershipRepository) LoadRemovalCandidate(ctx context.Context, appID shared.ApplicationID, membershipID testerdomain.ApplicationTesterMembershipID, adminID shared.AuthID) (*testerdomain.TesterRemovalCandidate, error) {
	if r == nil || r.database == nil || !appID.IsValid() || !membershipID.IsValid() || !adminID.IsValid() {
		return nil, fmt.Errorf("load tester removal: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	defer session.EndSession(ctx)
	// A REMOVED candidate may be returned directly without calling Remove, so
	// this snapshot must also serialize with administrator transfers and joins.
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadTesterRemovalCandidate(tx, appID, membershipID, adminID)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	return result.(*testerdomain.TesterRemovalCandidate), nil
}

func (r *ApplicationTesterMembershipRepository) Remove(ctx context.Context, appID shared.ApplicationID, membershipID testerdomain.ApplicationTesterMembershipID, adminID shared.AuthID, removedAt time.Time) (*testerdomain.RemoveApplicationTesterResult, error) {
	if r == nil || r.database == nil || !appID.IsValid() || !membershipID.IsValid() || !adminID.IsValid() {
		return nil, fmt.Errorf("remove tester: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.removeTesterTransaction(tx, appID, membershipID, adminID, removedAt.UTC().Truncate(time.Millisecond))
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	return result.(*testerdomain.RemoveApplicationTesterResult), nil
}

func (r *ApplicationTesterMembershipRepository) loadTesterRemovalCandidate(ctx context.Context, appID shared.ApplicationID, membershipID testerdomain.ApplicationTesterMembershipID, adminID shared.AuthID) (*testerdomain.TesterRemovalCandidate, error) {
	var app applicationDocument
	err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx,
		bson.D{{Key: "id", Value: appID.String()}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}},
	).Decode(&app)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrApplicationTesterMembershipNotFound
	}
	if err != nil {
		return nil, err
	}
	if app.AdminID != adminID.String() {
		return nil, testerport.ErrApplicationAdminRequired
	}
	collection := r.database.Collection(applicationTesterMembershipsCollectionName)
	var document applicationTesterMembershipDocument
	err = collection.FindOne(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "membershipId", Value: membershipID.String()}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrApplicationTesterMembershipNotFound
	}
	if err != nil {
		return nil, err
	}
	membership, err := testerMembershipFromDocument(document)
	if err != nil {
		return nil, testerport.ErrApplicationTesterStateInconsistent
	}
	count, err := collection.CountDocuments(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "status", Value: "ACTIVE"}})
	if err != nil {
		return nil, err
	}
	if count < 0 || count > int64(testerdomain.ApplicationTesterLimit) || (membership.Status() == testerdomain.MembershipStatusActive && count == 0) {
		return nil, testerport.ErrApplicationTesterStateInconsistent
	}
	// The hint does not read or expose credentials, and cannot prevent removal
	// when no ACTIVE link exists. All link writers share the Application fence.
	err = r.database.Collection(applicationTesterJoinLinksCollectionName).FindOne(ctx,
		bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "status", Value: "ACTIVE"}},
		options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}}),
	).Err()
	activeLink := err == nil
	if err != nil && !errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, err
	}
	return testerdomain.NewTesterRemovalCandidate(membership, int32(count), activeLink)
}

func (r *ApplicationTesterMembershipRepository) removeTesterTransaction(ctx context.Context, appID shared.ApplicationID, membershipID testerdomain.ApplicationTesterMembershipID, adminID shared.AuthID, removedAt time.Time) (*testerdomain.RemoveApplicationTesterResult, error) {
	candidate, err := r.loadTesterRemovalCandidate(ctx, appID, membershipID, adminID)
	if err != nil {
		return nil, err
	}
	membership := candidate.Membership()
	count := candidate.ActiveTesterCount()
	if membership.Status() == testerdomain.MembershipStatusRemoved {
		return testerdomain.NewRemoveApplicationTesterResult(membership, false, count, testerdomain.ApplicationTesterLimit, candidate.ActiveJoinLinkExists())
	}
	removed, err := membership.Remove(adminID, removedAt)
	if err != nil {
		return nil, err
	}
	result, err := r.database.Collection(applicationTesterMembershipsCollectionName).UpdateOne(ctx,
		bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "membershipId", Value: membershipID.String()}, {Key: "status", Value: "ACTIVE"}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REMOVED"}, {Key: "removedBy", Value: adminID.String()}, {Key: "removedAt", Value: removedAt}}}},
	)
	if err != nil {
		return nil, err
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return nil, testerport.ErrApplicationTesterStateInconsistent
	}
	// No persisted counter exists: the atomic ACTIVE -> REMOVED transition
	// releases exactly one slot in the count protected by this same fence.
	return testerdomain.NewRemoveApplicationTesterResult(removed, true, count-1, testerdomain.ApplicationTesterLimit, candidate.ActiveJoinLinkExists())
}
