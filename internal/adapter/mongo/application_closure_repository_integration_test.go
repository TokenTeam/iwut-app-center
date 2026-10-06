package mongo

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	applicationdomain "iwut-app-center/internal/application/domain"
	filterport "iwut-app-center/internal/filter/port"
	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	oauthport "iwut-app-center/internal/oauthclient/port"
	"iwut-app-center/internal/shared"
)

func TestApplicationClosureRepositoryIntegration_BR_APP_020_028(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	application := integrationApplication(t, "auth-close-owner", "close_me")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}

	clientID := integrationOAuthClientID(t, 2701)
	oauthRepository := NewOAuthClientRepository(database)
	if _, err := oauthRepository.Register(ctx, application.ID(), oauthdomain.ChannelTest, oauthdomain.ClientTypePublicPKCE, nil, clientID, nil, application.AdminID(), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	confidentialClientID := integrationOAuthClientID(t, 2702)
	digest, revision := integrationOAuthDigest(27), int64(1)
	if _, err := oauthRepository.Register(ctx, application.ID(), oauthdomain.ChannelTest, oauthdomain.ClientTypeConfidentialSecret, &revision, confidentialClientID, &digest, application.AdminID(), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	disabledClientID := integrationOAuthClientID(t, 2703)
	if _, err := oauthRepository.Register(ctx, application.ID(), oauthdomain.ChannelGrey, oauthdomain.ClientTypePublicPKCE, nil, disabledClientID, nil, application.AdminID(), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := oauthRepository.SetStatus(ctx, disabledClientID, 1, oauthdomain.ClientStatusDisabled, application.AdminID(), now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(database), application.ID(), application.AdminID(), nil, newIntegrationTesterJoinLink(t, application.ID(), application.AdminID()))
	transferID, _ := applicationdomain.ParseApplicationAdminTransferID("01890f47-0000-7000-8000-000000002701")
	if _, err := NewApplicationAdminTransferRepository(database, transferSecretFactory{}, 10).Initiate(ctx, application.ID(), application.AdminID(), shared.AuthID("auth-close-target"), 1, transferID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	repository := NewApplicationClosureRepository(database)
	closureID, _ := applicationdomain.ParseApplicationClosureID("01890f47-0000-7000-8000-000000002702")
	proof := applicationdomain.ApplicationCloseProof{Subject: application.AdminID(), ApplicationID: application.ID(), JTI: "01890f47-0000-7000-8000-000000002703", AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	closure, err := repository.Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, closureID, now)
	if err != nil || closure.Status != applicationdomain.ApplicationClosureClosing || closure.LifecycleRevision != 2 {
		t.Fatalf("Start() = %#v, %v", closure, err)
	}
	assertQuota(t, database, application.AdminID().String(), 10, 0, 2)

	var app applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": application.ID().String()}).Decode(&app); err != nil || app.LifecycleStatus != "CLOSING" || app.LifecycleRevision != 2 {
		t.Fatalf("application = %#v err=%v", app, err)
	}
	if _, err := database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.M{"id": application.ID().String()}, bson.M{"$set": bson.M{"lifecycleStatus": "ACTIVE"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(ctx, application.ID(), application.AdminID()); !errors.Is(err, applicationdomain.ErrApplicationClosureStateInconsistent) {
		t.Fatalf("Get() cross-collection drift error = %v", err)
	}
	if _, err := repository.NextPending(ctx, now.Add(time.Second)); !errors.Is(err, applicationdomain.ErrApplicationClosureStateInconsistent) {
		t.Fatalf("NextPending() cross-collection drift error = %v", err)
	}
	if _, err := database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.M{"id": application.ID().String()}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSING"}}); err != nil {
		t.Fatal(err)
	}
	var transfer applicationAdminTransferDocument
	if err := database.Collection(applicationAdminTransfersCollectionName).FindOne(ctx, bson.M{"transferId": transferID.String()}).Decode(&transfer); err != nil || transfer.Status != "CANCELLED" || transfer.ResolutionCause == nil || *transfer.ResolutionCause != "APPLICATION_CLOSURE" {
		t.Fatalf("transfer = %#v err=%v", transfer, err)
	}
	transferRepository := NewApplicationAdminTransferRepository(database, transferSecretFactory{}, 10)
	if _, err = transferRepository.Cancel(ctx, transferID, application.AdminID(), now.Add(time.Second)); !errors.Is(err, applicationdomain.ErrApplicationNotFound) {
		t.Fatalf("cancel closure-resolved transfer error = %v", err)
	}
	if _, err = transferRepository.Reject(ctx, transferID, shared.AuthID("auth-close-target"), now.Add(time.Second)); !errors.Is(err, applicationdomain.ErrApplicationNotFound) {
		t.Fatalf("reject closure-resolved transfer error = %v", err)
	}
	if _, err = transferRepository.Accept(ctx, transferID, shared.AuthID("auth-close-target"), applicationdomain.ConfidentialCredentialKeep, now.Add(time.Second)); !errors.Is(err, applicationdomain.ErrApplicationNotFound) {
		t.Fatalf("accept closure-resolved transfer error = %v", err)
	}
	storedLink := readTesterJoinLinkDocument(t, database, link.JoinLink().JoinLinkID())
	if storedLink.Status != "REVOKED" || storedLink.RevocationReason == nil || *storedLink.RevocationReason != "APPLICATION_CLOSURE" {
		t.Fatalf("link = %#v", storedLink)
	}
	var registration applicationOAuthRegistrationDocument
	if err := database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(ctx, bson.M{"applicationId": application.ID().String(), "channel": "TEST"}).Decode(&registration); err != nil || registration.PublicClient.Status != "DISABLED" || registration.PublicClient.AuthorizationEpoch != 2 || registration.ConfidentialClient.Status != "DISABLED" || registration.ConfidentialClient.AuthorizationEpoch != 2 || registration.RegistrationRevision != 3 {
		t.Fatalf("registration = %#v err=%v", registration, err)
	}
	if err := database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(ctx, bson.M{"applicationId": application.ID().String(), "channel": "GREY"}).Decode(&registration); err != nil || registration.PublicClient.Status != "DISABLED" || registration.PublicClient.AuthorizationEpoch != 2 || registration.RegistrationRevision != 2 {
		t.Fatalf("unchanged disabled registration = %#v err=%v", registration, err)
	}
	provider := NewOAuthProviderRepository(database, nil)
	if _, err = provider.GetClientConfiguration(ctx, clientID); !errors.Is(err, oauthport.ErrApplicationNotFound) {
		t.Fatalf("closed client configuration error = %v", err)
	}
	if verified, _, verifyErr := provider.VerifyClientSecret(ctx, confidentialClientID, "irrelevant", 1); verifyErr != nil || verified {
		t.Fatalf("closed secret verification = %v, %v", verified, verifyErr)
	}
	if _, err = provider.ResolveRuntime(ctx, clientID, oauthdomain.ChannelTest, 1, 2, now); !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("closed runtime error = %v", err)
	}
	if _, err = provider.ResolveAuthorizationContext(ctx, clientID, "tester", oauthdomain.ChannelTest, 1, 2, oauthdomain.RuntimeVersion{}, now); !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("closed authorization context error = %v", err)
	}
	if _, err = provider.GetPublishedRedirects(ctx, application.ID(), now); !errors.Is(err, oauthport.ErrApplicationNotFound) {
		t.Fatalf("closed published redirects error = %v", err)
	}

	retry, err := repository.Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, "01890f47-0000-7000-8000-000000002704", now.Add(time.Second))
	if err != nil || retry.ClosureID != closureID {
		t.Fatalf("idempotent Start() = %#v, %v", retry, err)
	}
	freshProof := proof
	freshProof.JTI = "01890f47-0000-7000-8000-000000002705"
	if _, err = repository.Start(ctx, application.ID(), application.AdminID(), 1, 2, freshProof, "01890f47-0000-7000-8000-000000002706", now.Add(time.Second)); !errors.Is(err, applicationdomain.ErrApplicationLifecycleChanged) {
		t.Fatalf("fresh proof against closing application error = %v", err)
	}
	appliedAt := now.Add(2 * time.Second)
	closed, err := repository.Complete(ctx, closureID, "auth-receipt-2701", appliedAt)
	if err != nil || closed.Status != applicationdomain.ApplicationClosureClosed || closed.LifecycleRevision != 3 {
		t.Fatalf("Complete() = %#v, %v", closed, err)
	}
	if _, err = repository.Complete(ctx, closureID, "different", appliedAt); err == nil {
		t.Fatal("mismatched terminal receipt accepted")
	}
}

func TestApplicationClosureRepositoryIntegration_RollsBackOverflowedOAuthShutdown(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	application := integrationApplication(t, "auth-close-overflow", "close_overflow")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	clientID := integrationOAuthClientID(t, 2710)
	if _, err := NewOAuthClientRepository(database).Register(ctx, application.ID(), oauthdomain.ChannelTest, oauthdomain.ClientTypePublicPKCE, nil, clientID, nil, application.AdminID(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(applicationOAuthRegistrationsCollectionName).UpdateOne(ctx, bson.M{"applicationId": application.ID().String()}, bson.M{"$set": bson.M{"publicClient.authorizationEpoch": int64(^uint64(0) >> 1)}}); err != nil {
		t.Fatal(err)
	}
	closureID, _ := applicationdomain.ParseApplicationClosureID("01890f47-0000-7000-8000-000000002710")
	proof := applicationdomain.ApplicationCloseProof{Subject: application.AdminID(), ApplicationID: application.ID(), JTI: "01890f47-0000-7000-8000-000000002711", AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if _, err := NewApplicationClosureRepository(database).Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, closureID, now); !errors.Is(err, applicationdomain.ErrApplicationClosureStateInconsistent) {
		t.Fatalf("Start() error = %v", err)
	}
	var stored applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": application.ID().String()}).Decode(&stored); err != nil || stored.LifecycleStatus != "ACTIVE" || stored.LifecycleRevision != 1 {
		t.Fatalf("application after rollback = %#v, %v", stored, err)
	}
	assertQuota(t, database, application.AdminID().String(), 10, 1, 1)
	if count, err := database.Collection(applicationClosuresCollectionName).CountDocuments(ctx, bson.M{"applicationId": application.ID().String()}); err != nil || count != 0 {
		t.Fatalf("closure count after rollback = %d, %v", count, err)
	}
}

func TestApplicationClosureRepositoryIntegration_ConcurrentSameProofIsIdempotent(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	application := integrationApplication(t, "auth-close-concurrent", "close_concurrent")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	proof := applicationdomain.ApplicationCloseProof{Subject: application.AdminID(), ApplicationID: application.ID(), JTI: "01890f47-0000-7000-8000-000000002720", AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	ids := []applicationdomain.ApplicationClosureID{"01890f47-0000-7000-8000-000000002721", "01890f47-0000-7000-8000-000000002722"}
	type outcome struct {
		closure applicationdomain.ApplicationClosure
		err     error
	}
	start, ready, results := make(chan struct{}), make(chan struct{}, 2), make(chan outcome, 2)
	var workers sync.WaitGroup
	for _, id := range ids {
		workers.Add(1)
		go func(closureID applicationdomain.ApplicationClosureID) {
			defer workers.Done()
			ready <- struct{}{}
			<-start
			closure, err := NewApplicationClosureRepository(database).Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, closureID, now)
			results <- outcome{closure: closure, err: err}
		}(id)
	}
	<-ready
	<-ready
	close(start)
	workers.Wait()
	close(results)
	var first applicationdomain.ApplicationClosureID
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent Start() error = %v", result.err)
		}
		if first == "" {
			first = result.closure.ClosureID
		} else if result.closure.ClosureID != first {
			t.Fatalf("closure ids = %s and %s", first, result.closure.ClosureID)
		}
	}
	assertQuota(t, database, application.AdminID().String(), 10, 0, 2)
	if count, err := database.Collection(applicationClosuresCollectionName).CountDocuments(ctx, bson.M{"applicationId": application.ID().String()}); err != nil || count != 1 {
		t.Fatalf("closure count = %d, %v", count, err)
	}
}

func TestApplicationClosureRepositoryIntegration_RacesFilterOnSharedFence(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	application := integrationApplication(t, "auth-close-filter-race", "close_filter_race")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	filterRepository := NewApplicationFilterRepository(database)
	current, err := filterRepository.LoadForAdmin(ctx, application.ID(), application.AdminID())
	if err != nil {
		t.Fatal(err)
	}
	candidate, revision := integrationFilterCandidate(t, current, "CN", application.AdminID().String(), now)
	closureID, _ := applicationdomain.ParseApplicationClosureID("01890f47-0000-7000-8000-000000002730")
	proof := applicationdomain.ApplicationCloseProof{Subject: application.AdminID(), ApplicationID: application.ID(), JTI: "01890f47-0000-7000-8000-000000002731", AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}

	type raceOutcome struct {
		kind string
		err  error
	}
	start, results := make(chan struct{}), make(chan raceOutcome, 2)
	go func() {
		<-start
		_, raceErr := NewApplicationClosureRepository(database).Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, closureID, now)
		results <- raceOutcome{kind: "closure", err: raceErr}
	}()
	go func() {
		<-start
		_, raceErr := filterRepository.Commit(ctx, application.AdminID(), 0, candidate, revision)
		results <- raceOutcome{kind: "filter", err: raceErr}
	}()
	close(start)
	var closureErr, filterErr error
	for range 2 {
		outcome := <-results
		if outcome.kind == "closure" {
			closureErr = outcome.err
		} else {
			filterErr = outcome.err
		}
	}
	if closureErr != nil {
		t.Fatalf("closure race error = %v", closureErr)
	}
	if filterErr != nil && !errors.Is(filterErr, filterport.ErrApplicationNotFound) {
		t.Fatalf("filter race error = %v", filterErr)
	}
	var app applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": application.ID().String()}).Decode(&app); err != nil || app.LifecycleStatus != "CLOSING" {
		t.Fatalf("application after race = %#v, %v", app, err)
	}
	filterCount, err := database.Collection(applicationFilterRevisionsCollectionName).CountDocuments(ctx, bson.M{"applicationId": application.ID().String()})
	if err != nil || filterErr == nil && filterCount != 1 || filterErr != nil && filterCount != 0 {
		t.Fatalf("filter after race = error:%v count:%d query:%v", filterErr, filterCount, err)
	}
}

func TestApplicationClosureMigrationIntegration_BackfillsStrict0021(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	ctx := t.Context()
	application := integrationApplication(t, "auth-close-migration", "close_migration")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	if err := database.RunCommand(ctx, bson.D{{Key: "collMod", Value: applicationsCollectionName}, {Key: "validator", Value: applicationOwnershipValidator()}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.M{"id": application.ID().String()}, bson.M{"$unset": bson.M{"lifecycleStatus": "", "lifecycleRevision": ""}}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(migrationLedgerCollectionName).DeleteOne(ctx, bson.M{"_id": applicationClosureMigrationID}); err != nil {
		t.Fatal(err)
	}
	if err := NewMigrator(database).Migrate(ctx); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	var app applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": application.ID().String()}).Decode(&app); err != nil || app.LifecycleStatus != "ACTIVE" || app.LifecycleRevision != 1 {
		t.Fatalf("backfill = %#v, %v", app, err)
	}
}
