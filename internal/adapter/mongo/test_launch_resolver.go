package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

// TestLaunchResolver combines existing facts in one read-only snapshot. Unlike
// write commands it deliberately acquires no coordination fence: a successful
// descriptor describes its snapshot, not a lease on future membership or slots.
type TestLaunchResolver struct{ database *drivermongo.Database }

func NewTestLaunchResolver(database *drivermongo.Database) *TestLaunchResolver {
	return &TestLaunchResolver{database: database}
}

var _ catalogport.TestLaunchResolver = (*TestLaunchResolver)(nil)

func (r *TestLaunchResolver) ResolveForTester(ctx context.Context, appID shared.ApplicationID, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName) (*catalogdomain.TestLaunchDescriptor, error) {
	if r == nil || r.database == nil || !appID.IsValid() || !authID.IsValid() || major < 1 {
		return nil, fmt.Errorf("resolve test launch: invalid repository input")
	}
	session, err := r.database.Client().StartSession()
	if err != nil {
		return nil, safeTestLaunchError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return r.resolveSnapshot(tx, appID, authID, major, host)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, safeTestLaunchError(err)
	}
	return result.(*catalogdomain.TestLaunchDescriptor), nil
}

func (r *TestLaunchResolver) resolveSnapshot(ctx context.Context, appID shared.ApplicationID, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName) (*catalogdomain.TestLaunchDescriptor, error) {
	// Authorization precedes every publication/version read, including failures.
	gate, err := readApplicationAvailabilityGate(ctx, r.database, appID.String())
	if err != nil {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	if gate != applicationGateAvailable {
		return nil, catalogport.ErrApplicationNotFound
	}
	err = r.database.Collection(applicationTesterMembershipsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "testerAuthId", Value: authID.String()}, {Key: "status", Value: "ACTIVE"}}, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, catalogport.ErrApplicationTesterRequired
	}
	if err != nil {
		return nil, err
	}
	var publication applicationPublicationDocument
	err = decodeTestLaunchDocument(r.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "rpcApiMajor", Value: major}}), &publication)
	if errors.Is(err, drivermongo.ErrNoDocuments) || (err == nil && publication.TestVersionID == nil) {
		return nil, catalogport.ErrApplicationTestTargetUnavailable
	}
	if err != nil {
		return nil, err
	}
	if _, err := applicationPublicationFromDocument(publication); err != nil {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	var history applicationPublicationHistoryDocument
	err = decodeTestLaunchDocument(r.database.Collection(applicationPublicationHistoryCollectionName).FindOne(ctx, bson.D{{Key: "publicationId", Value: publication.PublicationID}, {Key: "publicationRevision", Value: bson.D{{Key: "$lte", Value: publication.Revision}}}, {Key: "action", Value: "SET_TEST_VERSION"}, {Key: "newVersionId", Value: *publication.TestVersionID}}, options.FindOne().SetSort(bson.D{{Key: "publicationRevision", Value: -1}})), &history)
	if err != nil {
		return nil, missingTestLaunchFact(err)
	}
	if history.ApplicationID != appID.String() || history.RPCAPIMajor != major || history.NewVersionID == nil || *history.NewVersionID != *publication.TestVersionID || history.Action != "SET_TEST_VERSION" || history.ApprovedReviewID == nil || !shared.IsUUIDv7(*history.ApprovedReviewID) {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	var version applicationVersionDocument
	err = decodeTestLaunchDocument(r.database.Collection(applicationVersionsCollectionName).FindOne(ctx, bson.D{{Key: "versionId", Value: *publication.TestVersionID}, {Key: "applicationId", Value: appID.String()}}), &version)
	if err != nil {
		return nil, missingTestLaunchFact(err)
	}
	if _, err := applicationVersionFromDocument(version); err != nil || version.ReviewStatus != "APPROVED" || major < version.RPCApiMinVersion || major >= version.RPCApiMaxVersionExclusive {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	var review applicationReviewDocument
	err = decodeTestLaunchDocument(r.database.Collection(applicationReviewsCollectionName).FindOne(ctx, bson.D{{Key: "applicationId", Value: appID.String()}, {Key: "versionId", Value: version.VersionID}}, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})), &review)
	if err != nil {
		return nil, missingTestLaunchFact(err)
	}
	approved, err := applicationReviewFromDocument(review)
	if err != nil || review.ReviewID != *history.ApprovedReviewID || review.Status != "APPROVED" || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || version.Revision != review.SourceVersionRevision+2 {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	_, _, snapshot, err := applicationVersionDocumentToDecisionVersion(version)
	if err != nil || !snapshot.Equal(approved.Snapshot()) || version.UpdatedBy != review.Decision.DecidedBy || !version.UpdatedAt.Equal(review.Decision.DecidedAt) {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	required := make([]catalogdomain.CapabilityName, len(version.RequiredCapabilities))
	for i, name := range version.RequiredCapabilities {
		required[i] = catalogdomain.CapabilityName(name)
	}
	if missing := catalogdomain.MissingCapabilities(required, host); len(missing) != 0 {
		return nil, catalogdomain.NewHostCapabilitiesInsufficientError(missing)
	}
	descriptor, err := catalogdomain.NewTestLaunchDescriptor(appID, publication.PublicationID, publication.Revision, major, version.VersionID, version.VersionLabel, version.LaunchURL, version.RPCApiMinVersion, version.RPCApiMaxVersionExclusive, required, version.RequiredScopes, version.OptionalScopes)
	if err != nil {
		return nil, catalogport.ErrApplicationTestPublicationInconsistent
	}
	return descriptor, nil
}

// Decode errors describe corrupt stored facts, while operation errors retain
// their driver labels until WithTransaction finishes its retry decisions.
func decodeTestLaunchDocument(result *drivermongo.SingleResult, target any) error {
	raw, err := result.Raw()
	if err != nil {
		return err
	}
	if err := bson.Unmarshal(raw, target); err != nil {
		return catalogport.ErrApplicationTestPublicationInconsistent
	}
	return nil
}

func missingTestLaunchFact(err error) error {
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return catalogport.ErrApplicationTestPublicationInconsistent
	}
	return err
}

func safeTestLaunchError(err error) error {
	for _, business := range []error{catalogport.ErrApplicationNotFound, catalogport.ErrApplicationTesterRequired, catalogport.ErrApplicationTestTargetUnavailable, catalogport.ErrApplicationTestPublicationInconsistent, catalogdomain.ErrHostCapabilitiesInsufficient} {
		if errors.Is(err, business) {
			return err
		}
	}
	// Database error messages may contain arbitrary persisted user content.
	class := "storage failure"
	if errors.Is(err, context.Canceled) {
		class = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline exceeded"
	}
	return fmt.Errorf("test launch persistence failed: %s", class)
}
