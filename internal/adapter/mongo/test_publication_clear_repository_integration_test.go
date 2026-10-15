package mongo

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	drivermongo "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	applicationdomain "iwut-app-center/internal/application/domain"
	catalogdomain "iwut-app-center/internal/catalog/domain"
	catalogport "iwut-app-center/internal/catalog/port"
	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	oauthport "iwut-app-center/internal/oauthclient/port"
	publicationdomain "iwut-app-center/internal/publication/domain"
	publicationport "iwut-app-center/internal/publication/port"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
)

func clearTestAudit(t *testing.T) publicationport.TestClearAuditFactory {
	t.Helper()
	id := publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t))
	at := time.Now().UTC().Truncate(time.Millisecond)
	return func() (publicationdomain.ApplicationPublicationHistoryID, time.Time, error) { return id, at, nil }
}

func TestTestPublicationClearRepositoryIntegration_UCAPP036_AtomicPreservationNoOpAndRecovery(t *testing.T) {
	client := integrationClient(t)
	for _, slots := range []string{"test-only", "test-stable", "test-grey-stable"} {
		t.Run("BR-PUB-032034036 "+slots, func(t *testing.T) {
			f := newTestLaunchFixture(t, client)
			repo := NewApplicationPublicationRepository(f.db)
			before := f.publication
			if slots != "test-only" {
				expected := before.Revision()
				candidate, err := repo.LoadStablePlacementCandidate(t.Context(), f.seed.applicationID, 1, publicationdomain.ApplicationVersionID(f.seed.versionID), f.seed.adminID, &expected)
				if err != nil {
					t.Fatal(err)
				}
				set, err := repo.SetStable(t.Context(), candidate, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, publicationTestValidation(), time.Now())
				if err != nil {
					t.Fatal(err)
				}
				before = set.Publication()
			}
			if slots == "test-grey-stable" {
				candidate, err := repo.LoadGreyPlacementCandidate(t.Context(), f.seed.applicationID, 1, publicationdomain.ApplicationVersionID(f.seed.versionID), 10000, f.seed.adminID, before.Revision())
				if err != nil {
					t.Fatal(err)
				}
				seed, _ := publicationdomain.NewCohortSeed(make([]byte, 32))
				rollout := publicationdomain.GreyRolloutID(nextIntegrationApplicationReviewID(t))
				validation := publicationTestValidation()
				set, err := repo.SetGrey(t.Context(), candidate, &rollout, &seed, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, &validation, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				before = set.Publication()
			}
			other := placePublication(t, repo, loadPublicationCandidate(t, repo, f.seed, 2, nil), f.seed).Publication()
			appBefore := readPublicationApplication(t, f.db, f.seed)
			versionBefore := readVersionDocument(t, f.db, f.seed.versionID.String())
			reviewBefore := readReviewDocument(t, f.db, f.seed.reviewID)
			result, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, before.Revision(), clearTestAudit(t))
			if err != nil {
				t.Fatal(err)
			}
			p, h := result.Publication(), result.History()
			if !result.Changed() || p.TestVersionIDPtr() != nil || p.Revision() != before.Revision()+1 || p.PublicationID() != before.PublicationID() || !reflect.DeepEqual(p.StableVersionIDPtr(), before.StableVersionIDPtr()) || !reflect.DeepEqual(p.GreyRollout(), before.GreyRollout()) || h.Action() != publicationdomain.PublicationActionClearTestVersion || h.PreviousVersionID() == nil || h.PreviousVersionID().String() != f.seed.versionID.String() {
				t.Fatalf("clear=%#v history=%#v", p, h)
			}
			if !reflect.DeepEqual(versionBefore, readVersionDocument(t, f.db, f.seed.versionID.String())) || !reflect.DeepEqual(reviewBefore, readReviewDocument(t, f.db, f.seed.reviewID)) {
				t.Fatal("changed reviewed sources")
			}
			appAfter := readPublicationApplication(t, f.db, f.seed)
			if appAfter.CoordinationRevision != appBefore.CoordinationRevision+1 {
				t.Fatal("missing shared fence")
			}
			factoryCalls := 0
			noop, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, p.Revision(), func() (publicationdomain.ApplicationPublicationHistoryID, time.Time, error) {
				factoryCalls++
				return "", time.Time{}, nil
			})
			if err != nil || noop.Changed() || factoryCalls != 0 || !reflect.DeepEqual(noop.Publication(), p) || !reflect.DeepEqual(appAfter, readPublicationApplication(t, f.db, f.seed)) {
				t.Fatalf("no-op=%v %v", noop, err)
			}
			if _, err = repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, before.Revision(), clearTestAudit(t)); !errors.Is(err, publicationport.ErrApplicationPublicationRevisionConflict) {
				t.Fatal(err)
			}
			if _, err = repo.ClearTest(t.Context(), f.seed.applicationID, 7, f.seed.adminID, 1, clearTestAudit(t)); !errors.Is(err, publicationport.ErrApplicationPublicationNotFound) {
				t.Fatal(err)
			}
			var otherAfter applicationPublicationDocument
			if err := f.db.Collection(applicationPublicationsCollectionName).FindOne(t.Context(), bson.M{"publicationId": other.PublicationID().String()}).Decode(&otherAfter); err != nil || !reflect.DeepEqual(applicationPublicationToDocument(other), otherAfter) {
				t.Fatal("other major modified", err)
			}
			assertCollectionCount(t, f.db, applicationTesterMembershipsCollectionName, 1)
			assertCollectionCount(t, f.db, applicationTesterJoinLinksCollectionName, 1)
			if _, err := NewTestLaunchResolver(f.db).ResolveForTester(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"}); !errors.Is(err, catalogport.ErrApplicationTestTargetUnavailable) {
				t.Fatal("pure Test", err)
			}
			target, err := NewUnifiedLaunchResolver(f.db).Resolve(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"})
			if slots == "test-only" {
				if !errors.Is(err, catalogport.ErrApplicationLaunchTargetUnavailable) {
					t.Fatal(err)
				}
			} else if err != nil || target.Channel() == catalogdomain.LaunchChannelTest {
				t.Fatal("fallback", target, err)
			}
			if slots != "test-only" {
				item, err := NewPublicCatalogRepository(f.db).GetPublic(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"})
				if err != nil || item.LaunchTarget().Channel() == catalogdomain.LaunchChannelTest {
					t.Fatal("Stable-backed Catalog withdrawn", item, err)
				}
			}
			expected := p.Revision()
			reset := placePublication(t, repo, loadPublicationCandidate(t, repo, f.seed, 1, &expected), f.seed)
			if reset.Publication().PublicationID() != before.PublicationID() {
				t.Fatal("reset replaced identity")
			}
			if _, err := NewTestLaunchResolver(f.db).ResolveForTester(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"}); err != nil {
				t.Fatal("existing Tester recovery", err)
			}
		})
	}
}

func TestTestPublicationClearRepositoryIntegration_UCAPP036_TesterCountIndependentAndUnavailableOldTarget(t *testing.T) {
	client := integrationClient(t)
	for _, members := range []int{0, 100} {
		for _, activeLink := range []bool{false, true} {
			t.Run(fmt.Sprintf("BR-PUB-031035 ACTIVE=%d activeLink=%t", members, activeLink), func(t *testing.T) {
				db := reusableIntegrationDatabase(t, client)
				seed := createApprovedPublicationVersion(t, db, "clear-members")
				repo := NewApplicationPublicationRepository(db)
				before := placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed).Publication()
				if members > 0 || activeLink {
					link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), seed.applicationID, seed.adminID, nil, newIntegrationTesterJoinLink(t, seed.applicationID, seed.adminID)).JoinLink()
					for i := 0; i < members; i++ {
						joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, shared.AuthID(fmt.Sprintf("tester-%d", i))))
					}
					if !activeLink {
						result, err := NewApplicationTesterJoinLinkRepository(db).Revoke(t.Context(), seed.applicationID, link.JoinLinkID(), seed.adminID, time.Now())
						if err != nil || result.JoinLink().Status() != testerdomain.JoinLinkStatusRevoked {
							t.Fatal("construct revoked link history", err)
						}
					}
				}
				linkBefore, err := db.Collection(applicationTesterJoinLinksCollectionName).Find(t.Context(), bson.M{"applicationId": seed.applicationID.String()})
				if err != nil {
					t.Fatal(err)
				}
				var linksBefore []bson.Raw
				if err := linkBefore.All(t.Context(), &linksBefore); err != nil {
					t.Fatal(err)
				}
				activeCount, err := db.Collection(applicationTesterJoinLinksCollectionName).CountDocuments(t.Context(), bson.M{"applicationId": seed.applicationID.String(), "status": "ACTIVE"})
				if err != nil || activeCount != map[bool]int64{false: 0, true: 1}[activeLink] {
					t.Fatal("link fixture", activeCount, err)
				}
				// Removal only needs the Publication's valid pointer, even with every old eligibility source unavailable.
				for _, collection := range []string{applicationVersionsCollectionName, applicationReviewsCollectionName, applicationVersionOAuthConfigsCollectionName, applicationProfilesCollectionName, applicationProfileRevisionsCollectionName} {
					if _, err := db.Collection(collection).DeleteMany(t.Context(), bson.M{}); err != nil {
						t.Fatal(err)
					}
				}
				result, err := repo.ClearTest(t.Context(), seed.applicationID, 1, seed.adminID, before.Revision(), clearTestAudit(t))
				if err != nil || !result.Changed() {
					t.Fatal(result, err)
				}
				assertCollectionCount(t, db, applicationTesterMembershipsCollectionName, members)
				linksAfter, err := db.Collection(applicationTesterJoinLinksCollectionName).Find(t.Context(), bson.M{"applicationId": seed.applicationID.String()})
				if err != nil {
					t.Fatal(err)
				}
				var documentsAfter []bson.Raw
				if err := linksAfter.All(t.Context(), &documentsAfter); err != nil || !reflect.DeepEqual(linksBefore, documentsAfter) {
					t.Fatal("clear changed ACTIVE/REVOKED link facts", err)
				}
			})
		}
	}
}

func TestTestPublicationClearRepositoryIntegration_UCAPP036_GatesRollbackOverflow(t *testing.T) {
	client := integrationClient(t)
	f := newTestLaunchFixture(t, client)
	repo := NewApplicationPublicationRepository(f.db)
	for _, status := range []string{"CLOSING", "CLOSED"} {
		_, err := f.db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"lifecycleStatus": status}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 1, clearTestAudit(t)); !errors.Is(err, publicationport.ErrApplicationPublicationNotAllowed) {
			t.Fatal(status, err)
		}
	}
	_, err := f.db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"lifecycleStatus": "ACTIVE", "platformAvailabilityStatus": "SUSPENDED", "archiveStatus": "ARCHIVED"}}, options.UpdateOne().SetBypassDocumentValidation(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, "other", 1, clearTestAudit(t)); !errors.Is(err, publicationport.ErrApplicationAdminRequired) {
		t.Fatal(err)
	}
	before := readPublicationApplication(t, f.db, f.seed)
	var originalHistory applicationPublicationHistoryDocument
	if err := f.db.Collection(applicationPublicationHistoryCollectionName).FindOne(t.Context(), bson.M{"publicationId": f.publication.PublicationID().String()}).Decode(&originalHistory); err != nil {
		t.Fatal(err)
	}
	_, err = repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 1, func() (publicationdomain.ApplicationPublicationHistoryID, time.Time, error) {
		return publicationdomain.ApplicationPublicationHistoryID(originalHistory.HistoryID), time.Now(), nil
	})
	if err == nil || !reflect.DeepEqual(before, readPublicationApplication(t, f.db, f.seed)) {
		t.Fatal("history collision failed rollback", err)
	}
	assertPublicationCounts(t, f.db, 1, 1)
	_, err = f.db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"coordinationRevision": int64(math.MaxInt64)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 1, clearTestAudit(t)); err == nil {
		t.Fatal("fence overflow committed")
	}
	_, err = f.db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"coordinationRevision": before.CoordinationRevision}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 1, clearTestAudit(t))
	if err != nil || !result.Changed() {
		t.Fatal("suspended+archived clear", err)
	}
	// Current admin and lifecycle remain prerequisites even when the slot is empty.
	_, err = f.db.Collection(applicationsCollectionName).UpdateOne(t.Context(), bson.M{"id": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"lifecycleStatus": "CLOSED"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 2, clearTestAudit(t)); !errors.Is(err, publicationport.ErrApplicationPublicationNotAllowed) {
		t.Fatal("closed no-op", err)
	}
}

func TestTestPublicationClearRepositoryIntegration_UCAPP036_ProviderExactMajorAndRedirectUnion(t *testing.T) {
	f := newOAuthProviderFixture(t)
	provider := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
	repo := NewApplicationPublicationRepository(f.db)
	configuration, err := provider.GetClientConfiguration(t.Context(), f.publicID)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := provider.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, configuration.RegistrationRevision, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	before, err := provider.GetPublishedRedirects(t.Context(), f.seed.applicationID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClearTest(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 1, clearTestAudit(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, configuration.RegistrationRevision, time.Now()); !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatal(err)
	}
	if _, err := provider.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, configuration.RegistrationRevision, runtime.Version(), time.Now()); !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatal(err)
	}
	if _, err := provider.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 2, configuration.RegistrationRevision, time.Now()); err != nil {
		t.Fatal("other major", err)
	}
	after, err := provider.GetPublishedRedirects(t.Context(), f.seed.applicationID, time.Now())
	if err != nil || len(after.Entries) != 1 || !reflect.DeepEqual(before.RedirectURIs, after.RedirectURIs) {
		t.Fatal("cross-major callback union", after, err)
	}
	configurationAfter, err := provider.GetClientConfiguration(t.Context(), f.publicID)
	if err != nil || !reflect.DeepEqual(configuration, configurationAfter) {
		t.Fatal("metadata changed", err)
	}
	verified, revision, err := provider.VerifyClientSecret(t.Context(), f.confidentialID, f.confidentialSecret, 1)
	if err != nil || !verified || revision != 1 {
		t.Fatal("credential changed", err)
	}
	if _, err := repo.ClearTest(t.Context(), f.seed.applicationID, 2, f.seed.adminID, 1, clearTestAudit(t)); err != nil {
		t.Fatal(err)
	}
	empty, err := provider.GetPublishedRedirects(t.Context(), f.seed.applicationID, time.Now())
	if err != nil || len(empty.Entries) != 0 || len(empty.RedirectURIs) != 0 {
		t.Fatal(empty, err)
	}
	// A corrupted pointer cannot recover a retired target from the old SET.
	if _, err := f.db.Collection(applicationPublicationsCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": f.seed.applicationID.String(), "rpcApiMajor": int32(1)}, bson.M{"$set": bson.M{"testVersionId": f.seed.versionID.String()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, configuration.RegistrationRevision, time.Now()); !errors.Is(err, oauthport.ErrStateInconsistent) {
		t.Fatal("stale SET resurrected", err)
	}
	if _, err := NewUnifiedLaunchResolver(f.db).Resolve(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"}); !errors.Is(err, catalogport.ErrApplicationRuntimeStateInconsistent) {
		t.Fatal("unified stale SET", err)
	}
	if _, err := NewTestLaunchResolver(f.db).ResolveForTester(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, []catalogdomain.CapabilityName{"camera.read.v1"}); !errors.Is(err, catalogport.ErrApplicationTestPublicationInconsistent) {
		t.Fatal("pure Test stale SET", err)
	}
}

func TestTestPublicationHistoryIntegration_UCAPP036_StorageCancellationIsNotCorruption(t *testing.T) {
	f := newOAuthProviderFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	client, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.DatabaseName == f.db.Name() && e.CommandName == "find" && e.Command.Lookup("find").StringValue() == applicationPublicationHistoryCollectionName {
			cancel()
		}
	}}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(context.Background())
	_, err = NewOAuthProviderRepository(client.Database(f.db.Name()), providerTestSecretVerifier{}).ResolveRuntime(ctx, f.publicID, oauthdomain.ChannelTest, 1, 2, time.Now())
	if err == nil || errors.Is(err, oauthport.ErrStateInconsistent) || errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatal("storage cancellation mapped to business state", err)
	}
}

func TestTestPublicationClearRepositoryIntegration_UCAPP036_DeterministicPublicCommandRaces(t *testing.T) {
	client := integrationClient(t)
	for _, kind := range []string{"transfer", "close", "stable", "test", "grey", "clear"} {
		for _, winner := range []string{"clear", "other"} {
			t.Run("BR-PUB-036 "+kind+" winner="+winner, func(t *testing.T) {
				db := reusableIntegrationDatabase(t, client)
				seed := createApprovedPublicationVersion(t, db, "clear-race")
				repo := NewApplicationPublicationRepository(db)
				placePublication(t, repo, loadPublicationCandidate(t, repo, seed, 1, nil), seed)
				now := time.Now().UTC().Truncate(time.Millisecond)
				expected := int64(1)
				stable, err := repo.LoadStablePlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), seed.adminID, &expected)
				if err != nil {
					t.Fatal(err)
				}
				var replaceTest *publicationdomain.TestPlacementCandidate
				if kind == "test" {
					replacement := addApprovedPublicationVersion(t, db, seed, "v2")
					replaceTest = loadPublicationCandidate(t, repo, replacement, 1, &expected)
				}
				var grey *publicationdomain.GreyPlacementCandidate
				if kind == "grey" {
					set, err := repo.SetStable(t.Context(), stable, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), seed.adminID, publicationTestValidation(), now)
					if err != nil {
						t.Fatal(err)
					}
					expected = set.Publication().Revision()
					grey, err = repo.LoadGreyPlacementCandidate(t.Context(), seed.applicationID, 1, publicationdomain.ApplicationVersionID(seed.versionID), 5000, seed.adminID, expected)
					if err != nil {
						t.Fatal(err)
					}
				}
				transfer := applicationdomain.ApplicationAdminTransferID(nextIntegrationApplicationReviewID(t))
				if kind == "transfer" {
					if _, err := NewApplicationAdminTransferRepository(db, transferSecretFactory{}, 100).Initiate(t.Context(), seed.applicationID, seed.adminID, "new-admin", 1, transfer, now); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				reached, release := make(chan struct{}), make(chan struct{})
				var once, resumeOnce sync.Once
				resume := func() { resumeOnce.Do(func() { close(release) }) }
				defer resume()
				winningClient, err := drivermongo.Connect(options.Client().ApplyURI(os.Getenv(mongoIntegrationURIEnvironment)).SetMonitor(&event.CommandMonitor{Started: func(commandContext context.Context, e *event.CommandStartedEvent) {
					if e.CommandName == "commitTransaction" {
						once.Do(func() {
							close(reached)
							select {
							case <-release:
							case <-commandContext.Done():
							}
						})
					}
				}}))
				if err != nil {
					t.Fatal(err)
				}
				defer winningClient.Disconnect(context.Background())
				loserCommand := "findAndModify"
				if winner == "other" || kind == "clear" {
					loserCommand = "update"
				}
				competing, started := monitoredMongoClient(t, db.Name(), loserCommand)
				clear := func(database *drivermongo.Database) error {
					_, err := NewApplicationPublicationRepository(database).ClearTest(ctx, seed.applicationID, 1, seed.adminID, expected, clearTestAudit(t))
					return err
				}
				other := func(database *drivermongo.Database) error {
					switch kind {
					case "transfer":
						_, err := NewApplicationAdminTransferRepository(database, transferSecretFactory{}, 100).Accept(ctx, transfer, "new-admin", applicationdomain.ConfidentialCredentialKeep, now.Add(time.Second))
						return err
					case "close":
						proof := applicationdomain.ApplicationCloseProof{Subject: seed.adminID, ApplicationID: seed.applicationID, JTI: nextIntegrationApplicationReviewID(t).String(), AuthTime: now, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
						_, err := NewApplicationClosureRepository(database).Start(ctx, seed.applicationID, seed.adminID, 1, 1, proof, applicationdomain.ApplicationClosureID(nextIntegrationApplicationReviewID(t)), now)
						return err
					case "test":
						_, err := NewApplicationPublicationRepository(database).PlaceInTest(ctx, replaceTest, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), seed.adminID, publicationTestValidation(), now)
						return err
					case "grey":
						cohort, _ := publicationdomain.NewCohortSeed(make([]byte, 32))
						rollout := publicationdomain.GreyRolloutID(nextIntegrationApplicationReviewID(t))
						validation := publicationTestValidation()
						_, err := NewApplicationPublicationRepository(database).SetGrey(ctx, grey, &rollout, &cohort, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), seed.adminID, &validation, now)
						return err
					case "clear":
						return clear(database)
					default:
						_, err := NewApplicationPublicationRepository(database).SetStable(ctx, stable, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), seed.adminID, publicationTestValidation(), now)
						return err
					}
				}
				winning, losing := make(chan error, 1), make(chan error, 1)
				go func() {
					if winner == "clear" {
						winning <- clear(winningClient.Database(db.Name()))
					} else {
						winning <- other(winningClient.Database(db.Name()))
					}
				}()
				awaitMongoCommand(t, ctx, reached, "winner holds shared fence before commit")
				go func() {
					if winner == "clear" {
						losing <- other(competing.Database(db.Name()))
					} else {
						losing <- clear(competing.Database(db.Name()))
					}
				}()
				awaitMongoCommand(t, ctx, started, "competitor attempts shared write fence")
				resume()
				if err := <-winning; err != nil {
					t.Fatal("winner", err)
				}
				loserErr := <-losing
				want := error(nil)
				if kind == "stable" || kind == "test" || kind == "grey" || kind == "clear" {
					want = publicationport.ErrApplicationPublicationRevisionConflict
				} else if winner == "other" && kind == "transfer" {
					want = publicationport.ErrApplicationAdminRequired
				} else if winner == "other" && kind == "close" {
					want = publicationport.ErrApplicationPublicationNotAllowed
				}
				if want == nil && loserErr != nil || want != nil && !errors.Is(loserErr, want) {
					t.Fatalf("loser=%v want=%v", loserErr, want)
				}
				var p applicationPublicationDocument
				if err := db.Collection(applicationPublicationsCollectionName).FindOne(t.Context(), bson.M{"applicationId": seed.applicationID.String()}).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if winner == "clear" || kind == "clear" {
					if p.TestVersionID != nil || p.Revision != expected+1 {
						t.Fatal(p)
					}
				} else {
					if p.TestVersionID == nil {
						t.Fatal("clear committed after exclusion")
					}
				}
				assertPublicationCounts(t, db, 1, p.Revision)
			})
		}
	}
}
