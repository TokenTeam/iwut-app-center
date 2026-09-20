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
			{name: "unknown status", mutate: func(document *applicationVersionDocument) { document.ReviewStatus = "UNKNOWN" }},
			{name: "revision below one", mutate: func(document *applicationVersionDocument) { document.Revision = 0 }},
			{name: "empty updated identity", mutate: func(document *applicationVersionDocument) { document.UpdatedBy = "" }},
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

func TestApplicationVersionRepositoryUpdateIntegration(t *testing.T) {
	client := integrationClient(t)

	t.Run("BR-VER-011 BR-VER-012 BR-VER-013 BR-VER-014 BR-VER-016 atomically replaces all editable fields and preserves no-op audit", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-update", "update-success")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(
			t.Context(), shared.AuthID("auth-update"), integrationApplicationVersionDraft(t, application.ID(), "auth-update", "v1"),
		)
		if err != nil {
			t.Fatalf("create seed version: %v", err)
		}
		replacement := integrationApplicationVersionReplacement(
			t, "v2", "http://localhost:3000/app", []string{"user.profile.v2", "camera.read.v1"},
			[]string{"schedule.read", "profile.basic"}, nil,
		)
		// MongoDB stores BSON datetimes with millisecond precision, so the audit
		// instant under test must be millisecond-aligned while still exercising
		// non-UTC input conversion.
		updatedAt := time.Date(2026, time.September, 20, 10, 11, 12, 130_000_000, time.FixedZone("CST", 8*60*60))
		updated, err := repository.ReplaceDraft(
			t.Context(), application.ID(), created.ID(), "auth-update", 1, replacement, updatedAt,
		)
		if err != nil {
			t.Fatalf("replace draft: %v", err)
		}
		if updated.ID() != created.ID() || updated.ApplicationID() != created.ApplicationID() || updated.Sequence() != created.Sequence() ||
			updated.CreatedBy() != created.CreatedBy() || !updated.CreatedAt().Equal(created.CreatedAt()) || updated.ReviewStatus() != versiondomain.ReviewStatusDraft {
			t.Fatal("immutable version fields changed")
		}
		if updated.VersionLabel().String() != "v2" || updated.LaunchURL().String() != "http://localhost:3000/app" ||
			updated.RPCApiRange().Minimum() != 2 || updated.RPCApiRange().MaximumExclusive() != 5 || updated.Revision() != 2 ||
			updated.UpdatedBy() != "auth-update" || !updated.UpdatedAt().Equal(updatedAt) || updated.UpdatedAt().Location() != time.UTC {
			t.Fatalf("updated version content/audit is incorrect: %#v", updated)
		}
		if want := []versiondomain.CapabilityName{"camera.read.v1", "user.profile.v2"}; fmt.Sprint(updated.RequiredCapabilities()) != fmt.Sprint(want) {
			t.Fatalf("capabilities = %v, want %v", updated.RequiredCapabilities(), want)
		}
		if updated.OptionalScopes() == nil || len(updated.OptionalScopes()) != 0 {
			t.Fatalf("optional scopes = %#v, want non-nil empty array", updated.OptionalScopes())
		}

		noOp, err := repository.ReplaceDraft(
			t.Context(), application.ID(), created.ID(), "auth-update", 2, replacement, updatedAt.Add(time.Hour),
		)
		if err != nil {
			t.Fatalf("repeat normalized replacement: %v", err)
		}
		if noOp.Revision() != 2 || !noOp.UpdatedAt().Equal(updatedAt) || noOp.UpdatedBy() != "auth-update" {
			t.Fatalf("no-op changed audit: revision=%d by=%s at=%v", noOp.Revision(), noOp.UpdatedBy(), noOp.UpdatedAt())
		}
		stored := readApplicationVersionDocument(t, database, created.ID())
		if stored.Revision != 2 || stored.VersionLabel != "v2" || stored.UpdatedBy != "auth-update" || !stored.UpdatedAt.Equal(updatedAt) {
			t.Fatalf("stored update is not atomic: %#v", stored)
		}
	})

	t.Run("BR-VER-010 BR-VER-013 BR-VER-016 classifies ownership state revision and hidden path mismatch", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-classify", "update-classify")
		otherApplication := createVersionTestApplication(t, database, "auth-classify", "update-other")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(
			t.Context(), "auth-classify", integrationApplicationVersionDraft(t, application.ID(), "auth-classify", "v1"),
		)
		if err != nil {
			t.Fatalf("create seed version: %v", err)
		}
		replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/v2", nil, nil, nil)

		tests := []struct {
			name          string
			applicationID shared.ApplicationID
			adminID       shared.AuthID
			revision      int64
			want          error
		}{
			{name: "cross-application version ID", applicationID: otherApplication.ID(), adminID: "auth-classify", revision: 1, want: versionport.ErrApplicationVersionNotFound},
			{name: "wrong administrator", applicationID: application.ID(), adminID: "auth-other", revision: 1, want: versionport.ErrApplicationAdminRequired},
			{name: "stale revision", applicationID: application.ID(), adminID: "auth-classify", revision: 2, want: versionport.ErrApplicationVersionRevisionConflict},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				version, err := repository.ReplaceDraft(
					t.Context(), test.applicationID, created.ID(), test.adminID, test.revision, replacement, time.Now(),
				)
				if version != nil || !errors.Is(err, test.want) {
					t.Fatalf("ReplaceDraft() = (%v, %v), want nil and %v", version, err, test.want)
				}
			})
		}

		result, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
			t.Context(),
			bson.D{{Key: "versionId", Value: created.ID().String()}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: "SUBMITTED"}}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}},
		)
		if err != nil || result.ModifiedCount != 1 {
			t.Fatalf("seed submitted state: result=%#v error=%v", result, err)
		}
		version, err := repository.ReplaceDraft(
			t.Context(), application.ID(), created.ID(), "auth-classify", 2, replacement, time.Now(),
		)
		if version != nil || !errors.Is(err, versionport.ErrApplicationVersionNotDraft) {
			t.Fatalf("non-draft ReplaceDraft() = (%v, %v), want not draft", version, err)
		}
	})

	t.Run("BR-VER-013 two concurrent replacements with one expected revision allow one success", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-revision-race", "revision-race")
		repository := NewApplicationVersionRepository(database)
		created, err := repository.CreateDraft(
			t.Context(), "auth-revision-race", integrationApplicationVersionDraft(t, application.ID(), "auth-revision-race", "v1"),
		)
		if err != nil {
			t.Fatalf("create seed version: %v", err)
		}
		start := make(chan struct{})
		results := make(chan versionCreationResult, 2)
		var group sync.WaitGroup
		for index := 0; index < 2; index++ {
			index := index
			replacement := integrationApplicationVersionReplacement(
				t, fmt.Sprintf("v%d", index+2), fmt.Sprintf("https://example.edu/v%d", index+2), nil, nil, nil,
			)
			group.Add(1)
			go func() {
				defer group.Done()
				<-start
				version, replaceErr := repository.ReplaceDraft(
					t.Context(), application.ID(), created.ID(), "auth-revision-race", 1, replacement,
					time.Date(2026, time.September, 20, 11, 0, index, 0, time.UTC),
				)
				results <- versionCreationResult{version: version, err: replaceErr}
			}()
		}
		close(start)
		group.Wait()
		close(results)
		var successes, conflicts int
		for result := range results {
			switch {
			case result.err == nil:
				successes++
				if result.version == nil || result.version.Revision() != 2 {
					t.Errorf("successful result = %#v, want revision 2", result.version)
				}
			case errors.Is(result.err, versionport.ErrApplicationVersionRevisionConflict):
				conflicts++
			default:
				t.Errorf("unexpected concurrent result: %v", result.err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes/conflicts = %d/%d, want 1/1", successes, conflicts)
		}
		if stored := readApplicationVersionDocument(t, database, created.ID()); stored.Revision != 2 {
			t.Fatalf("stored revision = %d, want 2", stored.Revision)
		}
	})

	t.Run("BR-VER-013 BR-VER-016 label conflict rolls back the entire replacement", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-label-update", "label-update")
		repository := NewApplicationVersionRepository(database)
		first, err := repository.CreateDraft(
			t.Context(), "auth-label-update", integrationApplicationVersionDraft(t, application.ID(), "auth-label-update", "v1"),
		)
		if err != nil {
			t.Fatalf("create first version: %v", err)
		}
		if _, err := repository.CreateDraft(
			t.Context(), "auth-label-update", integrationApplicationVersionDraft(t, application.ID(), "auth-label-update", "v2"),
		); err != nil {
			t.Fatalf("create second version: %v", err)
		}
		replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/collision", nil, nil, nil)
		updated, err := repository.ReplaceDraft(
			t.Context(), application.ID(), first.ID(), "auth-label-update", 1, replacement, time.Now(),
		)
		if updated != nil || !errors.Is(err, versionport.ErrApplicationVersionLabelAlreadyExists) {
			t.Fatalf("ReplaceDraft() = (%v, %v), want label conflict", updated, err)
		}
		stored := readApplicationVersionDocument(t, database, first.ID())
		if stored.VersionLabel != "v1" || stored.Revision != 1 || stored.LaunchURL == "https://example.edu/collision" {
			t.Fatalf("failed replacement partially persisted: %#v", stored)
		}
	})

	t.Run("BR-VER-016 committed administrator transfer wins against an in-flight old-admin replacement", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-old-update", "admin-update-race")
		seedRepository := NewApplicationVersionRepository(database)
		created, err := seedRepository.CreateDraft(
			t.Context(), "auth-old-update", integrationApplicationVersionDraft(t, application.ID(), "auth-old-update", "v1"),
		)
		if err != nil {
			t.Fatalf("create seed version: %v", err)
		}

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
			bson.D{{Key: "id", Value: application.ID().String()}, {Key: "adminId", Value: "auth-old-update"}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "adminId", Value: "auth-new-update"}}}},
		)
		if err != nil || result.ModifiedCount != 1 {
			t.Fatalf("stage administrator transfer: result=%#v error=%v", result, err)
		}

		raceContext, cancelRace := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancelRace()
		ownershipCheckStarted := make(chan struct{}, 1)
		monitor := &event.CommandMonitor{Started: func(_ context.Context, started *event.CommandStartedEvent) {
			if started.DatabaseName == database.Name() && started.CommandName == "findAndModify" &&
				started.Command.Lookup("findAndModify").StringValue() == applicationsCollectionName {
				select {
				case ownershipCheckStarted <- struct{}{}:
				default:
				}
			}
		}}
		competingClient, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(monitor))
		if err != nil {
			t.Fatalf("connect competing client: %v", err)
		}
		defer competingClient.Disconnect(context.Background())
		repository := NewApplicationVersionRepository(competingClient.Database(database.Name()))
		replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/v2", nil, nil, nil)
		updateResult := make(chan error, 1)
		go func() {
			_, replaceErr := repository.ReplaceDraft(
				raceContext, application.ID(), created.ID(), "auth-old-update", 1, replacement, time.Now(),
			)
			updateResult <- replaceErr
		}()
		select {
		case <-ownershipCheckStarted:
		case <-raceContext.Done():
			t.Fatalf("wait for ownership check: %v", raceContext.Err())
		}
		if err := transferSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit administrator transfer: %v", err)
		}
		select {
		case err := <-updateResult:
			if !errors.Is(err, versionport.ErrApplicationAdminRequired) {
				t.Fatalf("old administrator result = %v, want admin required", err)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for replacement result: %v", raceContext.Err())
		}
		if stored := readApplicationVersionDocument(t, database, created.ID()); stored.Revision != 1 {
			t.Fatalf("old-admin replacement persisted with revision %d", stored.Revision)
		}
	})

	t.Run("BR-VER-010 BR-VER-016 committed submission wins against an in-flight replacement", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "auth-submit-race", "submit-update-race")
		seedRepository := NewApplicationVersionRepository(database)
		created, err := seedRepository.CreateDraft(
			t.Context(), "auth-submit-race", integrationApplicationVersionDraft(t, application.ID(), "auth-submit-race", "v1"),
		)
		if err != nil {
			t.Fatalf("create seed version: %v", err)
		}

		submitSession, err := client.StartSession()
		if err != nil {
			t.Fatalf("start submit session: %v", err)
		}
		defer submitSession.EndSession(context.Background())
		if err := submitSession.StartTransaction(); err != nil {
			t.Fatalf("start submit transaction: %v", err)
		}
		result, err := database.Collection(applicationVersionsCollectionName).UpdateOne(
			drivermongo.NewSessionContext(t.Context(), submitSession),
			bson.D{{Key: "versionId", Value: created.ID().String()}, {Key: "reviewStatus", Value: "DRAFT"}, {Key: "revision", Value: int64(1)}},
			bson.D{{Key: "$set", Value: bson.D{{Key: "reviewStatus", Value: "SUBMITTED"}}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}},
		)
		if err != nil || result.ModifiedCount != 1 {
			t.Fatalf("stage submission: result=%#v error=%v", result, err)
		}

		raceContext, cancelRace := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancelRace()
		versionReadStarted := make(chan struct{}, 1)
		monitor := &event.CommandMonitor{Started: func(_ context.Context, started *event.CommandStartedEvent) {
			if started.DatabaseName == database.Name() && started.CommandName == "find" &&
				started.Command.Lookup("find").StringValue() == applicationVersionsCollectionName {
				select {
				case versionReadStarted <- struct{}{}:
				default:
				}
			}
		}}
		competingClient, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(monitor))
		if err != nil {
			t.Fatalf("connect competing client: %v", err)
		}
		defer competingClient.Disconnect(context.Background())
		repository := NewApplicationVersionRepository(competingClient.Database(database.Name()))
		replacement := integrationApplicationVersionReplacement(t, "v2", "https://example.edu/v2", nil, nil, nil)
		updateResult := make(chan error, 1)
		go func() {
			_, replaceErr := repository.ReplaceDraft(
				raceContext, application.ID(), created.ID(), "auth-submit-race", 1, replacement, time.Now(),
			)
			updateResult <- replaceErr
		}()
		select {
		case <-versionReadStarted:
		case <-raceContext.Done():
			t.Fatalf("wait for version read: %v", raceContext.Err())
		}
		if err := submitSession.CommitTransaction(raceContext); err != nil {
			t.Fatalf("commit submission: %v", err)
		}
		select {
		case err := <-updateResult:
			if !errors.Is(err, versionport.ErrApplicationVersionNotDraft) {
				t.Fatalf("replacement result = %v, want not draft", err)
			}
		case <-raceContext.Done():
			t.Fatalf("wait for replacement result: %v", raceContext.Err())
		}
		stored := readApplicationVersionDocument(t, database, created.ID())
		if stored.ReviewStatus != "SUBMITTED" || stored.Revision != 2 || stored.VersionLabel != "v1" {
			t.Fatalf("replacement modified submitted version: %#v", stored)
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

func TestApplicationVersionDraftUpdateMigration_UpgradesExisting0002Schema(t *testing.T) {
	client := integrationClient(t)
	database := integrationDatabase(t, client)
	migrator := NewMigrator(database)
	if err := migrator.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	if err := migrator.applyMigration(t.Context(), applicationCreationMigrationID, migrator.applyApplicationCreationMigration); err != nil {
		t.Fatalf("apply 0001: %v", err)
	}
	if err := migrator.applyMigration(t.Context(), applicationVersionMigrationID, migrator.applyApplicationVersionMigration); err != nil {
		t.Fatalf("apply 0002: %v", err)
	}
	application := createVersionTestApplication(t, database, "auth-migration", "migration-update")
	repository := NewApplicationVersionRepository(database)
	created, err := repository.CreateDraft(
		t.Context(), "auth-migration", integrationApplicationVersionDraft(t, application.ID(), "auth-migration", "v1"),
	)
	if err != nil {
		t.Fatalf("create version under 0002 schema: %v", err)
	}
	versions := database.Collection(applicationVersionsCollectionName)
	_, err = versions.UpdateOne(
		t.Context(),
		bson.D{{Key: "versionId", Value: created.ID().String()}},
		bson.D{{Key: "$set", Value: bson.D{{Key: "updatedBy", Value: "auth-editor"}}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}},
	)
	assertDocumentValidationFailure(t, err)

	if err := migrator.Migrate(t.Context()); err != nil {
		t.Fatalf("upgrade to 0003: %v", err)
	}
	if err := migrator.Migrate(t.Context()); err != nil {
		t.Fatalf("repeat migrations: %v", err)
	}
	assertCollectionCount(t, database, migrationLedgerCollectionName, 3)
	result, err := versions.UpdateOne(
		t.Context(),
		bson.D{{Key: "versionId", Value: created.ID().String()}},
		bson.D{{Key: "$set", Value: bson.D{
			{Key: "reviewStatus", Value: "SUBMITTED"},
			{Key: "updatedBy", Value: "auth-editor"},
			{Key: "updatedAt", Value: time.Date(2026, time.September, 20, 15, 0, 0, 0, time.UTC)},
		}}, {Key: "$inc", Value: bson.D{{Key: "revision", Value: int64(1)}}}},
	)
	if err != nil || result.ModifiedCount != 1 {
		t.Fatalf("write fields enabled by 0003: result=%#v error=%v", result, err)
	}
	stored := readApplicationVersionDocument(t, database, created.ID())
	if stored.ReviewStatus != "SUBMITTED" || stored.Revision != 2 || stored.UpdatedBy != "auth-editor" {
		t.Fatalf("upgraded document = %#v", stored)
	}
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

func integrationApplicationVersionReplacement(
	t *testing.T,
	labelValue, launchURLValue string,
	capabilityValues, requiredScopeValues, optionalScopeValues []string,
) versiondomain.DraftApplicationVersionReplacement {
	t.Helper()
	label, err := versiondomain.NewVersionLabel(labelValue)
	if err != nil {
		t.Fatalf("create replacement label: %v", err)
	}
	launchURL, err := versiondomain.NewLaunchURL(launchURLValue)
	if err != nil {
		t.Fatalf("create replacement launch URL: %v", err)
	}
	rpcRange, err := versiondomain.NewRPCApiRange(2, 5)
	if err != nil {
		t.Fatalf("create replacement RPC range: %v", err)
	}
	capabilities, err := versiondomain.NewCapabilitySet(capabilityValues)
	if err != nil {
		t.Fatalf("create replacement capabilities: %v", err)
	}
	scopes, err := versiondomain.NewScopeRequest(requiredScopeValues, optionalScopeValues)
	if err != nil {
		t.Fatalf("create replacement scopes: %v", err)
	}
	replacement, err := versiondomain.NewDraftApplicationVersionReplacement(label, launchURL, rpcRange, capabilities, scopes)
	if err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	return replacement
}

func readApplicationVersionDocument(
	t *testing.T,
	database *drivermongo.Database,
	versionID versiondomain.ApplicationVersionID,
) applicationVersionDocument {
	t.Helper()
	var document applicationVersionDocument
	if err := database.Collection(applicationVersionsCollectionName).
		FindOne(t.Context(), bson.D{{Key: "versionId", Value: versionID.String()}}).
		Decode(&document); err != nil {
		t.Fatalf("read application version %s: %v", versionID, err)
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
