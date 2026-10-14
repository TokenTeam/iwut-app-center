package mongo

import (
	"context"
	"errors"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	"iwut-app-center/internal/shared"
	td "iwut-app-center/internal/tester/domain"
	tp "iwut-app-center/internal/tester/port"
)

var _ tp.ApplicationTesterExitRepository = (*ApplicationTesterMembershipRepository)(nil)

func (r *ApplicationTesterMembershipRepository) LoadExitCandidate(ctx context.Context, app shared.ApplicationID, id td.ApplicationTesterMembershipID, caller shared.AuthID) (*td.ApplicationTesterMembership, error) {
	value, err := r.exitTransaction(ctx, func(tx context.Context) (any, error) { return r.loadExit(tx, app, id, caller) })
	if err != nil {
		return nil, err
	}
	return value.(*td.ApplicationTesterMembership), nil
}
func (r *ApplicationTesterMembershipRepository) Leave(ctx context.Context, app shared.ApplicationID, id td.ApplicationTesterMembershipID, caller shared.AuthID, at time.Time) (*td.LeaveApplicationAsTesterResult, error) {
	value, err := r.exitTransaction(ctx, func(tx context.Context) (any, error) {
		return r.leaveTesterTransaction(tx, app, id, caller, at)
	})
	if err != nil {
		return nil, err
	}
	return value.(*td.LeaveApplicationAsTesterResult), nil
}
func (r *ApplicationTesterMembershipRepository) leaveTesterTransaction(tx context.Context, app shared.ApplicationID, id td.ApplicationTesterMembershipID, caller shared.AuthID, at time.Time) (*td.LeaveApplicationAsTesterResult, error) {
	membership, err := r.loadExit(tx, app, id, caller)
	if err != nil {
		return nil, err
	}
	if membership.Status() == td.MembershipStatusRemoved {
		return td.NewLeaveApplicationAsTesterResult(membership, false)
	}
	at = at.UTC().Truncate(time.Millisecond)
	if !td.ExitTimeValid(at, membership.JoinedAt()) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	removed, err := membership.Remove(caller, at)
	if err != nil {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	result, err := r.database.Collection(applicationTesterMembershipsCollectionName).UpdateOne(tx, bson.M{"applicationId": app.String(), "membershipId": id.String(), "testerAuthId": caller.String(), "status": "ACTIVE"}, bson.M{"$set": bson.M{"status": "REMOVED", "removedBy": caller.String(), "removedAt": at}})
	if err != nil {
		return nil, err
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	return td.NewLeaveApplicationAsTesterResult(removed, true)
}
func (r *ApplicationTesterMembershipRepository) exitTransaction(ctx context.Context, fn func(context.Context) (any, error)) (any, error) {
	if r == nil || r.database == nil {
		return nil, tp.ErrApplicationTesterExitUnavailable
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, tp.ErrApplicationTesterExitUnavailable
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, fn, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if errors.Is(err, tp.ErrApplicationTesterMembershipNotFound) {
		return nil, tp.ErrApplicationTesterMembershipNotFound
	}
	if errors.Is(err, tp.ErrApplicationTesterStateInconsistent) || errors.Is(err, td.ErrApplicationTesterStateInconsistent) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	if err != nil {
		return nil, tp.ErrApplicationTesterExitUnavailable
	}
	return result, nil
}

// Authorize on the owned episode before reading any necessary source. The App
// fence uses the same order as existing writers, without acquiring an account
// fence: this operation can only reduce already-existing personal state.
func (r *ApplicationTesterMembershipRepository) loadExit(ctx context.Context, app shared.ApplicationID, id td.ApplicationTesterMembershipID, caller shared.AuthID) (*td.ApplicationTesterMembership, error) {
	memberships := r.database.Collection(applicationTesterMembershipsCollectionName)
	rows, err := r.exitRows(ctx, applicationTesterMembershipsCollectionName, bson.M{"applicationId": app.String(), "membershipId": id.String(), "testerAuthId": caller.String()}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, tp.ErrApplicationTesterMembershipNotFound
	}
	if len(rows) != 1 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	raw := rows[0]
	if !exitShape(raw, testerMembershipManagementShape, map[string]bson.Type{"removedBy": bson.TypeString, "removedAt": bson.TypeDateTime}) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	var doc applicationTesterMembershipDocument
	if bson.Unmarshal(raw, &doc) != nil || doc.ApplicationID != app.String() || doc.MembershipID != id.String() || doc.TesterAuthID != caller.String() || !canonicalTesterManagementID(doc.MembershipID) || !canonicalTesterManagementID(doc.ApplicationID) || !canonicalTesterManagementID(doc.JoinedViaJoinLinkID) || !testerManagementTime(doc.JoinedAt) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	membership, err := testerMembershipFromDocument(doc)
	if err != nil || membership.RemovedAt() != nil && !td.ExitTimeValid(*membership.RemovedAt(), membership.JoinedAt()) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	// Reject colliding global IDs only after establishing caller ownership.
	if err = r.exitUnique(ctx, applicationTesterMembershipsCollectionName, bson.M{"membershipId": id.String()}); err != nil {
		return nil, err
	}
	roots, err := r.exitRows(ctx, applicationsCollectionName, bson.M{"id": app.String()}, nil)
	if err != nil {
		return nil, err
	}
	if len(roots) != 1 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	root := roots[0]
	if !exitShape(root, map[string]bson.Type{"id": bson.TypeString, "name": bson.TypeString, "adminId": bson.TypeString, "createdAt": bson.TypeDateTime, "lifecycleStatus": bson.TypeString, "platformAvailabilityStatus": bson.TypeString, "archiveStatus": bson.TypeString, "ownershipRevision": bson.TypeInt64, "lifecycleRevision": bson.TypeInt64, "platformAvailabilityRevision": bson.TypeInt64, "archiveRevision": bson.TypeInt64, "coordinationRevision": bson.TypeInt64}, nil) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	var application applicationDocument
	if bson.Unmarshal(root, &application) != nil || application.ID != app.String() || application.CoordinationRevision < 0 || application.CoordinationRevision == math.MaxInt64 || !testerManagementTime(application.CreatedAt) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	if _, err = managementCore(application); err != nil {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	fenced, err := r.database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.M{"id": app.String(), "coordinationRevision": application.CoordinationRevision}, bson.M{"$inc": bson.M{"coordinationRevision": int64(1)}})
	if err != nil {
		return nil, err
	}
	if fenced.MatchedCount != 1 || fenced.ModifiedCount != 1 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	// Metadata-only source projection deliberately never reads tokenHash.
	links, err := r.exitRows(ctx, applicationTesterJoinLinksCollectionName, bson.M{"joinLinkId": doc.JoinedViaJoinLinkID}, bson.M{"tokenHash": 0})
	if err != nil {
		return nil, err
	}
	if len(links) != 1 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	linkRaw := links[0]
	if !exitShape(linkRaw, map[string]bson.Type{"joinLinkId": bson.TypeString, "applicationId": bson.TypeString, "status": bson.TypeString, "createdBy": bson.TypeString, "createdAt": bson.TypeDateTime}, map[string]bson.Type{"revokedBy": bson.TypeString, "revokedAt": bson.TypeDateTime, "revocationReason": bson.TypeString, "replacedByJoinLinkId": bson.TypeString}) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	var linkDoc applicationTesterJoinLinkDocument
	if bson.Unmarshal(linkRaw, &linkDoc) != nil || linkDoc.ApplicationID != app.String() || !canonicalTesterManagementID(linkDoc.JoinLinkID) || !testerManagementTime(linkDoc.CreatedAt) || doc.JoinedAt.Before(linkDoc.CreatedAt) || linkDoc.RevokedAt != nil && (!td.ExitTimeValid(*linkDoc.RevokedAt, linkDoc.CreatedAt) || doc.JoinedAt.After(*linkDoc.RevokedAt)) || linkDoc.ReplacedByJoinLinkID != nil && !canonicalTesterManagementID(*linkDoc.ReplacedByJoinLinkID) {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	// Re-enter the existing domain's metadata invariants without using credentials.
	// Restore validates audit/status only; the placeholder is never persisted/used.
	linkDoc.TokenHash = bson.Binary{Subtype: 0, Data: make([]byte, 32)}
	if _, err = testerJoinLinkFromDocument(linkDoc); err != nil {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	ownActive, err := memberships.CountDocuments(ctx, bson.M{"applicationId": app.String(), "testerAuthId": caller.String(), "status": "ACTIVE"}, options.Count().SetLimit(2))
	if err != nil {
		return nil, err
	}
	if ownActive > 1 || membership.Status() == td.MembershipStatusActive && ownActive != 1 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	count, err := memberships.CountDocuments(ctx, bson.M{"applicationId": app.String(), "status": "ACTIVE"}, options.Count().SetLimit(101))
	if err != nil {
		return nil, err
	}
	if count < 0 || count > 100 || membership.Status() == td.MembershipStatusActive && count == 0 {
		return nil, tp.ErrApplicationTesterStateInconsistent
	}
	return membership, nil
}
func exitShape(raw bson.Raw, required, nullable map[string]bson.Type) bool {
	if !profileManagementRawValid(raw, false) {
		return false
	}
	for key, kind := range required {
		if raw.Lookup(key).Type != kind {
			return false
		}
	}
	for key, kind := range nullable {
		if v := raw.Lookup(key); v.Type != kind && v.Type != bson.TypeNull {
			return false
		}
	}
	return true
}
func (r *ApplicationTesterMembershipRepository) exitRows(ctx context.Context, collection string, filter, projection bson.M) ([]bson.Raw, error) {
	opts := options.Find().SetLimit(2)
	if projection != nil {
		opts.SetProjection(projection)
	}
	cursor, err := r.database.Collection(collection).Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	var rows []bson.Raw
	for cursor.Next(ctx) {
		rows = append(rows, append(bson.Raw(nil), cursor.Current...))
	}
	return rows, cursor.Err()
}
func (r *ApplicationTesterMembershipRepository) exitUnique(ctx context.Context, collection string, filter bson.M) error {
	count, err := r.database.Collection(collection).CountDocuments(ctx, filter, options.Count().SetLimit(2))
	if err != nil {
		return err
	}
	if count != 1 {
		return tp.ErrApplicationTesterStateInconsistent
	}
	return nil
}
