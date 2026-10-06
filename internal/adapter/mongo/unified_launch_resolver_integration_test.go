package mongo

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	"iwut-app-center/internal/application/domain"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
	publicationdomain "iwut-app-center/internal/publication/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
)

type unifiedLaunchFixture struct {
	db                                *drivermongo.Database
	testSeed, stableSeed, greySeed    decidableReview
	testAuthID, matchedAuthID, missed shared.AuthID
	publication                       *publicationdomain.ApplicationPublication
}

func addUnifiedApprovedVersion(t *testing.T, database *drivermongo.Database, seed decidableReview, label string, capabilities []string) decidableReview {
	t.Helper()
	version, err := NewApplicationVersionRepository(database).CreateDraft(t.Context(), seed.adminID, integrationApplicationVersionDraftWithSets(t, seed.applicationID, seed.adminID.String(), label, capabilities, []string{"profile.basic"}, []string{"schedule.read"}))
	if err != nil {
		t.Fatal(err)
	}
	seed.versionID = reviewdomain.ApplicationVersionID(version.ID())
	seed.reviewID = nextIntegrationApplicationReviewID(t)
	repository := NewApplicationReviewRepository(database)
	candidate := loadReviewCandidate(t, repository, seed.applicationID, seed.versionID, seed.adminID.String(), 1)
	if _, err = repository.Submit(t.Context(), candidate, seed.reviewID, seed.adminID, 41, "public-https.v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	approvePublicationVersion(t, database, seed)
	return seed
}

func newUnifiedLaunchFixture(t *testing.T, client *drivermongo.Client) unifiedLaunchFixture {
	t.Helper()
	database := migratedIntegrationDatabase(t, client)
	testSeed := createApprovedPublicationVersion(t, database, "unified-launch")
	joinLink := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(database), testSeed.applicationID, testSeed.adminID, nil, newIntegrationTesterJoinLink(t, testSeed.applicationID, testSeed.adminID)).JoinLink()
	testAuthID := shared.AuthID("unified-test-user")
	joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(database), joinLink, newIntegrationTesterMembership(t, joinLink, testAuthID))

	repository := NewApplicationPublicationRepository(database)
	publication := placePublication(t, repository, loadPublicationCandidate(t, repository, testSeed, 1, nil), testSeed).Publication()
	stableSeed := addUnifiedApprovedVersion(t, database, testSeed, "unified-stable", []string{"stable.host.v1"})
	expected := publication.Revision()
	stable, err := repository.LoadStablePlacementCandidate(t.Context(), testSeed.applicationID, 1, publicationdomain.ApplicationVersionID(stableSeed.versionID), testSeed.adminID, &expected)
	if err != nil {
		t.Fatal(err)
	}
	stableResult, err := repository.SetStable(t.Context(), stable, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), testSeed.adminID, publicationTestValidation(), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	greySeed := addUnifiedApprovedVersion(t, database, testSeed, "unified-grey", []string{"grey.host.v1"})
	exposure, _ := publicationdomain.NewExposureBasisPoints(5000)
	grey, err := repository.LoadGreyPlacementCandidate(t.Context(), testSeed.applicationID, 1, publicationdomain.ApplicationVersionID(greySeed.versionID), exposure, testSeed.adminID, stableResult.Publication().Revision())
	if err != nil {
		t.Fatal(err)
	}
	seedBytes := make([]byte, 32)
	for index := range seedBytes {
		seedBytes[index] = byte(index)
	}
	cohortSeed, _ := publicationdomain.NewCohortSeed(seedBytes)
	rolloutID := publicationdomain.GreyRolloutID(nextIntegrationApplicationReviewID(t))
	validation := publicationTestValidation()
	greyResult, err := repository.SetGrey(t.Context(), grey, &rolloutID, &cohortSeed, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), testSeed.adminID, &validation, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rollout := greyResult.Publication().GreyRollout()
	var matched, missed shared.AuthID
	for index := 0; !matched.IsValid() || !missed.IsValid(); index++ {
		candidate := shared.AuthID(fmt.Sprintf("unified-grey-user-%d", index))
		if rollout.Matches(candidate) && !matched.IsValid() {
			matched = candidate
		}
		if !rollout.Matches(candidate) && !missed.IsValid() {
			missed = candidate
		}
	}
	joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(database), joinLink, newIntegrationTesterMembership(t, joinLink, matched))
	return unifiedLaunchFixture{database, testSeed, stableSeed, greySeed, testAuthID, matched, missed, greyResult.Publication()}
}

func (fixture unifiedLaunchFixture) resolve(t *testing.T, resolver *UnifiedLaunchResolver, authID shared.AuthID, capabilities ...string) (*catalogdomain.LaunchTargetDescriptor, error) {
	t.Helper()
	host := make([]catalogdomain.CapabilityName, len(capabilities))
	for index, capability := range capabilities {
		host[index] = catalogdomain.CapabilityName(capability)
	}
	return resolver.Resolve(t.Context(), fixture.testSeed.applicationID, authID, 1, host)
}

func TestUnifiedLaunchResolverIntegration_UCAPP023_SelectionFallbackAndPrivacy(t *testing.T) {
	fixture := newUnifiedLaunchFixture(t, integrationClient(t))
	resolver := NewUnifiedLaunchResolver(fixture.db)
	for _, test := range []struct {
		name         string
		authID       shared.AuthID
		capabilities []string
		channel      catalogdomain.LaunchChannel
		versionID    string
	}{
		{"BR-RUN-013 tester prefers Test", fixture.testAuthID, []string{"camera.read.v1", "grey.host.v1", "stable.host.v1"}, catalogdomain.LaunchChannelTest, fixture.testSeed.versionID.String()},
		{"BR-RUN-014 tester capability fallback reaches matched Grey", fixture.matchedAuthID, []string{"grey.host.v1", "stable.host.v1"}, catalogdomain.LaunchChannelGrey, fixture.greySeed.versionID.String()},
		{"BR-RUN-014 matched non-tester uses Grey", findMatchedNonTester(fixture), []string{"grey.host.v1", "stable.host.v1"}, catalogdomain.LaunchChannelGrey, fixture.greySeed.versionID.String()},
		{"BR-RUN-015 missed user uses Stable", fixture.missed, []string{"stable.host.v1"}, catalogdomain.LaunchChannelStable, fixture.stableSeed.versionID.String()},
		{"BR-RUN-011 anonymous uses Stable", "", []string{"stable.host.v1"}, catalogdomain.LaunchChannelStable, fixture.stableSeed.versionID.String()},
		{"BR-RUN-016 Grey capability fallback reaches Stable", findMatchedNonTester(fixture), []string{"stable.host.v1"}, catalogdomain.LaunchChannelStable, fixture.stableSeed.versionID.String()},
	} {
		t.Run(test.name, func(t *testing.T) {
			descriptor, err := fixture.resolve(t, resolver, test.authID, test.capabilities...)
			if err != nil || descriptor.Channel() != test.channel || descriptor.VersionID() != test.versionID || descriptor.PublicationRevision() != fixture.publication.Revision() {
				t.Fatalf("descriptor=%#v error=%v", descriptor, err)
			}
		})
	}

	if descriptor, err := fixture.resolve(t, resolver, fixture.matchedAuthID); descriptor != nil || !errors.Is(err, catalogport.ErrApplicationLaunchTargetUnavailable) {
		t.Fatalf("missing all capabilities descriptor=%#v error=%v", descriptor, err)
	}
	if descriptor, err := resolver.Resolve(t.Context(), fixture.testSeed.applicationID, "", 2, []catalogdomain.CapabilityName{"stable.host.v1"}); descriptor != nil || !errors.Is(err, catalogport.ErrApplicationLaunchTargetUnavailable) {
		t.Fatalf("exact-major fallback descriptor=%#v error=%v", descriptor, err)
	}

	t.Run("BR-RUN-018 BR-RUN-019 read-only consistent snapshot", func(t *testing.T) {
		monitored, trace := testLaunchMonitoredClient(t, fixture.db.Name(), "")
		before := readTestLaunchFacts(t, fixture.db)
		descriptor, err := NewUnifiedLaunchResolver(monitored.Database(fixture.db.Name())).Resolve(t.Context(), fixture.testSeed.applicationID, "", 1, []catalogdomain.CapabilityName{"stable.host.v1"})
		if err != nil || descriptor.Channel() != catalogdomain.LaunchChannelStable {
			t.Fatalf("snapshot descriptor=%#v error=%v", descriptor, err)
		}
		if after := readTestLaunchFacts(t, fixture.db); !reflect.DeepEqual(before, after) {
			t.Fatal("resolver changed persisted facts")
		}
		trace.assertReadOnlySnapshot(t)
	})

	setTestLaunchFields(t, fixture.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "testVersionId", Value: []int32{1, 2, 3}}})
	if descriptor, err := fixture.resolve(t, resolver, "", "stable.host.v1"); err != nil || descriptor.Channel() != catalogdomain.LaunchChannelStable {
		t.Fatalf("anonymous observed private Test corruption: %#v %v", descriptor, err)
	}
	if descriptor, err := fixture.resolve(t, resolver, fixture.testAuthID, "camera.read.v1", "stable.host.v1"); descriptor != nil || !errors.Is(err, catalogport.ErrApplicationRuntimeStateInconsistent) {
		t.Fatalf("authorized Test corruption descriptor=%#v error=%v", descriptor, err)
	}
}

func TestUnifiedLaunchResolverIntegration_UCAPP028_SuspendedApplicationIsUnavailable(t *testing.T) {
	fixture := newUnifiedLaunchFixture(t, integrationClient(t))
	eventID := domain.ApplicationOperationEventID("0199b33c-d040-7abc-8abc-123456789012")
	_, err := NewApplicationOperationsRepository(fixture.db).Set(t.Context(), fixture.testSeed.applicationID, "platform-operator", domain.PlatformAvailabilitySuspended, 1, 1, "temporary suspension", eventID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := fixture.resolve(t, NewUnifiedLaunchResolver(fixture.db), fixture.testAuthID, "camera.read.v1", "stable.host.v1")
	if descriptor != nil || !errors.Is(err, catalogport.ErrApplicationNotFound) {
		t.Fatalf("descriptor=%#v error=%v", descriptor, err)
	}
}

func findMatchedNonTester(fixture unifiedLaunchFixture) shared.AuthID {
	rollout := fixture.publication.GreyRollout()
	for index := 1000; ; index++ {
		candidate := shared.AuthID(fmt.Sprintf("unified-grey-nontester-%d", index))
		if rollout.Matches(candidate) {
			return candidate
		}
	}
}

func TestUnifiedLaunchResolverIntegration_UCAPP023_InvariantsFailClosed(t *testing.T) {
	fixture := newUnifiedLaunchFixture(t, integrationClient(t))
	resolver := NewUnifiedLaunchResolver(fixture.db)
	for _, test := range []struct {
		name, collection, field string
		filter                  bson.D
		value, restore          any
	}{
		{"BR-RUN-016 dangling Stable", applicationPublicationsCollectionName, "stableVersionId", bson.D{{Key: "applicationId", Value: fixture.testSeed.applicationID.String()}}, "019543b0-0000-7000-8000-000000000099", fixture.stableSeed.versionID.String()},
		{"BR-RUN-016 snapshot drift", applicationVersionsCollectionName, "launchUrl", bson.D{{Key: "versionId", Value: fixture.stableSeed.versionID.String()}}, "https://private.example/drift", "https://example.edu/apps/unified-stable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			setTestLaunchFields(t, fixture.db, test.collection, test.filter, bson.D{{Key: test.field, Value: test.value}})
			descriptor, err := fixture.resolve(t, resolver, "", "stable.host.v1")
			if descriptor != nil || !errors.Is(err, catalogport.ErrApplicationRuntimeStateInconsistent) || strings.Contains(fmt.Sprint(err), "private.example") || strings.Contains(fmt.Sprint(err), fixture.matchedAuthID.String()) || strings.Contains(fmt.Sprint(err), fixture.greySeed.reviewID.String()) {
				t.Fatalf("descriptor=%#v error=%v", descriptor, err)
			}
			setTestLaunchFields(t, fixture.db, test.collection, test.filter, bson.D{{Key: test.field, Value: test.restore}})
		})
	}
	setTestLaunchFields(t, fixture.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "stableVersionId", Value: nil}})
	if descriptor, err := fixture.resolve(t, resolver, "", "stable.host.v1"); descriptor != nil || !errors.Is(err, catalogport.ErrApplicationRuntimeStateInconsistent) {
		t.Fatalf("Grey without Stable descriptor=%#v error=%v", descriptor, err)
	}
}
