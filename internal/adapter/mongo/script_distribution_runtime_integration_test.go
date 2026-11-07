package mongo

import (
	"errors"
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	od "iwut-app-center/internal/oauthclient/domain"
	op "iwut-app-center/internal/oauthclient/port"
	pd "iwut-app-center/internal/publication/domain"
	rd "iwut-app-center/internal/review/domain"
	sd "iwut-app-center/internal/script/domain"
	"iwut-app-center/internal/shared"
	vd "iwut-app-center/internal/version/domain"
)

func TestScriptDistributionRuntimeIntegration_UCAPP047_BRSCR019020(t *testing.T) {
	db := migratedIntegrationDatabase(t, integrationClient(t))
	base, app, version, at := publishedAppScriptFixture(t, db, "script-runtime", true)
	stable := addScriptPublicationVersion(t, db, base, nil, "unconditional-stable", nil)
	publications := NewApplicationPublicationRepository(db)
	stableCandidate, err := publications.LoadStablePlacementCandidate(t.Context(), app.applicationID, 1, pd.ApplicationVersionID(stable.versionID), app.adminID, nil)
	if err != nil {
		t.Fatal(err)
	}
	publicationID := pd.ApplicationPublicationID(nextIntegrationApplicationReviewID(t))
	if _, err := publications.SetStable(t.Context(), stableCandidate, &publicationID, pd.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, publicationTestValidation(), time.Now()); err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	placePublication(t, publications, loadPublicationCandidate(t, publications, app, 1, &revision), app)
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), app.applicationID, app.adminID, nil, newIntegrationTesterJoinLink(t, app.applicationID, app.adminID)).JoinLink()
	member := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "script-runtime-tester")).Membership()
	caller := member.TesterAuthID()
	host := []cd.CapabilityName{"camera.read.v1"}
	test, unified, catalog := NewTestLaunchResolver(db), NewUnifiedLaunchResolver(db), NewPublicCatalogRepository(db)
	writer := NewScriptPublicationRepository(db)
	checkReady := func() {
		t.Helper()
		if target, err := test.ResolveForTester(t.Context(), app.applicationID, caller, 1, host); err != nil || target.VersionID() != app.versionID.String() {
			t.Fatal("test target", err)
		}
		if target, err := unified.Resolve(t.Context(), app.applicationID, caller, 1, host); err != nil || target.Channel() != cd.LaunchChannelTest {
			t.Fatal("unified target", err)
		}
		if item, err := catalog.GetPublic(t.Context(), app.applicationID, caller, 1, host); err != nil || item.LaunchTarget().VersionID() != app.versionID.String() {
			t.Fatal("catalog target", err)
		}
		if item, err := myTestRepository(db).GetMyTest(t.Context(), app.applicationID, caller, 1, host); err != nil || item.Item.Availability != cd.MyTestReady {
			t.Fatal("my test ready", err)
		}
	}
	checkReady()
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.ClearScriptHead, 2, at)); err != nil {
		t.Fatal(err)
	}
	checkReady()
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.SuspendScript, 1, at)); err != nil {
		t.Fatal(err)
	}
	if _, err := test.ResolveForTester(t.Context(), app.applicationID, caller, 1, host); !errors.Is(err, cp.ErrApplicationTestTargetUnavailable) {
		t.Fatal("suspended pure Test", err)
	}
	if _, err := unified.Resolve(t.Context(), app.applicationID, caller, 1, host); !errors.Is(err, cp.ErrApplicationLaunchTargetUnavailable) {
		t.Fatal("suspended Test downgraded to Stable", err)
	}
	if target, err := unified.Resolve(t.Context(), app.applicationID, caller, 1, nil); err != nil || target.VersionID() != stable.versionID.String() {
		t.Fatal("capability branch no longer allowed Stable", err)
	}
	if target, err := unified.Resolve(t.Context(), app.applicationID, "ordinary-user", 1, nil); err != nil || target.VersionID() != stable.versionID.String() {
		t.Fatal("private Test altered ordinary runtime", err)
	}
	if _, err := catalog.GetPublic(t.Context(), app.applicationID, caller, 1, host); !errors.Is(err, cp.ErrPublicApplicationNotFound) {
		t.Fatal("catalog downgraded blocked target", err)
	}
	if page, err := catalog.ListPublic(t.Context(), caller, 1, host, 20, ""); err != nil || len(page.Items()) != 0 {
		t.Fatal("catalog did not hide blocked target", err)
	}
	if item, err := catalog.GetPublic(t.Context(), app.applicationID, "ordinary-user", 1, nil); err != nil || item.LaunchTarget().VersionID() != stable.versionID.String() {
		t.Fatal("ordinary catalog changed", err)
	}
	card, err := myTestRepository(db).GetMyTest(t.Context(), app.applicationID, caller, 1, host)
	if err != nil || card.Item.Availability != cd.MyTestScriptDependencyUnavailable || card.Item.LaunchTarget != nil || len(card.Item.MissingCapabilities) != 0 {
		t.Fatal("script-unavailable card", err)
	}
	card, err = myTestRepository(db).GetMyTest(t.Context(), app.applicationID, caller, 1, nil)
	if err != nil || card.Item.Availability != cd.MyTestCapabilitiesInsufficient {
		t.Fatal("capability priority", err)
	}
	if _, err := test.ResolveForTester(t.Context(), app.applicationID, "ordinary-user", 1, host); !errors.Is(err, cp.ErrApplicationTesterRequired) {
		t.Fatal("script result leaked before membership", err)
	}

	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.RestoreScript, 2, at)); err != nil {
		t.Fatal(err)
	}
	checkReady()
	// A valid Test cannot bypass the ordinary catalog's independent Stable gate.
	revision = 2
	stableCandidate, err = publications.LoadStablePlacementCandidate(t.Context(), app.applicationID, 1, pd.ApplicationVersionID(app.versionID), app.adminID, &revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publications.SetStable(t.Context(), stableCandidate, nil, pd.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), app.adminID, publicationTestValidation(), time.Now()); err != nil {
		t.Fatal(err)
	}
	revision = 3
	placePublication(t, publications, loadPublicationCandidate(t, publications, stable, 1, &revision), stable)
	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.SuspendScript, 3, at)); err != nil {
		t.Fatal(err)
	}
	if target, err := unified.Resolve(t.Context(), app.applicationID, caller, 1, nil); err != nil || target.VersionID() != stable.versionID.String() {
		t.Fatal("independent Test qualification", err)
	}
	if _, err := catalog.GetPublic(t.Context(), app.applicationID, caller, 1, nil); !errors.Is(err, cp.ErrPublicApplicationNotFound) {
		t.Fatal("Test bypassed unavailable Stable baseline", err)
	}
	if card, err := myTestRepository(db).GetMyTest(t.Context(), app.applicationID, caller, 1, nil); err != nil || card.Item.Availability != cd.MyTestReady {
		t.Fatal("my test incorrectly required Stable", err)
	}

	if _, err := writer.ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.RestoreScript, 4, at)); err != nil {
		t.Fatal(err)
	}
	revision = 4
	placePublication(t, publications, loadPublicationCandidate(t, publications, app, 1, &revision), app)
	if _, err := db.Collection(scriptPublishedVersionsCollection).DeleteOne(t.Context(), bson.M{"resourceId": version.ResourceID, "version.versionId": version.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := unified.Resolve(t.Context(), app.applicationID, caller, 1, host); !errors.Is(err, cp.ErrApplicationRuntimeStateInconsistent) {
		t.Fatal("missing proof became normal unavailable", err)
	}
	if _, err := test.ResolveForTester(t.Context(), app.applicationID, caller, 1, host); !errors.Is(err, cp.ErrApplicationTestPublicationInconsistent) {
		t.Fatal("pure Test proof damage", err)
	}
	if _, err := catalog.ListPublic(t.Context(), caller, 1, host, 20, ""); !errors.Is(err, cp.ErrApplicationCatalogStateInconsistent) {
		t.Fatal("catalog hid corrupted proof", err)
	}
	if _, err := myTestRepository(db).GetMyTest(t.Context(), app.applicationID, caller, 1, host); !errors.Is(err, cd.ErrMyTestApplicationStateInconsistent) {
		t.Fatal("corruption became an unavailable card", err)
	}
}

func TestScriptDistributionOAuthIntegration_UCAPP047_BRSCR020(t *testing.T) {
	f := newOAuthProviderFixture(t)
	version, _, at := approvedPublicationFixture(t, f.db)
	if _, err := NewScriptPublicationRepository(f.db).ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, version.ID, sd.PublishScript, 1, at)); err != nil {
		t.Fatal(err)
	}
	refs := []shared.ScriptReference{{ResourceID: string(version.ResourceID), VersionID: string(version.ID)}}
	writer := NewApplicationVersionRepository(f.db)
	draft, err := writer.CreateDraft(t.Context(), f.seed.adminID, integrationApplicationVersionDraft(t, f.seed.applicationID, f.seed.adminID.String(), "oauth-script").WithScriptReferences(refs))
	if err != nil {
		t.Fatal(err)
	}
	label, _ := vd.NewVersionLabel("oauth-script")
	launch, _ := vd.NewLaunchURL("https://example.edu/apps/oauth-script")
	rpc, _ := vd.NewRPCApiRange(1, 3)
	caps, _ := vd.NewCapabilitySet([]string{"camera.read.v1"})
	scopes, _ := vd.NewScopeRequest([]string{"profile.basic"}, nil)
	redirects, _ := vd.NewOAuthRedirectConfiguration([]string{"https://public-one.oauth.example.edu/callback"}, []string{"https://confidential-one.oauth.example.edu/callback"})
	replacement, err := vd.NewDraftApplicationVersionReplacementWithOAuth(label, launch, rpc, caps, scopes, redirects)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ReplaceDraft(t.Context(), f.seed.applicationID, draft.ID(), f.seed.adminID, 1, replacement.WithScriptReferences(refs), time.Now()); err != nil {
		t.Fatal(err)
	}
	seed := f.seed
	seed.versionID = rd.ApplicationVersionID(draft.ID())
	seed.reviewID = nextIntegrationApplicationReviewID(t)
	submitter := NewApplicationReviewRepository(f.db)
	candidate := loadReviewCandidate(t, submitter, seed.applicationID, seed.versionID, seed.adminID.String(), 2)
	if _, err := submitter.Submit(t.Context(), candidate, seed.reviewID, seed.adminID, 41, "public-https.v1", time.Now()); err != nil {
		t.Fatal(err)
	}
	approvePublicationVersion(t, f.db, seed)
	revision := int64(1)
	publications := NewApplicationPublicationRepository(f.db)
	placePublication(t, publications, loadPublicationCandidate(t, publications, seed, 1, &revision), seed)
	provider := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
	runtime, err := provider.ResolveRuntime(t.Context(), f.publicID, od.ChannelTest, 1, 2, time.Now())
	if err != nil || runtime.VersionID != seed.versionID.String() {
		t.Fatal("script-bound OAuth runtime", err)
	}
	before, err := provider.GetPublishedRedirects(t.Context(), seed.applicationID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewScriptPublicationRepository(f.db).ApplyPublication(t.Context(), scriptPublicationCommand(t, version.ResourceID, "", sd.SuspendScript, 1, at)); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ResolveRuntime(t.Context(), f.publicID, od.ChannelTest, 1, 2, time.Now()); !errors.Is(err, op.ErrRuntimeUnavailable) {
		t.Fatal("pre-login bypassed script pause", err)
	}
	if _, err := provider.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), od.ChannelTest, 1, 2, runtime.Version(), time.Now()); !errors.Is(err, op.ErrRuntimeUnavailable) {
		t.Fatal("unchanged tuple bypassed current gate", err)
	}
	after, err := provider.GetPublishedRedirects(t.Context(), seed.applicationID, time.Now())
	if err != nil || before.ApplicationID != after.ApplicationID || !slices.Equal(before.Entries, after.Entries) || !slices.Equal(before.RedirectURIs, after.RedirectURIs) {
		t.Fatal("script pause altered sector callback facts", err)
	}
}
