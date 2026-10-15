package mongo

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

func TestApplicationVersionManagementCursorRejectsNonCanonicalBase64(t *testing.T) {
	key := applicationVersionManagementTestCursorKey(t)
	adminID := shared.AuthID("version-management-admin")
	applicationID := shared.ApplicationID("019a0000-0000-7000-8000-000000000001")
	document := applicationVersionDocument{VersionID: "019a0000-0000-7000-8000-000000000002", Sequence: 2}
	token, err := encodeApplicationVersionManagementCursor(key, adminID, applicationID, nil, document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeApplicationVersionManagementCursor(key, token, adminID, applicationID, nil); err != nil {
		t.Fatalf("canonical token rejected: %v", err)
	}
	nonCanonical := token + "="
	if _, err = decodeApplicationVersionManagementCursor(key, nonCanonical, adminID, applicationID, nil); !errors.Is(err, versionport.ErrInvalidManagementPageToken) {
		t.Fatalf("non-canonical token error = %v, want invalid page token", err)
	}
}

func TestApplicationVersionManagementCursorRejectsTamperedSortKeys(t *testing.T) {
	key := applicationVersionManagementTestCursorKey(t)
	adminID := shared.AuthID("version-management-admin")
	applicationID := shared.ApplicationID("019a0000-0000-7000-8000-000000000001")
	document := applicationVersionDocument{VersionID: "019a0000-0000-7000-8000-000000000002", Sequence: 2}
	token, err := encodeApplicationVersionManagementCursor(key, adminID, applicationID, nil, document)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	var original applicationVersionManagementCursor
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name                   string
		mutate                 func(*applicationVersionManagementCursor)
		recalculateFingerprint bool
	}{
		{"sequence with retained fingerprint", func(cursor *applicationVersionManagementCursor) { cursor.Sequence++ }, false},
		{"sequence with recomputed fingerprint", func(cursor *applicationVersionManagementCursor) { cursor.Sequence++ }, true},
		{"version ID with retained fingerprint", func(cursor *applicationVersionManagementCursor) {
			cursor.VersionID = "019a0000-0000-7000-8000-000000000003"
		}, false},
		{"version ID with recomputed fingerprint", func(cursor *applicationVersionManagementCursor) {
			cursor.VersionID = "019a0000-0000-7000-8000-000000000003"
		}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forged := original
			test.mutate(&forged)
			if test.recalculateFingerprint {
				forged.Fingerprint = versionManagementFingerprint(adminID, applicationID, nil, forged.Sequence, forged.VersionID)
			}
			encoded, marshalErr := json.Marshal(forged)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			forgedToken := base64.RawURLEncoding.EncodeToString(encoded)
			if _, decodeErr := decodeApplicationVersionManagementCursor(key, forgedToken, adminID, applicationID, nil); !errors.Is(decodeErr, versionport.ErrInvalidManagementPageToken) {
				t.Fatalf("tampered token error = %v, want invalid page token", decodeErr)
			}
		})
	}
	otherKey, err := DeriveApplicationVersionManagementCursorKey([]byte("different-test-only-signing-material"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeApplicationVersionManagementCursor(otherKey, token, adminID, applicationID, nil); !errors.Is(err, versionport.ErrInvalidManagementPageToken) {
		t.Fatalf("token verified under a different key: %v", err)
	}
	if _, err = DeriveApplicationVersionManagementCursorKey(nil); err == nil {
		t.Fatal("empty signing material was accepted")
	}
}

func applicationVersionManagementTestCursorKey(t *testing.T) ApplicationVersionManagementCursorKey {
	t.Helper()
	key, err := DeriveApplicationVersionManagementCursorKey([]byte("test-only-application-version-management-signing-material"))
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func newApplicationVersionManagementTestRepository(t *testing.T, database *drivermongo.Database) *ApplicationVersionManagementRepository {
	t.Helper()
	return NewApplicationVersionManagementRepository(database, applicationVersionManagementTestCursorKey(t))
}

func TestApplicationVersionManagementRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-VER-019-025 lists all states and returns current detail with minimal latest review", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "version-management-admin", "version_management_all_states")
		fixture := createVersionManagementFixture(t, database, application.ID(), application.AdminID())
		repository := newApplicationVersionManagementTestRepository(t, database)

		first, err := repository.List(t.Context(), application.AdminID(), application.ID(), nil, 2, "")
		if err != nil || len(first.Items) != 2 || first.Items[0].Sequence <= first.Items[1].Sequence || first.NextPageToken == "" || first.AsOf.IsZero() {
			t.Fatalf("first page=%#v err=%v", first, err)
		}
		second, err := repository.List(t.Context(), application.AdminID(), application.ID(), nil, 100, first.NextPageToken)
		if err != nil || len(second.Items) != 3 || second.NextPageToken != "" {
			t.Fatalf("second page=%#v err=%v", second, err)
		}
		seen := map[string]bool{}
		allItems := append(first.Items, second.Items...)
		for _, item := range allItems {
			seen[item.ReviewStatus] = true
			if item.LaunchURL == "" || item.PKCERedirectURIs == nil || item.ConfidentialRedirectURIs == nil {
				t.Fatalf("current version projection incomplete: %#v", item)
			}
		}
		projection, err := json.Marshal(allItems)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(projection, []byte("version-management-reviewer")) || bytes.Contains(projection, []byte("version-management-system")) {
			t.Fatalf("management projection leaked Reviewer/SYSTEM authId: %s", projection)
		}
		for _, status := range []string{"DRAFT", "SUBMITTED", "APPROVED", "REJECTED"} {
			if !seen[status] {
				t.Fatalf("missing status %s in %#v", status, seen)
			}
		}

		rejected, err := repository.Get(t.Context(), application.AdminID(), application.ID(), fixture.rejected.ID())
		if err != nil || rejected.Version.LatestReview == nil || rejected.Version.LatestReview.Decision == nil || rejected.Version.LatestReview.Decision.Reason == nil || *rejected.Version.LatestReview.Decision.Reason != "management rejection" {
			t.Fatalf("rejected detail=%#v err=%v", rejected, err)
		}
		restored, err := repository.Get(t.Context(), application.AdminID(), application.ID(), fixture.restored.ID())
		if err != nil || restored.Version.ReviewStatus != "DRAFT" || restored.Version.LatestReview == nil || restored.Version.LatestReview.DraftRestoration == nil || restored.Version.LatestReview.Status != "REJECTED" {
			t.Fatalf("restored detail=%#v err=%v", restored, err)
		}
		approved := versiondomain.ReviewStatusApproved
		filtered, err := repository.List(t.Context(), application.AdminID(), application.ID(), &approved, 20, "")
		if err != nil || len(filtered.Items) != 1 || filtered.Items[0].VersionID != fixture.approved.ID().String() {
			t.Fatalf("approved filter=%#v err=%v", filtered, err)
		}
	})

	t.Run("BR-VER-019 BR-VER-022 BR-VER-023 transfer changes authority and binds page token", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "version-management-old", "version_management_transfer")
		for _, label := range []string{"one", "two"} {
			createVersionManagementDraft(t, database, application.ID(), application.AdminID(), label)
		}
		repository := newApplicationVersionManagementTestRepository(t, database)
		page, err := repository.List(t.Context(), application.AdminID(), application.ID(), nil, 1, "")
		if err != nil || page.NextPageToken == "" {
			t.Fatalf("old admin page=%#v err=%v", page, err)
		}
		for _, nonAdmin := range []shared.AuthID{"version-management-reviewer", "version-management-system"} {
			if _, err = repository.List(t.Context(), nonAdmin, application.ID(), nil, 20, ""); !errors.Is(err, versionport.ErrManagementNotFound) {
				t.Fatalf("non-admin %q error=%v, want hidden not found", nonAdmin, err)
			}
		}
		draft := versiondomain.ReviewStatusDraft
		if _, err = repository.List(t.Context(), application.AdminID(), application.ID(), &draft, 20, page.NextPageToken); !errors.Is(err, versionport.ErrInvalidManagementPageToken) {
			t.Fatalf("cross-filter token error=%v, want invalid token", err)
		}
		other := createVersionTestApplication(t, database, string(application.AdminID()), "version_management_other")
		otherVersion := createVersionManagementDraft(t, database, other.ID(), other.AdminID(), "other")
		if _, err = repository.List(t.Context(), application.AdminID(), other.ID(), nil, 20, page.NextPageToken); !errors.Is(err, versionport.ErrInvalidManagementPageToken) {
			t.Fatalf("cross-application token error=%v, want invalid token", err)
		}
		if _, err = repository.Get(t.Context(), application.AdminID(), application.ID(), otherVersion.ID()); !errors.Is(err, versionport.ErrManagementNotFound) {
			t.Fatalf("cross-application version error=%v, want hidden not found", err)
		}
		newAdmin := shared.AuthID("version-management-new")
		result, err := database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": application.ID().String(), "adminId": application.AdminID().String()}, bson.M{"$set": bson.M{"adminId": newAdmin.String()}, "$inc": bson.M{"ownershipRevision": int64(1), "coordinationRevision": int64(1)}})
		if err != nil || result.ModifiedCount != 1 {
			t.Fatalf("transfer fixture result=%#v err=%v", result, err)
		}
		if _, err = repository.List(t.Context(), application.AdminID(), application.ID(), nil, 20, ""); !errors.Is(err, versionport.ErrManagementNotFound) {
			t.Fatalf("old admin error=%v, want hidden not found", err)
		}
		if _, err = repository.List(t.Context(), newAdmin, application.ID(), nil, 20, page.NextPageToken); !errors.Is(err, versionport.ErrInvalidManagementPageToken) {
			t.Fatalf("reused token error=%v, want invalid token", err)
		}
		if current, currentErr := repository.List(t.Context(), newAdmin, application.ID(), nil, 20, ""); currentErr != nil || len(current.Items) != 2 {
			t.Fatalf("new admin page=%#v err=%v", current, currentErr)
		}
	})

	t.Run("BR-VER-020 lifecycle availability and archive states retain history", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "version-management-history", "version_management_history")
		version := createVersionManagementDraft(t, database, application.ID(), application.AdminID(), "history")
		repository := newApplicationVersionManagementTestRepository(t, database)
		states := []bson.M{
			{"platformAvailabilityStatus": "SUSPENDED", "platformAvailabilityRevision": int64(2)},
			{"archiveStatus": "ARCHIVED", "archiveRevision": int64(2)},
			{"lifecycleStatus": "CLOSING", "lifecycleRevision": int64(2)},
			{"lifecycleStatus": "CLOSED", "lifecycleRevision": int64(3)},
		}
		for _, state := range states {
			if _, err := database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": application.ID().String()}, bson.M{"$set": state}); err != nil {
				t.Fatalf("set state %#v: %v", state, err)
			}
			if detail, err := repository.Get(t.Context(), application.AdminID(), application.ID(), version.ID()); err != nil || detail.Version.VersionID != version.ID().String() {
				t.Fatalf("state %#v detail=%#v err=%v", state, detail, err)
			}
		}
	})

	t.Run("BR-VER-024 missing OAuth and review relation corruption fail closed", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "version-management-corrupt", "version_management_corrupt")
		version := createVersionManagementDraft(t, database, application.ID(), application.AdminID(), "corrupt")
		repository := newApplicationVersionManagementTestRepository(t, database)
		if _, err := database.Collection(applicationVersionOAuthConfigsCollectionName).DeleteOne(t.Context(), bson.M{"applicationVersionId": version.ID().String()}); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.Get(t.Context(), application.AdminID(), application.ID(), version.ID()); !errors.Is(err, versionport.ErrManagementStateInconsistent) {
			t.Fatalf("missing OAuth error=%v", err)
		}

		database = reusableIntegrationDatabase(t, client)
		application = createVersionTestApplication(t, database, "version-management-review-corrupt", "version_management_review_corrupt")
		version = createVersionManagementDraft(t, database, application.ID(), application.AdminID(), "submitted")
		submitVersionManagementReview(t, database, application.ID(), application.AdminID(), version)
		if _, err := database.Collection(applicationVersionsCollectionName).UpdateOne(t.Context(), bson.M{"versionId": version.ID().String()}, bson.M{"$set": bson.M{"revision": int64(9)}}); err != nil {
			t.Fatal(err)
		}
		repository = newApplicationVersionManagementTestRepository(t, database)
		if _, err := repository.List(t.Context(), application.AdminID(), application.ID(), nil, 20, ""); !errors.Is(err, versionport.ErrManagementStateInconsistent) {
			t.Fatalf("review relation error=%v", err)
		}
	})
}

type versionManagementFixture struct {
	approved, rejected, restored *versiondomain.ApplicationVersion
}

func createVersionManagementFixture(t *testing.T, database *drivermongo.Database, applicationID shared.ApplicationID, adminID shared.AuthID) versionManagementFixture {
	t.Helper()
	createVersionManagementDraft(t, database, applicationID, adminID, "draft")
	submitted := createVersionManagementDraft(t, database, applicationID, adminID, "submitted")
	submitVersionManagementReview(t, database, applicationID, adminID, submitted)
	approved := createVersionManagementDraft(t, database, applicationID, adminID, "approved")
	approvedReview := submitVersionManagementReview(t, database, applicationID, adminID, approved)
	decideVersionManagementReview(t, database, applicationID, approved, approvedReview, true)
	rejected := createVersionManagementDraft(t, database, applicationID, adminID, "rejected")
	rejectedReview := submitVersionManagementReview(t, database, applicationID, adminID, rejected)
	decideVersionManagementReviewAs(t, database, applicationID, rejected, rejectedReview, false, "version-management-system")
	restored := createVersionManagementDraft(t, database, applicationID, adminID, "restored")
	restoredReview := submitVersionManagementReview(t, database, applicationID, adminID, restored)
	decideVersionManagementReview(t, database, applicationID, restored, restoredReview, false)
	if _, err := NewApplicationReviewRestorationRepository(database).RestoreDraft(t.Context(), applicationID, reviewdomain.ApplicationVersionID(restored.ID()), restoredReview, adminID, 3, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("restore rejected version: %v", err)
	}
	return versionManagementFixture{approved: approved, rejected: rejected, restored: restored}
}

func createVersionManagementDraft(t *testing.T, database *drivermongo.Database, applicationID shared.ApplicationID, adminID shared.AuthID, label string) *versiondomain.ApplicationVersion {
	t.Helper()
	version, err := NewApplicationVersionRepository(database).CreateDraft(t.Context(), adminID, integrationApplicationVersionDraft(t, applicationID, adminID.String(), label))
	if err != nil {
		t.Fatalf("create %s: %v", label, err)
	}
	return version
}

func submitVersionManagementReview(t *testing.T, database *drivermongo.Database, applicationID shared.ApplicationID, adminID shared.AuthID, version *versiondomain.ApplicationVersion) reviewdomain.ApplicationReviewID {
	t.Helper()
	repository := NewApplicationReviewRepository(database)
	candidate := loadReviewCandidate(t, repository, applicationID, version.ID(), adminID.String(), 1)
	reviewID := nextIntegrationApplicationReviewID(t)
	if _, err := repository.Submit(t.Context(), candidate, reviewID, adminID, 41, "public-https.v1", time.Date(2026, 10, 10, 10, 0, int(version.Sequence()), 0, time.UTC)); err != nil {
		t.Fatalf("submit %s: %v", version.VersionLabel().String(), err)
	}
	return reviewID
}

func decideVersionManagementReview(t *testing.T, database *drivermongo.Database, applicationID shared.ApplicationID, version *versiondomain.ApplicationVersion, reviewID reviewdomain.ApplicationReviewID, approve bool) {
	decideVersionManagementReviewAs(t, database, applicationID, version, reviewID, approve, "version-management-reviewer")
}

func decideVersionManagementReviewAs(t *testing.T, database *drivermongo.Database, applicationID shared.ApplicationID, version *versiondomain.ApplicationVersion, reviewID reviewdomain.ApplicationReviewID, approve bool, actor string) {
	t.Helper()
	repository := NewApplicationReviewDecisionRepository(database)
	seed := decidableReview{applicationID: applicationID, versionID: reviewdomain.ApplicationVersionID(version.ID()), reviewID: reviewID}
	candidate := loadDecisionCandidate(t, repository, seed, actor)
	var decision *reviewdomain.ApplicationReviewDecision
	if approve {
		decision = integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 41, "public-https.v1", actor, time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC))
	} else {
		decision = integrationRejectedDecision(t, "review.v1", "management rejection", actor, time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC))
	}
	if _, err := repository.Decide(t.Context(), candidate, shared.AuthID(actor), decision); err != nil {
		t.Fatalf("decide %s: %v", version.VersionLabel().String(), err)
	}
}
