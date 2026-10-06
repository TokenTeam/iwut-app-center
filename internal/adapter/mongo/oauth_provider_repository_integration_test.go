package mongo

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	applicationdomain "iwut-app-center/internal/application/domain"
	oauthdomain "iwut-app-center/internal/oauthclient/domain"
	oauthport "iwut-app-center/internal/oauthclient/port"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	publicationdomain "iwut-app-center/internal/publication/domain"
	reviewdomain "iwut-app-center/internal/review/domain"
	"iwut-app-center/internal/shared"
	testerdomain "iwut-app-center/internal/tester/domain"
	versiondomain "iwut-app-center/internal/version/domain"
)

type providerTestSecretVerifier struct{}

func (providerTestSecretVerifier) Verify(clientID oauthdomain.ClientID, plain string, digest oauthdomain.SecretDigest) bool {
	want := sha256.Sum256([]byte("iwut-oauth-client-secret-v1\x00" + clientID.String() + "\x00" + plain))
	got := digest.Bytes()
	return subtle.ConstantTimeCompare(want[:], got[:]) == 1
}

func providerTestSecret(clientID oauthdomain.ClientID, plain string) oauthdomain.SecretDigest {
	digest := sha256.Sum256([]byte("iwut-oauth-client-secret-v1\x00" + clientID.String() + "\x00" + plain))
	return oauthdomain.NewSecretDigest(digest)
}

type oauthProviderFixture struct {
	testLaunchFixture
	publicID, confidentialID oauthdomain.ClientID
	confidentialSecret       string
	profileRevisionID        string
	joinLink                 *testerdomain.ApplicationTesterJoinLink
}

func newOAuthProviderFixture(t *testing.T) oauthProviderFixture {
	t.Helper()
	client := integrationClient(t)
	db := migratedIntegrationDatabase(t, client)
	application := createVersionTestApplication(t, db, "oauth-provider-admin", "oauth-provider")
	versionRepo := NewApplicationVersionRepository(db)
	created, err := versionRepo.CreateDraft(t.Context(), application.AdminID(), integrationApplicationVersionDraft(t, application.ID(), application.AdminID().String(), "oauth-v1"))
	if err != nil {
		t.Fatal(err)
	}
	redirects, err := versiondomain.NewOAuthRedirectConfiguration(
		[]string{"https://public-one.oauth.example.edu/callback", "https://public-two.oauth.example.edu/callback"},
		[]string{"https://confidential-one.oauth.example.edu/callback", "https://confidential-two.oauth.example.edu/callback"},
	)
	if err != nil {
		t.Fatal(err)
	}
	label, _ := versiondomain.NewVersionLabel("oauth-v1")
	launch, _ := versiondomain.NewLaunchURL("https://example.edu/apps/oauth-v1")
	rpcRange, _ := versiondomain.NewRPCApiRange(1, 3)
	capabilities, _ := versiondomain.NewCapabilitySet([]string{"camera.read.v1"})
	scopes, _ := versiondomain.NewScopeRequest([]string{"profile.basic"}, []string{"schedule.read"})
	replacement, err := versiondomain.NewDraftApplicationVersionReplacementWithOAuth(label, launch, rpcRange, capabilities, scopes, redirects)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := versionRepo.ReplaceDraft(t.Context(), application.ID(), created.ID(), application.AdminID(), 1, replacement, time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	seed := decidableReview{applicationID: application.ID(), versionID: reviewdomain.ApplicationVersionID(updated.ID()), reviewID: nextIntegrationApplicationReviewID(t), adminID: application.AdminID()}
	reviewRepo := NewApplicationReviewRepository(db)
	candidate := loadReviewCandidate(t, reviewRepo, seed.applicationID, seed.versionID, seed.adminID.String(), updated.Revision())
	if _, err = reviewRepo.Submit(t.Context(), candidate, seed.reviewID, seed.adminID, 41, "public-https.v1", time.Date(2026, 10, 3, 1, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	approvePublicationVersion(t, db, seed)
	profileID := approvePublicationProfile(t, db, seed)
	clientRepo := NewOAuthClientRepository(db)
	publicID := integrationOAuthClientID(t, 901)
	if _, err = clientRepo.Register(t.Context(), seed.applicationID, oauthdomain.ChannelTest, oauthdomain.ClientTypePublicPKCE, nil, publicID, nil, seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	confidentialID := integrationOAuthClientID(t, 902)
	secret := "fixture-confidential-secret"
	digest := providerTestSecret(confidentialID, secret)
	expected := int64(1)
	if _, err = clientRepo.Register(t.Context(), seed.applicationID, oauthdomain.ChannelTest, oauthdomain.ClientTypeConfidentialSecret, &expected, confidentialID, &digest, seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	publicationRepo := NewApplicationPublicationRepository(db)
	publication := placePublication(t, publicationRepo, loadPublicationCandidate(t, publicationRepo, seed, 1, nil), seed).Publication()
	placePublication(t, publicationRepo, loadPublicationCandidate(t, publicationRepo, seed, 2, nil), seed)
	link := createIntegrationTesterJoinLink(t, NewApplicationTesterJoinLinkRepository(db), seed.applicationID, seed.adminID, nil, newIntegrationTesterJoinLink(t, seed.applicationID, seed.adminID)).JoinLink()
	membership := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(db), link, newIntegrationTesterMembership(t, link, "oauth-tester")).Membership()
	return oauthProviderFixture{
		testLaunchFixture: testLaunchFixture{db: db, seed: seed, publication: publication, membership: membership},
		publicID:          publicID, confidentialID: confidentialID, confidentialSecret: secret, profileRevisionID: profileID.String(),
		joinLink: link,
	}
}

func TestOAuthProviderRepositoryIntegration(t *testing.T) {
	f := newOAuthProviderFixture(t)
	repository := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
	at := time.Date(2026, 10, 3, 2, 0, 0, 0, time.UTC)

	t.Run("BR-OAC-006 metadata and revision-bound secret verification", func(t *testing.T) {
		public, err := repository.GetClientConfiguration(t.Context(), f.publicID)
		if err != nil || public.Type != oauthdomain.ClientTypePublicPKCE || public.TokenEndpointAuth != oauthdomain.TokenEndpointAuthMethodNone || public.CredentialRevision != nil || public.RegistrationRevision != 2 {
			t.Fatalf("public configuration=%#v error=%v", public, err)
		}
		confidential, err := repository.GetClientConfiguration(t.Context(), f.confidentialID)
		if err != nil || confidential.Type != oauthdomain.ClientTypeConfidentialSecret || confidential.TokenEndpointAuth != oauthdomain.TokenEndpointAuthMethodClientSecretBasic || confidential.CredentialRevision == nil || *confidential.CredentialRevision != 1 {
			t.Fatalf("confidential configuration=%#v error=%v", confidential, err)
		}
		for _, test := range []struct {
			name     string
			clientID oauthdomain.ClientID
			secret   string
			expected int64
			verified bool
			revision int64
		}{
			{"match", f.confidentialID, f.confidentialSecret, 1, true, 1},
			{"wrong secret", f.confidentialID, "wrong", 1, false, 1},
			{"stale revision", f.confidentialID, f.confidentialSecret, 2, false, 1},
			{"public", f.publicID, f.confidentialSecret, 1, false, 0},
			{"unknown", integrationOAuthClientID(t, 999), f.confidentialSecret, 1, false, 0},
		} {
			t.Run(test.name, func(t *testing.T) {
				verified, revision, err := repository.VerifyClientSecret(t.Context(), test.clientID, test.secret, test.expected)
				if err != nil || verified != test.verified || revision != test.revision {
					t.Fatalf("VerifyClientSecret()=(%t,%d,%v)", verified, revision, err)
				}
			})
		}
		rotatedSecret := "fixture-rotated-secret"
		rotatedDigest := providerTestSecret(f.confidentialID, rotatedSecret)
		if _, err = NewOAuthClientRepository(f.db).RotateSecret(t.Context(), f.confidentialID, 1, rotatedDigest, f.seed.adminID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if verified, revision, err := repository.VerifyClientSecret(t.Context(), f.confidentialID, f.confidentialSecret, 1); err != nil || verified || revision != 2 {
			t.Fatalf("old revision verification=(%t,%d,%v)", verified, revision, err)
		}
		if verified, revision, err := repository.VerifyClientSecret(t.Context(), f.confidentialID, rotatedSecret, 2); err != nil || !verified || revision != 2 {
			t.Fatalf("rotated verification=(%t,%d,%v)", verified, revision, err)
		}
	})

	t.Run("BR-OAC-007 BR-OAC-008 exact-major approved runtime and tester episode", func(t *testing.T) {
		public, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, at)
		if err != nil || public.VersionID != f.seed.versionID.String() || public.PublicationRevision != 1 || public.Display.ProfileRevisionID != f.profileRevisionID || public.AdminAuthID != f.seed.adminID || public.ValidUntil.Sub(public.ObservedAt) != 5*time.Second || !reflect.DeepEqual(public.RedirectURIs, []string{"https://public-one.oauth.example.edu/callback", "https://public-two.oauth.example.edu/callback"}) || !reflect.DeepEqual(public.RequiredScopes, []string{"profile.basic"}) || !reflect.DeepEqual(public.OptionalScopes, []string{"schedule.read"}) {
			t.Fatalf("public runtime=%#v error=%v", public, err)
		}
		confidential, err := repository.ResolveRuntime(t.Context(), f.confidentialID, oauthdomain.ChannelTest, 1, 2, at)
		if err != nil || !reflect.DeepEqual(confidential.RedirectURIs, []string{"https://confidential-one.oauth.example.edu/callback", "https://confidential-two.oauth.example.edu/callback"}) {
			t.Fatalf("confidential runtime=%#v error=%v", confidential, err)
		}
		secondMajor, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 2, 2, at)
		if err != nil || secondMajor.RPCAPIMajor != 2 || secondMajor.VersionID != public.VersionID || secondMajor.PublicationRevision != 1 {
			t.Fatalf("second-major runtime=%#v error=%v", secondMajor, err)
		}
		context, err := repository.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, 2, public.Version(), at)
		if err != nil || context.TesterMembershipID != f.membership.MembershipID().String() || context.AuthID != f.membership.TesterAuthID() {
			t.Fatalf("authorization context=%#v error=%v", context, err)
		}
		wrongVersion := public.Version()
		wrongVersion.PublicationRevision++
		if got, err := repository.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, 2, wrongVersion, at); got != nil || !errors.Is(err, oauthport.ErrRuntimeVersionChanged) {
			t.Fatalf("mixed tuple result=%#v error=%v", got, err)
		}
		if got, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 3, 2, at); got != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
			t.Fatalf("major fallback result=%#v error=%v", got, err)
		}
		if got, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelGrey, 1, 2, at); got != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
			t.Fatalf("channel crossover result=%#v error=%v", got, err)
		}
	})

	t.Run("BR-OAC-011 current published redirect union", func(t *testing.T) {
		snapshot, err := repository.GetPublishedRedirects(t.Context(), f.seed.applicationID, at)
		want := []string{"https://confidential-one.oauth.example.edu/callback", "https://confidential-two.oauth.example.edu/callback", "https://public-one.oauth.example.edu/callback", "https://public-two.oauth.example.edu/callback"}
		if err != nil || len(snapshot.Entries) != 2 || snapshot.Entries[0].RPCAPIMajor != 1 || snapshot.Entries[1].RPCAPIMajor != 2 || snapshot.Entries[0].VersionID != f.seed.versionID.String() || snapshot.Entries[1].VersionID != f.seed.versionID.String() || !reflect.DeepEqual(snapshot.RedirectURIs, want) {
			t.Fatalf("published redirects=%#v error=%v", snapshot, err)
		}
		if _, err = NewOAuthClientRepository(f.db).SetStatus(t.Context(), f.publicID, 2, oauthdomain.ClientStatusDisabled, f.seed.adminID, time.Now()); err != nil {
			t.Fatal(err)
		}
		afterDisable, err := repository.GetPublishedRedirects(t.Context(), f.seed.applicationID, at.Add(time.Second))
		if err != nil || !reflect.DeepEqual(afterDisable.RedirectURIs, want) {
			t.Fatalf("disabled identity changed sector redirects=%#v error=%v", afterDisable, err)
		}
	})
}

func TestOAuthProviderRepositoryIntegration_UCAPP028_AllProviderMethodsFailClosedWhileSuspended(t *testing.T) {
	f := newOAuthProviderFixture(t)
	repository := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
	at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	runtime, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, at)
	if err != nil {
		t.Fatal(err)
	}
	eventID := applicationdomain.ApplicationOperationEventID("0199b33c-d050-7abc-8abc-123456789012")
	if _, err = NewApplicationOperationsRepository(f.db).Set(t.Context(), f.seed.applicationID, "platform-operator", applicationdomain.PlatformAvailabilitySuspended, 1, 1, "oauth incident", eventID, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	if value, err := repository.GetClientConfiguration(t.Context(), f.publicID); value != nil || !errors.Is(err, oauthport.ErrApplicationNotFound) {
		t.Fatalf("GetClientConfiguration()=(%#v,%v)", value, err)
	}
	if verified, revision, err := repository.VerifyClientSecret(t.Context(), f.confidentialID, f.confidentialSecret, 1); err != nil || verified || revision != 0 {
		t.Fatalf("VerifyClientSecret()=(%t,%d,%v)", verified, revision, err)
	}
	if value, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, at.Add(2*time.Second)); value != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("ResolveRuntime()=(%#v,%v)", value, err)
	}
	if value, err := repository.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, 2, runtime.Version(), at.Add(2*time.Second)); value != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("ResolveAuthorizationContext()=(%#v,%v)", value, err)
	}
	if value, err := repository.GetPublishedRedirects(t.Context(), f.seed.applicationID, at.Add(2*time.Second)); value != nil || !errors.Is(err, oauthport.ErrApplicationNotFound) {
		t.Fatalf("GetPublishedRedirects()=(%#v,%v)", value, err)
	}
}

func TestOAuthProviderRepositoryIntegration_UCAPP020_StableRuntimeAndSharedRevision(t *testing.T) {
	f := newOAuthProviderFixture(t)
	clientRepository := NewOAuthClientRepository(f.db)
	stablePublicID := integrationOAuthClientID(t, 903)
	if _, err := clientRepository.Register(t.Context(), f.seed.applicationID, oauthdomain.ChannelStable, oauthdomain.ClientTypePublicPKCE, nil, stablePublicID, nil, f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	stableConfidentialID := integrationOAuthClientID(t, 904)
	digest := providerTestSecret(stableConfidentialID, "stable-secret")
	expectedRegistrationRevision := int64(1)
	if _, err := clientRepository.Register(t.Context(), f.seed.applicationID, oauthdomain.ChannelStable, oauthdomain.ClientTypeConfidentialSecret, &expectedRegistrationRevision, stableConfidentialID, &digest, f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}

	publicationRepository := NewApplicationPublicationRepository(f.db)
	expectedPublicationRevision := int64(1)
	candidate, err := publicationRepository.LoadStablePlacementCandidate(t.Context(), f.seed.applicationID, 1, publicationdomain.ApplicationVersionID(f.seed.versionID), f.seed.adminID, &expectedPublicationRevision)
	if err != nil {
		t.Fatal(err)
	}
	set, err := publicationRepository.SetStable(t.Context(), candidate, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, publicationTestValidation(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if set.Publication().Revision() != 2 {
		t.Fatalf("publication revision=%d", set.Publication().Revision())
	}

	repository := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
	at := time.Date(2026, 10, 4, 1, 0, 0, 0, time.UTC)
	stableRuntime, err := repository.ResolveRuntime(t.Context(), stablePublicID, oauthdomain.ChannelStable, 1, 2, at)
	if err != nil || stableRuntime.Channel != oauthdomain.ChannelStable || stableRuntime.PublicationRevision != 2 || stableRuntime.VersionID != f.seed.versionID.String() {
		t.Fatalf("stable runtime=%#v error=%v", stableRuntime, err)
	}
	authorization, err := repository.ResolveAuthorizationContext(t.Context(), stablePublicID, "ordinary-user", oauthdomain.ChannelStable, 1, 2, stableRuntime.Version(), at)
	if err != nil || authorization.AuthID != "ordinary-user" || authorization.TesterMembershipID != "" {
		t.Fatalf("stable authorization=%#v error=%v", authorization, err)
	}
	if runtime, err := repository.ResolveRuntime(t.Context(), stablePublicID, oauthdomain.ChannelTest, 1, 2, at); runtime != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("stable client crossed into test: runtime=%#v error=%v", runtime, err)
	}
	// A stable write advances the shared publication revision without invalidating
	// the older SET_TEST history that still owns the test slot.
	testRuntime, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, at)
	if err != nil || testRuntime.PublicationRevision != 2 || testRuntime.VersionID != f.seed.versionID.String() {
		t.Fatalf("test runtime after stable set=%#v error=%v", testRuntime, err)
	}
	snapshot, err := repository.GetPublishedRedirects(t.Context(), f.seed.applicationID, at)
	if err != nil || len(snapshot.Entries) != 3 || snapshot.Entries[0].Channel != oauthdomain.ChannelStable || snapshot.Entries[1].Channel != oauthdomain.ChannelTest || snapshot.Entries[2].RPCAPIMajor != 2 {
		t.Fatalf("published snapshot=%#v error=%v", snapshot, err)
	}

	clearCandidate, err := publicationRepository.LoadStableClearCandidate(t.Context(), f.seed.applicationID, 1, f.seed.adminID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = publicationRepository.ClearStable(t.Context(), clearCandidate, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if runtime, err := repository.ResolveRuntime(t.Context(), stablePublicID, oauthdomain.ChannelStable, 1, 2, at); runtime != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("cleared stable runtime=%#v error=%v", runtime, err)
	}
	testRuntime, err = repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, at)
	if err != nil || testRuntime.PublicationRevision != 3 {
		t.Fatalf("test runtime after stable clear=%#v error=%v", testRuntime, err)
	}
}

func TestOAuthProviderRepositoryIntegration_UCAPP021_GreyCohort(t *testing.T) {
	f := newOAuthProviderFixture(t)
	clientRepository := NewOAuthClientRepository(f.db)
	greyPublicID := integrationOAuthClientID(t, 905)
	if _, err := clientRepository.Register(t.Context(), f.seed.applicationID, oauthdomain.ChannelGrey, oauthdomain.ClientTypePublicPKCE, nil, greyPublicID, nil, f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	greyConfidentialID := integrationOAuthClientID(t, 906)
	registrationRevision := int64(1)
	greyDigest := providerTestSecret(greyConfidentialID, "grey-secret")
	if _, err := clientRepository.Register(t.Context(), f.seed.applicationID, oauthdomain.ChannelGrey, oauthdomain.ClientTypeConfidentialSecret, &registrationRevision, greyConfidentialID, &greyDigest, f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	stablePublicID := integrationOAuthClientID(t, 907)
	if _, err := clientRepository.Register(t.Context(), f.seed.applicationID, oauthdomain.ChannelStable, oauthdomain.ClientTypePublicPKCE, nil, stablePublicID, nil, f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	stableConfidentialID := integrationOAuthClientID(t, 908)
	stableDigest := providerTestSecret(stableConfidentialID, "stable-secret")
	registrationRevision = 1
	if _, err := clientRepository.Register(t.Context(), f.seed.applicationID, oauthdomain.ChannelStable, oauthdomain.ClientTypeConfidentialSecret, &registrationRevision, stableConfidentialID, &stableDigest, f.seed.adminID, time.Now()); err != nil {
		t.Fatal(err)
	}
	publicationRepository := NewApplicationPublicationRepository(f.db)
	expected := int64(1)
	stable, err := publicationRepository.LoadStablePlacementCandidate(t.Context(), f.seed.applicationID, 1, publicationdomain.ApplicationVersionID(f.seed.versionID), f.seed.adminID, &expected)
	if err != nil {
		t.Fatal(err)
	}
	stableResult, err := publicationRepository.SetStable(t.Context(), stable, nil, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, publicationTestValidation(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	exposure, _ := publicationdomain.NewExposureBasisPoints(5000)
	grey, err := publicationRepository.LoadGreyPlacementCandidate(t.Context(), f.seed.applicationID, 1, publicationdomain.ApplicationVersionID(f.seed.versionID), exposure, f.seed.adminID, stableResult.Publication().Revision())
	if err != nil {
		t.Fatal(err)
	}
	seedBytes := make([]byte, 32)
	for index := range seedBytes {
		seedBytes[index] = byte(index)
	}
	cohortSeed, _ := publicationdomain.NewCohortSeed(seedBytes)
	rolloutID := publicationdomain.GreyRolloutID(nextIntegrationApplicationReviewID(t))
	validation := publicationTestValidation()
	set, err := publicationRepository.SetGrey(t.Context(), grey, &rolloutID, &cohortSeed, publicationdomain.ApplicationPublicationHistoryID(nextIntegrationApplicationReviewID(t)), f.seed.adminID, &validation, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	repository := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
	at := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)
	runtime, err := repository.ResolveRuntime(t.Context(), greyPublicID, oauthdomain.ChannelGrey, 1, 2, at)
	if err != nil || runtime.Channel != oauthdomain.ChannelGrey || runtime.PublicationRevision != set.Publication().Revision() || runtime.VersionID != f.seed.versionID.String() {
		t.Fatalf("runtime=%#v error=%v", runtime, err)
	}
	rollout := set.Publication().GreyRollout()
	var matched, missed string
	for index := 0; matched == "" || missed == ""; index++ {
		candidate := shared.AuthID(fmt.Sprintf("grey-user-%d", index))
		if rollout.Matches(candidate) {
			matched = candidate.String()
		} else {
			missed = candidate.String()
		}
	}
	context, err := repository.ResolveAuthorizationContext(t.Context(), greyPublicID, shared.AuthID(matched), oauthdomain.ChannelGrey, 1, 2, runtime.Version(), at)
	if err != nil || context.TesterMembershipID != "" || context.AuthID.String() != matched {
		t.Fatalf("matched context=%#v error=%v", context, err)
	}
	if context, err := repository.ResolveAuthorizationContext(t.Context(), greyPublicID, shared.AuthID(missed), oauthdomain.ChannelGrey, 1, 2, runtime.Version(), at); context != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
		t.Fatalf("missed context=%#v error=%v", context, err)
	}
}

func TestOAuthProviderRepositoryFailsClosedOnEligibilityChanges(t *testing.T) {
	t.Run("tester removal", func(t *testing.T) {
		f := newOAuthProviderFixture(t)
		repository := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
		runtime, err := repository.ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = NewApplicationTesterMembershipRepository(f.db).Remove(t.Context(), f.seed.applicationID, f.membership.MembershipID(), f.seed.adminID, time.Now()); err != nil {
			t.Fatal(err)
		}
		if got, err := repository.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, 2, runtime.Version(), time.Now()); got != nil || !errors.Is(err, oauthport.ErrRuntimeUnavailable) {
			t.Fatalf("removed tester result=%#v error=%v", got, err)
		}
		rejoined := joinIntegrationTesterMembership(t, NewApplicationTesterMembershipRepository(f.db), f.joinLink, newIntegrationTesterMembership(t, f.joinLink, f.membership.TesterAuthID())).Membership()
		if rejoined.MembershipID() == f.membership.MembershipID() {
			t.Fatal("rejoin reused the removed membership episode")
		}
		got, err := repository.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), oauthdomain.ChannelTest, 1, 2, runtime.Version(), time.Now())
		if err != nil || got.TesterMembershipID != rejoined.MembershipID().String() {
			t.Fatalf("rejoined tester result=%#v error=%v", got, err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, oauthProviderFixture)
		want   error
	}{
		{"missing public profile", func(t *testing.T, f oauthProviderFixture) {
			if _, err := f.db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"currentPublishedProfileRevisionId": nil}}); err != nil {
				t.Fatal(err)
			}
		}, oauthport.ErrRuntimeUnavailable},
		{"dangling public profile", func(t *testing.T, f oauthProviderFixture) {
			if _, err := f.db.Collection(applicationProfilesCollectionName).UpdateOne(t.Context(), bson.M{"applicationId": f.seed.applicationID.String()}, bson.M{"$set": bson.M{"currentPublishedProfileRevisionId": "01890f47-0000-7000-8000-000000009999"}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
		}, oauthport.ErrProfileStateInconsistent},
		{"approved snapshot drift", func(t *testing.T, f oauthProviderFixture) {
			if _, err := f.db.Collection(applicationVersionOAuthConfigsCollectionName).UpdateOne(t.Context(), bson.M{"applicationVersionId": f.seed.versionID.String()}, bson.M{"$set": bson.M{"oauthRedirects.pkceRedirectUris": []string{"https://drift.oauth.example.edu/callback"}}}, options.UpdateOne().SetBypassDocumentValidation(true)); err != nil {
				t.Fatal(err)
			}
		}, oauthport.ErrStateInconsistent},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newOAuthProviderFixture(t)
			test.mutate(t, f)
			got, err := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{}).ResolveRuntime(t.Context(), f.publicID, oauthdomain.ChannelTest, 1, 2, time.Now())
			if got != nil || !errors.Is(err, test.want) {
				t.Fatalf("ResolveRuntime()=(%#v,%v), want %v", got, err, test.want)
			}
		})
	}
}

func TestOAuthProviderRepositoryProfileSwitchUsesOneSnapshot(t *testing.T) {
	for _, order := range []string{"before", "after"} {
		t.Run("BR-OAC-008 profile switch "+order+" application read", func(t *testing.T) {
			f := newOAuthProviderFixture(t)
			monitored, trace := testLaunchMonitoredClient(t, f.db.Name(), order)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			type result struct {
				runtime *oauthdomain.RuntimeConfiguration
				err     error
			}
			done := make(chan result, 1)
			go func() {
				runtime, err := NewOAuthProviderRepository(monitored.Database(f.db.Name()), providerTestSecretVerifier{}).ResolveRuntime(ctx, f.publicID, oauthdomain.ChannelTest, 1, 2, time.Now())
				done <- result{runtime, err}
			}()
			select {
			case <-trace.reached:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			defer trace.resume()
			replacementProfileID := approveReplacementProfile(t, f).String()
			trace.resume()
			var old result
			select {
			case old = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if old.err != nil || old.runtime == nil || old.runtime.Display.ProfileRevisionID != f.profileRevisionID {
				t.Fatalf("mixed old snapshot runtime=%#v error=%v", old.runtime, old.err)
			}
			fresh, err := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{}).ResolveRuntime(ctx, f.publicID, oauthdomain.ChannelTest, 1, 2, time.Now())
			if err != nil || fresh.Display.ProfileRevisionID != replacementProfileID {
				t.Fatalf("fresh profile runtime=%#v error=%v", fresh, err)
			}
			trace.assertReadOnlySnapshot(t)
		})
	}
}

func approveReplacementProfile(t *testing.T, f oauthProviderFixture) profiledomain.ApplicationProfileRevisionID {
	t.Helper()
	expected, err := profiledomain.ParseApplicationProfileRevisionID(f.profileRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewApplicationProfileRevisionRepository(f.db)
	draft, err := repository.CreateDraft(t.Context(), f.seed.adminID, profileDraftFixture(t, f.seed.applicationID, f.seed.adminID))
	if err != nil {
		t.Fatal(err)
	}
	submission, err := repository.SubmitDraft(t.Context(), draft.ApplicationID(), draft.ProfileRevisionID(), f.seed.adminID, 1, profileReviewID(t), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.DecideReview(t.Context(), profileport.ProfileReviewDecisionInput{
		ApplicationID: f.seed.applicationID, ProfileRevisionID: draft.ProfileRevisionID(), ProfileReviewID: submission.Review.ProfileReviewID(),
		ReviewerID: "independent-profile-reviewer", Permissions: []string{profiledomain.ProfileReviewPermission}, ExpectedRevision: 2,
		ExpectedPublishedID: &expected, PolicyVersion: profiledomain.InitialProfileReviewPolicyVersion, Outcome: "APPROVE",
		ConfirmedCheckIDs: profiledomain.InitialProfileReviewChecks(), DecidedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return result.ProfileRevision.ProfileRevisionID()
}
