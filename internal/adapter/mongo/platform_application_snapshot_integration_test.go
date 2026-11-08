package mongo

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
)

func TestPlatformApplicationSnapshotIntegration_UCAPP051_BRAPP066(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	rows := make([]any, 300)
	var target applicationDocument
	for i := range rows {
		d := platformSeedDocument(t, "Batch_App", nextIntegrationApplicationID(t).String())
		if i == len(rows)-1 {
			d.ArchiveStatus = "ARCHIVED"
			target = d
		}
		rows[i] = d
	}
	if _, err := db.Collection(applicationsCollectionName).InsertMany(t.Context(), rows); err != nil {
		t.Fatal(err)
	}
	reached, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
		if e.CommandName == "find" {
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
	defer client.Disconnect(context.Background())
	defer func() {
		select {
		case <-resume:
		default:
			close(resume)
		}
	}()
	r := platformReader(t, client.Database(db.Name()))
	at := time.Now().UTC().Truncate(time.Millisecond)
	q := platformQuery(t, ad.PlatformApplicationQuery{LifecycleStatuses: []string{"ACTIVE"}, ArchiveStatuses: []string{"ARCHIVED"}})
	type result struct {
		page ad.PlatformApplicationPage
		err  error
	}
	done := make(chan result, 1)
	go func() { p, err := r.SearchPlatformApplications(t.Context(), "reader", q, at); done <- result{p, err} }()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot read did not start")
	}
	if _, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": target.ID}, bson.M{"$set": bson.M{"adminId": "new-owner", "ownershipRevision": int64(2), "lifecycleStatus": "CLOSED", "lifecycleRevision": int64(2)}}); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case got := <-done:
		if got.err != nil || len(got.page.Items) != 1 || got.page.Items[0].AdminAuthID.String() != target.AdminID || got.page.Items[0].LifecycleStatus != ad.ApplicationLifecycleActive {
			t.Fatal("mixed snapshot", got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot read did not finish")
	}
	fresh, err := platformReader(t, db).SearchPlatformApplications(t.Context(), "reader", q, at)
	if err != nil || len(fresh.Items) != 0 {
		t.Fatal("new snapshot", err)
	}
}
