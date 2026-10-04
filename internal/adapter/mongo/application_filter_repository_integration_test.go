package mongo

import (
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	filterdomain "iwut-app-center/internal/filter/domain"
	filterport "iwut-app-center/internal/filter/port"
	"iwut-app-center/internal/shared"
)

func integrationFilterRule(t *testing.T, country string) filterdomain.Rule {
	t.Helper()
	value, err := filterdomain.NewStringScalar(country)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := filterdomain.NewPredicate("profile.country", filterdomain.PredicateOperatorEQ, &value)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func integrationFilterCandidate(t *testing.T, current *filterdomain.ApplicationFilter, country string, admin string, at time.Time) (*filterdomain.ApplicationFilter, *filterdomain.ApplicationFilterRevision) {
	t.Helper()
	next, revision, err := current.PublishRule(filterdomain.FilterRevisionID(nextIntegrationTesterJoinLinkID(t)), integrationFilterRule(t, country), sharedAuthID(admin), at.UTC())
	if err != nil {
		t.Fatal(err)
	}
	return next, revision
}

func sharedAuthID(value string) shared.AuthID { return shared.AuthID(value) }

func TestApplicationFilterRepositoryIntegration_BR_FLT_002_003_008_009_010(t *testing.T) {
	client := integrationClient(t)

	t.Run("default set clear preserves immutable history and write fence", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "filter-admin", "filter_lifecycle")
		repository := NewApplicationFilterRepository(database)
		current, err := repository.LoadForAdmin(t.Context(), application.ID(), application.AdminID())
		if err != nil || current.Revision() != 0 || !current.IsAllowAll() {
			t.Fatalf("default=%v err=%v", current, err)
		}
		beforeApplication := readTesterLinkApplication(t, database, application.ID())
		set, setRevision := integrationFilterCandidate(t, current, "CN", application.AdminID().String(), time.Date(2026, 10, 4, 9, 0, 0, 123456789, time.UTC))
		committed, err := repository.Commit(t.Context(), application.AdminID(), 0, set, setRevision)
		if err != nil || committed.Revision() != 1 || committed.NextSequence() != 2 || !committed.MatchesRule(integrationFilterRule(t, "CN")) {
			t.Fatalf("set=%v err=%v", committed, err)
		}
		afterSetApplication := readTesterLinkApplication(t, database, application.ID())
		if afterSetApplication.CoordinationRevision != beforeApplication.CoordinationRevision+1 {
			t.Fatalf("coordination revision=%d want=%d", afterSetApplication.CoordinationRevision, beforeApplication.CoordinationRevision+1)
		}

		clearID := filterdomain.FilterRevisionID(nextIntegrationTesterJoinLinkID(t))
		cleared, clearRevision, err := committed.PublishAllowAll(clearID, application.AdminID(), time.Date(2026, 10, 4, 9, 1, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		committed, err = repository.Commit(t.Context(), application.AdminID(), 1, cleared, clearRevision)
		if err != nil || committed.Revision() != 2 || !committed.IsAllowAll() {
			t.Fatalf("clear=%v err=%v", committed, err)
		}
		if count, err := database.Collection(applicationFilterRevisionsCollectionName).CountDocuments(t.Context(), bson.D{{Key: "applicationId", Value: application.ID().String()}}); err != nil || count != 2 {
			t.Fatalf("history count=%d err=%v", count, err)
		}
	})

	t.Run("same expected revision commits at most once", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "filter-race", "filter_race")
		repository := NewApplicationFilterRepository(database)
		current, err := repository.LoadForAdmin(t.Context(), application.ID(), application.AdminID())
		if err != nil {
			t.Fatal(err)
		}
		first, firstRevision := integrationFilterCandidate(t, current, "CN", application.AdminID().String(), time.Now().UTC())
		second, secondRevision := integrationFilterCandidate(t, current, "US", application.AdminID().String(), time.Now().UTC())
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for _, candidate := range []struct {
			filter   *filterdomain.ApplicationFilter
			revision *filterdomain.ApplicationFilterRevision
		}{{first, firstRevision}, {second, secondRevision}} {
			group.Add(1)
			go func(candidate *filterdomain.ApplicationFilter, revision *filterdomain.ApplicationFilterRevision) {
				defer group.Done()
				<-start
				_, err := repository.Commit(t.Context(), application.AdminID(), 0, candidate, revision)
				results <- err
			}(candidate.filter, candidate.revision)
		}
		close(start)
		group.Wait()
		close(results)
		successes, conflicts := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, filterport.ErrApplicationFilterRevisionConflict) {
				conflicts++
			} else {
				t.Fatalf("race error=%v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
		}
	})

	t.Run("administrator and corrupt pointer fail closed", func(t *testing.T) {
		database := migratedIntegrationDatabase(t, client)
		application := createVersionTestApplication(t, database, "filter-owner", "filter_fail_closed")
		repository := NewApplicationFilterRepository(database)
		if _, err := repository.LoadForAdmin(t.Context(), application.ID(), "outsider"); !errors.Is(err, filterport.ErrApplicationAdminRequired) {
			t.Fatalf("outsider error=%v", err)
		}
		current, _ := repository.LoadForAdmin(t.Context(), application.ID(), application.AdminID())
		candidate, revision := integrationFilterCandidate(t, current, "CN", application.AdminID().String(), time.Now().UTC())
		if _, err := repository.Commit(t.Context(), application.AdminID(), 0, candidate, revision); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Collection(applicationFiltersCollectionName).UpdateOne(t.Context(), bson.D{{Key: "applicationId", Value: application.ID().String()}}, bson.D{{Key: "$set", Value: bson.D{{Key: "currentFilterRevisionId", Value: nextIntegrationTesterJoinLinkID(t)}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.LoadForAdmin(t.Context(), application.ID(), application.AdminID()); !errors.Is(err, filterport.ErrApplicationFilterStateInconsistent) {
			t.Fatalf("corrupt pointer error=%v", err)
		}
	})
}
