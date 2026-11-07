package mongo

import (
	"context"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	dm "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/script/port"
)

func scriptPublicationCommand(t *testing.T, resource domain.ResourceID, version domain.VersionID, action string, revision int64, at time.Time) port.PublicationCommand {
	t.Helper()
	return port.PublicationCommand{ResourceID: resource, VersionID: version, Action: action, ExpectedRevision: revision,
		Reason: "controlled distribution change", EventID: domain.PublicationEventID(nextIntegrationApplicationID(t)), Actor: "publisher", At: at}
}

func approvedPublicationFixture(t *testing.T, db *dm.Database) (domain.Version, domain.Version, time.Time) {
	t.Helper()
	repo, first, submission := newScriptSubmissionFixture(t, db)
	if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	at := submission.At.Add(time.Second).Truncate(time.Millisecond)
	seedTerminalScriptDependency(t, db, string(first.ID), string(submission.ReviewID), "APPROVED", at)
	second, err := domain.NewDraft(domain.VersionID(nextIntegrationApplicationID(t)), first.ResourceID, "release-2", first.Content, "author-2", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateDraft(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	secondSubmission := port.SubmitDraft{ResourceID: first.ResourceID, VersionID: second.ID, ReviewID: domain.ReviewID(nextIntegrationApplicationID(t)), ExpectedRevision: 1, Actor: "submitter-2", At: at.Add(time.Second)}
	if _, err := repo.SubmitDraft(t.Context(), secondSubmission); err != nil {
		t.Fatal(err)
	}
	seedTerminalScriptDependency(t, db, string(second.ID), string(secondSubmission.ReviewID), "APPROVED", at.Add(2*time.Second))
	return first, second, at.Add(3*time.Second + 123*time.Microsecond)
}

func TestScriptPublicationIntegration_UCAPP046_BRSCR015016017018(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	first, second, at := approvedPublicationFixture(t, db)
	writer := NewScriptPublicationRepository(db)
	query := NewScriptQueryRepository(db, ScriptQueryCursorKey{1})
	state, err := query.GetPublication(t.Context(), first.ResourceID)
	if err != nil || state.Head != nil || state.PublicationRevision != 1 || state.AvailabilityRevision != 1 || state.Availability != domain.DistributionAvailable {
		t.Fatalf("initial state %+v %v", state, err)
	}
	assertCollectionCount(t, db, scriptPublishedVersionsCollection, 0)
	clear := scriptPublicationCommand(t, first.ResourceID, "", domain.ClearScriptHead, 1, at)
	result, err := writer.ApplyPublication(t.Context(), clear)
	if err != nil || result.Event != nil || !reflect.DeepEqual(result.State, state) {
		t.Fatalf("empty head no-op: %+v %v", result, err)
	}
	publish := scriptPublicationCommand(t, first.ResourceID, first.ID, domain.PublishScript, 1, at)
	result, err = writer.ApplyPublication(t.Context(), publish)
	if err != nil || result.Event == nil || result.State.Head.VersionID != first.ID || result.State.PublicationRevision != 2 || !result.Event.At.Equal(at.Truncate(time.Millisecond)) {
		t.Fatalf("first publication %+v %v", result, err)
	}
	assertCollectionCount(t, db, scriptPublishedVersionsCollection, 1)
	if stored, err := query.GetPublication(t.Context(), first.ResourceID); err != nil || !reflect.DeepEqual(stored, result.State) {
		t.Fatalf("ACK readback %v", err)
	}
	publish.ExpectedRevision = 2
	unchanged, err := writer.ApplyPublication(t.Context(), publish)
	if err != nil || unchanged.Event != nil || !reflect.DeepEqual(unchanged.State, result.State) {
		t.Fatal("same head no-op", err)
	}
	publish.ExpectedRevision = 1
	if _, err := writer.ApplyPublication(t.Context(), publish); !errors.Is(err, domain.ErrPublicationRevisionConflict) {
		t.Fatal("stale same head accepted", err)
	}
	publish.VersionID, publish.ExpectedRevision, publish.EventID = second.ID, 2, domain.PublicationEventID(nextIntegrationApplicationID(t))
	if result, err = writer.ApplyPublication(t.Context(), publish); err != nil || result.State.Head.VersionID != second.ID {
		t.Fatal(err)
	}
	// Return to A, then clear the recommendation. Both permanent qualifications survive.
	publish.VersionID, publish.ExpectedRevision, publish.EventID = first.ID, 3, domain.PublicationEventID(nextIntegrationApplicationID(t))
	if _, err := writer.ApplyPublication(t.Context(), publish); err != nil {
		t.Fatal(err)
	}
	clear.ExpectedRevision, clear.EventID = 4, domain.PublicationEventID(nextIntegrationApplicationID(t))
	if result, err = writer.ApplyPublication(t.Context(), clear); err != nil || result.State.Head != nil || result.State.PublicationRevision != 5 {
		t.Fatal("clear", err)
	}
	assertCollectionCount(t, db, scriptPublishedVersionsCollection, 2)
	suspend := scriptPublicationCommand(t, first.ResourceID, "", domain.SuspendScript, 1, at.Add(time.Second))
	if result, err = writer.ApplyPublication(t.Context(), suspend); err != nil || result.State.Availability != domain.DistributionSuspended || result.State.AvailabilityRevision != 2 || result.State.PublicationRevision != 5 {
		t.Fatal("suspend", err)
	}
	publish.ExpectedRevision, publish.EventID = 5, domain.PublicationEventID(nextIntegrationApplicationID(t))
	if _, err := writer.ApplyPublication(t.Context(), publish); !errors.Is(err, domain.ErrDistributionSuspended) {
		t.Fatal("publication while suspended", err)
	}
	suspend.ExpectedRevision = 2
	if noop, err := writer.ApplyPublication(t.Context(), suspend); err != nil || noop.Event != nil || !reflect.DeepEqual(noop.State, result.State) {
		t.Fatal("repeat suspend", err)
	}
	suspend.ExpectedRevision = 1
	if _, err := writer.ApplyPublication(t.Context(), suspend); !errors.Is(err, domain.ErrAvailabilityRevisionConflict) {
		t.Fatal("stale gate", err)
	}
	restore := scriptPublicationCommand(t, first.ResourceID, "", domain.RestoreScript, 2, at.Add(2*time.Second))
	if result, err = writer.ApplyPublication(t.Context(), restore); err != nil || result.State.Head != nil || result.State.PublicationRevision != 5 || result.State.AvailabilityRevision != 3 || result.State.Availability != domain.DistributionAvailable {
		t.Fatal("restore changed publication", err)
	}
	assertCollectionCount(t, db, scriptPublicationEventsCollection, 6)
	scope := port.ScriptReadScope{Actor: "publisher", Publish: true}
	page, err := query.ListPublicationHistory(t.Context(), scope, first.ResourceID, port.ScriptPageRequest{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Action != domain.ClearScriptHead || page.NextCursor == "" {
		t.Fatalf("history %+v %v", page, err)
	}
	seen := map[domain.PublicationEventID]bool{page.Items[0].EventID: true}
	cursor := page.NextCursor
	for cursor != "" {
		page, err = query.ListPublicationHistory(t.Context(), scope, first.ResourceID, port.ScriptPageRequest{Limit: 1, Cursor: cursor})
		if err != nil || len(page.Items) != 1 || seen[page.Items[0].EventID] {
			t.Fatal("same-time history pagination", err)
		}
		seen[page.Items[0].EventID] = true
		cursor = page.NextCursor
	}
	if len(seen) != 4 {
		t.Fatal("availability audit leaked into publication history")
	}
	scope.Actor = "other"
	if _, err := query.ListPublicationHistory(t.Context(), scope, first.ResourceID, port.ScriptPageRequest{Limit: 1, Cursor: "invalid"}); !errors.Is(err, port.ErrInvalidScriptCursor) {
		t.Fatal(err)
	}
}

func TestScriptPublicationRollbackIntegration_UCAPP046_BRSCR015018(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	first, second, at := approvedPublicationFixture(t, db)
	writer := NewScriptPublicationRepository(db)
	command := scriptPublicationCommand(t, first.ResourceID, first.ID, domain.PublishScript, 1, at)
	firstResult, err := writer.ApplyPublication(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	command.VersionID, command.ExpectedRevision = second.ID, 2
	if _, err := writer.ApplyPublication(t.Context(), command); !dm.IsDuplicateKeyError(err) {
		t.Fatalf("expected final audit insertion failure: %v", err)
	}
	stored, _, err := writer.loadState(t.Context(), first.ResourceID)
	if err != nil || !reflect.DeepEqual(stored, firstResult.State) {
		t.Fatal("partial publication persisted", err)
	}
	assertCollectionCount(t, db, scriptPublishedVersionsCollection, 1)
	assertCollectionCount(t, db, scriptPublicationEventsCollection, 1)
	// Missing immutable qualification cannot be silently reconstructed by another publish.
	if _, err := db.Collection(scriptPublishedVersionsCollection).DeleteMany(t.Context(), bson.M{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := writer.loadState(t.Context(), first.ResourceID); !errors.Is(err, domain.ErrPublicationInconsistent) {
		t.Fatal("missing proof hidden", err)
	}
}

func TestScriptPublicationConcurrencyIntegration_UCAPP046_BRSCR017018(t *testing.T) {
	for _, winner := range []string{"publish", "suspend"} {
		t.Run(winner, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			first, _, at := approvedPublicationFixture(t, db)
			paused, resume := make(chan struct{}), make(chan struct{})
			var once sync.Once
			client, err := dm.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
				if e.CommandName == "update" && e.Command.Lookup("update").StringValue() == scriptAvailabilityCollection {
					once.Do(func() {
						close(paused)
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
			writer := NewScriptPublicationRepository(db)
			loserWriter := NewScriptPublicationRepository(client.Database(db.Name()))
			publish := scriptPublicationCommand(t, first.ResourceID, first.ID, domain.PublishScript, 1, at)
			suspend := scriptPublicationCommand(t, first.ResourceID, "", domain.SuspendScript, 1, at)
			loser := make(chan error, 1)
			if winner == "publish" {
				go func() { _, err := loserWriter.ApplyPublication(t.Context(), suspend); loser <- err }()
			} else {
				go func() { _, err := loserWriter.ApplyPublication(t.Context(), publish); loser <- err }()
			}
			select {
			case <-paused:
			case <-time.After(10 * time.Second):
				t.Fatal("command did not reach shared gate")
			}
			var winnerErr error
			if winner == "publish" {
				_, winnerErr = writer.ApplyPublication(t.Context(), publish)
			} else {
				_, winnerErr = writer.ApplyPublication(t.Context(), suspend)
			}
			close(resume)
			if winnerErr != nil {
				t.Fatal(winnerErr)
			}
			loserErr := <-loser
			if winner == "publish" {
				if loserErr != nil {
					t.Fatal(loserErr)
				}
			} else if !errors.Is(loserErr, domain.ErrDistributionSuspended) {
				t.Fatal("late publication succeeded", loserErr)
			}
			state, _, err := writer.loadState(t.Context(), first.ResourceID)
			if err != nil || state.Availability != domain.DistributionSuspended {
				t.Fatal(err)
			}
			expected := 0
			if winner == "publish" {
				expected = 1
			}
			assertCollectionCount(t, db, scriptPublishedVersionsCollection, expected)
		})
	}
	db := migratedIntegrationDatabase(t, integrationClient(t))
	first, second, at := approvedPublicationFixture(t, db)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, version := range []domain.VersionID{first.ID, second.ID} {
		command := scriptPublicationCommand(t, first.ResourceID, version, domain.PublishScript, 1, at)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), command)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, domain.ErrPublicationRevisionConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("competing publications did not consume one revision")
	}
	assertCollectionCount(t, db, scriptPublishedVersionsCollection, 1)
	assertCollectionCount(t, db, scriptPublicationEventsCollection, 1)
}

func TestScriptPublicationQualificationIntegration_UCAPP046_BRSCR015018(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	repo, draft, submission := newScriptSubmissionFixture(t, db)
	writer := NewScriptPublicationRepository(db)
	command := scriptPublicationCommand(t, draft.ResourceID, draft.ID, domain.PublishScript, 1, submission.At.Add(2*time.Second))
	if _, err := writer.ApplyPublication(t.Context(), command); !errors.Is(err, domain.ErrScriptNotApproved) {
		t.Fatal("draft published", err)
	}
	if _, err := repo.SubmitDraft(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ApplyPublication(t.Context(), command); !errors.Is(err, domain.ErrScriptNotApproved) {
		t.Fatal("pending published", err)
	}
	reject := scriptDecisionCommand(t, draft, submission, "REJECT")
	if _, err := repo.DecideReview(t.Context(), reject); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ApplyPublication(t.Context(), command); !errors.Is(err, domain.ErrScriptNotApproved) {
		t.Fatal("rejected published", err)
	}
	assertCollectionCount(t, db, scriptPublishedVersionsCollection, 0)
	assertCollectionCount(t, db, scriptPublicationEventsCollection, 0)
	first, second, at := approvedPublicationFixture(t, db)
	command = scriptPublicationCommand(t, first.ResourceID, first.ID, domain.PublishScript, 1, at)
	if _, err := writer.ApplyPublication(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	command.ResourceID = draft.ResourceID
	if _, err := writer.ApplyPublication(t.Context(), command); !errors.Is(err, domain.ErrVersionNotFound) {
		t.Fatal("cross-resource version published", err)
	}
	command.ResourceID, command.VersionID, command.ExpectedRevision, command.EventID = first.ResourceID, second.ID, 2, domain.PublicationEventID(nextIntegrationApplicationID(t))
	if _, err := writer.ApplyPublication(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	// Clearing the head cannot hide a broken historical publication proof.
	if _, err := db.Collection(scriptPublishedVersionsCollection).DeleteOne(t.Context(), bson.M{"version.versionId": first.ID}); err != nil {
		t.Fatal(err)
	}
	command.VersionID, command.ExpectedRevision, command.EventID = first.ID, 3, domain.PublicationEventID(nextIntegrationApplicationID(t))
	if _, err := writer.ApplyPublication(t.Context(), command); !errors.Is(err, domain.ErrPublicationInconsistent) {
		t.Fatal("historical proof reconstructed", err)
	}
}

func TestScriptPublicationInitializationRollbackIntegration_UCAPP046_BRSCR015(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	at := time.Now().UTC().Truncate(time.Millisecond)
	resource, err := domain.NewResource(domain.ResourceID(nextIntegrationApplicationID(t)), domain.Metadata{Name: "Script", Purpose: "Test"}, "owner", at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(scriptAvailabilityCollection).InsertOne(t.Context(), scriptAvailabilityDocument{ResourceID: string(resource.ID), State: domain.DistributionAvailable, Revision: 1, CoordinationRevision: 1, UpdatedBy: "owner", UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScriptResourceRepository(db).CreateResource(t.Context(), resource); !dm.IsDuplicateKeyError(err) {
		t.Fatal("expected initialization conflict", err)
	}
	assertCollectionCount(t, db, scriptResourcesCollectionName, 0)
	assertCollectionCount(t, db, scriptPublicationsCollection, 0)
	assertCollectionCount(t, db, scriptResourceAuditsCollectionName, 0)
}
