package mongo

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	applicationdomain "iwut-app-center/internal/application/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
	reviewport "iwut-app-center/internal/review/port"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"
)

var abandonmentIDCounter atomic.Uint64

func integrationAbandonmentFactory(t *testing.T, app shared.ApplicationID, version versiondomain.ApplicationVersionID, revision int64, admin shared.AuthID, at time.Time) versionport.ApplicationVersionAbandonmentFactory {
	t.Helper()
	id := versiondomain.ApplicationVersionAbandonmentID(fmt.Sprintf("01990000-0000-7000-8000-%012x", abandonmentIDCounter.Add(1)))
	return func(sequence versiondomain.VersionSequence, label versiondomain.VersionLabel) (*versiondomain.ApplicationVersionAbandonment, error) {
		return versiondomain.NewApplicationVersionAbandonment(id, app, version, sequence, label, revision, admin, at)
	}
}

func TestApplicationVersionAbandonmentRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-VER-026-030 atomically abandons and permits label reuse without identity reuse", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-abandon", "abandon-success")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(t.Context(), "auth-abandon", integrationApplicationVersionDraft(t, application.ID(), "auth-abandon", "reusable label"))
		if err != nil {
			t.Fatal(err)
		}
		at := time.Date(2026, 10, 10, 8, 9, 10, 0, time.UTC)
		result, err := repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-abandon", 1, integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-abandon", at))
		if err != nil || result == nil || !result.Changed || result.Abandonment.AbandonedBy() != "auth-abandon" {
			t.Fatalf("AbandonDraft() = (%v,%v)", result, err)
		}
		assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
		assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 0)
		assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
		assertApplicationCoordinationRevision(t, database, application.ID(), 1)

		var raw bson.M
		if err := database.Collection(applicationVersionAbandonmentsCollectionName).FindOne(t.Context(), bson.M{"versionId": created.ID().String()}).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"launchUrl", "requiredCapabilities", "requiredScopes", "optionalScopes", "oauthRedirects", "createdBy", "updatedBy"} {
			if _, ok := raw[forbidden]; ok {
				t.Fatalf("tombstone exposes %s: %#v", forbidden, raw)
			}
		}

		noOp, err := repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-abandon", 1, func(versiondomain.VersionSequence, versiondomain.VersionLabel) (*versiondomain.ApplicationVersionAbandonment, error) {
			t.Fatal("no-op invoked audit factory")
			return nil, nil
		})
		if err != nil || noOp == nil || noOp.Changed || noOp.Abandonment.ID() != result.Abandonment.ID() {
			t.Fatalf("no-op=(%v,%v)", noOp, err)
		}
		assertApplicationCoordinationRevision(t, database, application.ID(), 1)
		if _, err := repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-abandon", 2, integrationAbandonmentFactory(t, application.ID(), created.ID(), 2, "auth-abandon", at)); !errors.Is(err, versionport.ErrApplicationVersionRevisionConflict) {
			t.Fatalf("stale retry error=%v", err)
		}

		recreated, err := repository.CreateDraft(t.Context(), "auth-abandon", integrationApplicationVersionDraft(t, application.ID(), "auth-abandon", "reusable label"))
		if err != nil {
			t.Fatal(err)
		}
		if recreated.ID() == created.ID() || recreated.Sequence() != 2 {
			t.Fatalf("recreated identity=%s sequence=%d", recreated.ID(), recreated.Sequence())
		}
		assertNextVersionSequence(t, database, application.ID(), 3)
	})

	t.Run("BR-VER-027 reviewed draft and non-draft are never deleted", func(t *testing.T) {
		for index, test := range []struct {
			name, status string
			addReview    bool
		}{
			{"submitted", "SUBMITTED", false},
			{"approved", "APPROVED", false},
			{"rejected", "REJECTED", false},
			{"restored draft", "DRAFT", true},
		} {
			t.Run(test.name, func(t *testing.T) {
				database := reusableIntegrationDatabase(t, client)
				application := createVersionTestApplication(t, database, "auth-reviewed", fmt.Sprintf("reviewed_%d", index))
				repository := NewApplicationVersionRepository(database)
				created, err := repository.CreateDraft(t.Context(), "auth-reviewed", integrationApplicationVersionDraft(t, application.ID(), "auth-reviewed", "v1"))
				if err != nil {
					t.Fatal(err)
				}
				if test.status != "DRAFT" {
					_, err = database.Collection(applicationVersionsCollectionName).UpdateOne(t.Context(), bson.M{"versionId": created.ID().String()}, bson.M{"$set": bson.M{"reviewStatus": test.status}}, options.UpdateOne().SetBypassDocumentValidation(true))
					if err != nil {
						t.Fatal(err)
					}
				}
				if test.addReview {
					_, err = database.Collection(applicationReviewsCollectionName).InsertOne(t.Context(), bson.M{"reviewId": "01990000-0000-7000-8000-000000000101", "applicationId": application.ID().String(), "versionId": created.ID().String()}, options.InsertOne().SetBypassDocumentValidation(true))
					if err != nil {
						t.Fatal(err)
					}
				}
				_, err = repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-reviewed", 1, integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-reviewed", time.Now()))
				if !errors.Is(err, versionport.ErrApplicationVersionNotAbandonable) {
					t.Fatalf("error=%v", err)
				}
				assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
				assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 1)
				assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 0)
			})
		}
	})

	t.Run("BR-VER-026 authorization and lifecycle gates", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		repository := NewApplicationVersionRepository(database)
		application := createVersionTestApplication(t, database, "auth-gates", "abandon-gates")
		created, err := repository.CreateDraft(t.Context(), "auth-gates", integrationApplicationVersionDraft(t, application.ID(), "auth-gates", "v1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-other", 1, integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-other", time.Now())); !errors.Is(err, versionport.ErrApplicationVersionNotFound) {
			t.Fatalf("non-admin error=%v", err)
		}
		if _, err = database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": application.ID().String()}, bson.M{"$set": bson.M{
			"platformAvailabilityStatus": "SUSPENDED", "archiveStatus": "ARCHIVED",
		}}); err != nil {
			t.Fatal(err)
		}
		if _, err = repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-gates", 1, integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-gates", time.Now())); err != nil {
			t.Fatalf("suspended and archived abandonment=%v", err)
		}

		for index, lifecycle := range []string{"CLOSING", "CLOSED"} {
			admin := shared.AuthID(fmt.Sprintf("auth-%s", lifecycle))
			blocked := createVersionTestApplication(t, database, admin.String(), fmt.Sprintf("abandon-blocked-%d", index))
			blockedDraft, createErr := repository.CreateDraft(t.Context(), admin, integrationApplicationVersionDraft(t, blocked.ID(), admin.String(), "v1"))
			if createErr != nil {
				t.Fatal(createErr)
			}
			if _, err = database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": blocked.ID().String()}, bson.M{"$set": bson.M{"lifecycleStatus": lifecycle}}); err != nil {
				t.Fatal(err)
			}
			if _, err = repository.AbandonDraft(t.Context(), blocked.ID(), blockedDraft.ID(), admin, 1, integrationAbandonmentFactory(t, blocked.ID(), blockedDraft.ID(), 1, admin, time.Now())); !errors.Is(err, versionport.ErrVersionAbandonmentNotAllowed) {
				t.Fatalf("%s application error=%v", lifecycle, err)
			}
		}
		assertCollectionCount(t, database, applicationVersionsCollectionName, 2)
	})

	t.Run("BR-VER-031 missing dependency and tombstone overlap fail closed", func(t *testing.T) {
		for index, test := range []struct {
			name    string
			corrupt func(*testing.T, *ApplicationVersionRepository, shared.ApplicationID, *versiondomain.ApplicationVersion)
		}{
			{"missing OAuth", func(t *testing.T, r *ApplicationVersionRepository, _ shared.ApplicationID, v *versiondomain.ApplicationVersion) {
				if _, err := r.database.Collection(applicationVersionOAuthConfigsCollectionName).DeleteOne(t.Context(), bson.M{"applicationVersionId": v.ID().String()}); err != nil {
					t.Fatal(err)
				}
			}},
			{"active tombstone overlap", func(t *testing.T, r *ApplicationVersionRepository, app shared.ApplicationID, v *versiondomain.ApplicationVersion) {
				fact, _ := integrationAbandonmentFactory(t, app, v.ID(), 1, "auth-corrupt", time.Now())(v.Sequence(), v.VersionLabel())
				doc, _ := applicationVersionAbandonmentToDocument(fact)
				if _, err := r.database.Collection(applicationVersionAbandonmentsCollectionName).InsertOne(t.Context(), doc); err != nil {
					t.Fatal(err)
				}
			}},
			{"cross-application review", func(t *testing.T, r *ApplicationVersionRepository, _ shared.ApplicationID, v *versiondomain.ApplicationVersion) {
				if _, err := r.database.Collection(applicationReviewsCollectionName).InsertOne(t.Context(), bson.M{
					"reviewId": "01990000-0000-7000-8000-000000000102", "applicationId": "01990000-0000-7000-8000-000000000103", "versionId": v.ID().String(),
				}, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}},
			{"publication history reference", func(t *testing.T, r *ApplicationVersionRepository, _ shared.ApplicationID, v *versiondomain.ApplicationVersion) {
				if _, err := r.database.Collection(applicationPublicationHistoryCollectionName).InsertOne(t.Context(), bson.M{
					"eventId": "01990000-0000-7000-8000-000000000104", "previousVersionId": v.ID().String(),
				}, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}},
			{"current publication reference", func(t *testing.T, r *ApplicationVersionRepository, app shared.ApplicationID, v *versiondomain.ApplicationVersion) {
				if _, err := r.database.Collection(applicationPublicationsCollectionName).InsertOne(t.Context(), bson.M{
					"applicationId": app.String(), "stableVersionId": v.ID().String(),
				}, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
					t.Fatal(err)
				}
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				database := reusableIntegrationDatabase(t, client)
				application := createVersionTestApplication(t, database, "auth-corrupt", fmt.Sprintf("corrupt_%d", index))
				repository := NewApplicationVersionRepository(database)
				created, err := repository.CreateDraft(t.Context(), "auth-corrupt", integrationApplicationVersionDraft(t, application.ID(), "auth-corrupt", "v1"))
				if err != nil {
					t.Fatal(err)
				}
				test.corrupt(t, repository, application.ID(), created)
				_, err = repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-corrupt", 1, integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-corrupt", time.Now()))
				if !errors.Is(err, versionport.ErrVersionAbandonmentStateInconsistent) {
					t.Fatalf("error=%v", err)
				}
				assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
				assertApplicationCoordinationRevision(t, database, application.ID(), 0)
			})
		}
	})

	t.Run("BR-VER-030-032 concurrent abandon writes one immutable fact", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-race", "abandon-race")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(t.Context(), "auth-race", integrationApplicationVersionDraft(t, application.ID(), "auth-race", "v1"))
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan *versiondomain.AbandonApplicationVersionDraftResult, 2)
		errorsOut := make(chan error, 2)
		var group sync.WaitGroup
		for i := 0; i < 2; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				result, err := repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-race", 1, integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-race", time.Now()))
				results <- result
				errorsOut <- err
			}()
		}
		close(start)
		group.Wait()
		close(results)
		close(errorsOut)
		var changed, unchanged int
		for err := range errorsOut {
			if err != nil {
				t.Errorf("concurrent error: %v", err)
			}
		}
		for result := range results {
			if result != nil && result.Changed {
				changed++
			} else if result != nil {
				unchanged++
			}
		}
		if changed != 1 || unchanged != 1 {
			t.Fatalf("changed/no-op=%d/%d", changed, unchanged)
		}
		assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
	})

	t.Run("BR-VER-030-032 edit and abandonment serialize on the shared fence", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-edit-race", "abandon-edit-race")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(t.Context(), "auth-edit-race", integrationApplicationVersionDraft(t, application.ID(), "auth-edit-race", "v1"))
		if err != nil {
			t.Fatal(err)
		}
		replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/v2", nil, nil, nil)
		factory := integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-edit-race", time.Now())
		start := make(chan struct{})
		replaceResult := make(chan error, 1)
		abandonResult := make(chan error, 1)
		go func() {
			<-start
			_, replaceErr := repository.ReplaceDraft(t.Context(), application.ID(), created.ID(), "auth-edit-race", 1, replacement, time.Now())
			replaceResult <- replaceErr
		}()
		go func() {
			<-start
			_, abandonErr := repository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-edit-race", 1, factory)
			abandonResult <- abandonErr
		}()
		close(start)
		replaceErr, abandonErr := <-replaceResult, <-abandonResult
		switch {
		case replaceErr == nil && errors.Is(abandonErr, versionport.ErrApplicationVersionRevisionConflict):
			stored := readApplicationVersionDocument(t, database, created.ID())
			if stored.Revision != 2 || stored.VersionLabel != "v2" {
				t.Fatalf("winning edit stored %#v", stored)
			}
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 0)
		case errors.Is(replaceErr, versionport.ErrApplicationVersionNotFound) && abandonErr == nil:
			assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
		default:
			t.Fatalf("replace/abandon results=(%v,%v)", replaceErr, abandonErr)
		}
	})

	t.Run("BR-VER-032 submission and abandonment serialize on the shared fence", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-submit-race", "abandon-submit-race")
		versionRepository := NewApplicationVersionRepository(database)
		created, err := versionRepository.CreateDraft(t.Context(), "auth-submit-race", integrationApplicationVersionDraft(t, application.ID(), "auth-submit-race", "v1"))
		if err != nil {
			t.Fatal(err)
		}
		reviewRepository := NewApplicationReviewRepository(database)
		candidate, err := reviewRepository.LoadSubmissionCandidate(t.Context(), application.ID(), reviewdomain.ApplicationVersionID(created.ID().String()), "auth-submit-race", 1)
		if err != nil {
			t.Fatal(err)
		}
		reviewID := nextIntegrationApplicationReviewID(t)
		factory := integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, "auth-submit-race", time.Now())
		start := make(chan struct{})
		submitResult := make(chan error, 1)
		abandonResult := make(chan error, 1)
		go func() {
			<-start
			_, submitErr := reviewRepository.Submit(t.Context(), candidate, reviewID, "auth-submit-race", 1, "v1", time.Now())
			submitResult <- submitErr
		}()
		go func() {
			<-start
			_, abandonErr := versionRepository.AbandonDraft(t.Context(), application.ID(), created.ID(), "auth-submit-race", 1, factory)
			abandonResult <- abandonErr
		}()
		close(start)
		submitErr, abandonErr := <-submitResult, <-abandonResult
		switch {
		case submitErr == nil && (errors.Is(abandonErr, versionport.ErrApplicationVersionNotAbandonable) || errors.Is(abandonErr, versionport.ErrApplicationVersionRevisionConflict)):
			assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 1)
			assertCollectionCount(t, database, applicationReviewsCollectionName, 1)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 0)
		case errors.Is(submitErr, reviewport.ErrApplicationVersionNotFound) && abandonErr == nil:
			assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 0)
			assertCollectionCount(t, database, applicationReviewsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
		default:
			t.Fatalf("submit/abandon results=(%v,%v)", submitErr, abandonErr)
		}
	})

	t.Run("BR-VER-032 administrator transfer and abandonment serialize on the shared fence", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		const oldAdmin, newAdmin = "auth-transfer-race-old", "auth-transfer-race-new"
		application := createVersionTestApplication(t, database, oldAdmin, "abandon-transfer-race")
		versionRepository := NewApplicationVersionRepository(database)
		created, err := versionRepository.CreateDraft(t.Context(), oldAdmin, integrationApplicationVersionDraft(t, application.ID(), oldAdmin, "v1"))
		if err != nil {
			t.Fatal(err)
		}
		transferRepository := NewApplicationAdminTransferRepository(database, transferSecretFactory{}, 100)
		transferID, _ := applicationdomain.ParseApplicationAdminTransferID("01990000-0000-7000-8000-000000000201")
		now := time.Now().UTC().Truncate(time.Millisecond)
		if _, err = transferRepository.Initiate(t.Context(), application.ID(), oldAdmin, newAdmin, 1, transferID, now); err != nil {
			t.Fatal(err)
		}
		factory := integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, oldAdmin, now)
		start := make(chan struct{})
		transferResult := make(chan error, 1)
		abandonResult := make(chan error, 1)
		go func() {
			<-start
			_, acceptErr := transferRepository.Accept(t.Context(), transferID, newAdmin, applicationdomain.ConfidentialCredentialKeep, now.Add(time.Second))
			transferResult <- acceptErr
		}()
		go func() {
			<-start
			_, abandonErr := versionRepository.AbandonDraft(t.Context(), application.ID(), created.ID(), oldAdmin, 1, factory)
			abandonResult <- abandonErr
		}()
		close(start)
		acceptErr, abandonErr := <-transferResult, <-abandonResult
		if acceptErr != nil {
			t.Fatalf("accept transfer=%v", acceptErr)
		}
		var stored applicationDocument
		if err := database.Collection(applicationsCollectionName).FindOne(t.Context(), bson.M{"id": application.ID().String()}).Decode(&stored); err != nil || stored.AdminID != newAdmin {
			t.Fatalf("transferred application=%#v err=%v", stored, err)
		}
		switch {
		case abandonErr == nil:
			assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
			var fact applicationVersionAbandonmentDocument
			if err := database.Collection(applicationVersionAbandonmentsCollectionName).FindOne(t.Context(), bson.M{"versionId": created.ID().String()}).Decode(&fact); err != nil || fact.AbandonedBy != oldAdmin {
				t.Fatalf("abandonment executor=%q err=%v", fact.AbandonedBy, err)
			}
		case errors.Is(abandonErr, versionport.ErrApplicationVersionNotFound):
			assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 0)
		default:
			t.Fatalf("abandon after transfer=%v", abandonErr)
		}
	})

	t.Run("BR-VER-032 application closure and abandonment serialize on the shared fence", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		const admin = "auth-close-race"
		application := createVersionTestApplication(t, database, admin, "abandon-close-race")
		versionRepository := NewApplicationVersionRepository(database)
		created, err := versionRepository.CreateDraft(t.Context(), admin, integrationApplicationVersionDraft(t, application.ID(), admin, "v1"))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		closureID, _ := applicationdomain.ParseApplicationClosureID("01990000-0000-7000-8000-000000000202")
		proof := applicationdomain.ApplicationCloseProof{
			Subject: admin, ApplicationID: application.ID(), JTI: "01990000-0000-7000-8000-000000000203",
			AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute),
		}
		factory := integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, admin, now)
		start := make(chan struct{})
		closureResult := make(chan error, 1)
		abandonResult := make(chan error, 1)
		go func() {
			<-start
			_, closeErr := NewApplicationClosureRepository(database).Start(t.Context(), application.ID(), admin, 1, 1, proof, closureID, now)
			closureResult <- closeErr
		}()
		go func() {
			<-start
			_, abandonErr := versionRepository.AbandonDraft(t.Context(), application.ID(), created.ID(), admin, 1, factory)
			abandonResult <- abandonErr
		}()
		close(start)
		closeErr, abandonErr := <-closureResult, <-abandonResult
		if closeErr != nil {
			t.Fatalf("start closure=%v", closeErr)
		}
		var stored applicationDocument
		if err := database.Collection(applicationsCollectionName).FindOne(t.Context(), bson.M{"id": application.ID().String()}).Decode(&stored); err != nil || stored.LifecycleStatus != "CLOSING" {
			t.Fatalf("closing application=%#v err=%v", stored, err)
		}
		switch {
		case abandonErr == nil:
			assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
		case errors.Is(abandonErr, versionport.ErrVersionAbandonmentNotAllowed):
			assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
			assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 0)
		default:
			t.Fatalf("abandon during closure=%v", abandonErr)
		}
	})

	t.Run("BR-VER-029-032 same-label creation and abandonment serialize with fresh identity", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		const admin = "auth-label-race"
		application := createVersionTestApplication(t, database, admin, "abandon-label-race")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(t.Context(), admin, integrationApplicationVersionDraft(t, application.ID(), admin, "v1"))
		if err != nil {
			t.Fatal(err)
		}
		newDraft := integrationApplicationVersionDraft(t, application.ID(), admin, "v1")
		factory := integrationAbandonmentFactory(t, application.ID(), created.ID(), 1, admin, time.Now())
		start := make(chan struct{})
		createdResult := make(chan *versiondomain.ApplicationVersion, 1)
		createErrors := make(chan error, 1)
		abandonResult := make(chan error, 1)
		go func() {
			<-start
			version, createErr := repository.CreateDraft(t.Context(), admin, newDraft)
			createdResult <- version
			createErrors <- createErr
		}()
		go func() {
			<-start
			_, abandonErr := repository.AbandonDraft(t.Context(), application.ID(), created.ID(), admin, 1, factory)
			abandonResult <- abandonErr
		}()
		close(start)
		recreated, createErr, abandonErr := <-createdResult, <-createErrors, <-abandonResult
		if abandonErr != nil {
			t.Fatalf("abandon=%v", abandonErr)
		}
		assertCollectionCount(t, database, applicationVersionAbandonmentsCollectionName, 1)
		switch {
		case createErr == nil:
			if recreated == nil || recreated.ID() == created.ID() || recreated.Sequence() != 2 || recreated.VersionLabel() != created.VersionLabel() {
				t.Fatalf("recreated version=%#v", recreated)
			}
			assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 1)
			assertNextVersionSequence(t, database, application.ID(), 3)
		case errors.Is(createErr, versionport.ErrApplicationVersionLabelAlreadyExists):
			assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
			assertCollectionCount(t, database, applicationVersionOAuthConfigsCollectionName, 0)
			assertNextVersionSequence(t, database, application.ID(), 2)
		default:
			t.Fatalf("same-label creation=%v", createErr)
		}
	})

	t.Run("BR-VER-028-031 migration installs strict validator and unique identity indexes", func(t *testing.T) {
		database := reusableIntegrationDatabase(t, client)
		bad := bson.M{
			"abandonmentId": "01990000-0000-7000-8000-000000000301", "applicationId": "01990000-0000-7000-8000-000000000302",
			"versionId": "01990000-0000-7000-8000-000000000303", "sequence": int32(1), "versionLabel": "v1",
			"sourceRevision": int64(1), "abandonedBy": "auth", "abandonedAt": time.Now(), "extra": "forbidden",
		}
		if _, err := database.Collection(applicationVersionAbandonmentsCollectionName).InsertOne(t.Context(), bad); err == nil {
			t.Fatal("strict validator accepted an extra field")
		}
		cursor, err := database.Collection(applicationVersionAbandonmentsCollectionName).Indexes().List(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var indexes []bson.M
		if err := cursor.All(t.Context(), &indexes); err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, index := range indexes {
			if name, ok := index["name"].(string); ok {
				names[name] = true
			}
		}
		for _, name := range []string{applicationVersionAbandonmentIDIndexName, applicationVersionAbandonmentVersionIndexName, applicationVersionAbandonmentSequenceIndexName, applicationVersionAbandonmentAuditIndexName} {
			if !names[name] {
				t.Fatalf("missing index %s: %v", name, names)
			}
		}
	})
}
