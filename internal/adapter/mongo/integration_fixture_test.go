package mongo

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// A pool belongs to one integrationClient (and therefore one test suite), never
// to the MongoDB server or another process. Each lease owns its entire database.
var integrationFixturePools sync.Map // *mongo.Client -> *integrationFixturePool

type integrationFixturePool struct {
	mu   sync.Mutex
	idle []*integrationFixture
	all  []*drivermongo.Database
}

type integrationFixture struct {
	database *drivermongo.Database
	schema   []integrationCollectionSchema
	seeds    map[string][]bson.Raw
}

type integrationCollectionSchema struct {
	Name    string
	Type    string
	Options string
	Indexes []string
}

// reusableIntegrationDatabase is for repository behavior tests. Migration tests
// keep using migratedIntegrationDatabase/integrationDatabase to start cold.
// A modified schema is discarded rather than repaired or passed to another test.
func reusableIntegrationDatabase(t *testing.T, client *drivermongo.Client) *drivermongo.Database {
	t.Helper()
	value, ok := integrationFixturePools.Load(client)
	if !ok {
		t.Fatal("reusable fixture requires a suite-owned integrationClient")
	}
	pool := value.(*integrationFixturePool)
	pool.mu.Lock()
	var fixture *integrationFixture
	if count := len(pool.idle); count > 0 {
		fixture = pool.idle[count-1]
		pool.idle = pool.idle[:count-1]
	}
	pool.mu.Unlock()
	if fixture == nil {
		database := client.Database("iwut_app_center_test_" + nextIntegrationApplicationID(t).String()[24:])
		pool.mu.Lock()
		pool.all = append(pool.all, database)
		pool.mu.Unlock()
		if err := NewMigrator(database).Migrate(t.Context()); err != nil {
			t.Fatalf("migrate reusable database: %v", err)
		}
		var err error
		fixture, err = snapshotIntegrationFixture(t.Context(), database)
		if err != nil {
			t.Fatalf("snapshot reusable database: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if t.Failed() {
			return // Keep failed fixtures out of circulation until suite cleanup.
		}
		schema, err := integrationSchema(ctx, fixture.database)
		if err != nil {
			t.Errorf("inspect reusable database schema: %v", err)
			return
		}
		if !reflect.DeepEqual(schema, fixture.schema) {
			return // Fault-injection/index tests must not contaminate later leases.
		}
		if err := fixture.reset(ctx); err != nil {
			t.Errorf("reset reusable database: %v", err)
			return
		}
		pool.mu.Lock()
		pool.idle = append(pool.idle, fixture)
		pool.mu.Unlock()
	})
	return fixture.database
}

func integrationSchema(ctx context.Context, database *drivermongo.Database) ([]integrationCollectionSchema, error) {
	specs, err := database.ListCollectionSpecifications(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	result := make([]integrationCollectionSchema, 0, len(specs))
	for _, spec := range specs {
		// Decode into maps so field order does not affect schema comparisons.
		options, err := canonicalIntegrationDocument(spec.Options)
		if err != nil {
			return nil, err
		}
		cursor, err := database.Collection(spec.Name).Indexes().List(ctx)
		if err != nil {
			return nil, err
		}
		var indexes []bson.Raw
		if err := cursor.All(ctx, &indexes); err != nil {
			return nil, err
		}
		entry := integrationCollectionSchema{Name: spec.Name, Type: spec.Type, Options: options}
		for _, index := range indexes {
			canonical, err := canonicalIntegrationDocument(index)
			if err != nil {
				return nil, err
			}
			entry.Indexes = append(entry.Indexes, canonical)
		}
		sort.Strings(entry.Indexes)
		result = append(result, entry)
	}
	return result, nil
}

func canonicalIntegrationDocument(raw bson.Raw) (string, error) {
	var document bson.M
	if err := bson.Unmarshal(raw, &document); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(document)
	return string(encoded), err
}

func snapshotIntegrationFixture(ctx context.Context, database *drivermongo.Database) (*integrationFixture, error) {
	schema, err := integrationSchema(ctx, database)
	if err != nil {
		return nil, err
	}
	fixture := &integrationFixture{database: database, schema: schema, seeds: make(map[string][]bson.Raw)}
	for _, collection := range schema {
		cursor, err := database.Collection(collection.Name).Find(ctx, bson.D{})
		if err != nil {
			return nil, err
		}
		var seeds []bson.Raw
		if err := cursor.All(ctx, &seeds); err != nil {
			return nil, err
		}
		fixture.seeds[collection.Name] = seeds
	}
	return fixture, nil
}

func (fixture *integrationFixture) reset(ctx context.Context) error {
	for _, collection := range fixture.schema {
		coll := fixture.database.Collection(collection.Name)
		if _, err := coll.DeleteMany(ctx, bson.D{}); err != nil {
			return fmt.Errorf("clear %s: %w", collection.Name, err)
		}
		seeds := fixture.seeds[collection.Name]
		if len(seeds) > 0 {
			documents := make([]any, len(seeds))
			for i := range seeds {
				documents[i] = seeds[i]
			}
			if _, err := coll.InsertMany(ctx, documents); err != nil {
				return fmt.Errorf("restore %s seeds: %w", collection.Name, err)
			}
		}
	}
	return nil
}

func (pool *integrationFixturePool) close(t *testing.T) {
	t.Helper()
	for _, database := range pool.all {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := database.Drop(ctx)
		cancel()
		if err != nil {
			t.Errorf("drop reusable database %s: %v", database.Name(), err)
		}
	}
}

func TestReusableIntegrationDatabaseRestoresIsolation(t *testing.T) {
	client := integrationClient(t)
	var name string
	var originalPolicy bson.Raw
	t.Run("first lease mutates data and migration seeds", func(t *testing.T) {
		db := reusableIntegrationDatabase(t, client)
		name = db.Name()
		var err error
		originalPolicy, err = db.Collection(versionReviewPoliciesCollectionName).FindOne(t.Context(), bson.M{"version": "app-version-review-v2"}).Raw()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(applicationsCollectionName).InsertOne(t.Context(), bson.M{"corrupt": true}, options.InsertOne().SetBypassDocumentValidation(true)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(migrationLedgerCollectionName).DeleteMany(t.Context(), bson.D{}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(versionReviewPoliciesCollectionName).UpdateOne(t.Context(), bson.M{"version": "app-version-review-v2"}, bson.M{"$set": bson.M{"status": "RETIRED"}}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("next lease retains schema and restores exact seeds", func(t *testing.T) {
		db := reusableIntegrationDatabase(t, client)
		if db.Name() != name {
			t.Fatal("unchanged schema was not reused")
		}
		assertCollectionCount(t, db, applicationsCollectionName, 0)
		assertCollectionCount(t, db, migrationLedgerCollectionName, 32)
		policy, err := db.Collection(versionReviewPoliciesCollectionName).FindOne(t.Context(), bson.M{"version": "app-version-review-v2"}).Raw()
		if err != nil || !reflect.DeepEqual(policy, originalPolicy) {
			t.Fatal("policy seed or its original identity changed", err)
		}
		if _, err := db.Collection(applicationsCollectionName).InsertOne(t.Context(), bson.M{"corrupt": true}); err == nil {
			t.Fatal("reused database lost its validator")
		}
		// Two live leases must never share data, even when requested by one test.
		other := reusableIntegrationDatabase(t, client)
		if db.Name() == other.Name() {
			t.Fatal("live leases share a database")
		}
		createVersionTestApplication(t, db, "isolation-owner", "isolation_app")
		assertCollectionCount(t, other, applicationsCollectionName, 0)
	})
}

func TestReusableIntegrationDatabaseDiscardsSchemaChanges(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(context.Context, *drivermongo.Database) error
	}{
		{"validator", func(ctx context.Context, db *drivermongo.Database) error {
			return db.RunCommand(ctx, bson.D{{Key: "collMod", Value: applicationsCollectionName}, {Key: "validator", Value: bson.M{}}}).Err()
		}},
		{"index", func(ctx context.Context, db *drivermongo.Database) error {
			return db.Collection(applicationsCollectionName).Indexes().DropOne(ctx, applicationIDUniqueIndexName)
		}},
		{"new collection", func(ctx context.Context, db *drivermongo.Database) error {
			return db.CreateCollection(ctx, "unexpected_fixture_collection")
		}},
		{"dropped collection", func(ctx context.Context, db *drivermongo.Database) error {
			return db.Collection(applicationsCollectionName).Drop(ctx)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := integrationClient(t)
			var name string
			t.Run("mutate", func(t *testing.T) {
				db := reusableIntegrationDatabase(t, client)
				name = db.Name()
				if err := test.mutate(t.Context(), db); err != nil {
					t.Fatal(err)
				}
			})
			t.Run("fresh lease", func(t *testing.T) {
				db := reusableIntegrationDatabase(t, client)
				if db.Name() == name {
					t.Fatal("changed schema was reused")
				}
				assertCollectionCount(t, db, migrationLedgerCollectionName, 32)
				createVersionTestApplication(t, db, "schema-owner", "schema_app")
			})
		})
	}
}
