package mongo

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"

	applicationdomain "iwut-app-center/internal/application/domain"
	oauthclientdomain "iwut-app-center/internal/oauthclient/domain"
	oauthclientport "iwut-app-center/internal/oauthclient/port"
)

func integrationOAuthClientID(t *testing.T, value int) oauthclientdomain.ClientID {
	t.Helper()
	id, err := oauthclientdomain.ParseClientID(fmt.Sprintf("123e4567-e89b-42d3-a456-%012x", value))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func integrationOAuthDigest(value byte) oauthclientdomain.SecretDigest {
	var digest [32]byte
	digest[0] = value
	return oauthclientdomain.NewSecretDigest(digest)
}

func createOAuthTestApplication(t *testing.T, database *drivermongo.Database, adminID, name string) *applicationdomain.Application {
	t.Helper()
	application := integrationApplication(t, adminID, name)
	if err := NewApplicationRepository(database).CreateWithinQuota(t.Context(), application, 10); err != nil {
		t.Fatal(err)
	}
	return application
}

func TestOAuthClientRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)

	t.Run("BR-OAC-001 BR-OAC-003 register both stable identities atomically", func(t *testing.T) {
		application := createOAuthTestApplication(t, database, "oauth-admin", "oauth-both-types")
		repository := NewOAuthClientRepository(database)
		at := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
		publicID := integrationOAuthClientID(t, 1)
		publicResult, err := repository.Register(t.Context(), application.ID(), oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypePublicPKCE, nil, publicID, nil, application.AdminID(), at)
		if err != nil || publicResult.Registration.Revision() != 1 || publicResult.Credential != nil || publicResult.Registration.PublicClient().ClientID() != publicID {
			t.Fatalf("public result=%v err=%v", publicResult, err)
		}
		expected := int64(1)
		confidentialID := integrationOAuthClientID(t, 2)
		digest := integrationOAuthDigest(2)
		confidentialResult, err := repository.Register(t.Context(), application.ID(), oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypeConfidentialSecret, &expected, confidentialID, &digest, application.AdminID(), at.Add(time.Second))
		if err != nil || confidentialResult.Registration.Revision() != 2 || confidentialResult.Credential == nil || confidentialResult.Credential.Revision() != 1 || confidentialResult.Registration.ConfidentialClient().ClientID() != confidentialID {
			t.Fatalf("confidential result=%v err=%v", confidentialResult, err)
		}
		registration, err := repository.GetRegistration(t.Context(), application.ID(), oauthclientdomain.ChannelTest, application.AdminID())
		if err != nil || registration.Revision() != 2 || registration.PublicClient() == nil || registration.ConfidentialClient() == nil {
			t.Fatalf("registration=%v err=%v", registration, err)
		}
		credential, err := repository.GetCredential(t.Context(), confidentialID, application.AdminID())
		if err != nil || credential.Digest().Bytes() != digest.Bytes() {
			t.Fatalf("credential=%v err=%v", credential, err)
		}
		var raw bson.M
		if err = database.Collection(oauthClientCredentialsCollectionName).FindOne(t.Context(), bson.D{{Key: "clientId", Value: confidentialID.String()}}).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"secret", "clientSecret", "plainSecret"} {
			if _, ok := raw[forbidden]; ok {
				t.Fatalf("persisted forbidden field %s", forbidden)
			}
		}
	})

	t.Run("BR-OAC-004 status no-op and secret rotation use independent revisions", func(t *testing.T) {
		application := createOAuthTestApplication(t, database, "oauth-admin", "oauth-independent-revisions")
		repository := NewOAuthClientRepository(database)
		at := time.Date(2026, 9, 28, 14, 10, 0, 0, time.UTC)
		clientID := integrationOAuthClientID(t, 3)
		digest := integrationOAuthDigest(3)
		registered, err := repository.Register(t.Context(), application.ID(), oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypeConfidentialSecret, nil, clientID, &digest, application.AdminID(), at)
		if err != nil {
			t.Fatal(err)
		}
		noOp, err := repository.SetStatus(t.Context(), clientID, 1, oauthclientdomain.ClientStatusActive, application.AdminID(), at.Add(time.Second))
		if err != nil || noOp.Changed || noOp.Registration.Revision() != 1 || noOp.Registration.ConfidentialClient().AuthorizationEpoch() != 1 {
			t.Fatalf("no-op=%v err=%v", noOp, err)
		}
		disabled, err := repository.SetStatus(t.Context(), clientID, 1, oauthclientdomain.ClientStatusDisabled, application.AdminID(), at.Add(2*time.Second))
		if err != nil || !disabled.Changed || disabled.Registration.Revision() != 2 || disabled.Registration.ConfidentialClient().AuthorizationEpoch() != 2 {
			t.Fatalf("disabled=%v err=%v", disabled, err)
		}
		rotatedDigest := integrationOAuthDigest(4)
		credential, err := repository.RotateSecret(t.Context(), clientID, registered.Credential.Revision(), rotatedDigest, application.AdminID(), at.Add(3*time.Second))
		if err != nil || credential.Revision() != 2 || credential.Digest().Bytes() != rotatedDigest.Bytes() {
			t.Fatalf("credential=%v err=%v", credential, err)
		}
		registration, err := repository.GetRegistration(t.Context(), application.ID(), oauthclientdomain.ChannelTest, application.AdminID())
		if err != nil || registration.Revision() != 2 || registration.ConfidentialClient().AuthorizationEpoch() != 2 {
			t.Fatalf("rotation changed registration=%v err=%v", registration, err)
		}
		if _, err = repository.RotateSecret(t.Context(), clientID, 1, integrationOAuthDigest(5), application.AdminID(), at.Add(4*time.Second)); !errors.Is(err, oauthclientport.ErrCredentialChanged) {
			t.Fatalf("stale credential revision error=%v", err)
		}
		if _, err = repository.SetStatus(t.Context(), clientID, 1, oauthclientdomain.ClientStatusActive, application.AdminID(), at.Add(5*time.Second)); !errors.Is(err, oauthclientport.ErrRegistrationChanged) {
			t.Fatalf("stale registration revision error=%v", err)
		}
	})

	t.Run("BR-OAC-001 client ID is globally unique across both slots", func(t *testing.T) {
		first := createOAuthTestApplication(t, database, "oauth-first-admin", "oauth-global-id-first")
		second := createOAuthTestApplication(t, database, "oauth-second-admin", "oauth-global-id-second")
		repository := NewOAuthClientRepository(database)
		clientID := integrationOAuthClientID(t, 6)
		at := time.Date(2026, 9, 28, 14, 20, 0, 0, time.UTC)
		if _, err := repository.Register(t.Context(), first.ID(), oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypePublicPKCE, nil, clientID, nil, first.AdminID(), at); err != nil {
			t.Fatal(err)
		}
		digest := integrationOAuthDigest(6)
		if result, err := repository.Register(t.Context(), second.ID(), oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypeConfidentialSecret, nil, clientID, &digest, second.AdminID(), at); result != nil || !errors.Is(err, oauthclientport.ErrClientAlreadyExists) {
			t.Fatalf("cross-slot duplicate result=%v err=%v", result, err)
		}
		if count, err := database.Collection(applicationOAuthRegistrationsCollectionName).CountDocuments(t.Context(), bson.D{{Key: "applicationId", Value: second.ID().String()}}); err != nil || count != 0 {
			t.Fatalf("registration count=%d err=%v", count, err)
		}
		if count, err := database.Collection(oauthClientCredentialsCollectionName).CountDocuments(t.Context(), bson.D{{Key: "applicationId", Value: second.ID().String()}}); err != nil || count != 0 {
			t.Fatalf("credential count=%d err=%v", count, err)
		}
	})

	t.Run("BR-OAC-004 simultaneous initial types serialize and loser can retry", func(t *testing.T) {
		application := createOAuthTestApplication(t, database, "oauth-race-admin", "oauth-dual-race")
		repository := NewOAuthClientRepository(database)
		at := time.Date(2026, 9, 28, 14, 30, 0, 0, time.UTC)
		start := make(chan struct{})
		clientIDs := []oauthclientdomain.ClientID{integrationOAuthClientID(t, 8), integrationOAuthClientID(t, 9)}
		errorsByType := make(map[oauthclientdomain.ClientType]error)
		var mutex sync.Mutex
		var group sync.WaitGroup
		for index, typ := range []oauthclientdomain.ClientType{oauthclientdomain.ClientTypePublicPKCE, oauthclientdomain.ClientTypeConfidentialSecret} {
			group.Add(1)
			go func(index int, typ oauthclientdomain.ClientType) {
				defer group.Done()
				<-start
				var digest *oauthclientdomain.SecretDigest
				if typ == oauthclientdomain.ClientTypeConfidentialSecret {
					value := integrationOAuthDigest(8)
					digest = &value
				}
				_, err := repository.Register(t.Context(), application.ID(), oauthclientdomain.ChannelTest, typ, nil, clientIDs[index], digest, application.AdminID(), at)
				mutex.Lock()
				errorsByType[typ] = err
				mutex.Unlock()
			}(index, typ)
		}
		close(start)
		group.Wait()
		var winner, loser oauthclientdomain.ClientType
		for typ, err := range errorsByType {
			if err == nil {
				winner = typ
			} else if errors.Is(err, oauthclientport.ErrRegistrationChanged) || errors.Is(err, oauthclientport.ErrClientAlreadyExists) {
				loser = typ
			} else {
				t.Fatalf("unexpected %s error=%v", typ, err)
			}
		}
		if winner == "" || loser == "" || winner == loser {
			t.Fatalf("race results=%v", errorsByType)
		}
		registration, err := repository.GetRegistration(t.Context(), application.ID(), oauthclientdomain.ChannelTest, application.AdminID())
		if err != nil || registration.Revision() != 1 {
			t.Fatalf("registration=%v err=%v", registration, err)
		}
		expected := registration.Revision()
		var digest *oauthclientdomain.SecretDigest
		if loser == oauthclientdomain.ClientTypeConfidentialSecret {
			value := integrationOAuthDigest(10)
			digest = &value
		}
		if _, err = repository.Register(t.Context(), application.ID(), oauthclientdomain.ChannelTest, loser, &expected, integrationOAuthClientID(t, 10), digest, application.AdminID(), at.Add(time.Second)); err != nil {
			t.Fatalf("loser retry: %v", err)
		}
		registration, err = repository.GetRegistration(t.Context(), application.ID(), oauthclientdomain.ChannelTest, application.AdminID())
		if err != nil || registration.Revision() != 2 || registration.PublicClient() == nil || registration.ConfidentialClient() == nil {
			t.Fatalf("final registration=%v err=%v", registration, err)
		}
	})

	t.Run("BR-OAC-001 administrator transfer is rechecked by write fence", func(t *testing.T) {
		application := createOAuthTestApplication(t, database, "old-oauth-admin", "oauth-admin-transfer")
		repository := NewOAuthClientRepository(database)
		if _, err := database.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.D{{Key: "id", Value: application.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "new-oauth-admin"}}}}); err != nil {
			t.Fatal(err)
		}
		if result, err := repository.Register(t.Context(), application.ID(), oauthclientdomain.ChannelTest, oauthclientdomain.ClientTypePublicPKCE, nil, integrationOAuthClientID(t, 11), nil, application.AdminID(), time.Now()); result != nil || !errors.Is(err, oauthclientport.ErrApplicationAdminRequired) {
			t.Fatalf("old admin result=%v err=%v", result, err)
		}
		if count, err := database.Collection(applicationOAuthRegistrationsCollectionName).CountDocuments(t.Context(), bson.D{{Key: "applicationId", Value: application.ID().String()}}); err != nil || count != 0 {
			t.Fatalf("unauthorized write count=%d err=%v", count, err)
		}
	})

	t.Run("API implementation contract validator rejects non-32-byte digest", func(t *testing.T) {
		_, err := database.Collection(oauthClientCredentialsCollectionName).InsertOne(t.Context(), oauthClientCredentialDocument{
			ClientID: integrationOAuthClientID(t, 12).String(), ApplicationID: nextIntegrationApplicationID(t).String(), SecretDigest: make([]byte, 31),
			CredentialRevision: 1, RotatedBy: "admin", RotatedAt: time.Now(),
		})
		if err == nil {
			t.Fatal("validator accepted a non-32-byte digest")
		}
	})
}
