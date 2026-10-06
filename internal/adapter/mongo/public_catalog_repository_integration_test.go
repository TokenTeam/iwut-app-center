package mongo

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
)

func TestPublicCatalogRepositoryIntegration_UCAPP024_EmptyCatalog(t *testing.T) {
	database := migratedIntegrationDatabase(t, integrationClient(t))
	page, err := NewPublicCatalogRepository(database).ListPublic(t.Context(), "", 1, nil, 20, "")
	if err != nil || page == nil || len(page.Items()) != 0 || page.NextPageToken() != "" {
		t.Fatalf("empty catalog page=%#v err=%v", page, err)
	}
}

func TestPublicCatalogRepositoryIntegration_UCAPP024_ListDetailIdentityAndEligibility(t *testing.T) {
	fixture := newUnifiedLaunchFixture(t, integrationClient(t))
	repository := NewPublicCatalogRepository(fixture.db)
	host := []catalogdomain.CapabilityName{"grey.host.v1", "stable.host.v1"}

	page, err := repository.ListPublic(t.Context(), "", 1, host, 20, "")
	if err != nil || len(page.Items()) != 1 || page.NextPageToken() != "" {
		t.Fatalf("anonymous page=%#v err=%v", page, err)
	}
	item := page.Items()[0]
	if item.ApplicationID() != fixture.testSeed.applicationID || item.LaunchTarget().Channel() != catalogdomain.LaunchChannelStable || item.Profile().DisplayName() == "" || item.Filter().Revision() != 0 || item.Filter().Mode() != "ALLOW_ALL" {
		t.Fatalf("anonymous item=%#v target=%#v", item, item.LaunchTarget())
	}

	item, err = repository.GetPublic(t.Context(), fixture.testSeed.applicationID, fixture.matchedAuthID, 1, host)
	if err != nil || item.LaunchTarget().Channel() != catalogdomain.LaunchChannelGrey {
		t.Fatalf("matched detail=%#v err=%v", item, err)
	}
	item, err = repository.GetPublic(t.Context(), fixture.testSeed.applicationID, fixture.testAuthID, 1, []catalogdomain.CapabilityName{"camera.read.v1", "stable.host.v1"})
	if err != nil || item.LaunchTarget().Channel() != catalogdomain.LaunchChannelTest {
		t.Fatalf("tester detail=%#v err=%v", item, err)
	}

	if page, err = repository.ListPublic(t.Context(), "", 1, nil, 20, ""); err != nil || len(page.Items()) != 0 {
		t.Fatalf("incompatible stable page=%#v err=%v", page, err)
	}
	if item, err = repository.GetPublic(t.Context(), fixture.testSeed.applicationID, "", 1, nil); item != nil || !errors.Is(err, catalogport.ErrPublicApplicationNotFound) {
		t.Fatalf("incompatible detail=%#v err=%v", item, err)
	}
	if page, err = repository.ListPublic(t.Context(), "", 1, host, 20, "invalid"); page != nil || !errors.Is(err, catalogport.ErrInvalidPageToken) {
		t.Fatalf("invalid cursor page=%#v err=%v", page, err)
	}
}

func TestPublicCatalogRepositoryIntegration_UCAPP024_InvariantAndIndex(t *testing.T) {
	fixture := newUnifiedLaunchFixture(t, integrationClient(t))
	repository := NewPublicCatalogRepository(fixture.db)
	host := []catalogdomain.CapabilityName{"stable.host.v1"}

	var approvedProfile applicationProfileRevisionDocument
	if err := fixture.db.Collection(applicationProfileRevisionsCollectionName).FindOne(t.Context(), bson.M{"applicationId": fixture.testSeed.applicationID.String(), "reviewStatus": "APPROVED"}).Decode(&approvedProfile); err != nil {
		t.Fatal(err)
	}
	setTestLaunchFields(t, fixture.db, applicationProfileReviewsCollectionName, bson.D{{Key: "applicationId", Value: fixture.testSeed.applicationID.String()}, {Key: "status", Value: "APPROVED"}}, bson.D{{Key: "snapshot.displayName", Value: "Drifted profile"}})
	if page, err := repository.ListPublic(t.Context(), "", 1, host, 20, ""); page != nil || !errors.Is(err, catalogport.ErrApplicationCatalogStateInconsistent) {
		t.Fatalf("drifted profile approval page=%#v err=%v", page, err)
	}
	setTestLaunchFields(t, fixture.db, applicationProfileReviewsCollectionName, bson.D{{Key: "applicationId", Value: fixture.testSeed.applicationID.String()}, {Key: "status", Value: "APPROVED"}}, bson.D{{Key: "snapshot.displayName", Value: approvedProfile.DisplayName}})
	setTestLaunchFields(t, fixture.db, applicationProfilesCollectionName, bson.D{{Key: "applicationId", Value: fixture.testSeed.applicationID.String()}}, bson.D{{Key: "currentPublishedProfileRevisionId", Value: "01890f47-0000-7000-8000-000000009999"}})
	if page, err := repository.ListPublic(t.Context(), "", 1, host, 20, ""); page != nil || !errors.Is(err, catalogport.ErrApplicationCatalogStateInconsistent) {
		t.Fatalf("corrupt profile page=%#v err=%v", page, err)
	}

	cursor, err := fixture.db.Collection(applicationPublicationsCollectionName).Indexes().List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close(t.Context())
	found := false
	for cursor.Next(t.Context()) {
		var index bson.M
		if err = cursor.Decode(&index); err != nil {
			t.Fatal(err)
		}
		if index["name"] == applicationCatalogStableScanIndexName {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing index %s", applicationCatalogStableScanIndexName)
	}
}
