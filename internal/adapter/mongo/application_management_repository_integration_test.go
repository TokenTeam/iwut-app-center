package mongo

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	managementport "iwut-app-center/internal/management/port"
	"iwut-app-center/internal/shared"
)

func TestApplicationManagementRepositoryIntegration(t *testing.T) {
	database := migratedIntegrationDatabase(t, integrationClient(t))
	admin := shared.AuthID("management-query-admin")
	first := createVersionTestApplication(t, database, admin.String(), "management_query_one")
	second := createVersionTestApplication(t, database, admin.String(), "management_query_two")
	other := createVersionTestApplication(t, database, "other-management-admin", "management_query_other")
	if _, err := NewApplicationVersionRepository(database).CreateDraft(t.Context(), admin, integrationApplicationVersionDraft(t, first.ID(), admin.String(), "v1")); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationManagementRepository(database)
	lifecycle := []string{"ACTIVE", "CLOSING"}

	t.Run("BR-APP-038 BR-APP-039 BR-APP-040 private pagination and token binding", func(t *testing.T) {
		page, err := repository.List(t.Context(), admin, lifecycle, nil, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.NextPageToken == "" || page.Items[0].Application.AdminID != admin.String() || page.Items[0].Application.ApplicationID == other.ID().String() {
			t.Fatalf("first page=%#v", page)
		}
		next, err := repository.List(t.Context(), admin, lifecycle, nil, 1, page.NextPageToken)
		if err != nil {
			t.Fatal(err)
		}
		if len(next.Items) != 1 || next.NextPageToken != "" || next.Items[0].Application.ApplicationID == page.Items[0].Application.ApplicationID {
			t.Fatalf("next page=%#v", next)
		}
		if _, err = repository.List(t.Context(), shared.AuthID("other-management-admin"), lifecycle, nil, 1, page.NextPageToken); !errors.Is(err, managementport.ErrInvalidPageToken) {
			t.Fatalf("rebound token err=%v", err)
		}
	})

	t.Run("BR-APP-041 BR-APP-043 management detail and hidden ownership", func(t *testing.T) {
		detail, err := repository.Get(t.Context(), admin, first.ID())
		if err != nil {
			t.Fatal(err)
		}
		if detail.Application.ApplicationID != first.ID().String() || detail.Counts.VersionCount != 1 || detail.Counts.DraftVersionCount != 1 || detail.FilterState.Mode != "ALLOW_ALL" || detail.FilterState.SchemaVersion != "profile-filter-v1" {
			t.Fatalf("detail=%#v", detail)
		}
		if _, err = repository.Get(t.Context(), admin, other.ID()); !errors.Is(err, managementport.ErrNotFound) {
			t.Fatalf("non-owner err=%v", err)
		}
	})

	t.Run("BR-APP-042 malformed application fails closed", func(t *testing.T) {
		_, err := database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": second.ID().String()}, bson.M{"$set": bson.M{"platformAvailabilityStatus": "BROKEN"}}, options.UpdateOne().SetBypassDocumentValidation(true))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repository.List(t.Context(), admin, lifecycle, nil, 10, ""); !errors.Is(err, managementport.ErrStateInconsistent) {
			t.Fatalf("corruption err=%v", err)
		}
	})
}
