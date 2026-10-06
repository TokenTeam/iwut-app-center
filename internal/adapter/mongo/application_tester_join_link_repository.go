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

type ApplicationTesterJoinLinkRepository struct{ database *drivermongo.Database }

func NewApplicationTesterJoinLinkRepository(database *drivermongo.Database) *ApplicationTesterJoinLinkRepository {
	return &ApplicationTesterJoinLinkRepository{database}
}

func (r *ApplicationTesterJoinLinkRepository) LoadCurrent(ctx context.Context, applicationID shared.ApplicationID, adminID shared.AuthID) (*testerdomain.TesterJoinLinkCandidate, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !adminID.IsValid() {
		return nil, fmt.Errorf("load tester join link: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.loadCurrentTesterJoinLink(tx, applicationID, adminID, false)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	return result.(*testerdomain.TesterJoinLinkCandidate), nil
}

func (r *ApplicationTesterJoinLinkRepository) loadCurrentTesterJoinLink(ctx context.Context, applicationID shared.ApplicationID, adminID shared.AuthID, lock bool) (*testerdomain.TesterJoinLinkCandidate, error) {
	var app applicationDocument
	filter := bson.D{{Key: "id", Value: applicationID.String()}, {Key: "lifecycleStatus", Value: "ACTIVE"}}
	var err error
	if lock {
		// This real write conflicts with administrator transfers even when reads use
		// an older transaction snapshot. It changes no Application business fields.
		err = r.database.Collection(applicationsCollectionName).FindOneAndUpdate(ctx, filter, bson.D{{Key: "$inc", Value: bson.D{{Key: "coordinationRevision", Value: int64(1)}}}}).Decode(&app)
	} else {
		err = r.database.Collection(applicationsCollectionName).FindOne(ctx, filter).Decode(&app)
	}
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, testerport.ErrApplicationNotFound
	}
	if err != nil {
		return nil, err
	}
	if app.AdminID != adminID.String() {
		return nil, testerport.ErrApplicationAdminRequired
	}
	var document applicationTesterJoinLinkDocument
	err = r.database.Collection(applicationTesterJoinLinksCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: applicationID.String()}, {Key: "status", Value: "ACTIVE"}}).Decode(&document)
	var active *testerdomain.ApplicationTesterJoinLink
	if err == nil {
		active, err = testerJoinLinkFromDocument(document)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, err
	}
	return testerdomain.NewTesterJoinLinkCandidate(applicationID, active)
}

func (r *ApplicationTesterJoinLinkRepository) CreateOrRotate(ctx context.Context, applicationID shared.ApplicationID, adminID shared.AuthID, expected *testerdomain.ApplicationTesterJoinLinkID, newLink *testerdomain.ApplicationTesterJoinLink) (*testerdomain.CreateOrRotateTesterJoinLinkResult, error) {
	if r == nil || r.database == nil || !applicationID.IsValid() || !adminID.IsValid() || newLink == nil || newLink.ApplicationID() != applicationID || newLink.CreatedBy() != adminID || string(newLink.Status()) != "ACTIVE" || (expected != nil && !expected.IsValid()) {
		return nil, fmt.Errorf("create tester join link: invalid repository input")
	}
	// MongoDB timestamps have millisecond precision. Reconstructing the new
	// immutable entity here keeps returned audit values identical to persistence.
	document := testerJoinLinkToDocument(newLink)
	document.CreatedAt = document.CreatedAt.UTC().Truncate(time.Millisecond)
	persistedLink, err := testerJoinLinkFromDocument(document)
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.createOrRotateTesterJoinLinkTransaction(tx, applicationID, adminID, expected, persistedLink)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTesterJoinLinkError(err)
	}
	return result.(*testerdomain.CreateOrRotateTesterJoinLinkResult), nil
}

func (r *ApplicationTesterJoinLinkRepository) createOrRotateTesterJoinLinkTransaction(ctx context.Context, applicationID shared.ApplicationID, adminID shared.AuthID, expected *testerdomain.ApplicationTesterJoinLinkID, newLink *testerdomain.ApplicationTesterJoinLink) (*testerdomain.CreateOrRotateTesterJoinLinkResult, error) {
	candidate, err := r.loadCurrentTesterJoinLink(ctx, applicationID, adminID, true)
	if err != nil {
		return nil, err
	}
	if err = candidate.EnsureExpected(expected); err != nil {
		return nil, mapTesterJoinLinkExpectationError(err)
	}
	var replaced *testerdomain.ApplicationTesterJoinLinkID
	if active := candidate.ActiveLink(); active != nil {
		revoked, err := active.Rotate(newLink.JoinLinkID(), adminID, newLink.CreatedAt())
		if err != nil {
			return nil, err
		}
		old := testerJoinLinkToDocument(revoked)
		// Only revocation fields are mutable. Neither hash nor original creation
		// audit is present in the update, and a revoked row is never selected again.
		update := bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: old.Status}, {Key: "revokedBy", Value: old.RevokedBy}, {Key: "revokedAt", Value: old.RevokedAt}, {Key: "revocationReason", Value: old.RevocationReason}, {Key: "replacedByJoinLinkId", Value: old.ReplacedByJoinLinkID}}}}
		outcome, err := r.database.Collection(applicationTesterJoinLinksCollectionName).UpdateOne(ctx, bson.D{{Key: "joinLinkId", Value: active.JoinLinkID().String()}, {Key: "applicationId", Value: applicationID.String()}, {Key: "status", Value: "ACTIVE"}}, update)
		if err != nil {
			return nil, err
		}
		if outcome.MatchedCount != 1 {
			return nil, testerport.ErrApplicationTesterJoinLinkChanged
		}
		id := active.JoinLinkID()
		replaced = &id
	}
	if _, err = r.database.Collection(applicationTesterJoinLinksCollectionName).InsertOne(ctx, testerJoinLinkToDocument(newLink)); err != nil {
		return nil, err
	}
	return testerdomain.NewCreateOrRotateTesterJoinLinkResult(newLink, replaced)
}

func mapTesterJoinLinkExpectationError(err error) error {
	switch {
	case errors.Is(err, testerdomain.ErrApplicationTesterJoinLinkAlreadyExists):
		return testerport.ErrApplicationTesterJoinLinkAlreadyExists
	case errors.Is(err, testerdomain.ErrApplicationTesterJoinLinkNotFound):
		return testerport.ErrApplicationTesterJoinLinkNotFound
	case errors.Is(err, testerdomain.ErrApplicationTesterJoinLinkChanged):
		return testerport.ErrApplicationTesterJoinLinkChanged
	default:
		return err
	}
}

// Driver duplicate-key errors may embed the indexed hash. Public adapter errors
// use a constant description so ordinary logging never prints credential hashes.
type testerJoinLinkPersistenceError struct{ class string }

func (e *testerJoinLinkPersistenceError) Error() string {
	return "tester join link persistence failed: " + e.class
}
func safeTesterJoinLinkError(err error) error {
	if errors.Is(err, testerdomain.ErrApplicationTesterJoinLinkStateInconsistent) {
		return testerport.ErrApplicationTesterJoinLinkStateInconsistent
	}
	for _, business := range []error{testerport.ErrApplicationNotFound, testerport.ErrApplicationAdminRequired, testerport.ErrApplicationTesterJoinLinkAlreadyExists, testerport.ErrApplicationTesterJoinLinkNotFound, testerport.ErrApplicationTesterJoinLinkChanged, testerport.ErrApplicationTesterJoinLinkStateInconsistent} {
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
	// Do not retain the driver cause: duplicate key details can contain tokenHash.
	return &testerJoinLinkPersistenceError{class: class}
}
