package mongo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
	publicationdomain "iwut-app-center/internal/publication/domain"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
)

type testLaunchFixture struct {
	db          *drivermongo.Database
	seed        decidableReview
	publication *publicationdomain.ApplicationPublication
	membership  *testerdomain.ApplicationTesterMembership
}

func newTestLaunchFixture(t *testing.T, client *drivermongo.Client) testLaunchFixture {
	t.Helper()
	db := migratedIntegrationDatabase(t, client)
	seed := createApprovedPublicationVersion(t, db, "launch-resolution")
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), seed.applicationID, seed.adminID, nil, newIntegrationTesterJoinLink(t, seed.applicationID, seed.adminID)).JoinLink()
	member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "ordinary-tester")).Membership()
	repo := NewApplicationPublicationRepository(db)
	publication := placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed).Publication()
	return testLaunchFixture{db, seed, publication, member}
}

func (f testLaunchFixture) resolve(t *testing.T, repo *TestLaunchResolver, authID shared.AuthID, major int32, caps []catalogdomain.CapabilityName) (*catalogdomain.TestLaunchDescriptor, error) {
	t.Helper()
	return repo.ResolveForTester(t.Context(), f.seed.applicationID, authID, major, caps)
}

func TestTestLaunchResolverIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("BR-RUN-001 BR-RUN-005 BR-RUN-006 BR-RUN-008 complete descriptor and strictly read-only snapshot", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		monitored, trace := testLaunchMonitoredClient(t, f.db.Name(), "")
		repo := NewTestLaunchResolver(monitored.Database(f.db.Name()))
		before := readTestLaunchFacts(t, f.db)
		got, err := f.resolve(t, repo, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1", "other.feature.v2"})
		if err != nil {
			t.Fatal(err)
		}
		version := readVersionDocument(t, f.db, f.seed.versionID.String())
		if got.ApplicationID() != f.seed.applicationID || got.PublicationID() != f.publication.PublicationID().String() || got.PublicationRevision() != 1 || got.RPCAPIMajor() != 1 || got.VersionID() != version.VersionID || got.VersionLabel() != version.VersionLabel || got.LaunchURL() != version.LaunchURL || got.RPCAPIMinVersion() != version.RPCApiMinVersion || got.RPCAPIMaxVersionExclusive() != version.RPCApiMaxVersionExclusive || !reflect.DeepEqual(got.RequiredCapabilities(), []catalogdomain.CapabilityName{"camera.read.v1"}) || !reflect.DeepEqual(got.RequiredScopes(), version.RequiredScopes) || !reflect.DeepEqual(got.OptionalScopes(), version.OptionalScopes) {
			t.Fatalf("incomplete descriptor: %#v", got)
		}
		if after := readTestLaunchFacts(t, f.db); !reflect.DeepEqual(before, after) {
			t.Fatal("resolution changed stored facts or technical fences")
		}
		trace.assertReadOnlySnapshot(t)
	})
	t.Run("BR-RUN-001 membership authorization precedes publication inspection including administrators", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		// An intentionally corrupt slot must not disclose a different error to non-testers.
		setTestLaunchFields(t, f.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "testVersionId", Value: "private-invalid-version"}})
		monitored, trace := testLaunchMonitoredClient(t, f.db.Name(), "")
		repo := NewTestLaunchResolver(monitored.Database(f.db.Name()))
		for _, authID := range []shared.AuthID{"outsider", f.seed.adminID, "auth-reviewer"} {
			got, err := f.resolve(t, repo, authID, 1, nil)
			if got != nil || !errors.Is(err, catalogport.ErrApplicationTesterRequired) {
				t.Fatalf("unauthorized resolve: %v %v", got, err)
			}
		}
		if _, err := NewApplicationTesterMembershipRepository(f.db).Remove(t.Context(), f.seed.applicationID, f.membership.MembershipID(), f.seed.adminID, time.Now()); err != nil {
			t.Fatal(err)
		}
		got, err := f.resolve(t, repo, f.membership.TesterAuthID(), 1, nil)
		if got != nil || !errors.Is(err, catalogport.ErrApplicationTesterRequired) {
			t.Fatalf("removed resolve: %v %v", got, err)
		}
		for _, collection := range trace.collections() {
			if collection != applicationsCollectionName && collection != applicationTesterMembershipsCollectionName {
				t.Fatalf("unauthorized read exposed %s", collection)
			}
		}
		trace.assertReadOnlySnapshot(t)
	})
	t.Run("BR-RUN-001 missing application", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		got, err := NewTestLaunchResolver(f.db).ResolveForTester(t.Context(), nextIntegrationApplicationID(t), f.membership.TesterAuthID(), 1, nil)
		if got != nil || !errors.Is(err, catalogport.ErrApplicationNotFound) {
			t.Fatalf("missing app: %v %v", got, err)
		}
	})
	t.Run("BR-RUN-002 BR-RUN-003 exact major and absent test never fall back", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		repo := NewTestLaunchResolver(f.db)
		// The published version covers major 2, but only the major 1 partition exists.
		if got, err := f.resolve(t, repo, f.membership.TesterAuthID(), 2, []catalogdomain.CapabilityName{"camera.read.v1"}); got != nil || !errors.Is(err, catalogport.ErrApplicationTestTargetUnavailable) {
			t.Fatalf("major fallback: %v %v", got, err)
		}
		setTestLaunchFields(t, f.db, applicationPublicationsCollectionName, bson.D{}, bson.D{{Key: "testVersionId", Value: nil}, {Key: "greyVersionId", Value: f.seed.versionID.String()}, {Key: "stableVersionId", Value: f.seed.versionID.String()}})
		if got, err := f.resolve(t, repo, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"}); got != nil || !errors.Is(err, catalogport.ErrApplicationTestTargetUnavailable) {
			t.Fatalf("slot fallback: %v %v", got, err)
		}
	})
	t.Run("BR-RUN-005 missing host capabilities are a safe typed failure", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		got, err := f.resolve(t, NewTestLaunchResolver(f.db), f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"unrelated.v1"})
		if got != nil || !errors.Is(err, catalogdomain.ErrHostCapabilitiesInsufficient) {
			t.Fatalf("missing capabilities: %v %v", got, err)
		}
		var capabilityError *catalogdomain.Error
		if !errors.As(err, &capabilityError) || !reflect.DeepEqual(capabilityError.MissingCapabilities(), []catalogdomain.CapabilityName{"camera.read.v1"}) {
			t.Fatalf("missing capability details lost: %v", err)
		}
	})
}

func TestTestLaunchResolverCorruptFactsIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, tc := range []struct {
		name, collection, field string
		value                   any
	}{
		{"dangling version", applicationPublicationsCollectionName, "testVersionId", "019543b0-0000-7000-8000-000000000099"},
		{"invalid publication revision", applicationPublicationsCollectionName, "revision", int64(0)},
		{"unreadable publication", applicationPublicationsCollectionName, "testVersionId", []int32{1}},
		{"history application mismatch", applicationPublicationHistoryCollectionName, "applicationId", "019543b0-0000-7000-8000-000000000099"},
		{"history major mismatch", applicationPublicationHistoryCollectionName, "rpcApiMajor", int32(2)},
		{"history version mismatch", applicationPublicationHistoryCollectionName, "newVersionId", "019543b0-0000-7000-8000-000000000099"},
		{"history wrong action", applicationPublicationHistoryCollectionName, "action", "OTHER"},
		{"history different approval", applicationPublicationHistoryCollectionName, "approvedReviewId", "019543b0-0000-7000-8000-000000000099"},
		{"cross application version", applicationVersionsCollectionName, "applicationId", "019543b0-0000-7000-8000-000000000099"},
		{"nonapproved version", applicationVersionsCollectionName, "reviewStatus", "REVOKED"},
		{"version content drift", applicationVersionsCollectionName, "launchUrl", "https://private.example/drift"},
		{"version revision drift", applicationVersionsCollectionName, "revision", int64(999)},
		{"version incompatible major", applicationVersionsCollectionName, "rpcApiMinVersion", int32(2)},
		{"version audit drift", applicationVersionsCollectionName, "updatedBy", "wrong-reviewer"},
		{"unreadable version", applicationVersionsCollectionName, "launchUrl", []int32{1}},
		{"nonapproved latest review", applicationReviewsCollectionName, "status", "REJECTED"},
		{"missing decision", applicationReviewsCollectionName, "decision", nil},
		{"missing approval validation", applicationReviewsCollectionName, "decision.approvalValidation", nil},
		{"decision outcome mismatch", applicationReviewsCollectionName, "decision.outcome", "REJECTED"},
		{"review snapshot drift", applicationReviewsCollectionName, "snapshot.versionLabel", "drift"},
		{"review revision overflow", applicationReviewsCollectionName, "sourceVersionRevision", int64(9223372036854775807)},
	} {
		t.Run("BR-RUN-004 "+tc.name, func(t *testing.T) {
			f := newTestLaunchFixture(t, client)
			setTestLaunchFields(t, f.db, tc.collection, bson.D{}, bson.D{{Key: tc.field, Value: tc.value}})
			assertTestLaunchInconsistent(t, f)
		})
	}
	for _, collection := range []string{applicationPublicationHistoryCollectionName, applicationVersionsCollectionName, applicationReviewsCollectionName} {
		t.Run("BR-RUN-004 missing "+collection, func(t *testing.T) {
			f := newTestLaunchFixture(t, client)
			if _, err := f.db.Collection(collection).DeleteMany(t.Context(), bson.D{}); err != nil {
				t.Fatal(err)
			}
			assertTestLaunchInconsistent(t, f)
		})
	}
	t.Run("BR-RUN-004 historical approval cannot replace latest review", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		review := readReviewDocument(t, f.db, f.seed.reviewID)
		review.ReviewID = nextIntegrationApplicationReviewID(t).String()
		review.Attempt++
		review.SourceVersionRevision++
		if _, err := f.db.Collection(applicationReviewsCollectionName).InsertOne(t.Context(), review); err != nil {
			t.Fatal(err)
		}
		assertTestLaunchInconsistent(t, f)
	})
}

func TestTestLaunchResolverSnapshotIntegration(t *testing.T) {
	client := integrationClient(t)
	for _, mutation := range []string{"remove", "replace"} {
		for _, order := range []string{"before", "after"} {
			t.Run("BR-RUN-007 "+mutation+" "+order+" snapshot", func(t *testing.T) {
				f := newTestLaunchFixture(t, client)
				second := addApprovedPublicationVersion(t, f.db, f.seed, "v2")
				monitored, trace := testLaunchMonitoredClient(t, f.db.Name(), order)
				ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
				defer cancel()
				type result struct {
					descriptor *catalogdomain.TestLaunchDescriptor
					err        error
				}
				done := make(chan result, 1)
				go func() {
					d, e := NewTestLaunchResolver(monitored.Database(f.db.Name())).ResolveForTester(ctx, f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"})
					done <- result{d, e}
				}()
				select {
				case <-trace.reached:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if mutation == "remove" {
					if _, err := NewApplicationTesterMembershipRepository(f.db).Remove(ctx, f.seed.applicationID, f.membership.MembershipID(), f.seed.adminID, time.Now()); err != nil {
						t.Fatal(err)
					}
				} else {
					repo := NewApplicationPublicationRepository(f.db)
					revision := int64(1)
					placePublication(t, repo, loadPublicationCandidate(t, repo, second, 1, &revision), second)
				}
				trace.resume()
				var got result
				select {
				case got = <-done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if mutation == "remove" && order == "before" {
					if got.descriptor != nil || !errors.Is(got.err, catalogport.ErrApplicationTesterRequired) {
						t.Fatalf("removed before snapshot: %#v %v", got.descriptor, got.err)
					}
				} else {
					wantID, wantRevision := f.seed.versionID.String(), int64(1)
					if mutation == "replace" && order == "before" {
						wantID, wantRevision = second.versionID.String(), 2
					}
					if got.err != nil || got.descriptor == nil || got.descriptor.VersionID() != wantID || got.descriptor.PublicationRevision() != wantRevision {
						t.Fatalf("mixed snapshot: %#v %v, want %s/%d", got.descriptor, got.err, wantID, wantRevision)
					}
				}
				// A fresh resolution must observe the committed mutation, regardless of the old snapshot.
				fresh, err := f.resolve(t, NewTestLaunchResolver(f.db), f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"})
				if mutation == "remove" {
					if fresh != nil || !errors.Is(err, catalogport.ErrApplicationTesterRequired) {
						t.Fatalf("fresh removal: %v %v", fresh, err)
					}
				} else if err != nil || fresh.VersionID() != second.versionID.String() || fresh.PublicationRevision() != 2 {
					t.Fatalf("fresh replacement: %v %v", fresh, err)
				}
				trace.assertReadOnlySnapshot(t)
			})
		}
	}
}

func setTestLaunchFields(t *testing.T, db *drivermongo.Database, collection string, filter, fields bson.D) {
	t.Helper()
	if _, err := db.Collection(collection).UpdateOne(t.Context(), filter, bson.D{{Key: "$set", Value: fields}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
		t.Fatal(err)
	}
}

func assertTestLaunchInconsistent(t *testing.T, f testLaunchFixture) {
	t.Helper()
	got, err := f.resolve(t, NewTestLaunchResolver(f.db), f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"})
	if got != nil || !errors.Is(err, catalogport.ErrApplicationTestPublicationInconsistent) {
		t.Fatalf("corruption returned %v %v", got, err)
	}
	for _, secret := range []string{"private.example", f.membership.TesterAuthID().String(), f.seed.reviewID.String()} {
		if strings.Contains(fmt.Sprintf("%+v", err), secret) {
			t.Fatalf("failure leaked %s", secret)
		}
	}
}

func readTestLaunchFacts(t *testing.T, db *drivermongo.Database) map[string][]bson.M {
	t.Helper()
	result := make(map[string][]bson.M)
	for _, name := range []string{applicationsCollectionName, applicationTesterMembershipsCollectionName, applicationTesterJoinLinksCollectionName, applicationPublicationsCollectionName, applicationPublicationHistoryCollectionName, applicationVersionsCollectionName, applicationReviewsCollectionName} {
		cursor, err := db.Collection(name).Find(t.Context(), bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
		if err != nil {
			t.Fatal(err)
		}
		var docs []bson.M
		if err := cursor.All(t.Context(), &docs); err != nil {
			t.Fatal(err)
		}
		result[name] = docs
	}
	return result
}

type testLaunchTrace struct {
	mu                    sync.Mutex
	commands              []bson.Raw
	names                 []string
	reached, release      chan struct{}
	gateOnce, releaseOnce sync.Once
	firstFindRequest      int64
}

func (trace *testLaunchTrace) resume() { trace.releaseOnce.Do(func() { close(trace.release) }) }
func (trace *testLaunchTrace) gate(ctx context.Context) {
	trace.gateOnce.Do(func() {
		close(trace.reached)
		select {
		case <-trace.release:
		case <-ctx.Done():
		}
	})
}

func testLaunchMonitoredClient(t *testing.T, database, gateOrder string) (*drivermongo.Client, *testLaunchTrace) {
	t.Helper()
	trace := &testLaunchTrace{reached: make(chan struct{}), release: make(chan struct{})}
	monitor := &event.CommandMonitor{
		Started: func(ctx context.Context, e *event.CommandStartedEvent) {
			if e.DatabaseName != database && e.CommandName != "commitTransaction" && e.CommandName != "abortTransaction" {
				return
			}
			trace.mu.Lock()
			trace.names = append(trace.names, e.CommandName)
			trace.commands = append(trace.commands, append(bson.Raw{}, e.Command...))
			first := e.CommandName == "find" && e.Command.Lookup("find").StringValue() == applicationsCollectionName && trace.firstFindRequest == 0
			if first {
				trace.firstFindRequest = e.RequestID
			}
			trace.mu.Unlock()
			if first && gateOrder == "before" {
				trace.gate(ctx)
			}
		},
		Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
			trace.mu.Lock()
			first := e.RequestID == trace.firstFindRequest && e.CommandName == "find"
			trace.mu.Unlock()
			if first && gateOrder == "after" {
				trace.gate(ctx)
			}
		},
	}
	client, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		trace.resume()
		if err := client.Disconnect(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return client, trace
}

func (trace *testLaunchTrace) collections() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	var result []string
	for i, name := range trace.names {
		if name == "find" {
			result = append(result, trace.commands[i].Lookup("find").StringValue())
		}
	}
	return result
}

func (trace *testLaunchTrace) assertReadOnlySnapshot(t *testing.T) {
	t.Helper()
	trace.mu.Lock()
	defer trace.mu.Unlock()
	finds, snapshots := 0, 0
	for i, name := range trace.names {
		command := trace.commands[i]
		switch name {
		case "find":
			finds++
			if v, ok := command.Lookup("autocommit").BooleanOK(); !ok || v {
				t.Fatalf("find outside transaction: %s", command)
			}
			if _, err := command.LookupErr("lsid"); err != nil {
				t.Fatal("missing transaction session")
			}
			if start, _ := command.Lookup("startTransaction").BooleanOK(); start {
				if command.Lookup("readConcern").Document().Lookup("level").StringValue() != "snapshot" {
					t.Fatal("query transaction not snapshot")
				}
				snapshots++
			}
		case "commitTransaction", "abortTransaction":
		default:
			t.Fatalf("resolver issued non-read command %s", name)
		}
	}
	if finds == 0 || snapshots == 0 {
		t.Fatal("missing snapshot reads")
	}
}

func TestTestLaunchResolverSafeErrors(t *testing.T) {
	for _, err := range []error{errors.New("database exception private-content"), context.Canceled, context.DeadlineExceeded} {
		if strings.Contains(safeTestLaunchError(err).Error(), "private-content") {
			t.Fatal("storage error exposed user content")
		}
	}
}
