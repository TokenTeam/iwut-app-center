package mongo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
)

func platformReader(t *testing.T, db *dm.Database) *PlatformApplicationQueryRepository {
	t.Helper()
	key, err := DerivePlatformApplicationCursorKey([]byte("platform-query-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return NewPlatformApplicationQueryRepository(db, key)
}
func platformSeedDocument(t *testing.T, name, owner string) applicationDocument {
	t.Helper()
	d, err := applicationToDocument(integrationApplication(t, owner, name))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func platformQuery(t *testing.T, q ad.PlatformApplicationQuery) ad.PlatformApplicationQuery {
	t.Helper()
	result, err := ad.NewPlatformApplicationQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestPlatformApplicationDirectoryIntegration_UCAPP051_BRAPP064066(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	r := platformReader(t, db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	docs := []any{}
	var expected applicationDocument
	for _, life := range []string{"ACTIVE", "CLOSING", "CLOSED"} {
		for _, available := range []string{"AVAILABLE", "SUSPENDED"} {
			for _, archive := range []string{"UNARCHIVED", "ARCHIVED"} {
				d := platformSeedDocument(t, "Same_App", fmt.Sprintf("owner-%d", len(docs)))
				d.LifecycleStatus = life
				d.PlatformAvailabilityStatus = available
				d.ArchiveStatus = archive
				docs = append(docs, d)
				expected = d
			}
		}
	}
	if _, err := db.Collection(applicationsCollectionName).InsertMany(t.Context(), docs); err != nil {
		t.Fatal(err)
	}
	q := platformQuery(t, ad.PlatformApplicationQuery{})
	p, err := r.SearchPlatformApplications(t.Context(), "reader", q, at)
	if err != nil || len(p.Items) != 12 {
		t.Fatal("all independent states/no Profile", len(p.Items), err)
	}
	q = platformQuery(t, ad.PlatformApplicationQuery{NamePrefix: "SAME_", LifecycleStatuses: []string{"CLOSED"}, PlatformAvailabilityStatuses: []string{"SUSPENDED"}, ArchiveStatuses: []string{"ARCHIVED"}, AdminAuthID: expected.AdminID, ApplicationID: expected.ID})
	p, err = r.SearchPlatformApplications(t.Context(), "reader", q, at)
	if err != nil || len(p.Items) != 1 || p.Items[0].ApplicationID.String() != expected.ID {
		t.Fatal("AND filters", err)
	}
	s, err := r.GetPlatformApplicationSummary(t.Context(), shared.ApplicationID(expected.ID))
	if err != nil || s.LifecycleStatus != ad.ApplicationLifecycleClosed {
		t.Fatal("closed summary", err)
	}
	if _, err := r.GetPlatformApplicationSummary(t.Context(), nextIntegrationApplicationID(t)); !errors.Is(err, ad.ErrPlatformApplicationNotFound) {
		t.Fatal("missing", err)
	}
	before := s
	if _, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": expected.ID}, bson.M{"$set": bson.M{"adminId": "new-owner", "ownershipRevision": int64(2)}}); err != nil {
		t.Fatal(err)
	}
	after, err := r.GetPlatformApplicationSummary(t.Context(), s.ApplicationID)
	if err != nil || before.AdminAuthID == after.AdminAuthID || after.OwnershipRevision != 2 {
		t.Fatal("fresh core read", err)
	}
	indices, err := db.Collection(applicationsCollectionName).Indexes().List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var rows []bson.M
	if err = indices.All(t.Context(), &rows); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row["name"].(string)] = true
	}
	for _, name := range []string{platformApplicationQueryIndexName, platformApplicationAdminQueryIndexName, applicationAdminNameUniqueIndexName} {
		if !seen[name] {
			t.Fatal("index", name)
		}
	}
}
func TestPlatformApplicationPaginationIntegration_UCAPP051_BRAPP065(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	r := platformReader(t, db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	docs := make([]any, 0, 503)
	for i := range 503 {
		d := platformSeedDocument(t, fmt.Sprintf("App_%04d", i), "pagination-owner")
		if i < 500 {
			d.LifecycleStatus = "CLOSED"
		}
		docs = append(docs, d)
	}
	if _, err := db.Collection(applicationsCollectionName).InsertMany(t.Context(), docs); err != nil {
		t.Fatal(err)
	}
	q := platformQuery(t, ad.PlatformApplicationQuery{LifecycleStatuses: []string{"ACTIVE"}, PageSize: 1})
	p, err := r.SearchPlatformApplications(t.Context(), "reader", q, at)
	if err != nil || len(p.Items) != 0 || p.NextPageToken == "" {
		t.Fatal("500 raw empty continuation", err)
	}
	seen := map[shared.ApplicationID]bool{}
	for p.NextPageToken != "" {
		q.PageToken = p.NextPageToken
		p, err = r.SearchPlatformApplications(t.Context(), "reader", q, at)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range p.Items {
			if seen[item.ApplicationID] {
				t.Fatal("duplicate")
			}
			seen[item.ApplicationID] = true
		}
	}
	if len(seen) != 3 {
		t.Fatal("skipped legitimate rows", len(seen))
	}
	// Same comparison key across owners must continue by Application ID.
	docs = nil
	for i := range 3 {
		docs = append(docs, platformSeedDocument(t, "Tied", fmt.Sprintf("tied-%d", i)))
	}
	if _, err := db.Collection(applicationsCollectionName).InsertMany(t.Context(), docs); err != nil {
		t.Fatal(err)
	}
	q = platformQuery(t, ad.PlatformApplicationQuery{NamePrefix: "TIED", PageSize: 1})
	p, err = r.SearchPlatformApplications(t.Context(), "reader", q, at)
	if err != nil || len(p.Items) != 1 || p.NextPageToken == "" {
		t.Fatal(err)
	}
	token := p.NextPageToken
	for _, mutate := range []func(*ad.PlatformApplicationQuery, *shared.AuthID){func(q *ad.PlatformApplicationQuery, c *shared.AuthID) { *c = "other" }, func(q *ad.PlatformApplicationQuery, c *shared.AuthID) { q.NamePrefix = "different" }, func(q *ad.PlatformApplicationQuery, c *shared.AuthID) { q.ArchiveStatuses = []string{"ARCHIVED"} }, func(q *ad.PlatformApplicationQuery, c *shared.AuthID) { q.PageToken = token + "!" }} {
		changed := q
		changed.PageToken = token
		caller := shared.AuthID("reader")
		mutate(&changed, &caller)
		if _, err := r.SearchPlatformApplications(t.Context(), caller, changed, at); !errors.Is(err, ad.ErrInvalidPlatformApplicationPageToken) {
			t.Fatal("cursor scope", err)
		}
	}
	q.PageToken = token
	q.PageSize = 100
	p, err = r.SearchPlatformApplications(t.Context(), "reader", q, at)
	if err != nil || len(p.Items) != 2 || p.Items[0].Name != "Tied" {
		t.Fatal("same key continuation", err)
	}
}
func TestPlatformApplicationCorruptionIntegration_UCAPP051_BRAPP066(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	r := platformReader(t, db)
	at := time.Now().UTC().Truncate(time.Millisecond)
	for _, field := range []string{"nameKey", "lifecycleStatus", "ownershipRevision", "createdAt", "adminId"} {
		t.Run(field, func(t *testing.T) {
			d := platformSeedDocument(t, "Corrupt", field)
			if _, err := db.Collection(applicationsCollectionName).InsertOne(t.Context(), d); err != nil {
				t.Fatal(err)
			}
			var value any = "invalid"
			if field == "ownershipRevision" {
				value = float64(1)
			}
			if field == "adminId" {
				value = ""
			}
			if _, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": d.ID}, bson.M{"$set": bson.M{field: value}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
			q := platformQuery(t, ad.PlatformApplicationQuery{ApplicationID: d.ID})
			page, err := r.SearchPlatformApplications(t.Context(), "reader", q, at)
			if !errors.Is(err, ad.ErrPlatformApplicationStateInconsistent) || page.Items != nil {
				t.Fatal("partial/hidden corruption", err)
			}
			if _, err := r.GetPlatformApplicationSummary(t.Context(), shared.ApplicationID(d.ID)); !errors.Is(err, ad.ErrPlatformApplicationStateInconsistent) {
				t.Fatal("detail corruption", err)
			}
			if _, err := db.Collection(applicationsCollectionName).DeleteOne(t.Context(), bson.M{"id": d.ID}); err != nil {
				t.Fatal(err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.SearchPlatformApplications(canceled, "reader", platformQuery(t, ad.PlatformApplicationQuery{}), at); !errors.Is(err, ad.ErrPlatformApplicationUnavailable) {
		t.Fatal("storage failure", err)
	}
	// A corrupted private counter is outside this minimum projection.
	d := platformSeedDocument(t, "Private_Unused", "owner")
	if _, err := db.Collection(applicationsCollectionName).InsertOne(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": d.ID}, bson.M{"$set": bson.M{"nextVersionSequence": "private-damage"}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
	if s, err := r.GetPlatformApplicationSummary(t.Context(), shared.ApplicationID(d.ID)); err != nil || !strings.HasPrefix(s.Name, "Private") {
		t.Fatal("private projection coupling", err)
	}
}
