package mongo

import (
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	applicationdomain "iwut-app-center/internal/application/domain"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	oauthport "iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	testerport "iwut-app-center/internal/tester/port"
)

func archiveIntegrationApplication(t *testing.T, db *drivermongo.Database, appID shared.ApplicationID, admin shared.AuthID) {
	t.Helper()
	_, err := NewApplicationArchivalRepository(db).SetArchiveState(t.Context(), appID, admin, applicationdomain.ApplicationArchiveArchived, 1, func() (applicationdomain.ApplicationArchiveEventID, time.Time, error) {
		return "0199b33c-d049-7abc-8abc-123456789012", time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplicationArchivalConsumersIntegration_BR_APP_053_CatalogResolversAndJoinFailClosed(t *testing.T) {
	t.Run("TEST descriptor", func(t *testing.T) {
		fixture := newTestLaunchFixture(t, integrationClient(t))
		archiveIntegrationApplication(t, fixture.db, fixture.seed.applicationID, fixture.seed.adminID)
		value, err := fixture.resolve(t, NewTestLaunchResolver(fixture.db), fixture.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"})
		if value != nil || !errors.Is(err, catalogport.ErrApplicationNotFound) {
			t.Fatalf("descriptor=%#v error=%v", value, err)
		}
	})

	t.Run("unified resolver", func(t *testing.T) {
		fixture := newUnifiedLaunchFixture(t, integrationClient(t))
		archiveIntegrationApplication(t, fixture.db, fixture.testSeed.applicationID, fixture.testSeed.adminID)
		value, err := fixture.resolve(t, NewUnifiedLaunchResolver(fixture.db), fixture.testAuthID, "camera.read.v1", "stable.host.v1")
		if value != nil || !errors.Is(err, catalogport.ErrApplicationNotFound) {
			t.Fatalf("descriptor=%#v error=%v", value, err)
		}
	})

	t.Run("public catalog", func(t *testing.T) {
		fixture := newUnifiedLaunchFixture(t, integrationClient(t))
		archiveIntegrationApplication(t, fixture.db, fixture.testSeed.applicationID, fixture.testSeed.adminID)
		repository := NewPublicCatalogRepository(fixture.db)
		page, err := repository.ListPublic(t.Context(), "", 1, []catalogdomain.CapabilityName{"stable.host.v1"}, 20, "")
		if err != nil || len(page.Items()) != 0 {
			t.Fatalf("page=%#v error=%v", page, err)
		}
		item, err := repository.GetPublic(t.Context(), fixture.testSeed.applicationID, "", 1, []catalogdomain.CapabilityName{"stable.host.v1"})
		if item != nil || !errors.Is(err, catalogport.ErrPublicApplicationNotFound) {
			t.Fatalf("item=%#v error=%v", item, err)
		}
	})

	t.Run("tester join", func(t *testing.T) {
		db, link := testerMembershipFixture(t, integrationClient(t), "tester-archived")
		archiveIntegrationApplication(t, db, link.ApplicationID(), "tester-admin")
		candidate := newIntegrationTesterMembership(t, link, "new-archived-tester")
		result, err := NewApplicationTesterMembershipRepository(db).Join(t.Context(), link.JoinLinkID(), link.TokenHash().Bytes(), candidate.TesterAuthID(), candidate, testerdomain.ApplicationTesterLimit)
		if result != nil || !errors.Is(err, testerport.ErrTesterJoinLinkInvalid) {
			t.Fatalf("result=%#v error=%v", result, err)
		}
		count, countErr := db.Collection(applicationTesterMembershipsCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": link.ApplicationID().String()})
		if countErr != nil || count != 0 {
			t.Fatalf("memberships=%d error=%v", count, countErr)
		}
	})
}

func TestApplicationArchivalConsumersIntegration_BR_APP_053_054_055_AllOAuthProviderMethodsFailClosedAndConfigurationSurvives(t *testing.T) {
	fixture := newOAuthProviderFixture(t)
	repository := NewOAuthProviderRepository(fixture.db, providerTestSecretVerifier{})
	at := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	runtime, err := repository.ResolveRuntime(t.Context(), fixture.publicID, oauthdomain.ChannelTest, 1, 2, at)
	if err != nil {
		t.Fatal(err)
	}
	archiveIntegrationApplication(t, fixture.db, fixture.seed.applicationID, fixture.seed.adminID)
	if value, err := repository.GetClientConfiguration(t.Context(), fixture.publicID); value != nil || !errors.Is(err, oauthport.ErrApplicationNotFound) {
		t.Fatalf("GetClientConfiguration()=(%#v,%v)", value, err)
	}
	if verified, revision, err := repository.VerifyClientSecret(t.Context(), fixture.confidentialID, fixture.confidentialSecret, 1); err != nil || verified || revision != 0 {
		t.Fatalf("VerifyClientSecret()=(%t,%d,%v)", verified, revision, err)
	}
	if value, err := repository.ResolveRuntime(t.Context(), fixture.publicID, oauthdomain.ChannelTest, 1, 2, at.Add(time.Second)); value != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("ResolveRuntime()=(%#v,%v)", value, err)
	}
	if value, err := repository.ResolveAuthorizationContext(t.Context(), fixture.publicID, fixture.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, 2, runtime.Version(), at.Add(time.Second)); value != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("ResolveAuthorizationContext()=(%#v,%v)", value, err)
	}
	if value, err := repository.GetPublishedRedirects(t.Context(), fixture.seed.applicationID, at.Add(time.Second)); value != nil || !errors.Is(err, oauthport.ErrApplicationNotFound) {
		t.Fatalf("GetPublishedRedirects()=(%#v,%v)", value, err)
	}
	_, err = NewApplicationArchivalRepository(fixture.db).SetArchiveState(t.Context(), fixture.seed.applicationID, fixture.seed.adminID, applicationdomain.ApplicationArchiveUnarchived, 2, func() (applicationdomain.ApplicationArchiveEventID, time.Time, error) {
		return "0199b33c-d04c-7abc-8abc-123456789012", at.Add(2 * time.Second), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := repository.ResolveRuntime(t.Context(), fixture.publicID, oauthdomain.ChannelTest, 1, 2, at.Add(3*time.Second))
	if err != nil || after.Version() != runtime.Version() || after.VersionID != runtime.VersionID {
		t.Fatalf("runtime after unarchive=%#v error=%v; before=%#v", after, err, runtime)
	}
}
