package mongo

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

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
	now := time.Date(2026, 11, 11, 15, 0, 0, 123456789, time.FixedZone("UTC+08", 8*60*60))
	wantStartedAt := time.Date(2026, 11, 11, 7, 0, 0, 123000000, time.UTC)
	assertStartedAt := func(label string, got time.Time) {
		t.Helper()
		if !got.Equal(wantStartedAt) || got.Location() != time.UTC {
			t.Errorf("%s = %s, want %s", label, got.Format(time.RFC3339Nano), wantStartedAt.Format(time.RFC3339Nano))
		}
	}
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
	if _, err := repository.Start(ctx, application.ID(), application.AdminID(), 2, 1, proof, closureID, now); !errors.Is(err, applicationdomain.ErrApplicationOwnershipChanged) {
		t.Fatalf("stale ownership Start() error = %v", err)
	}
	if _, err := repository.Start(ctx, application.ID(), application.AdminID(), 1, 2, proof, closureID, now); !errors.Is(err, applicationdomain.ErrApplicationLifecycleChanged) {
		t.Fatalf("stale lifecycle Start() error = %v", err)
	}
	assertQuota(t, database, application.AdminID().String(), 10, 1, 1)
	closure, err := repository.Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, closureID, now)
	if err != nil || closure.Status != applicationdomain.ApplicationClosureClosing || closure.LifecycleRevision != 2 {
		t.Fatalf("Start() = %#v, %v", closure, err)
	}
	assertStartedAt("Start closingStartedAt", closure.ClosingStartedAt)
	readback, err := repository.Get(ctx, application.ID(), application.AdminID())
	if err != nil || readback.ClosureID != closure.ClosureID || readback.LifecycleRevision != closure.LifecycleRevision {
		t.Fatalf("Get after Start() = %#v, %v", readback, err)
	}
	if !readback.ClosingStartedAt.Equal(closure.ClosingStartedAt) {
		t.Errorf("Start and Get closingStartedAt differ: %s, %s", closure.ClosingStartedAt.Format(time.RFC3339Nano), readback.ClosingStartedAt.Format(time.RFC3339Nano))
	}
	assertStartedAt("Get closingStartedAt", readback.ClosingStartedAt)
	assertQuota(t, database, application.AdminID().String(), 10, 0, 2)
	var storedClosure applicationClosureDocument
	if err := database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"closureId": closureID.String()}).Decode(&storedClosure); err != nil {
		t.Fatal(err)
	}
	assertStartedAt("stored closingStartedAt", storedClosure.ClosingStartedAt)
	if storedClosure.NextAttemptAt == nil || !storedClosure.NextAttemptAt.Equal(wantStartedAt.Add(time.Second)) {
		t.Errorf("nextAttemptAt = %v, want %s", storedClosure.NextAttemptAt, wantStartedAt.Add(time.Second))
	}
	var quota applicationCreationQuotaDocument
	if err := database.Collection(applicationCreationQuotasCollectionName).FindOne(ctx, bson.M{"adminId": application.AdminID().String()}).Decode(&quota); err != nil {
		t.Fatal(err)
	}
	assertStartedAt("quota updatedAt", quota.UpdatedAt)

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
	if transfer.ResolvedAt == nil {
		t.Fatal("cancelled transfer has no resolvedAt")
	}
	assertStartedAt("transfer resolvedAt", *transfer.ResolvedAt)
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
	if storedLink.RevokedAt == nil {
		t.Fatal("revoked link has no revokedAt")
	}
	assertStartedAt("join link revokedAt", *storedLink.RevokedAt)
	var registration applicationOAuthRegistrationDocument
	if err := database.Collection(applicationOAuthRegistrationsCollectionName).FindOne(ctx, bson.M{"applicationId": application.ID().String(), "channel": "TEST"}).Decode(&registration); err != nil || registration.PublicClient.Status != "DISABLED" || registration.PublicClient.AuthorizationEpoch != 2 || registration.ConfidentialClient.Status != "DISABLED" || registration.ConfidentialClient.AuthorizationEpoch != 2 || registration.RegistrationRevision != 3 {
		t.Fatalf("registration = %#v err=%v", registration, err)
	}
	assertStartedAt("OAuth updatedAt", registration.UpdatedAt)
	assertStartedAt("public client statusUpdatedAt", registration.PublicClient.StatusUpdatedAt)
	assertStartedAt("confidential client statusUpdatedAt", registration.ConfidentialClient.StatusUpdatedAt)
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
	assertStartedAt("repeated Start closingStartedAt", retry.ClosingStartedAt)
	if !retry.ClosingStartedAt.Equal(closure.ClosingStartedAt) || retry.LifecycleRevision != closure.LifecycleRevision {
		t.Errorf("idempotent Start changed the original result: first=%#v retry=%#v", closure, retry)
	}
	freshProof := proof
	freshProof.JTI = "01890f47-0000-7000-8000-000000002705"
	if _, err = repository.Start(ctx, application.ID(), application.AdminID(), 1, 2, freshProof, "01890f47-0000-7000-8000-000000002706", now.Add(time.Second)); !errors.Is(err, applicationdomain.ErrApplicationLifecycleChanged) {
		t.Fatalf("fresh proof against closing application error = %v", err)
	}
	appliedAt := wantStartedAt.Add(2 * time.Second)
	closed, err := repository.Complete(ctx, closureID, "auth-receipt-2701", appliedAt)
	if err != nil || closed.Status != applicationdomain.ApplicationClosureClosed || closed.LifecycleRevision != 3 {
		t.Fatalf("Complete() = %#v, %v", closed, err)
	}
	if _, err = repository.Complete(ctx, closureID, "different", appliedAt); err == nil {
		t.Fatal("mismatched terminal receipt accepted")
	}
}

func TestApplicationClosureRepositoryIntegration_BR_APP_026_028_CompleteTimeMatchesReadbackAndRetry(t *testing.T) {
	database := migratedIntegrationDatabase(t, integrationClient(t))
	ctx := t.Context()
	application := integrationApplication(t, "auth-close-applied-time", "close_applied_time")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationClosureRepository(database)
	startedAt := time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)
	closureID := applicationdomain.ApplicationClosureID("01890f47-0000-7000-8000-000000002740")
	proof := applicationdomain.ApplicationCloseProof{Subject: application.AdminID(), ApplicationID: application.ID(), JTI: "01890f47-0000-7000-8000-000000002741", AuthTime: startedAt, IssuedAt: startedAt, ExpiresAt: startedAt.Add(5 * time.Minute)}
	if _, err := repository.Start(ctx, application.ID(), application.AdminID(), 1, 1, proof, closureID, startedAt); err != nil {
		t.Fatal(err)
	}
	appliedAt := time.Date(2026, 11, 11, 2, 0, 2, 987654321, time.FixedZone("UTC-05", -5*60*60))
	wantAppliedAt := time.Date(2026, 11, 11, 7, 0, 2, 987000000, time.UTC)
	assertClosed := func(label string, closure applicationdomain.ApplicationClosure) {
		t.Helper()
		if closure.ClosureID != closureID || closure.Status != applicationdomain.ApplicationClosureClosed || closure.AuthRevocationState != applicationdomain.AuthRevocationApplied || closure.AuthReceiptID != "auth-receipt-2740" || closure.LifecycleRevision != 3 || !closure.ClosingStartedAt.Equal(startedAt) {
			t.Errorf("%s = %#v", label, closure)
		}
		for field, value := range map[string]*time.Time{"authAppliedAt": closure.AuthAppliedAt, "closedAt": closure.ClosedAt} {
			if value == nil || !value.Equal(wantAppliedAt) || value.Location() != time.UTC {
				t.Errorf("%s %s = %v, want %s", label, field, value, wantAppliedAt.Format(time.RFC3339Nano))
			}
		}
	}
	closed, err := repository.Complete(ctx, closureID, "auth-receipt-2740", appliedAt)
	if err != nil {
		t.Fatal(err)
	}
	assertClosed("Complete", closed)
	readback, err := repository.Get(ctx, application.ID(), application.AdminID())
	if err != nil {
		t.Fatal(err)
	}
	assertClosed("Get after Complete", readback)
	if closed.AuthAppliedAt == nil || readback.AuthAppliedAt == nil || !closed.AuthAppliedAt.Equal(*readback.AuthAppliedAt) || closed.ClosedAt == nil || readback.ClosedAt == nil || !closed.ClosedAt.Equal(*readback.ClosedAt) {
		t.Errorf("Complete and Get times differ: complete=%#v get=%#v", closed, readback)
	}
	repeated, err := repository.Complete(ctx, closureID, "auth-receipt-2740", appliedAt)
	if err != nil {
		t.Errorf("repeated identical Auth receipt rejected: %v", err)
	} else {
		assertClosed("repeated Complete", repeated)
	}
	for _, tc := range []struct {
		name      string
		receipt   string
		appliedAt time.Time
	}{
		{"different receipt", "different", appliedAt},
		{"different time", "auth-receipt-2740", appliedAt.Add(time.Millisecond)},
		{"missing receipt", "", appliedAt},
		{"missing time", "auth-receipt-2740", time.Time{}},
	} {
		if _, err := repository.Complete(ctx, closureID, tc.receipt, tc.appliedAt); !errors.Is(err, applicationdomain.ErrApplicationClosureStateInconsistent) {
			t.Errorf("%s Complete() error = %v", tc.name, err)
		}
	}
	final, err := repository.Get(ctx, application.ID(), application.AdminID())
	if err != nil {
		t.Fatal(err)
	}
	assertClosed("Get after repeated and invalid receipts", final)
	var stored applicationClosureDocument
	if err := database.Collection(applicationClosuresCollectionName).FindOne(ctx, bson.M{"closureId": closureID.String()}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.AuthAppliedAt == nil || !stored.AuthAppliedAt.Equal(wantAppliedAt) || stored.ClosedAt == nil || !stored.ClosedAt.Equal(wantAppliedAt) || stored.NextAttemptAt != nil {
		t.Errorf("stored completion timestamps = %#v", stored)
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
	now := time.Date(2026, 11, 11, 15, 0, 0, 123456789, time.FixedZone("UTC+08", 8*60*60))
	wantStartedAt := time.Date(2026, 11, 11, 7, 0, 0, 123000000, time.UTC)
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
		if !result.closure.ClosingStartedAt.Equal(wantStartedAt) || result.closure.ClosingStartedAt.Location() != time.UTC {
			t.Errorf("concurrent Start closingStartedAt = %s, want %s", result.closure.ClosingStartedAt.Format(time.RFC3339Nano), wantStartedAt.Format(time.RFC3339Nano))
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
	if _, err := database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.M{"id": application.ID().String()}, bson.M{"$unset": bson.M{"lifecycleStatus": "", "lifecycleRevision": "", "platformAvailabilityStatus": "", "platformAvailabilityRevision": "", "lastPlatformOperationEventId": "", "suspendedAt": "", "restoredAt": "", "archiveStatus": "", "archiveRevision": "", "lastArchiveEventId": ""}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	if err := database.RunCommand(ctx, bson.D{{Key: "collMod", Value: applicationsCollectionName}, {Key: "validator", Value: applicationOwnershipValidator()}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(migrationLedgerCollectionName).DeleteOne(ctx, bson.M{"_id": applicationClosureMigrationID}); err != nil {
		t.Fatal(err)
	}
	_, _ = database.Collection(migrationLedgerCollectionName).DeleteOne(ctx, bson.M{"_id": applicationOperationsMigrationID})
	_, _ = database.Collection(migrationLedgerCollectionName).DeleteOne(ctx, bson.M{"_id": applicationArchivalMigrationID})
	if err := NewMigrator(database).Migrate(ctx); err != nil {
		t.Fatalf("Migrate() = %v", err)
	}
	var app applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.M{"id": application.ID().String()}).Decode(&app); err != nil || app.LifecycleStatus != "ACTIVE" || app.LifecycleRevision != 1 {
		t.Fatalf("backfill = %#v, %v", app, err)
	}
}
