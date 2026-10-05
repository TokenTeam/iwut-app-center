package mongo

import (
	"context"
	"crypto/subtle"
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

type ApplicationTesterMembershipRepository struct{ database *drivermongo.Database }

func NewApplicationTesterMembershipRepository(database *drivermongo.Database) *ApplicationTesterMembershipRepository {
	return &ApplicationTesterMembershipRepository{database: database}
}

func (r *ApplicationTesterMembershipRepository) ResolveJoinCandidate(ctx context.Context, linkID testerdomain.ApplicationTesterJoinLinkID, hash [32]byte) (*testerdomain.TesterJoinCandidate, error) {
	if r == nil || r.database == nil || !linkID.IsValid() {
		return nil, fmt.Errorf("resolve tester join: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		link, err := r.validJoinLink(tx, linkID, hash)
		if err != nil {
			return nil, err
		}
		err = r.database.Collection(applicationsCollectionName).FindOne(tx, bson.D{{Key: "id", Value: link.ApplicationID().String()}}).Err()
		if errors.Is(err, drivermongo.ErrNoDocuments) {
			return nil, testerport.ErrTesterJoinLinkInvalid
		}
		if err != nil {
			return nil, err
		}
		return testerdomain.NewTesterJoinCandidate(link.ApplicationID())
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	return result.(*testerdomain.TesterJoinCandidate), nil
}

// Resolve is preliminary only: every Join obtains the same Application write
// fence as link rotation before reading any eligibility or capacity facts.
func (r *ApplicationTesterMembershipRepository) Join(ctx context.Context, linkID testerdomain.ApplicationTesterJoinLinkID, hash [32]byte, authID shared.AuthID, candidate *testerdomain.ApplicationTesterMembership, limit int32) (*testerdomain.JoinApplicationAsTesterResult, error) {
	if r == nil || r.database == nil || !linkID.IsValid() || !authID.IsValid() || candidate == nil || candidate.TesterAuthID() != authID || candidate.JoinedViaJoinLinkID() != linkID || candidate.Status() != testerdomain.MembershipStatusActive || limit != testerdomain.ApplicationTesterLimit {
		return nil, fmt.Errorf("join tester: invalid repository input")
	}
	document := testerMembershipToDocument(candidate)
	document.JoinedAt = document.JoinedAt.UTC().Truncate(time.Millisecond)
	persisted, err := testerMembershipFromDocument(document)
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.joinTesterTransaction(tx, linkID, hash, authID, persisted, limit)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTesterMembershipError(err)
	}
	return result.(*testerdomain.JoinApplicationAsTesterResult), nil
}

func (r *ApplicationTesterMembershipRepository) joinTesterTransaction(ctx context.Context, linkID testerdomain.ApplicationTesterJoinLinkID, hash [32]byte, authID shared.AuthID, candidate *testerdomain.ApplicationTesterMembership, limit int32) (*testerdomain.JoinApplicationAsTesterResult, error) {
	if err := requireOwnerWritable(ctx, r.database, authID.String(), true); err != nil {
		return nil, err
	}
	appID := candidate.ApplicationID()
	err := r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, bson.D{{Key: "id", Value: appID.String()}}, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Err()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrTesterJoinLinkInvalid
	}
	if err != nil {
		return nil, err
	}
	link, err := r.validJoinLink(ctx, linkID, hash)
	if err != nil {
		return nil, err
	}
	if link.ApplicationID() != appID {
		return nil, testerport.ErrTesterJoinLinkInvalid
	}
	collection := r.database.Collection(applicationTesterMembershipsCollectionName)
	var existing applicationTesterMembershipDocument
	err = collection.FindOne(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "testerAuthId", Value: authID.String()}, {Key: "status", Value: "ACTIVE"}}).Decode(&existing)
	found := err == nil
	if err != nil && !errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, err
	}
	// The Application fence serializes every membership writer and rotation, so
	// this snapshot count and insert are one atomic capacity decision. There is
	// no separate counter to drift or repair.
	count, err := collection.CountDocuments(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "status", Value: "ACTIVE"}})
	if err != nil {
		return nil, err
	}
	if count > int64(limit) {
		return nil, fmt.Errorf("tester membership capacity invariant failed")
	}
	if found {
		membership, err := testerMembershipFromDocument(existing)
		if err != nil {
			return nil, err
		}
		return testerdomain.NewJoinApplicationAsTesterResult(membership, false, int32(count), limit)
	}
	if count >= int64(limit) {
		return nil, testerport.ErrApplicationTesterLimitReached
	}
	if _, err = collection.InsertOne(ctx, testerMembershipToDocument(candidate)); err != nil {
		return nil, err
	}
	return testerdomain.NewJoinApplicationAsTesterResult(candidate, true, int32(count+1), limit)
}

func (r *ApplicationTesterMembershipRepository) validJoinLink(ctx context.Context, id testerdomain.ApplicationTesterJoinLinkID, hash [32]byte) (*testerdomain.ApplicationTesterJoinLink, error) {
	var document applicationTesterJoinLinkDocument
	err := r.database.Collection(applicationTesterJoinLinksCollectionName).FindOne(ctx, bson.D{{Key: "joinLinkId", Value: id.String()}}).Decode(&document)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrTesterJoinLinkInvalid
	}
	if err != nil {
		return nil, err
	}
	link, err := testerJoinLinkFromDocument(document)
	if err != nil {
		return nil, err
	}
	expected := link.TokenHash().Bytes()
	matches := subtle.ConstantTimeCompare(expected[:], hash[:])
	if matches != 1 || link.Status() != testerdomain.JoinLinkStatusActive {
		return nil, testerport.ErrTesterJoinLinkInvalid
	}
	return link, nil
}

// Errors never retain driver causes: validation or indexed values may include
// confidential hashes or other users' identities. Business sentinels remain
// stable while all unexpected persistence details are deliberately discarded.
func safeTesterMembershipError(err error) error {
	if errors.Is(err, shared.ErrAccountExitBlocked) {
		return shared.ErrAccountExitBlocked
	}
	if errors.Is(err, testerdomain.ErrApplicationTesterStateInconsistent) {
		return testerport.ErrApplicationTesterStateInconsistent
	}
	for _, business := range []error{testerport.ErrTesterJoinLinkInvalid, testerport.ErrApplicationTesterLimitReached, testerport.ErrApplicationTesterMembershipNotFound, testerport.ErrApplicationAdminRequired, testerport.ErrApplicationTesterStateInconsistent} {
		if errors.Is(err, business) {
			return business
		}
	}
	class := "storage failure"
	switch {
	case drivermongo.IsDuplicateKeyError(err):
		class = "unique constraint"
	case errors.Is(err, context.Canceled):
		class = "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		class = "deadline exceeded"
	default:
		var serverError drivermongo.ServerError
		if errors.As(err, &serverError) && serverError.HasErrorCode(121) {
			class = "document validation"
		}
	}
	return fmt.Errorf("tester membership persistence failed: %s", class)
}
