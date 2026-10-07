package mongo

import (
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
)

func createPendingVersionReviewQueryFixture(t *testing.T, database *drivermongo.Database, admin, applicationName, label string, submittedAt time.Time) (*reviewdomain.ApplicationReview, shared.ApplicationID) {
	t.Helper()
	application, version := createReviewTestVersion(t, database, admin, applicationName, label)
	repository := NewApplicationReviewRepository(database)
	candidate := loadReviewCandidate(t, repository, application.ID(), version.ID(), admin, version.Revision())
	result, err := repository.Submit(t.Context(), candidate, nextIntegrationApplicationReviewID(t), shared.AuthID(admin), 41, "public-https.v1", submittedAt)
	if err != nil {
		t.Fatal(err)
	}
	return result.Review(), application.ID()
}

func TestApplicationReviewQueryRepositoryIntegration(t *testing.T) {
	database := migratedIntegrationDatabase(t, integrationClient(t))
	firstReview, firstApplicationID := createPendingVersionReviewQueryFixture(t, database, "version-query-admin-1", "version_query_one", "v1", time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC))
	secondReview, secondApplicationID := createPendingVersionReviewQueryFixture(t, database, "version-query-admin-2", "version_query_two", "v2", time.Date(2026, 10, 7, 2, 0, 0, 0, time.UTC))
	repository := NewApplicationReviewQueryRepository(database)
	reviewer := shared.AuthID("independent-version-query-reviewer")

	t.Run("BR-REV-030 BR-REV-031 BR-REV-033 queue pagination filter and token binding", func(t *testing.T) {
		firstPage, err := repository.ListPending(t.Context(), reviewer, nil, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(firstPage.Items) != 1 || firstPage.Items[0].ReviewID != firstReview.ReviewID().String() || firstPage.NextPageToken == "" || !firstPage.Items[0].DecisionEligibility.Eligible {
			t.Fatalf("first page=%#v", firstPage)
		}
		secondPage, err := repository.ListPending(t.Context(), reviewer, nil, 1, firstPage.NextPageToken)
		if err != nil {
			t.Fatal(err)
		}
		if len(secondPage.Items) != 1 || secondPage.Items[0].ReviewID != secondReview.ReviewID().String() || secondPage.NextPageToken != "" {
			t.Fatalf("second page=%#v", secondPage)
		}
		filtered, err := repository.ListPending(t.Context(), reviewer, &firstApplicationID, 10, "")
		if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ReviewID != firstReview.ReviewID().String() {
			t.Fatalf("filtered=%#v err=%v", filtered, err)
		}
		if _, err = repository.ListPending(t.Context(), shared.AuthID("another-reviewer"), nil, 1, firstPage.NextPageToken); !errors.Is(err, reviewport.ErrInvalidReviewPageToken) {
			t.Fatalf("rebound token err=%v", err)
		}
	})

	t.Run("BR-REV-032 detail exposes policy and complete conflicts", func(t *testing.T) {
		detail, err := repository.Get(t.Context(), firstReview.SubmittedBy(), firstApplicationID, firstReview.VersionID(), firstReview.ReviewID())
		if err != nil {
			t.Fatal(err)
		}
		if detail.Review.Snapshot().VersionLabel() != "v1" || detail.CurrentPolicy.Version == "" || detail.DecisionEligibility.Eligible || len(detail.DecisionEligibility.Conflicts) != 3 {
			t.Fatalf("detail=%#v", detail)
		}
		if _, err = repository.Get(t.Context(), reviewer, secondApplicationID, firstReview.VersionID(), firstReview.ReviewID()); !errors.Is(err, reviewport.ErrReviewQueryNotFound) {
			t.Fatalf("path mismatch err=%v", err)
		}
	})

	t.Run("BR-REV-034 pending snapshot drift fails closed", func(t *testing.T) {
		_, err := database.Collection(applicationReviewsCollectionName).UpdateOne(t.Context(), bson.M{"reviewId": secondReview.ReviewID().String()}, bson.M{"$set": bson.M{"snapshot.versionLabel": "drift"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repository.ListPending(t.Context(), reviewer, &secondApplicationID, 10, ""); !errors.Is(err, reviewport.ErrReviewQueryStateInconsistent) {
			t.Fatalf("corruption err=%v", err)
		}
	})
}
