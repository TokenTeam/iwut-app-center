package mongo

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	oauthclientdomain "iwut-app-center/internal/oauthclient/domain"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	reviewdomain "iwut-app-center/internal/review/domain"
)

func TestApplicationPublicationRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-PUB-002 BR-PUB-004 BR-PUB-006 BR-PUB-007 BR-PUB-010 create replace noop partition and immutable sources", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		seed := createApprovedPublicationVersion(t, db, "publication-success")
		repo := NewApplicationPublicationRepository(db)
		initialVersion := readVersionDocument(t, db, seed.versionID.String())
		initialReview := readReviewDocument(t, db, seed.reviewID)
		initialApp := readPublicationApplication(t, db, seed)
		first := placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed)
		if !first.Changed() || first.Publication().Revision() != 1 || first.History().PreviousVersionID() != nil || first.History().ApprovedReviewID().String() != seed.reviewID.String() || first.History().ScopeCatalogRevision() != 81 {
			t.Fatalf("invalid creation: %#v", first)
		}
		revision := int64(1)
		noop := loadPublicationCandidate(t, repo, seed, 1, &revision)
		if !noop.IsNoOp() {
			t.Fatal("expected no-op candidate")
		}
		noopResult, err := repo.PlaceInTest(t.Context(), noop, nil, "", seed.adminID, publicationdomain.PublicationValidation{}, time.Time{})
		if err != nil || noopResult.Changed() || noopResult.History() != nil || !reflect.DeepEqual(noopResult.Publication(), first.Publication()) {
			t.Fatalf("no-op result=%#v err=%v", noopResult, err)
		}
		second := addApprovedPublicationVersion(t, db, seed, "v2")
		replaced := placePublication(t, repo, loadPublicationCandidate(t, repo, second, 1, &revision), second)
		if replaced.Publication().Revision() != 2 || replaced.History().PreviousVersionID() == nil || replaced.History().PreviousVersionID().String() != seed.versionID.String() || replaced.Publication().CreatedBy() != first.Publication().CreatedBy() || !replaced.Publication().CreatedAt().Equal(first.Publication().CreatedAt()) {
			t.Fatalf("invalid replacement: %#v", replaced)
		}
		if !reflect.DeepEqual(initialVersion, readVersionDocument(t, db, seed.versionID.String())) || !reflect.DeepEqual(initialReview, readReviewDocument(t, db, seed.reviewID)) {
			t.Fatal("publication mutated reviewed business facts")
		}
		// Creating the second version advances the application sequence; publication itself must not.
		afterSecond := readPublicationApplication(t, db, seed)
		afterSecond.NextVersionSequence = initialApp.NextVersionSequence
		afterSecond.CoordinationRevision = initialApp.CoordinationRevision
		if !reflect.DeepEqual(initialApp, afterSecond) {
			t.Fatal("publication changed application business fields")
		}
		otherMajor := placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 2, nil), seed)
		if otherMajor.Publication().PublicationID() == first.Publication().PublicationID() || otherMajor.Publication().RPCAPIMajor() != 2 {
			t.Fatal("partition collapsed")
		}
		assertPublicationCounts(t, db, 2, 3)
		var persisted applicationPublicationHistoryDocument
		if err := db.Collection(applicationPublicationHistoryCollectionName).FindOne(t.Context(), bson.D{{Key: "historyId", Value: first.History().HistoryID().String()}}).Decode(&persisted); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(persisted, applicationPublicationHistoryToDocument(first.History())) {
			t.Fatal("replacement changed past history")
		}
	})
	t.Run("BR-PUB-009 history failure rolls back publication and fences", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		seed := createApprovedPublicationVersion(t, db, "publication-rollback")
		repo := NewApplicationPublicationRepository(db)
		first := placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed)
		beforeApp := readPublicationApplication(t, db, seed)
		beforeVersionFence := readPublicationSourceFence(t, db, applicationVersionsCollectionName, seed)
		beforeReviewFence := readPublicationSourceFence(t, db, applicationReviewsCollectionName, seed)
		candidate := loadPublicationCandidate(t, repo, seed, 2, nil)
		id := publicationdomain.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
		result, err := repo.PlaceInTest(t.Context(), candidate, &id, first.History().HistoryID(), seed.adminID, publicationTestValidation(), time.Now())
		if err == nil || result != nil {
			t.Fatalf("collision committed: %v %v", result, err)
		}
		assertPublicationCounts(t, db, 1, 1)
		if !reflect.DeepEqual(beforeApp, readPublicationApplication(t, db, seed)) {
			t.Fatal("aborted transaction left a fence write")
		}

		if readPublicationSourceFence(t, db, applicationVersionsCollectionName, seed) != beforeVersionFence || readPublicationSourceFence(t, db, applicationReviewsCollectionName, seed) != beforeReviewFence {
			t.Fatal("aborted transaction left version/review fence writes")
		}
		// A failure while replacing must retain the old pointer and old revision.
		second := addApprovedPublicationVersion(t, db, seed, "v2")
		revision := int64(1)
		candidate = loadPublicationCandidate(t, repo, second, 1, &revision)
		result, err = repo.PlaceInTest(t.Context(), candidate, nil, first.History().HistoryID(), seed.adminID, publicationTestValidation(), time.Now())
		if err == nil || result != nil {
			t.Fatalf("replacement collision committed: %v %v", result, err)
		}
		assertPublicationCounts(t, db, 1, 1)
		stillFirst := loadPublicationCandidate(t, repo, seed, 1, &revision)
		if !stillFirst.IsNoOp() {
			t.Fatal("failed replacement changed pointer")
		}
	})
	t.Run("BR-PUB-006 BR-PUB-009 concurrent creates and replaces have exactly one winner", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		seed := createApprovedPublicationVersion(t, db, "publication-race")
		repo := NewApplicationPublicationRepository(db)
		candidates := []*publicationdomain.TestPlacementCandidate{loadPublicationCandidate(t, repo, seed, 1, nil), loadPublicationCandidate(t, repo, seed, 1, nil)}
		runPublicationRace(t, repo, candidates, seed, publicationport.ErrApplicationPublicationAlreadyExists)
		assertPublicationCounts(t, db, 1, 1)
		second := addApprovedPublicationVersion(t, db, seed, "v2")
		third := addApprovedPublicationVersion(t, db, seed, "v3")
		revision := int64(1)
		candidates = []*publicationdomain.TestPlacementCandidate{loadPublicationCandidate(t, repo, second, 1, &revision), loadPublicationCandidate(t, repo, third, 1, &revision)}
		runPublicationRace(t, repo, candidates, seed, publicationport.ErrApplicationPublicationRevisionConflict)
		assertPublicationCounts(t, db, 1, 2)
	})
	t.Run("BR-PUB-001 BR-PUB-003 BR-PUB-009 rechecks eligibility after external validation", func(t *testing.T) {
		for _, test := range []struct {
			name       string
			collection string
			update     bson.D
			want       error
		}{
			{"administrator transfer", applicationsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "new-admin"}}}}, publicationport.ErrApplicationAdminRequired},
			{"version revoke", applicationVersionsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: "REVOKED"}}}}, publicationport.ErrApplicationVersionNotApproved},
			{"review revoke", applicationReviewsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REVOKED"}}}}, publicationport.ErrApplicationVersionNotApproved},
			{"version content drift", applicationVersionsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "versionLabel", Value: "tampered"}}}}, publicationport.ErrApplicationReviewStateInconsistent},
			{"version revision drift", applicationVersionsCollectionName, bson.D{{Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}}, publicationport.ErrApplicationReviewStateInconsistent},
		} {
			t.Run(test.name, func(t *testing.T) {
				db := migratedIntegrationDatabase(t, client)
				seed := createApprovedPublicationVersion(t, db, "publication-recheck")
				repo := NewApplicationPublicationRepository(db)
				candidate := loadPublicationCandidate(t, repo, seed, 1, nil)
				filter := publicationSourceFilter(test.collection, seed)
				if _, err := db.Collection(test.collection).UpdateOne(t.Context(), filter, test.update, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
				id := publicationdomain.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
				history := publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t))
				result, err := repo.PlaceInTest(t.Context(), candidate, &id, history, seed.adminID, publicationTestValidation(), time.Now())
				if result != nil || !errors.Is(err, test.want) {
					t.Fatalf("result=%v err=%v want %v", result, err, test.want)
				}
				assertPublicationCounts(t, db, 0, 0)
			})
		}
	})
	// Revocation is a future capability. These concurrency fixtures bypass the
	// current lifecycle validator solely to simulate losing approval; UC007 does
	// not add a revoke endpoint or enable the future state in its migration.
	t.Run("BR-PUB-001 BR-PUB-003 BR-PUB-009 concurrent source writes invalidate stale transaction snapshots", func(t *testing.T) {
		for _, test := range []struct {
			name, collection string
			update           bson.D
			want             error
		}{
			{"administrator", applicationsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "new-admin"}}}}, publicationport.ErrApplicationAdminRequired},
			{"version", applicationVersionsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: "REVOKED"}}}}, publicationport.ErrApplicationVersionNotApproved},
			{"review", applicationReviewsCollectionName, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: "REVOKED"}}}}, publicationport.ErrApplicationVersionNotApproved},
		} {
			t.Run(test.name, func(t *testing.T) {
				db := migratedIntegrationDatabase(t, client)
				seed := createApprovedPublicationVersion(t, db, "publication-source-race")
				repo := NewApplicationPublicationRepository(db)
				candidate := loadPublicationCandidate(t, repo, seed, 1, nil)
				session, err := client.StartSession()
				if err != nil {
					t.Fatal(err)
				}
				defer session.EndSession(context.Background())
				if err = session.StartTransaction(); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Collection(test.collection).UpdateOne(drivermongo.NewSessionContext(t.Context(), session), publicationSourceFilter(test.collection, seed), test.update, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
				competing, started := publicationMonitoredClient(t, db.Name(), test.collection)
				competitor := NewApplicationPublicationRepository(competing.Database(db.Name()))
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				id := publicationdomain.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
				history := publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t))
				done := make(chan error, 1)
				go func() {
					_, err := competitor.PlaceInTest(ctx, candidate, &id, history, seed.adminID, publicationTestValidation(), time.Now())
					done <- err
				}()
				awaitMongoCommand(t, ctx, started, "publication source fence")
				if err = session.CommitTransaction(ctx); err != nil {
					t.Fatal(err)
				}
				select {
				case err = <-done:
					if !errors.Is(err, test.want) {
						t.Fatalf("stale transaction error=%v want %v", err, test.want)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				assertPublicationCounts(t, db, 0, 0)
			})
		}
	})
}

func TestApplicationPublicationRepositoryIntegration_BR_PUB_003_008_009_OAuthRegistrationRequirements(t *testing.T) {
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	seed := createApprovedPublicationVersion(t, db, "publication-oauth-registration")
	pkceURI := "https://app.example.edu/oauth/callback"
	confidentialURI := "https://server.example.edu/oauth/callback"

	setRedirects := func(pkce, confidential []string) {
		t.Helper()
		redirects := oauthRedirectConfigurationDocument{PKCERedirectURIs: pkce, ConfidentialRedirectURIs: confidential}
		if _, err := db.Collection(applicationVersionOAuthConfigsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "applicationVersionId", Value: seed.versionID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "oauthRedirects", Value: redirects}}}},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(applicationReviewsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "reviewId", Value: seed.reviewID.String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "snapshot.oauthRedirects", Value: redirects}}}},
		); err != nil {
			t.Fatal(err)
		}
	}

	repository := NewApplicationPublicationRepository(db)
	setRedirects([]string{pkceURI}, []string{})
	if candidate, err := repository.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); candidate != nil || !errors.Is(err, publicationport.ErrOAuthClientRegistrationRequired) {
		t.Fatalf("missing PUBLIC registration = (%v, %v)", candidate, err)
	}
	oauthRepository := NewOAuthClientRepository(db)
	if _, err := oauthRepository.Register(t.Context(), seed.applicationID, oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypePublicPKCE, nil, oauthclientdomain.ClientID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"), nil, seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if candidate, err := repository.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); err != nil || candidate == nil {
		t.Fatalf("PUBLIC registration was not accepted = (%v, %v)", candidate, err)
	}

	setRedirects([]string{pkceURI}, []string{confidentialURI})
	if candidate, err := repository.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); candidate != nil || !errors.Is(err, publicationport.ErrOAuthClientRegistrationRequired) {
		t.Fatalf("missing CONFIDENTIAL registration = (%v, %v)", candidate, err)
	}
	revision := int64(1)
	digest := oauthclientdomain.NewSecretDigest([32]byte{1})
	if _, err := oauthRepository.Register(t.Context(), seed.applicationID, oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypeConfidentialSecret, &revision, oauthclientdomain.ClientID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"), &digest, seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if candidate, err := repository.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); err != nil || candidate == nil {
		t.Fatalf("complete registration was not accepted = (%v, %v)", candidate, err)
	}
	if _, err := db.Collection(oauthClientCredentialsCollectionName).DeleteOne(t.Context(), bson.D{{Key: "clientId", Value: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}}); err != nil {
		t.Fatal(err)
	}
	if candidate, err := repository.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); candidate != nil || !errors.Is(err, publicationport.ErrOAuthClientRegistrationRequired) {
		t.Fatalf("missing credential = (%v, %v)", candidate, err)
	}
}

func TestApplicationPublicationEligibilityIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, test := range []struct {
		name   string
		mutate func(*applicationVersionDocument, *applicationReviewDocument)
		want   error
	}{
		{"nonapproved version", func(v *applicationVersionDocument, r *applicationReviewDocument) { v.ReviewStatus = "SUBMITTED" }, publicationport.ErrApplicationVersionNotApproved},
		{"nonapproved latest review", func(v *applicationVersionDocument, r *applicationReviewDocument) { r.Status = "REJECTED" }, publicationport.ErrApplicationVersionNotApproved},
		{"missing decision", func(v *applicationVersionDocument, r *applicationReviewDocument) { r.Decision = nil }, publicationport.ErrApplicationReviewStateInconsistent},
		{"mismatched outcome", func(v *applicationVersionDocument, r *applicationReviewDocument) { r.Decision.Outcome = "REJECTED" }, publicationport.ErrApplicationReviewStateInconsistent},
		{"missing approval validation", func(v *applicationVersionDocument, r *applicationReviewDocument) { r.Decision.ApprovalValidation = nil }, publicationport.ErrApplicationReviewStateInconsistent},
		{"changed snapshot", func(v *applicationVersionDocument, r *applicationReviewDocument) {
			r.Snapshot.VersionLabel = "different"
		}, publicationport.ErrApplicationReviewStateInconsistent},
		{"wrong revision relation", func(v *applicationVersionDocument, r *applicationReviewDocument) { v.Revision++ }, publicationport.ErrApplicationReviewStateInconsistent},
	} {
		t.Run("BR-PUB-003 "+test.name, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, client)
			seed := createApprovedPublicationVersion(t, db, "publication-eligibility")
			v := readVersionDocument(t, db, seed.versionID.String())
			r := readReviewDocument(t, db, seed.reviewID)
			test.mutate(&v, &r)
			if _, err := db.Collection(applicationVersionsCollectionName).ReplaceOne(t.Context(), bson.D{{Key: "versionId", Value: v.VersionID}}, v, options.Replace().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Collection(applicationReviewsCollectionName).ReplaceOne(t.Context(), bson.D{{Key: "reviewId", Value: r.ReviewID}}, r, options.Replace().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
			result, err := NewApplicationPublicationRepository(db).LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil)
			if result != nil || !errors.Is(err, test.want) {
				t.Fatalf("candidate=%v error=%v want=%v", result, err, test.want)
			}
			assertPublicationCounts(t, db, 0, 0)
		})
	}
	t.Run("BR-PUB-003 latest attempt overrides historical approval", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		seed := createApprovedPublicationVersion(t, db, "publication-latest")
		review := readReviewDocument(t, db, seed.reviewID)
		review.ReviewID = nextIntegrationApplicationReviewID(t).String()
		review.Attempt++
		review.SourceVersionRevision += 3
		review.Status = "REJECTED"
		review.Decision.Outcome = "REJECTED"
		review.Decision.ApprovalValidation = nil
		review.Decision.ConfirmedCheckIDs = []string{}
		reason := "later review rejected"
		review.Decision.Reason = &reason
		if _, err := db.Collection(applicationReviewsCollectionName).InsertOne(t.Context(), review); err != nil {
			t.Fatal(err)
		}
		candidate, err := NewApplicationPublicationRepository(db).LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil)
		if candidate != nil || !errors.Is(err, publicationport.ErrApplicationVersionNotApproved) {
			t.Fatalf("latest attempt ignored: candidate=%v err=%v", candidate, err)
		}
		assertPublicationCounts(t, db, 0, 0)
	})
	t.Run("BR-PUB-002 BR-PUB-006 bounds paths existence and revisions", func(t *testing.T) {
		db := migratedIntegrationDatabase(t, client)
		seed := createApprovedPublicationVersion(t, db, "publication-bounds")
		repo := NewApplicationPublicationRepository(db)
		revision := int64(1)
		if _, err := repo.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, &revision); !errors.Is(err, publicationport.ErrApplicationPublicationNotFound) {
			t.Fatalf("missing publication: %v", err)
		}
		if _, err := repo.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 3, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); !errors.Is(err, publicationport.ErrApplicationVersionRpcApiIncompatible) {
			t.Fatalf("incompatible major: %v", err)
		}
		foreign := createApprovedPublicationVersion(t, db, "publication-foreign")
		if _, err := repo.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(foreign.versionID), foreign.adminID, nil); !errors.Is(err, publicationport.ErrApplicationVersionNotFound) {
			t.Fatalf("foreign path leaked: %v", err)
		}
		placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed)
		if _, err := repo.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, nil); !errors.Is(err, publicationport.ErrApplicationPublicationAlreadyExists) {
			t.Fatalf("existing publication: %v", err)
		}
		revision = 2
		if _, err := repo.LoadTestPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, &revision); !errors.Is(err, publicationport.ErrApplicationPublicationRevisionConflict) {
			t.Fatalf("stale noop: %v", err)
		}
	})
}

func createApprovedPublicationVersion(t *testing.T, db *drivermongo.Database, name string) decidableReview {
	t.Helper()
	seed := createDecidableReview(t, db, "auth-publisher", name, "v1")
	approvePublicationVersion(t, db, seed)
	return seed
}
func addApprovedPublicationVersion(t *testing.T, db *drivermongo.Database, seed decidableReview, label string) decidableReview {
	t.Helper()
	version, err := NewApplicationVersionRepository(db).CreateDraft(t.Context(), seed.adminID, integrationApplicationVersionDraft(t, seed.applicationID, seed.adminID.String(), label))
	if err != nil {
		t.Fatal(err)
	}
	seed.versionID = reviewdomain.ApplicationVersionID(version.ID())
	seed.reviewID = nextIntegrationApplicationReviewID(t)
	repo := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, repo, seed.applicationID, seed.versionID, seed.adminID.String(), 1)
	if _, err = repo.Submit(t.Context(), candidate, seed.reviewID, seed.adminID, 41, "public-https.v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	approvePublicationVersion(t, db, seed)
	return seed
}
func approvePublicationVersion(t *testing.T, db *drivermongo.Database, seed decidableReview) {
	t.Helper()
	repo := NewApplicationReviewDecisionRepository(db)
	candidate := loadDecisionCandidate(t, repo, seed, "auth-reviewer")
	if _, err := repo.Decide(t.Context(), candidate, "auth-reviewer", integrationApprovedDecision(t, "review.v1", []string{"content-reviewed"}, 77, "public-https.v1", "auth-reviewer", time.Now())); err != nil {
		t.Fatal(err)
	}
}
func loadPublicationCandidate(t *testing.T, repo *ApplicationPublicationRepository, seed decidableReview, major int32, revision *int64) *publicationdomain.TestPlacementCandidate {
	t.Helper()
	c, err := repo.LoadTestPlacementCandidate(t.Context(), seed.applicationID, major, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, revision)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func publicationTestValidation() publicationdomain.PublicationValidation {
	return publicationdomain.PublicationValidation{ScopeCatalogRevision: 81, PreflightPolicyVersion: "public-https.v2"}
}
func placePublication(t *testing.T, repo *ApplicationPublicationRepository, c *publicationdomain.TestPlacementCandidate, seed decidableReview) *publicationdomain.PlaceInTestResult {
	t.Helper()
	var id *publicationdomain.ApplicationPublicationID
	if c.Publication() == nil {
		v := publicationdomain.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
		id = &v
	}
	result, err := repo.PlaceInTest(t.Context(), c, id, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), seed.adminID, publicationTestValidation(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func assertPublicationCounts(t *testing.T, db *drivermongo.Database, publications, histories int64) {
	t.Helper()
	for _, v := range []struct {
		name string
		want int64
	}{{applicationPublicationsCollectionName, publications}, {applicationPublicationHistoryCollectionName, histories}} {
		got, err := db.Collection(v.name).CountDocuments(t.Context(), bson.D{})
		if err != nil || got != v.want {
			t.Fatalf("%s count=%d error=%v want=%d", v.name, got, err, v.want)
		}
	}
}
func readPublicationApplication(t *testing.T, db *drivermongo.Database, seed decidableReview) applicationDocument {
	t.Helper()
	var d applicationDocument
	if err := db.Collection(applicationsCollectionName).FindOne(t.Context(), bson.D{{Key: "id", Value: seed.applicationID.String()}}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	return d
}
func publicationSourceFilter(collection string, seed decidableReview) bson.D {
	switch collection {
	case applicationsCollectionName:
		return bson.D{{Key: "id", Value: seed.applicationID.String()}}
	case applicationVersionsCollectionName:
		return bson.D{{Key: "versionId", Value: seed.versionID.String()}}
	default:
		return bson.D{{Key: "reviewId", Value: seed.reviewID.String()}}
	}
}
func runPublicationRace(t *testing.T, repo *ApplicationPublicationRepository, candidates []*publicationdomain.TestPlacementCandidate, seed decidableReview, want error) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, len(candidates))
	var group sync.WaitGroup
	for _, c := range candidates {
		var id *publicationdomain.ApplicationPublicationID
		if c.Publication() == nil {
			v := publicationdomain.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
			id = &v
		}
		history := publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t))
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := repo.PlaceInTest(t.Context(), c, id, history, seed.adminID, publicationTestValidation(), time.Now())
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, want) {
			conflict++
		} else {
			t.Errorf("unexpected race error: %v", err)
		}
	}
	if success != 1 || conflict != len(candidates)-1 {
		t.Fatalf("success=%d conflict=%d", success, conflict)
	}
}
func publicationMonitoredClient(t *testing.T, database, collection string) (*drivermongo.Client, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{}, 1)
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.DatabaseName == database && e.CommandName == "findAndModify" && e.Command.Lookup("findAndModify").StringValue() == collection {
			select {
			case started <- struct{}{}:
			default:
			}
		}
	}}
	client, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Disconnect(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return client, started
}

func TestApplicationPublicationMigrationIntegration(t *testing.T) {
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	seed := createApprovedPublicationVersion(t, db, "publication-schema")
	repo := NewApplicationPublicationRepository(db)
	result := placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed)
	base := applicationPublicationToDocument(result.Publication())
	history := applicationPublicationHistoryToDocument(result.History())
	t.Run("BR-PUB-002 BR-PUB-007 unique partition IDs and history revision", func(t *testing.T) {
		for _, test := range []struct {
			name, collection, index string
			document                any
		}{
			{"publication ID", applicationPublicationsCollectionName, publicationIDUniqueIndexName, func() any { d := base; d.RPCAPIMajor = 2; return d }()},
			{"partition", applicationPublicationsCollectionName, publicationPartitionUniqueIndexName, func() any { d := base; d.PublicationID = nextIntegrationApplicationReviewID(t).String(); return d }()},
			{"history ID", applicationPublicationHistoryCollectionName, publicationHistoryIDUniqueIndexName, func() any { d := history; d.PublicationID = nextIntegrationApplicationReviewID(t).String(); return d }()},
			{"history revision", applicationPublicationHistoryCollectionName, publicationHistoryRevisionUniqueIndexName, func() any { d := history; d.HistoryID = nextIntegrationApplicationReviewID(t).String(); return d }()},
		} {
			t.Run(test.name, func(t *testing.T) {
				_, err := db.Collection(test.collection).InsertOne(t.Context(), test.document)
				if !drivermongo.IsDuplicateKeyError(err) || !strings.Contains(err.Error(), test.index) {
					t.Fatalf("error=%v want duplicate %s", err, test.index)
				}
			})
		}
	})
	t.Run("BR-PUB-002 BR-PUB-006 BR-PUB-007 validators reject incomplete and sentinel state", func(t *testing.T) {
		for _, test := range []struct {
			name, collection string
			base             any
			key              string
			value            any
			omit             bool
		}{
			{"missing test pointer", applicationPublicationsCollectionName, base, "testVersionId", nil, true},
			{"empty test pointer", applicationPublicationsCollectionName, base, "testVersionId", "", false},
			{"sentinel test pointer", applicationPublicationsCollectionName, base, "testVersionId", "-1", false},
			{"zero major", applicationPublicationsCollectionName, base, "rpcApiMajor", int32(0), false},
			{"zero revision", applicationPublicationsCollectionName, base, "revision", int64(0), false},
			{"missing audit", applicationPublicationsCollectionName, base, "updatedAt", nil, true},
			{"invalid history action", applicationPublicationHistoryCollectionName, history, "action", "CLEAR_TEST_VERSION", false},
			{"missing approved review", applicationPublicationHistoryCollectionName, history, "approvedReviewId", nil, true},
			{"missing catalog revision", applicationPublicationHistoryCollectionName, history, "scopeCatalogRevision", nil, true},
			{"zero catalog revision", applicationPublicationHistoryCollectionName, history, "scopeCatalogRevision", int64(0), false},
			{"invalid policy", applicationPublicationHistoryCollectionName, history, "preflightPolicyVersion", "space value", false},
			{"initial history previous pointer", applicationPublicationHistoryCollectionName, history, "previousVersionId", nextIntegrationApplicationReviewID(t).String(), false},
		} {
			t.Run(test.name, func(t *testing.T) {
				data, err := bson.Marshal(test.base)
				if err != nil {
					t.Fatal(err)
				}
				var d bson.M
				if err = bson.Unmarshal(data, &d); err != nil {
					t.Fatal(err)
				}
				if test.omit {
					delete(d, test.key)
				} else {
					d[test.key] = test.value
				}
				if test.collection == applicationPublicationsCollectionName {
					d["publicationId"] = nextIntegrationApplicationReviewID(t).String()
					d["applicationId"] = nextIntegrationApplicationReviewID(t).String()
				} else {
					d["historyId"] = nextIntegrationApplicationReviewID(t).String()
					d["publicationId"] = nextIntegrationApplicationReviewID(t).String()
				}
				_, err = db.Collection(test.collection).InsertOne(t.Context(), d)
				var serverError drivermongo.ServerError
				if !errors.As(err, &serverError) || !serverError.HasErrorCode(121) {
					t.Fatalf("error=%v want document validation failure", err)
				}
			})
		}
	})
}

func readPublicationSourceFence(t *testing.T, db *drivermongo.Database, collection string, seed decidableReview) int64 {
	t.Helper()
	var document struct {
		Revision int64 `bson:"publicationCoordinationRevision"`
	}
	if err := db.Collection(collection).FindOne(t.Context(), publicationSourceFilter(collection, seed)).Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document.Revision
}
