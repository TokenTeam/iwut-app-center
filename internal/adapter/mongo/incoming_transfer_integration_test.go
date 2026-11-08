package mongo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func seedIncomingTransfer(t *testing.T, db *dm.Database, to shared.AuthID, at time.Time) ad.ApplicationAdminTransfer {
	t.Helper()
	app := integrationApplication(t, "incoming-source", fmt.Sprintf("incoming_%d", applicationIDCounter.Load()+1))
	if err := NewApplicationRepository(db).CreateWithinQuota(t.Context(), app, 100); err != nil {
		t.Fatal(err)
	}
	id := ad.ApplicationAdminTransferID(nextIntegrationApplicationID(t).String())
	transfer, err := NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 100).Initiate(t.Context(), app.ID(), app.AdminID(), to, 1, id, at)
	if err != nil {
		t.Fatal(err)
	}
	return transfer
}
func incomingReader(t *testing.T, db *dm.Database) *IncomingApplicationAdminTransferRepository {
	t.Helper()
	key, err := DeriveIncomingTransferCursorKey([]byte("real-mongo-incoming-material"))
	if err != nil {
		t.Fatal(err)
	}
	return NewIncomingApplicationAdminTransferRepository(db, key)
}
func TestIncomingApplicationTransferIntegration_UCAPP050_BRAPP059060062(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Truncate(time.Millisecond)
	r := incomingReader(t, db)
	const target shared.AuthID = "incoming-target"
	if p, err := r.ListIncoming(t.Context(), target, ad.IncomingTransferQuery{PageSize: 20}, at); err != nil || p.Items == nil || len(p.Items) != 0 {
		t.Fatal("explicit empty", err)
	}
	transfers := []ad.ApplicationAdminTransfer{}
	for range 3 {
		transfers = append(transfers, seedIncomingTransfer(t, db, target, at.Add(-time.Hour)))
	}
	_ = seedIncomingTransfer(t, db, "other-target", at.Add(-time.Hour))
	expired := seedIncomingTransfer(t, db, target, at.Add(-ad.ApplicationAdminTransferLifetime))
	for _, action := range []string{"cancel", "reject", "accept"} {
		tr := seedIncomingTransfer(t, db, target, at.Add(-time.Hour))
		commands := NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 100)
		var err error
		switch action {
		case "cancel":
			_, err = commands.Cancel(t.Context(), tr.TransferID, tr.FromAdminID, at)
		case "reject":
			_, err = commands.Reject(t.Context(), tr.TransferID, target, at)
		case "accept":
			_, err = commands.Accept(t.Context(), tr.TransferID, target, ad.ConfidentialCredentialKeep, at)
		}
		if err != nil {
			t.Fatal(action, err)
		}
	}
	sort.Slice(transfers, func(i, j int) bool { return transfers[i].TransferID.String() > transfers[j].TransferID.String() })
	p, err := r.ListIncoming(t.Context(), target, ad.IncomingTransferQuery{PageSize: 1}, at)
	if err != nil || len(p.Items) != 1 || p.Items[0].TransferID != transfers[0].TransferID || p.NextPageToken == "" {
		t.Fatal("first tied page", err)
	}
	next, err := r.ListIncoming(t.Context(), target, ad.IncomingTransferQuery{PageSize: 100, PageToken: p.NextPageToken}, at)
	if err != nil || len(next.Items) != 2 || next.Items[0].TransferID != transfers[1].TransferID || next.Items[1].TransferID != transfers[2].TransferID || next.NextPageToken != "" {
		t.Fatal("tied continuation", err)
	}
	if _, err := r.ListIncoming(t.Context(), "other-target", ad.IncomingTransferQuery{PageSize: 20, PageToken: p.NextPageToken}, at); !errors.Is(err, ad.ErrInvalidIncomingTransferPageToken) {
		t.Fatal("cursor identity", err)
	}
	before, err := r.ListIncoming(t.Context(), target, ad.IncomingTransferQuery{PageSize: 20}, at.Add(-time.Millisecond))
	if err != nil || len(before.Items) != 4 {
		t.Fatal("just before expiry", err)
	}
	var persisted applicationAdminTransferDocument
	if err := db.Collection(applicationAdminTransfersCollectionName).FindOne(t.Context(), bson.M{"transferId": expired.TransferID.String()}).Decode(&persisted); err != nil || persisted.Status != "PENDING" {
		t.Fatal("list wrote expiry", err)
	}
	got, err := NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 100).Get(t.Context(), expired.TransferID, target, at)
	if err != nil || got.Status != ad.ApplicationAdminTransferExpired {
		t.Fatal("detail no longer converges expiry", err)
	}
	// The new index coexists with owner-exit/expiry and audit indexes.
	indexes, err := db.Collection(applicationAdminTransfersCollectionName).Indexes().List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer indexes.Close(t.Context())
	var values []bson.M
	if err = indexes.All(t.Context(), &values); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, v := range values {
		seen[v["name"].(string)] = true
	}
	for _, name := range []string{incomingTransferIndexName, "ix_application_admin_transfer_target_status_expiry", "uq_application_admin_transfer_pending"} {
		if !seen[name] {
			t.Fatal("missing index", name)
		}
	}
}
func TestIncomingApplicationTransferCorruptionIntegration_UCAPP050_BRAPP061(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Truncate(time.Millisecond)
	r := incomingReader(t, db)
	for _, kind := range []string{"missing app", "ownership", "name", "expiry", "typed field"} {
		t.Run(kind, func(t *testing.T) {
			target := shared.AuthID("target-" + strings.ReplaceAll(kind, " ", "-"))
			tr := seedIncomingTransfer(t, db, target, at.Add(-time.Hour))
			var err error
			switch kind {
			case "missing app":
				_, err = db.Collection(applicationsCollectionName).DeleteOne(t.Context(), bson.M{"id": tr.ApplicationID.String()})
			case "ownership":
				_, err = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": tr.ApplicationID.String()}, bson.M{"$inc": bson.M{"ownershipRevision": int64(1)}})
			case "name":
				_, err = db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": tr.ApplicationID.String()}, bson.M{"$set": bson.M{"nameKey": "wrong"}}, options.UpdateOne().SetBypassDocumentValidation(true))
			case "expiry":
				_, err = db.Collection(applicationAdminTransfersCollectionName).UpdateOne(t.Context(), bson.M{"transferId": tr.TransferID.String()}, bson.M{"$set": bson.M{"expiresAt": at.Add(time.Hour)}})
			case "typed field":
				_, err = db.Collection(applicationAdminTransfersCollectionName).UpdateOne(t.Context(), bson.M{"transferId": tr.TransferID.String()}, bson.M{"$set": bson.M{"fromAdminId": true}}, options.UpdateOne().SetBypassDocumentValidation(true))
			}
			if err != nil {
				t.Fatal(err)
			}
			page, err := r.ListIncoming(t.Context(), target, ad.IncomingTransferQuery{PageSize: 20}, at)
			if !errors.Is(err, ad.ErrApplicationAdminTransferStateInconsistent) || page.Items != nil {
				t.Fatalf("corruption returned partial: %#v %v", page, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.ListIncoming(canceled, "target", ad.IncomingTransferQuery{PageSize: 20}, at); !errors.Is(err, ad.ErrIncomingTransferUnavailable) {
		t.Fatal("dependency error", err)
	}
}
func TestIncomingApplicationTransferSnapshotIntegration_UCAPP050_BRAPP060061(t *testing.T) {
	for _, action := range []string{"cancel", "accept", "close"} {
		t.Run(action, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			at := time.Now().UTC().Truncate(time.Millisecond)
			tr := seedIncomingTransfer(t, db, "snapshot-target", at.Add(-time.Hour))
			reached, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
				if e.CommandName == "find" && e.Command.Lookup("find").StringValue() == applicationsCollectionName {
					once.Do(func() {
						close(reached)
						select {
						case <-resume:
						case <-ctx.Done():
						}
					})
				}
			}}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
			defer func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			}()
			reader := incomingReader(t, client.Database(db.Name()))
			result := make(chan error, 1)
			go func() {
				page, err := reader.ListIncoming(t.Context(), tr.ToAdminID, ad.IncomingTransferQuery{PageSize: 20}, at)
				if err == nil && (len(page.Items) != 1 || page.Items[0].TransferID != tr.TransferID) {
					err = errors.New("mixed transfer snapshot")
				}
				result <- err
			}()
			select {
			case <-reached:
			case <-time.After(10 * time.Second):
				t.Fatal("reader did not reach application batch")
			}
			commands := NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 100)
			switch action {
			case "cancel":
				_, err = commands.Cancel(t.Context(), tr.TransferID, tr.FromAdminID, at)
			case "accept":
				_, err = commands.Accept(t.Context(), tr.TransferID, tr.ToAdminID, ad.ConfidentialCredentialKeep, at)
			case "close":
				_, err = NewApplicationClosureRepository(db).Start(t.Context(), tr.ApplicationID, tr.FromAdminID, 1, 1, ad.ApplicationCloseProof{Subject: tr.FromAdminID, ApplicationID: tr.ApplicationID, JTI: nextIntegrationApplicationID(t).String(), AuthTime: at, IssuedAt: at, ExpiresAt: at.Add(5 * time.Minute)}, ad.ApplicationClosureID(nextIntegrationApplicationID(t).String()), at)
			}
			if err != nil {
				t.Fatal(action, err)
			}
			close(resume)
			if err = <-result; err != nil {
				t.Fatal("snapshot before writer", err)
			}
			after, err := incomingReader(t, db).ListIncoming(t.Context(), tr.ToAdminID, ad.IncomingTransferQuery{PageSize: 20}, at)
			if err != nil || len(after.Items) != 0 {
				t.Fatal("new page retained resolved transfer", err)
			}
		})
	}
}
