package mongo

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

// TestLaunchResolver retains UC012's single-target RPC semantics and delegates
// pure Test fact loading/resolution to the same batch core as UC038.
type TestLaunchResolver struct{ database *m.Database }

func NewTestLaunchResolver(db *m.Database) *TestLaunchResolver { return &TestLaunchResolver{db} }

var _ cp.TestLaunchResolver = (*TestLaunchResolver)(nil)

func (r *TestLaunchResolver) ResolveForTester(ctx context.Context, app shared.ApplicationID, caller shared.AuthID, major int32, host []cd.CapabilityName) (*cd.TestLaunchDescriptor, error) {
	if r == nil || r.database == nil || !app.IsValid() || !caller.IsValid() || major < 1 {
		return nil, fmt.Errorf("resolve test launch: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTestLaunchError(err)
	}
	defer session.EndSession(ctx)
	value, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) { return r.resolveSnapshot(tx, app, caller, major, host) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.Majority()))
	if err != nil {
		return nil, safeTestLaunchError(err)
	}
	return value.(*cd.TestLaunchDescriptor), nil
}
func (r *TestLaunchResolver) resolveSnapshot(ctx context.Context, app shared.ApplicationID, caller shared.AuthID, major int32, host []cd.CapabilityName) (*cd.TestLaunchDescriptor, error) {
	gate, err := readApplicationAvailabilityGate(ctx, r.database, app.String())
	if err != nil {
		return nil, cp.ErrApplicationTestPublicationInconsistent
	}
	if gate != applicationGateAvailable {
		return nil, cp.ErrApplicationNotFound
	}
	err = r.database.Collection(applicationTesterMembershipsCollectionName).FindOne(ctx, bson.M{"applicationId": app.String(), "testerAuthId": caller.String(), "status": "ACTIVE"}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, m.ErrNoDocuments) {
		return nil, cp.ErrApplicationTesterRequired
	}
	if err != nil {
		return nil, err
	}
	facts, err := loadPureTestFacts(ctx, r.database, []string{app.String()}, major)
	if errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
		return nil, cp.ErrApplicationTestPublicationInconsistent
	}
	if err != nil {
		return nil, err
	}
	target, missing, err := resolvePureTest(app, major, host, facts)
	if err != nil {
		return nil, cp.ErrApplicationTestPublicationInconsistent
	}
	if len(missing) > 0 {
		return nil, cd.NewHostCapabilitiesInsufficientError(missing)
	}
	if target == nil {
		return nil, cp.ErrApplicationTestTargetUnavailable
	}
	return target, nil
}
func safeTestLaunchError(err error) error {
	for _, known := range []error{cp.ErrApplicationNotFound, cp.ErrApplicationTesterRequired, cp.ErrApplicationTestTargetUnavailable, cp.ErrApplicationTestPublicationInconsistent, cd.ErrHostCapabilitiesInsufficient} {
		if errors.Is(err, known) {
			return err
		}
	}
	class := "storage failure"
	if errors.Is(err, context.Canceled) {
		class = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline exceeded"
	}
	return fmt.Errorf("test launch persistence failed: %s", class)
}
