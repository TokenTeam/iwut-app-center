package mongo

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	applicationdomain "iwut-app-center/internal/application/domain"
	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	"iwut-app-center/internal/shared"
)

func TestApplicationAdminTransferMigrationIntegration_BackfillsAfterCollMod(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	ctx := t.Context()

	application := integrationApplication(t, "auth-migration-source", "transfer_migration")
	if err := NewApplicationRepository(database).CreateWithinQuota(ctx, application, 10); err != nil {
		t.Fatal(err)
	}
	if err := database.RunCommand(ctx, bson.D{{Key: "collMod", Value: applicationsCollectionName}, {Key: "validator", Value: applicationValidator()}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(applicationsCollectionName).UpdateOne(ctx, bson.D{{Key: "id", Value: application.ID().String()}}, bson.D{{Key: "$unset", Value: bson.D{{Key: "ownershipRevision", Value: ""}}}}); err != nil {
		t.Fatal(err)
	}
	if err := database.RunCommand(ctx, bson.D{{Key: "collMod", Value: ownerOperations}, {Key: "validator", Value: accountOwnerExitOperationValidator(false)}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(ownerOperations).InsertOne(ctx, bson.D{
		{Key: "authId", Value: "auth-migration-target"}, {Key: "operationId", Value: "123e4567-e89b-42d3-a456-426614174001"},
		{Key: "purpose", Value: 1}, {Key: "receiptId", Value: "receipt"}, {Key: "decision", Value: 1}, {Key: "cleanup", Value: 1},
		{Key: "blocked", Value: false}, {Key: "attempt", Value: 0}, {Key: "nextAttemptAt", Value: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Collection(applicationAdminTransfersCollectionName).Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Collection(migrationLedgerCollectionName).DeleteOne(ctx, bson.D{{Key: "_id", Value: applicationAdminTransferMigrationID}}); err != nil {
		t.Fatal(err)
	}

	if err := NewMigrator(database).Migrate(ctx); err != nil {
		t.Fatalf("apply 0021 over strict 0020 validators: %v", err)
	}
	var stored applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(ctx, bson.D{{Key: "id", Value: application.ID().String()}}).Decode(&stored); err != nil || stored.OwnershipRevision != 1 {
		t.Fatalf("ownership backfill = %#v err=%v", stored, err)
	}
	var operation bson.M
	if err := database.Collection(ownerOperations).FindOne(ctx, bson.D{{Key: "operationId", Value: "123e4567-e89b-42d3-a456-426614174001"}}).Decode(&operation); err != nil {
		t.Fatal(err)
	}
	if blocker, ok := operation["blocker"].(int32); !ok || blocker != 0 {
		t.Fatalf("owner-exit blocker backfill = %#v", operation["blocker"])
	}
}

type transferSecretFactory struct{}

func (transferSecretFactory) NewSecret(clientID oauthdomain.ClientID) (string, oauthdomain.SecretDigest, error) {
	return "one-time-" + clientID.String(), integrationOAuthDigest(99), nil
}

func TestApplicationAdminTransferRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	const source, target = "auth-transfer-source", "auth-transfer-target"

	application := integrationApplication(t, source, "transfer_success")
	if err := NewApplicationRepository(database).CreateWithinQuota(t.Context(), application, 27); err != nil {
		t.Fatal(err)
	}
	clientID := integrationOAuthClientID(t, 2601)
	originalDigest := integrationOAuthDigest(1)
	if _, err := NewOAuthClientRepository(database).Register(t.Context(), application.ID(), oauthdomain.ChannelTest, oauthdomain.ClientTypeConfidentialSecret, nil, clientID, &originalDigest, application.AdminID(), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	linkRepository := NewApplicationTesterJoinLinkRepository(database)
	activeLink := createIntegrationTesterJoinLink(t, linkRepository, application.ID(), application.AdminID(), nil, newIntegrationTesterJoinLink(t, application.ID(), application.AdminID()))

	repository := NewApplicationAdminTransferRepository(database, transferSecretFactory{}, 27)
	transferID, _ := applicationdomain.ParseApplicationAdminTransferID("01890f47-0000-7000-8000-000000002601")
	transfer, err := repository.Initiate(t.Context(), application.ID(), application.AdminID(), shared.AuthID(target), 1, transferID, now)
	if err != nil || transfer.Status != applicationdomain.ApplicationAdminTransferPending || !transfer.ExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("Initiate() = (%#v, %v)", transfer, err)
	}

	accepted, err := repository.Accept(t.Context(), transferID, shared.AuthID(target), applicationdomain.ConfidentialCredentialRotate, now.Add(time.Minute))
	if err != nil || accepted.Transfer.Status != applicationdomain.ApplicationAdminTransferAccepted || len(accepted.RotatedCredentials) != 1 || !accepted.SecretsDisclosed {
		t.Fatalf("Accept() = (%#v, %v)", accepted, err)
	}
	rotated := accepted.RotatedCredentials[0]
	if rotated.ClientID != clientID.String() || rotated.Channel != "TEST" || rotated.CredentialRevision != 2 || rotated.ClientSecret == "" {
		t.Fatalf("rotated credential = %#v", rotated)
	}

	var credential oauthClientCredentialDocument
	if err := database.Collection(oauthClientCredentialsCollectionName).FindOne(t.Context(), bson.D{{Key: "clientId", Value: clientID.String()}}).Decode(&credential); err != nil {
		t.Fatal(err)
	}
	wantDigest := integrationOAuthDigest(99).Bytes()
	if rotated.ClientSecret != "one-time-"+clientID.String() || len(credential.SecretDigest) != len(wantDigest) || string(credential.SecretDigest) != string(wantDigest[:]) {
		t.Fatal("returned one-time secret and committed digest do not match the generated material")
	}

	var stored applicationDocument
	if err := database.Collection(applicationsCollectionName).FindOne(t.Context(), bson.D{{Key: "id", Value: application.ID().String()}}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.AdminID != target || stored.OwnershipRevision != 2 {
		t.Fatalf("application owner = %q revision=%d", stored.AdminID, stored.OwnershipRevision)
	}
	assertQuota(t, database, source, 27, 0, 2)
	assertQuota(t, database, target, 27, 1, 1)
	link := readTesterJoinLinkDocument(t, database, activeLink.JoinLink().JoinLinkID())
	if link.Status != "REVOKED" || link.RevocationReason == nil || *link.RevocationReason != "ADMIN_TRANSFER" || link.RevokedBy == nil || *link.RevokedBy != target || link.ReplacedByJoinLinkID != nil {
		t.Fatalf("tester link audit = %#v", link)
	}

	retry, err := repository.Accept(t.Context(), transferID, shared.AuthID(target), applicationdomain.ConfidentialCredentialRotate, now.Add(2*time.Minute))
	if err != nil || retry.SecretsDisclosed || len(retry.RotatedCredentials) != 0 {
		t.Fatalf("Accept() retry = (%#v, %v), want terminal result without secret replay", retry, err)
	}
}
