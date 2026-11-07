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
	publication "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	review "iwut-app-center/internal/review/domain"
	script "iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
)

func addScriptPublicationVersion(t *testing.T, db *dm.Database, base decidableReview, refs []shared.ScriptReference, label string, capabilities ...[]string) decidableReview {
	t.Helper()
	var err error
	refs, err = shared.NormalizeScriptReferences(refs)
	if err != nil {
		t.Fatal(err)
	}
	draft := integrationApplicationVersionDraft(t, base.applicationID, base.adminID.String(), label).WithScriptReferences(refs)
	if len(capabilities) > 0 {
		draft = integrationApplicationVersionDraftWithSets(t, base.applicationID, base.adminID.String(), label, capabilities[0], nil, nil).WithScriptReferences(refs)
	}
	version, err := NewApplicationVersionRepository(db).CreateDraft(t.Context(), base.adminID, draft)
	if err != nil {
		t.Fatal(err)
	}
	base.versionID, base.reviewID = review.ApplicationVersionID(version.ID()), nextIntegrationApplicationReviewID(t)
	repo := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, repo, base.applicationID, base.versionID, base.adminID.String(), 1)
	if _, err := repo.Submit(t.Context(), candidate, base.reviewID, base.adminID, 41, "public-https.v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	approvePublicationVersion(t, db, base)
	return base
}

func publishedAppScriptFixture(t *testing.T, db *dm.Database, name string, published bool) (decidableReview, decidableReview, script.Version, time.Time) {
	t.Helper()
	version, _, at := approvedPublicationFixture(t, db)
	base := createApprovedPublicationVersion(t, db, name)
	dependent := addScriptPublicationVersion(t, db, base,
		[]shared.ScriptReference{{ResourceID: string(version.ResourceID), VersionID: string(version.ID)}}, "script-dependent")
	if published {
		if _, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, version.ID, script.PublishScript, 1, at)); err != nil {
			t.Fatal(err)
		}
	}
	return base, dependent, version, at
}

func TestScriptDistributionPublicationIntegration_UCAPP047_BRSCR020(t *testing.T) {
	for _, channel := range []string{"TEST", "STABLE", "GREY"} {
		t.Run(channel, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			base, app, version, at := publishedAppScriptFixture(t, db, "script-gated-"+channel, false)
			repo := NewApplicationPublicationRepository(db)
			var revision *int64
			if channel == "GREY" {
				c, err := repo.LoadStablePlacementCandidate(t.Context(), base.applicationID, 1, publication.ApplicationVersionID(base.versionID), base.adminID, nil)
				if err != nil {
					t.Fatal(err)
				}
				id := publication.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
				if _, err := repo.SetStable(t.Context(), c, &id, publication.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), base.adminID, publicationTestValidation(), time.Now()); err != nil {
					t.Fatal(err)
				}
				v := int64(1)
				revision = &v
			}
			type placement func(context.Context) (*publication.PlaceInTestResult, error)
			load := func() (bool, placement, error) {
				switch channel {
				case "TEST":
					c, err := repo.LoadTestPlacementCandidate(t.Context(), app.applicationID, 1, publication.ApplicationVersionID(app.versionID), app.adminID, revision)
					if err != nil {
						return false, nil, err
					}
					return c.IsNoOp(), func(ctx context.Context) (*publication.PlaceInTestResult, error) {
						var id *publication.ApplicationPublicationID
						if c.Publication() == nil {
							v := publication.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
							id = &v
						}
						return repo.PlaceInTest(ctx, c, id, publication.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, publicationTestValidation(), time.Now())
					}, nil
				case "STABLE":
					c, err := repo.LoadStablePlacementCandidate(t.Context(), app.applicationID, 1, publication.ApplicationVersionID(app.versionID), app.adminID, revision)
					if err != nil {
						return false, nil, err
					}
					return c.IsNoOp(), func(ctx context.Context) (*publication.PlaceInTestResult, error) {
						var id *publication.ApplicationPublicationID
						if c.Publication() == nil {
							v := publication.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
							id = &v
						}
						return repo.SetStable(ctx, c, id, publication.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, publicationTestValidation(), time.Now())
					}, nil
				default:
					exposure, _ := publication.NewExposureBasisPoints(1000)
					c, err := repo.LoadGreyPlacementCandidate(t.Context(), app.applicationID, 1, publication.ApplicationVersionID(app.versionID), exposure, app.adminID, *revision)
					if err != nil {
						return false, nil, err
					}
					return c.IsNoOp(), func(ctx context.Context) (*publication.PlaceInTestResult, error) {
						id := publication.GreyRolloutID(nextIntegrationApplicationReviewID(t))
						seed, _ := publication.NewCohortSeed(make([]byte, 32))
						validation := publicationTestValidation()
						return repo.SetGrey(ctx, c, &id, &seed, publication.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, &validation, time.Now())
					}, nil
				}
			}
			if _, _, err := load(); !errors.Is(err, publicationport.ErrApplicationScriptDependencyUnavailable) {
				t.Fatal("unpublished script permitted placement", err)
			}
			writer := NewScriptPublicationRepository(db)
			if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, version.ID, script.PublishScript, 1, at)); err != nil {
				t.Fatal(err)
			}
			_, place, err := load()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", script.SuspendScript, 1, at)); err != nil {
				t.Fatal(err)
			}
			before := readPublicationApplication(t, db, app)
			if _, err := place(t.Context()); !errors.Is(err, publicationport.ErrApplicationScriptDependencyUnavailable) {
				t.Fatal("final transaction reused a stale availability observation", err)
			}
			if !reflect.DeepEqual(before, readPublicationApplication(t, db, app)) {
				t.Fatal("failed gate left an application fence")
			}
			if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", script.RestoreScript, 2, at)); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", script.ClearScriptHead, 2, at)); err != nil {
				t.Fatal(err)
			}
			result, err := place(t.Context())
			if err != nil || !result.Changed() {
				t.Fatal("head clear invalidated exact publication qualification", err)
			}
			v := result.Publication().Revision()
			revision = &v
			if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", script.SuspendScript, 3, at)); err != nil {
				t.Fatal(err)
			}
			if noop, _, err := load(); err != nil || !noop {
				t.Fatal("matching no-op changed semantics", err)
			}
			currentRevision := v
			if v > 1 {
				v--
			} else {
				v++
			}
			if _, _, err := load(); !errors.Is(err, publicationport.ErrApplicationPublicationRevisionConflict) {
				t.Fatal("mismatched positive revision ignored OCC", err)
			}
			v = currentRevision
			if channel == "GREY" {
				exposure, _ := publication.NewExposureBasisPoints(100)
				if _, err := repo.LoadGreyPlacementCandidate(t.Context(), app.applicationID, 1, publication.ApplicationVersionID(app.versionID), exposure, app.adminID, v); !errors.Is(err, publicationport.ErrApplicationScriptDependencyUnavailable) {
					t.Fatal("decrease bypassed the local script gate", err)
				}
			}
		})
	}
}

func TestScriptDistributionPublicationConcurrencyIntegration_UCAPP047_BRSCR020(t *testing.T) {
	for _, winner := range []string{"application", "suspend"} {
		t.Run(winner, func(t *testing.T) {
			db := migratedIntegrationDatabase(t, integrationClient(t))
			_, app, version, at := publishedAppScriptFixture(t, db, "script-publication-race-"+winner, true)
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
			defer func() {
				select {
				case <-resume:
				default:
					close(resume)
				}
			}()
			plain, monitored := NewApplicationPublicationRepository(db), NewApplicationPublicationRepository(client.Database(db.Name()))
			candidate := loadPublicationCandidate(t, plain, app, 1, nil)
			place := func(repo *ApplicationPublicationRepository) error {
				id := publication.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
				_, err := repo.PlaceInTest(t.Context(), candidate, &id, publication.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, publicationTestValidation(), time.Now())
				return err
			}
			loser := make(chan error, 1)
			suspend := scriptPublicationCommand(t, version.ResourceID, "", script.SuspendScript, 1, at)
			if winner == "application" {
				go func() {
					_, err := NewScriptPublicationRepository(client.Database(db.Name())).ApplyPublication(t.Context(), suspend)
					loser <- err
				}()
			} else {
				go func() { loser <- place(monitored) }()
			}
			select {
			case <-paused:
			case <-time.After(10 * time.Second):
				t.Fatal("shared resource gate not reached")
			}
			if winner == "application" {
				if err := place(plain); err != nil {
					t.Fatal(err)
				}
			} else if _, err := NewScriptPublicationRepository(db).ApplyPublication(t.Context(), suspend); err != nil {
				t.Fatal(err)
			}
			close(resume)
			loserErr := <-loser
			count := 1
			if winner == "application" {
				if loserErr != nil {
					t.Fatal(loserErr)
				}
			} else {
				count = 0
				if !errors.Is(loserErr, publicationport.ErrApplicationScriptDependencyUnavailable) {
					t.Fatal("late application placement bypassed suspension", loserErr)
				}
			}
			assertCollectionCount(t, db, applicationPublicationsCollectionName, count)
			assertCollectionCount(t, db, applicationPublicationHistoryCollectionName, count)
			var gate scriptAvailabilityDocument
			if err := db.Collection(scriptAvailabilityCollection).FindOne(t.Context(), bson.M{"resourceId": version.ResourceID}).Decode(&gate); err != nil || gate.State != script.DistributionSuspended {
				t.Fatal("suspension fact", err)
			}
		})
	}
}
