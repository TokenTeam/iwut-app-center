package mongo

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	ad "iwut-app-center/internal/application/domain"
	cd "iwut-app-center/internal/catalog/domain"
	od "iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/shared"
	"os"
	"reflect"
	"testing"
	"time"
)

func myTestMonitoredClient(t *testing.T, database, order string) (*m.Client, *testLaunchTrace) {
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
			first := e.CommandName == "find" && e.Command.Lookup("find").StringValue() == applicationTesterMembershipsCollectionName && trace.firstFindRequest == 0
			if first {
				trace.firstFindRequest = e.RequestID
			}
			trace.mu.Unlock()
			if first && order == "before" {
				trace.gate(ctx)
			}
		},
		Succeeded: func(ctx context.Context, e *event.CommandSucceededEvent) {
			trace.mu.Lock()
			first := e.RequestID == trace.firstFindRequest && e.CommandName == "find"
			trace.mu.Unlock()
			if first && order == "after" {
				trace.gate(ctx)
			}
		},
	}
	client, err := m.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(monitor))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { trace.resume(); _ = client.Disconnect(context.Background()) })
	return client, trace
}
func TestUCAPP038_BR_CAT_016_017_OneSnapshotAcrossConcurrentCommands(t *testing.T) {
	client := integrationClient(t)
	for _, command := range []string{"clear", "replace", "profile approval", "remove", "rejoin", "suspend", "archive", "close", "account cleanup"} {
		for _, order := range []string{"before", "after"} {
			t.Run(command+"-"+order, func(t *testing.T) {
				f := newTestLaunchFixture(t, client)
				original := myTestDetail(t, f)
				expectedID := f.seed.versionID.String()
				expectedProfile := original.Profile.RevisionID()
				expectedMembership := original.Membership.MembershipID.String()
				expectedState := cd.MyTestReady
				hidden := false
				var linkDoc applicationTesterJoinLinkDocument
				if err := f.db.Collection(applicationTesterJoinLinksCollectionName).FindOne(t.Context(), bson.D{}).Decode(&linkDoc); err != nil {
					t.Fatal(err)
				}
				link, _ := testerJoinLinkFromDocument(linkDoc)
				var replacement decidableReview
				if command == "replace" {
					replacement = addApprovedPublicationVersion(t, f.db, f.seed, "v2")
				}
				monitored, trace := myTestMonitoredClient(t, f.db.Name(), order)
				r := myTestRepository(monitored.Database(f.db.Name()))
				ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
				defer cancel()
				done := make(chan struct{})
				var page *cd.MyTestApplicationPage
				var queryErr error
				go func() {
					page, queryErr = r.ListMyTest(ctx, f.membership.TesterAuthID(), 1, myTestHost, 20, "")
					close(done)
				}()
				select {
				case <-trace.reached:
				case <-ctx.Done():
					t.Fatal("query did not reach snapshot barrier")
				}
				now := time.Now()
				switch command {
				case "clear":
					if _, err := NewApplicationPublicationRepository(f.db).ClearTest(ctx, f.seed.applicationID, 1, f.seed.adminID, 1, clearTestAudit(t)); err != nil {
						t.Fatal(err)
					}
					expectedState = cd.MyTestTargetUnavailable
				case "replace":
					pubs := NewApplicationPublicationRepository(f.db)
					revision := int64(1)
					placed := placePublication(t, pubs, loadPublicationCandidate(t, pubs, replacement, 1, &revision), replacement)
					expectedID = placed.Publication().TestVersionIDPtr().String()
				case "profile approval":
					expectedProfile = approveReplacementProfile(t, oauthProviderFixture{testLaunchFixture: f, profileRevisionID: expectedProfile}).String()
				case "remove", "rejoin":
					if _, err := NewApplicationTesterMembershipRepository(f.db).Remove(ctx, f.seed.applicationID, f.membership.MembershipID(), f.seed.adminID, now); err != nil {
						t.Fatal(err)
					}
					hidden = true
					if command == "rejoin" {
						member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(f.db), link, newIntegrationTesterMembership(t, link, f.membership.TesterAuthID())).Membership()
						expectedMembership = member.MembershipID().String()
						hidden = false
					}
				case "suspend":
					if _, err := NewApplicationOperationsRepository(f.db).Set(ctx, f.seed.applicationID, "operator", ad.PlatformAvailabilitySuspended, 1, 1, "snapshot", ad.ApplicationOperationEventID(nextIntegrationTesterJoinLinkID(t)), now); err != nil {
						t.Fatal(err)
					}
					hidden = true
				case "archive":
					if _, err := NewApplicationArchivalRepository(f.db).SetArchiveState(ctx, f.seed.applicationID, f.seed.adminID, ad.ApplicationArchiveArchived, 1, func() (ad.ApplicationArchiveEventID, time.Time, error) {
						return ad.ApplicationArchiveEventID(nextIntegrationTesterJoinLinkID(t)), now, nil
					}); err != nil {
						t.Fatal(err)
					}
					hidden = true
				case "close":
					id := ad.ApplicationClosureID(nextIntegrationTesterJoinLinkID(t))
					proof := ad.ApplicationCloseProof{Subject: f.seed.adminID, ApplicationID: f.seed.applicationID, JTI: nextIntegrationTesterJoinLinkID(t).String(), AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
					if _, err := NewApplicationClosureRepository(f.db).Start(ctx, f.seed.applicationID, f.seed.adminID, 1, 1, proof, id, now); err != nil {
						t.Fatal(err)
					}
					hidden = true
				case "account cleanup":
					exit := NewAccountOwnerExitRepository(f.db)
					p := exitPrepare(f.membership.TesterAuthID().String(), od.Closure)
					out, err := exit.Prepare(ctx, p, exitReceipt(t), now)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = exit.Finish(ctx, od.Finish{Prepare: p, ReceiptID: out.ReceiptID, Decision: od.Committed}, now); err != nil {
						t.Fatal(err)
					}
					if err = exit.CleanupBatch(ctx, p.Key, 100); err != nil {
						t.Fatal(err)
					}
					hidden = true
				}
				trace.resume()
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("snapshot query stalled")
				}
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				assertCurrent := func(page *cd.MyTestApplicationPage) {
					t.Helper()
					if hidden {
						if len(page.Items) != 0 {
							t.Fatal("gate/qualification mixing")
						}
						return
					}
					if len(page.Items) != 1 {
						t.Fatal("missing card")
					}
					item := page.Items[0]
					if item.Availability != expectedState || item.Profile.RevisionID() != expectedProfile || item.Membership.MembershipID.String() != expectedMembership {
						t.Fatal("mixed current facts", item)
					}
					if item.LaunchTarget != nil && item.LaunchTarget.VersionID() != expectedID {
						t.Fatal("mixed Version")
					}
				}
				if order == "before" {
					assertCurrent(page)
				} else {
					if len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0], original) {
						t.Fatal("snapshot combined pre/post facts")
					}
				}
				fresh, err := myTestRepository(f.db).ListMyTest(t.Context(), shared.AuthID(f.membership.TesterAuthID()), 1, myTestHost, 20, "")
				if err != nil {
					t.Fatal(err)
				}
				assertCurrent(fresh)
				trace.assertReadOnlySnapshot(t)
				trace.mu.Lock()
				counts := map[string]int{}
				starts := 0
				for i, name := range trace.names {
					if name == "find" {
						counts[trace.commands[i].Lookup("find").StringValue()]++
						if start, _ := trace.commands[i].Lookup("startTransaction").BooleanOK(); start {
							starts++
						}
					}
				}
				trace.mu.Unlock()
				if starts != 1 {
					t.Fatal("per-item transaction", starts)
				}
				for collection, count := range counts {
					limit := 2
					if collection == applicationTesterMembershipsCollectionName {
						limit = 3
					}
					if collection == applicationFiltersCollectionName || collection == applicationFilterRevisionsCollectionName || count > limit {
						t.Fatal("non-batch/private read", collection, count)
					}
				}
			})
		}
	}
}
