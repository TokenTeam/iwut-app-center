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
	testerdomain "iwut-app-center/internal/tester/domain"
)

// UnifiedLaunchResolver composes existing Application, Membership,
// Publication, Version and Review facts in one read-only MongoDB snapshot.
type UnifiedLaunchResolver struct{ database *drivermongo.Database }

func NewUnifiedLaunchResolver(database *drivermongo.Database) *UnifiedLaunchResolver {
	return &UnifiedLaunchResolver{database: database}
}

var _ catalogport.LaunchTargetResolver = (*UnifiedLaunchResolver)(nil)

func (resolver *UnifiedLaunchResolver) Resolve(ctx context.Context, applicationID shared.ApplicationID, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName) (*catalogdomain.LaunchTargetDescriptor, error) {
	if resolver == nil || resolver.database == nil || !applicationID.IsValid() || major < 1 {
		return nil, fmt.Errorf("resolve unified launch target: invalid repository input")
	}
	session, err := resolver.database.Client().StartSession()
	if err != nil {
		return nil, safeUnifiedLaunchError(err)
	}
	defer session.EndSession(ctx)
	result, err := session.WithTransaction(ctx, func(tx context.Context) (any, error) {
		return resolver.resolveSnapshot(tx, applicationID, authID, major, host)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		return nil, safeUnifiedLaunchError(err)
	}
	descriptor, ok := result.(*catalogdomain.LaunchTargetDescriptor)
	if !ok || descriptor == nil {
		return nil, fmt.Errorf("unified launch persistence failed: invalid result")
	}
	return descriptor, nil
}

func (resolver *UnifiedLaunchResolver) resolveSnapshot(ctx context.Context, applicationID shared.ApplicationID, authID shared.AuthID, major int32, host []catalogdomain.CapabilityName) (*catalogdomain.LaunchTargetDescriptor, error) {
	err := resolver.database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": applicationID.String(), "lifecycleStatus": "ACTIVE"}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, catalogport.ErrApplicationNotFound
	}
	if err != nil {
		return nil, err
	}

	activeTester := false
	if authID.IsValid() {
		var membership applicationTesterMembershipDocument
		err = decodeUnifiedLaunchDocument(resolver.database.Collection(applicationTesterMembershipsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "testerAuthId": authID.String(), "status": "ACTIVE"}), &membership)
		switch {
		case errors.Is(err, drivermongo.ErrNoDocuments):
		case err != nil:
			return nil, err
		default:
			restored, restoreErr := testerMembershipFromDocument(membership)
			if restoreErr != nil || restored.ApplicationID() != applicationID || restored.TesterAuthID() != authID || restored.Status() != testerdomain.MembershipStatusActive {
				return nil, catalogport.ErrApplicationRuntimeStateInconsistent
			}
			activeTester = true
		}
	}

	var publicationDocument applicationPublicationDocument
	findPublication := options.FindOne()
	if !activeTester {
		findPublication.SetProjection(bson.M{"testVersionId": 0})
	}
	err = decodeUnifiedLaunchDocument(resolver.database.Collection(applicationPublicationsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "rpcApiMajor": major}, findPublication), &publicationDocument)
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return nil, catalogport.ErrApplicationLaunchTargetUnavailable
	}
	if err != nil {
		return nil, err
	}

	// The query projection omits testVersionId for callers without an ACTIVE
	// Tester episode, so malformed Test-only state cannot affect their result.
	publication, err := applicationPublicationFromDocument(publicationDocument)
	if err != nil || publication.ApplicationID() != applicationID || publication.RPCAPIMajor() != major {
		return nil, catalogport.ErrApplicationRuntimeStateInconsistent
	}

	if activeTester && publicationDocument.TestVersionID != nil {
		descriptor, compatible, candidateErr := resolver.resolveCandidate(ctx, applicationID, publicationDocument, *publicationDocument.TestVersionID, major, catalogdomain.LaunchChannelTest, []string{"SET_TEST_VERSION"}, host)
		if candidateErr != nil {
			return nil, candidateErr
		}
		if compatible {
			return descriptor, nil
		}
	}

	if authID.IsValid() {
		if rollout := publication.GreyRollout(); rollout != nil && rollout.Matches(authID) {
			descriptor, compatible, candidateErr := resolver.resolveCandidate(ctx, applicationID, publicationDocument, rollout.VersionID().String(), major, catalogdomain.LaunchChannelGrey, []string{"SET_GREY_ROLLOUT", "INCREASE_GREY_EXPOSURE", "REPLACE_GREY_VERSION"}, host)
			if candidateErr != nil {
				return nil, candidateErr
			}
			if compatible {
				return descriptor, nil
			}
		}
	}

	if stable := publication.StableVersionIDPtr(); stable != nil {
		descriptor, compatible, candidateErr := resolver.resolveCandidate(ctx, applicationID, publicationDocument, stable.String(), major, catalogdomain.LaunchChannelStable, []string{"SET_STABLE_VERSION"}, host)
		if candidateErr != nil {
			return nil, candidateErr
		}
		if compatible {
			return descriptor, nil
		}
	}
	return nil, catalogport.ErrApplicationLaunchTargetUnavailable
}

func (resolver *UnifiedLaunchResolver) resolveCandidate(ctx context.Context, applicationID shared.ApplicationID, publication applicationPublicationDocument, versionID string, major int32, channel catalogdomain.LaunchChannel, actions []string, host []catalogdomain.CapabilityName) (*catalogdomain.LaunchTargetDescriptor, bool, error) {
	var history applicationPublicationHistoryDocument
	err := decodeUnifiedLaunchDocument(resolver.database.Collection(applicationPublicationHistoryCollectionName).FindOne(ctx, bson.M{
		"publicationId": publication.PublicationID, "publicationRevision": bson.M{"$lte": publication.Revision},
		"action": bson.M{"$in": actions}, "newVersionId": versionID,
	}, options.FindOne().SetSort(bson.D{{Key: "publicationRevision", Value: -1}})), &history)
	if err != nil {
		return nil, false, missingUnifiedLaunchFact(err)
	}
	if history.ApplicationID != applicationID.String() || history.RPCAPIMajor != major || history.NewVersionID == nil || *history.NewVersionID != versionID || history.ApprovedReviewID == nil || !shared.IsUUIDv7(*history.ApprovedReviewID) || !containsString(actions, history.Action) {
		return nil, false, catalogport.ErrApplicationRuntimeStateInconsistent
	}

	var version applicationVersionDocument
	err = decodeUnifiedLaunchDocument(resolver.database.Collection(applicationVersionsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "versionId": versionID}), &version)
	if err != nil {
		return nil, false, missingUnifiedLaunchFact(err)
	}
	var oauthConfig applicationVersionOAuthConfigDocument
	err = decodeUnifiedLaunchDocument(resolver.database.Collection(applicationVersionOAuthConfigsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "applicationVersionId": versionID}), &oauthConfig)
	if err != nil {
		return nil, false, missingUnifiedLaunchFact(err)
	}
	if _, restoreErr := applicationVersionFromDocument(version, oauthConfig); restoreErr != nil || version.ReviewStatus != "APPROVED" || major < version.RPCApiMinVersion || major >= version.RPCApiMaxVersionExclusive {
		return nil, false, catalogport.ErrApplicationRuntimeStateInconsistent
	}

	var review applicationReviewDocument
	err = decodeUnifiedLaunchDocument(resolver.database.Collection(applicationReviewsCollectionName).FindOne(ctx, bson.M{"applicationId": applicationID.String(), "versionId": versionID}, options.FindOne().SetSort(bson.D{{Key: "attempt", Value: -1}})), &review)
	if err != nil {
		return nil, false, missingUnifiedLaunchFact(err)
	}
	approved, restoreErr := applicationReviewFromDocument(review)
	if restoreErr != nil || review.ReviewID != *history.ApprovedReviewID || review.Status != "APPROVED" || review.Decision == nil || review.Decision.Outcome != "APPROVED" || review.SourceVersionRevision > math.MaxInt64-2 || version.Revision != review.SourceVersionRevision+2 {
		return nil, false, catalogport.ErrApplicationRuntimeStateInconsistent
	}
	_, _, snapshot, restoreErr := applicationVersionDocumentToDecisionVersion(version, oauthConfig)
	if restoreErr != nil || !snapshot.Equal(approved.Snapshot()) || version.UpdatedBy != review.Decision.DecidedBy || !version.UpdatedAt.Equal(review.Decision.DecidedAt) {
		return nil, false, catalogport.ErrApplicationRuntimeStateInconsistent
	}

	required := make([]catalogdomain.CapabilityName, len(version.RequiredCapabilities))
	for index, name := range version.RequiredCapabilities {
		required[index] = catalogdomain.CapabilityName(name)
	}
	if len(catalogdomain.MissingCapabilities(required, host)) != 0 {
		return nil, false, nil
	}
	descriptor, createErr := catalogdomain.NewLaunchTargetDescriptor(applicationID, publication.PublicationID, publication.Revision, channel, major, version.VersionID, version.VersionLabel, version.LaunchURL, version.RPCApiMinVersion, version.RPCApiMaxVersionExclusive, required, version.RequiredScopes, version.OptionalScopes)
	if createErr != nil {
		return nil, false, catalogport.ErrApplicationRuntimeStateInconsistent
	}
	return descriptor, true, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func decodeUnifiedLaunchDocument(result *drivermongo.SingleResult, target any) error {
	raw, err := result.Raw()
	if err != nil {
		return err
	}
	if err := bson.Unmarshal(raw, target); err != nil {
		return catalogport.ErrApplicationRuntimeStateInconsistent
	}
	return nil
}

func missingUnifiedLaunchFact(err error) error {
	if errors.Is(err, drivermongo.ErrNoDocuments) {
		return catalogport.ErrApplicationRuntimeStateInconsistent
	}
	return err
}

func safeUnifiedLaunchError(err error) error {
	for _, business := range []error{catalogport.ErrApplicationNotFound, catalogport.ErrApplicationLaunchTargetUnavailable, catalogport.ErrApplicationRuntimeStateInconsistent} {
		if errors.Is(err, business) {
			return err
		}
	}
	class := "storage failure"
	if errors.Is(err, context.Canceled) {
		class = "canceled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		class = "deadline exceeded"
	}
	return fmt.Errorf("unified launch persistence failed: %s", class)
}
