package mongo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionport "iwut-app-center/internal/version/port"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var applicationVersionIDCounter atomic.Uint64

func TestApplicationVersionRepositoryIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-VER-001 BR-VER-009 concurrent creation allocates strict unique sequences", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-sequence-race", "sequence-race")
		repository := NewApplicationVersionRepository(database)
		const contenders = 16
		start := make(chan struct{})
		results := make(chan versionCreationResult, contenders)
		var group sync.WaitGroup

		for index := 0; index < contenders; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				draft := integrationApplicationVersionDraft(t, application.ID(), "auth-sequence-race", fmt.Sprintf("v%d", index))
				<-start
				version, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-sequence-race"), draft)
				results <- versionCreationResult{version: version, err: err}
			}(index)
		}
		close(start)
		group.Wait()
		close(results)

		sequences := make([]int, 0, contenders)
		for result := range results {
			if result.err != nil {
				t.Errorf("concurrent CreateDraft error: %v", result.err)
				continue
			}
			sequences = append(sequences, int(result.version.Sequence().Int32()))
		}
		if len(sequences) != contenders {
			t.Fatalf("successful versions = %d, want %d", len(sequences), contenders)
		}
		sort.Ints(sequences)
		for index, sequence := range sequences {
			if want := index + 1; sequence != want {
				t.Fatalf("sorted sequences = %v, want 1..%d", sequences, contenders)
			}
		}
		assertNextVersionSequence(t, database, application.ID(), contenders+1)
		assertCollectionCount(t, database, applicationVersionsCollectionName, contenders)
	})

	t.Run("BR-VER-003 label uniqueness is per application and case sensitive", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		firstApplication := createVersionTestApplication(t, database, "auth-label", "labels-one")
		secondApplication := createVersionTestApplication(t, database, "auth-label", "labels-two")
		repository := NewApplicationVersionRepository(database)

		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-label"), integrationApplicationVersionDraft(t, firstApplication.ID(), "auth-label", "v1.0.0")); err != nil {
			t.Fatalf("create first label: %v", err)
		}
		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-label"), integrationApplicationVersionDraft(t, firstApplication.ID(), "auth-label", "v1.0.0")); !errors.Is(err, versionport.ErrApplicationVersionLabelAlreadyExists) {
			t.Fatalf("duplicate label error = %v, want label conflict", err)
		}
		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-label"), integrationApplicationVersionDraft(t, firstApplication.ID(), "auth-label", "V1.0.0")); err != nil {
			t.Fatalf("create differently cased label: %v", err)
		}
		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-label"), integrationApplicationVersionDraft(t, secondApplication.ID(), "auth-label", "v1.0.0")); err != nil {
			t.Fatalf("reuse label in another application: %v", err)
		}
		assertCollectionCount(t, database, applicationVersionsCollectionName, 3)
		assertNextVersionSequence(t, database, firstApplication.ID(), 3)
	})

	t.Run("BR-VER-002 distinguishes not found and current administrator mismatch", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-owner", "ownership")
		repository := NewApplicationVersionRepository(database)

		missingID, ok := shared.ParseApplicationID("01890f47-0000-7000-8000-00000000ff01")
		if !ok {
			t.Fatal("parse missing application ID")
		}
		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-owner"), integrationApplicationVersionDraft(t, missingID, "auth-owner", "missing")); !errors.Is(err, versionport.ErrApplicationNotFound) {
			t.Fatalf("missing application error = %v, want not found", err)
		}
		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-other"), integrationApplicationVersionDraft(t, application.ID(), "auth-other", "not-admin")); !errors.Is(err, versionport.ErrApplicationAdminRequired) {
			t.Fatalf("wrong administrator error = %v, want admin required", err)
		}
		assertNextVersionSequence(t, database, application.ID(), 1)
		assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
	})

	t.Run("BR-VER-002 completed administrator transfer rejects old administrator", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-old", "transferred")
		result, err := database.Collection(applicationsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "id", Value: application.ID().String()}, {Key: "adminId", Value: "auth-old"}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new"}}}},
		)
		if err != nil || result.ModifiedCount != 1 {
			t.Fatalf("complete administrator transfer: result=%#v error=%v", result, err)
		}

		repository := NewApplicationVersionRepository(database)
		_, err = repository.CreateDraft(t.Context(), shared.AuthID("auth-old"), integrationApplicationVersionDraft(t, application.ID(), "auth-old", "after-transfer"))
		if !errors.Is(err, versionport.ErrApplicationAdminRequired) {
			t.Fatalf("old administrator error = %v, want admin required", err)
		}
		assertNextVersionSequence(t, database, application.ID(), 1)
		assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
	})

	t.Run("BR-VER-002 BR-VER-009 concurrent committed transfer wins before old-admin write", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-old-race", "transfer-race")

		transferSession, err := client.StartSession()
		if err != nil {
			t.Fatalf("start transfer session: %v", err)
		}
		defer transferSession.EndSession(context.Background())
		if err := transferSession.StartTransaction(); err != nil {
			t.Fatalf("start transfer transaction: %v", err)
		}
		result, err := database.Collection(applicationsCollectionName).UpdateOne(
			drivermongo.NewSessionContext(t.Context(), transferSession),
			bson.D{{Key: "id", Value: application.ID().String()}, {Key: "adminId", Value: "auth-old-race"}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-race"}}}},
		)
		if err != nil || result.ModifiedCount != 1 {
			t.Fatalf("stage administrator transfer: result=%#v error=%v", result, err)
		}
		raceContext, cancelRace := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancelRace()

		findAndModifyStarted := make(chan struct{}, 1)
		monitor := &event.CommandMonitor{Started: func(_ context.Context, started *event.CommandStartedEvent) {
			if started.CommandName == "findAndModify" && started.DatabaseName == database.Name() {
				select {
				case findAndModifyStarted <- struct{}{}:
				default:
				}
			}
		}}
		competingClient, err := drivermongo.Connect(options.Client().
			ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).
			SetMonitor(monitor))
		if err != nil {
			t.Fatalf("connect competing MongoDB client: %v", err)
		}
		defer competingClient.Disconnect(context.Background())

		repository := NewApplicationVersionRepository(competingClient.Database(database.Name()))
		createResult := make(chan error, 1)
		go func() {
			_, createErr := repository.CreateDraft(
				raceContext,
				shared.AuthID("auth-old-race"),
				integrationApplicationVersionDraft(t, application.ID(), "auth-old-race", "racing"),
			)
			createResult <- createErr
		}()

		select {
		case <-findAndModifyStarted:
		case <-raceContext.Done():
			t.Fatalf("wait for competing version transaction: %v", raceContext.Err())
		}
		if err := transferSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit administrator transfer: %v", err)
		}
		select {
		case createErr := <-createResult:
			if !errors.Is(createErr, versionport.ErrApplicationAdminRequired) {
				t.Fatalf("old administrator concurrent error = %v, want admin required", createErr)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for competing version result: %v", raceContext.Err())
		}
		assertNextVersionSequence(t, database, application.ID(), 1)
		assertCollectionCount(t, database, applicationVersionsCollectionName, 0)
	})

	t.Run("BR-VER-002 repository rejects expected admin and created-by mismatch", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-owner", "created-by")
		repository := NewApplicationVersionRepository(database)
		_, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-owner"), integrationApplicationVersionDraft(t, application.ID(), "auth-another", "v1"))
		if err == nil || errors.Is(err, versionport.ErrApplicationAdminRequired) {
			t.Fatalf("identity mismatch error = %v, want invalid adapter input", err)
		}
		assertNextVersionSequence(t, database, application.ID(), 1)
	})

	t.Run("BR-VER-009 duplicate version ID rolls back sequence allocation", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-rollback", "rollback")
		repository := NewApplicationVersionRepository(database)
		firstDraft := integrationApplicationVersionDraft(t, application.ID(), "auth-rollback", "seed")
		if _, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-rollback"), firstDraft); err != nil {
			t.Fatalf("create seed version: %v", err)
		}

		collidingDraft := integrationApplicationVersionDraftWithID(t, firstDraft.ID(), application.ID(), "auth-rollback", "distinct-label")
		_, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-rollback"), collidingDraft)
		if err == nil || errors.Is(err, versionport.ErrApplicationVersionLabelAlreadyExists) {
			t.Fatalf("duplicate version-ID error = %v, want technical duplicate with cause", err)
		}
		if !drivermongo.IsDuplicateKeyError(err) {
			t.Fatalf("duplicate version-ID error lost MongoDB cause: %v", err)
		}
		assertNextVersionSequence(t, database, application.ID(), 2)
		assertCollectionCount(t, database, applicationVersionsCollectionName, 1)
	})

	t.Run("BR-VER-006 BR-VER-007 BR-VER-008 BR-VER-009 stores empty arrays and creation audit", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-audit", "audit")
		repository := NewApplicationVersionRepository(database)
		draft := integrationApplicationVersionDraftWithSets(t, application.ID(), "auth-audit", "empty", nil, nil, nil)
		version, err := repository.CreateDraft(t.Context(), shared.AuthID("auth-audit"), draft)
		if err != nil {
			t.Fatalf("create empty-set version: %v", err)
		}
		if version.ReviewStatus() != versiondomain.ReviewStatusDraft || version.Revision() != 1 ||
			version.CreatedBy() != version.UpdatedBy() || !version.CreatedAt().Equal(version.UpdatedAt()) {
			t.Fatalf("invalid returned lifecycle/audit: %#v", version)
		}

		var raw bson.Raw
		if err := database.Collection(applicationVersionsCollectionName).FindOne(t.Context(), bson.D{{Key: "versionId", Value: version.ID().String()}}).Decode(&raw); err != nil {
			t.Fatalf("read raw version: %v", err)
		}
		for _, field := range []string{"requiredCapabilities", "requiredScopes", "optionalScopes"} {
			value := raw.Lookup(field)
			array, ok := value.ArrayOK()
			if !ok {
				t.Fatalf("%s BSON type = %v, want array", field, value.Type)
			}
			values, err := array.Values()
			if err != nil || len(values) != 0 {
				t.Fatalf("%s values = %v, error=%v, want []", field, values, err)
			}
		}
	})

	t.Run("validators reject malformed version documents", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-validator-version", "validator-version")
		valid := integrationApplicationVersionDocument(t, application.ID(), "auth-validator-version")
		versions := database.Collection(applicationVersionsCollectionName)
		tests := []struct {
			name   string
			mutate func(*applicationVersionDocument)
		}{
			{name: "invalid required version ID", mutate: func(document *applicationVersionDocument) { document.VersionID = "" }},
			{name: "launch URL over 2048 bytes", mutate: func(document *applicationVersionDocument) {
				document.LaunchURL = "https://example.edu/" + strings.Repeat("a", 2049)
			}},
			{name: "RPC max is not greater than min", mutate: func(document *applicationVersionDocument) {
				document.RPCApiMaxVersionExclusive = document.RPCApiMinVersion
			}},
			{name: "null capability array", mutate: func(document *applicationVersionDocument) {
				document.RequiredCapabilities = nil
			}},
			{name: "duplicate capability", mutate: func(document *applicationVersionDocument) {
				document.RequiredCapabilities = []string{"camera.read.v1", "camera.read.v1"}
			}},
			{name: "invalid capability", mutate: func(document *applicationVersionDocument) { document.RequiredCapabilities = []string{"Camera.Read.v1"} }},
			{name: "duplicate required scope", mutate: func(document *applicationVersionDocument) { document.RequiredScopes = []string{"profile", "profile"} }},
			{name: "crossed scopes", mutate: func(document *applicationVersionDocument) { document.OptionalScopes = []string{"profile.basic"} }},
			{name: "non-DRAFT status", mutate: func(document *applicationVersionDocument) { document.ReviewStatus = "SUBMITTED" }},
			{name: "revision other than one", mutate: func(document *applicationVersionDocument) { document.Revision = 2 }},
			{name: "created and updated identities differ", mutate: func(document *applicationVersionDocument) { document.UpdatedBy = "auth-other" }},
			{name: "created and updated times differ", mutate: func(document *applicationVersionDocument) { document.UpdatedAt = document.CreatedAt.Add(time.Second) }},
		}
		_, err := versions.InsertOne(t.Context(), bson.D{{Key: "versionId", Value: valid.VersionID}})
		assertDocumentValidationFailure(t, err)
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				document := valid
				document.RequiredCapabilities = append([]string(nil), valid.RequiredCapabilities...)
				document.RequiredScopes = append([]string(nil), valid.RequiredScopes...)
				document.OptionalScopes = append([]string(nil), valid.OptionalScopes...)
				test.mutate(&document)
				_, err := versions.InsertOne(t.Context(), document)
				assertDocumentValidationFailure(t, err)
			})
		}
	})
}

func TestApplicationVersionRepositoryConstructionDoesNotMutateSchema(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	_ = NewApplicationVersionRepository(database)
	names, err := database.ListCollectionNames(t.Context(), bson.D{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("repository construction created collections: %v", names)
	}
}

func TestApplicationVersionMigrationUsesSimpleCollationForLabelIndex(t *testing.T) {
	client := integrationClient(t)
	database := migratedIntegrationDatabase(t, client)
	cursor, err := database.Collection(applicationVersionsCollectionName).Indexes().List(t.Context())
	if err != nil {
		t.Fatalf("list version indexes: %v", err)
	}
	defer cursor.Close(context.Background())
	var indexes []bson.M
	if err := cursor.All(t.Context(), &indexes); err != nil {
		t.Fatalf("decode version indexes: %v", err)
	}
	for _, index := range indexes {
		if index["name"] != applicationVersionLabelUniqueIndexName {
			continue
		}
		// MongoDB omits an explicitly requested "simple" collation from
		// listIndexes because it is the binary default. If it is present, it
		// must still report that exact locale. Case-sensitive behavior is
		// exercised by the repository label-uniqueness test above.
		if rawCollation, present := index["collation"]; present {
			collation, ok := rawCollation.(bson.M)
			if !ok || collation["locale"] != "simple" {
				t.Fatalf("label index collation = %#v, want locale simple", rawCollation)
			}
		}
		if unique, ok := index["unique"].(bool); !ok || !unique {
			t.Fatalf("label index unique = %#v, want true", index["unique"])
		}
		return
	}
	t.Fatalf("label index %q not found in %#v", applicationVersionLabelUniqueIndexName, indexes)
}

type versionCreationResult struct {
	version *versiondomain.ApplicationVersion
	err     error
}

func createVersionTestApplication(t *testing.T, database *drivermongo.Database, adminID, name string) *domain.Application {
	t.Helper()
	application := integrationApplication(t, adminID, name)
	if err := NewApplicationRepository(database).CreateWithinQuota(t.Context(), application, 100); err != nil {
		t.Fatalf("create version-test application: %v", err)
	}
	return application
}

func integrationApplicationVersionDraft(t *testing.T, applicationID shared.ApplicationID, createdBy, label string) *versiondomain.DraftApplicationVersion {
	t.Helper()
	return integrationApplicationVersionDraftWithSets(t, applicationID, createdBy, label, []string{"camera.read.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
}

func integrationApplicationVersionDraftWithSets(
	t *testing.T,
	applicationID shared.ApplicationID,
	createdBy, label string,
	capabilities, requiredScopes, optionalScopes []string,
) *versiondomain.DraftApplicationVersion {
	t.Helper()
	return integrationApplicationVersionDraftWithIDAndSets(t, nextIntegrationApplicationVersionID(t), applicationID, createdBy, label, capabilities, requiredScopes, optionalScopes)
}

func integrationApplicationVersionDraftWithID(
	t *testing.T,
	versionID versiondomain.ApplicationVersionID,
	applicationID shared.ApplicationID,
	createdBy, label string,
) *versiondomain.DraftApplicationVersion {
	t.Helper()
	return integrationApplicationVersionDraftWithIDAndSets(t, versionID, applicationID, createdBy, label, []string{"camera.read.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
}

func integrationApplicationVersionDraftWithIDAndSets(
	t *testing.T,
	versionID versiondomain.ApplicationVersionID,
	applicationID shared.ApplicationID,
	createdBy, label string,
	capabilityValues, requiredScopeValues, optionalScopeValues []string,
) *versiondomain.DraftApplicationVersion {
	t.Helper()
	versionLabel, err := versiondomain.NewVersionLabel(label)
	if err != nil {
		t.Fatalf("create version label: %v", err)
	}
	launchURL, err := versiondomain.NewLaunchURL("https://example.edu/apps/" + label)
	if err != nil {
		t.Fatalf("create launch URL: %v", err)
	}
	rpcRange, err := versiondomain.NewRPCApiRange(1, 3)
	if err != nil {
		t.Fatalf("create RPC range: %v", err)
	}
	capabilities, err := versiondomain.NewCapabilitySet(capabilityValues)
	if err != nil {
		t.Fatalf("create capabilities: %v", err)
	}
	scopes, err := versiondomain.NewScopeRequest(requiredScopeValues, optionalScopeValues)
	if err != nil {
		t.Fatalf("create scopes: %v", err)
	}
	draft, err := versiondomain.NewDraftApplicationVersion(
		versionID,
		applicationID,
		versionLabel,
		launchURL,
		rpcRange,
		capabilities,
		scopes,
		shared.AuthID(createdBy),
		time.Date(2026, time.September, 20, 2, 3, 4, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("create draft version: %v", err)
	}
	return draft
}

func nextIntegrationApplicationVersionID(t *testing.T) versiondomain.ApplicationVersionID {
	t.Helper()
	value := applicationVersionIDCounter.Add(1)
	id := versiondomain.ApplicationVersionID(fmt.Sprintf("01890f48-0000-7000-8000-%012x", value))
	if !id.IsValid() {
		t.Fatalf("generated invalid version ID %q", id)
	}
	return id
}

func integrationApplicationVersionDocument(t *testing.T, applicationID shared.ApplicationID, createdBy string) applicationVersionDocument {
	t.Helper()
	draft := integrationApplicationVersionDraft(t, applicationID, createdBy, "validator")
	sequence, err := versiondomain.NewVersionSequence(1)
	if err != nil {
		t.Fatalf("create sequence: %v", err)
	}
	document, err := applicationVersionToDocument(draft, sequence)
	if err != nil {
		t.Fatalf("map version document: %v", err)
	}
	return document
}

func assertNextVersionSequence(t *testing.T, database *drivermongo.Database, applicationID shared.ApplicationID, want int) {
	t.Helper()
	var document struct {
		NextVersionSequence int32 `bson:"nextVersionSequence"`
	}
	if err := database.Collection(applicationsCollectionName).
		FindOne(t.Context(), bson.D{{Key: "id", Value: applicationID.String()}}).
		Decode(&document); err != nil {
		t.Fatalf("read next version sequence: %v", err)
	}
	if document.NextVersionSequence != int32(want) {
		t.Fatalf("nextVersionSequence = %d, want %d", document.NextVersionSequence, want)
	}
}
