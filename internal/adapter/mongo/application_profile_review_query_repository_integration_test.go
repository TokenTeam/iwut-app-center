package mongo

import (
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
)

func createPendingProfileReviewQueryFixture(t *testing.T, database *drivermongo.Database, admin, name string, submittedAt time.Time) (*profiledomain.ApplicationProfileRevision, *profiledomain.ApplicationProfileReview) {
	t.Helper()
	application := createVersionTestApplication(t, database, admin, name)
	repository := NewApplicationProfileRevisionRepository(database)
	draft, err := repository.CreateDraft(t.Context(), application.AdminID(), profileDraftFixture(t, application.ID(), application.AdminID()))
	if err != nil {
		t.Fatal(err)
	}
	submission, err := repository.SubmitDraft(t.Context(), application.ID(), draft.ProfileRevisionID(), application.AdminID(), draft.Revision(), profileReviewID(t), submittedAt)
	if err != nil {
		t.Fatal(err)
	}
	return submission.ProfileRevision, submission.Review
}

func TestApplicationProfileReviewQueryRepositoryIntegration(t *testing.T) {
	database := migratedIntegrationDatabase(t, integrationClient(t))
	firstRevision, firstReview := createPendingProfileReviewQueryFixture(t, database, "profile-query-admin-1", "profile_query_one", time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC))
	_, secondReview := createPendingProfileReviewQueryFixture(t, database, "profile-query-admin-2", "profile_query_two", time.Date(2026, 10, 7, 2, 0, 0, 0, time.UTC))
	repository := NewApplicationProfileReviewQueryRepository(database)
	reviewer := shared.AuthID("independent-profile-query-reviewer")

	t.Run("BR-PRF-042 BR-PRF-043 BR-PRF-045 queue pagination and filter", func(t *testing.T) {
		firstPage, err := repository.ListPending(t.Context(), reviewer, nil, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(firstPage.Items) != 1 || firstPage.Items[0].ProfileReviewID != firstReview.ProfileReviewID().String() || firstPage.NextPageToken == "" || !firstPage.Items[0].DecisionEligibility.Eligible {
			t.Fatalf("first page=%#v", firstPage)
		}
		secondPage, err := repository.ListPending(t.Context(), reviewer, nil, 1, firstPage.NextPageToken)
		if err != nil {
			t.Fatal(err)
		}
		if len(secondPage.Items) != 1 || secondPage.Items[0].ProfileReviewID != secondReview.ProfileReviewID().String() || secondPage.NextPageToken != "" {
			t.Fatalf("second page=%#v", secondPage)
		}
		filter := firstReview.ApplicationID()
		filtered, err := repository.ListPending(t.Context(), reviewer, &filter, 10, "")
		if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ProfileReviewID != firstReview.ProfileReviewID().String() {
			t.Fatalf("filtered=%#v err=%v", filtered, err)
		}
		if _, err = repository.ListPending(t.Context(), shared.AuthID("different-reviewer"), nil, 1, firstPage.NextPageToken); !errors.Is(err, profileport.ErrInvalidProfileReviewPageToken) {
			t.Fatalf("rebound token err=%v", err)
		}
	})

	t.Run("BR-PRF-044 detail policy and complete conflicts", func(t *testing.T) {
		detail, err := repository.Get(t.Context(), firstReview.SubmittedBy(), firstReview.ApplicationID(), firstReview.ProfileRevisionID(), firstReview.ProfileReviewID())
		if err != nil {
			t.Fatal(err)
		}
		if detail.Review.Snapshot().DisplayName() != firstRevision.DisplayName() || detail.CurrentPolicy.Version != profiledomain.InitialProfileReviewPolicyVersion || len(detail.CurrentPolicy.RequiredCheckIDs) != len(profiledomain.InitialProfileReviewChecks()) {
			t.Fatalf("detail=%#v", detail)
		}
		if detail.DecisionEligibility.Eligible || len(detail.DecisionEligibility.Conflicts) != 3 {
			t.Fatalf("eligibility=%#v", detail.DecisionEligibility)
		}
		wrongApplication := shared.ApplicationID(nextIntegrationTesterJoinLinkID(t))
		if _, err = repository.Get(t.Context(), reviewer, wrongApplication, firstReview.ProfileRevisionID(), firstReview.ProfileReviewID()); !errors.Is(err, profileport.ErrProfileReviewQueryNotFound) {
			t.Fatalf("path mismatch err=%v", err)
		}
	})

	t.Run("BR-PRF-042 closing application excluded", func(t *testing.T) {
		_, err := database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": firstReview.ApplicationID().String()}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSING"}}, options.UpdateOne().SetBypassDocumentValidation(true))
		if err != nil {
			t.Fatal(err)
		}
		filter := firstReview.ApplicationID()
		page, err := repository.ListPending(t.Context(), reviewer, &filter, 10, "")
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("page=%#v err=%v", page, err)
		}
	})

	t.Run("BR-PRF-046 corrupted pending snapshot fails closed", func(t *testing.T) {
		_, err := database.Collection(applicationProfileReviewsCollectionName).UpdateOne(t.Context(), bson.M{"profileReviewId": secondReview.ProfileReviewID().String()}, bson.M{"$set": bson.M{"snapshot.displayName": "drift"}})
		if err != nil {
			t.Fatal(err)
		}
		filter := secondReview.ApplicationID()
		if _, err = repository.ListPending(t.Context(), reviewer, &filter, 10, ""); !errors.Is(err, profileport.ErrProfileReviewQueryStateInconsistent) {
			t.Fatalf("corruption err=%v", err)
		}
	})

	t.Run("profile review queue index is deployed", func(t *testing.T) {
		cursor, err := database.Collection(applicationProfileReviewsCollectionName).Indexes().List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var indexes []struct {
			Name string `bson:"name"`
		}
		if err = cursor.All(t.Context(), &indexes); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, index := range indexes {
			if index.Name == profileReviewQueueIndexName {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing index %q in %v", profileReviewQueueIndexName, indexes)
		}
	})
}
