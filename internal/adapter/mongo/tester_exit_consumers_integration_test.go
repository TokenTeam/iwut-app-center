package mongo

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	cd "iwut-app-center/internal/catalog/domain"
	cp "iwut-app-center/internal/catalog/port"
	od "iwut-app-center/internal/oauthclient/domain"
	op "iwut-app-center/internal/oauthclient/port"
)

func TestUCAPP040BRTST049CurrentTesterConsumersIntegration(t *testing.T) {
	client := integrationClient(t)
	t.Run("UC038 and UC012 stop consuming removed episode", func(t *testing.T) {
		f := newTestLaunchFixture(t, client)
		launch := NewTestLaunchResolver(f.db)
		query := myTestRepository(f.db)
		if d, err := launch.ResolveForTester(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost); err != nil || d == nil {
			t.Fatal(err)
		}
		if item := myTestDetail(t, f); item.Availability != cd.MyTestReady {
			t.Fatal("not READY")
		}
		repo := NewApplicationTesterMembershipRepository(f.db)
		r, err := repo.Leave(t.Context(), f.seed.applicationID, f.membership.MembershipID(), f.membership.TesterAuthID(), time.Now())
		if err != nil || !r.Left() {
			t.Fatal(err)
		}
		if d, err := launch.ResolveForTester(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost); d != nil || !errors.Is(err, cp.ErrApplicationTesterRequired) {
			t.Fatal("UC012 consumed removed", err)
		}
		if page, err := query.ListMyTest(t.Context(), f.membership.TesterAuthID(), 1, myTestHost, 20, ""); err != nil || len(page.Items) != 0 {
			t.Fatal("UC038 list consumed removed", err)
		}
		if d, err := query.GetMyTest(t.Context(), f.seed.applicationID, f.membership.TesterAuthID(), 1, myTestHost); d != nil || !errors.Is(err, cd.ErrMyTestApplicationNotFound) {
			t.Fatal("UC038 detail consumed removed", err)
		}
	})
	t.Run("UC019 rechecks membership while preserving runtime and credentials", func(t *testing.T) {
		f := newOAuthProviderFixture(t)
		provider := NewOAuthProviderRepository(f.db, providerTestSecretVerifier{})
		runtime, err := provider.ResolveRuntime(t.Context(), f.publicID, od.ChannelTest, 1, 2, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if auth, err := provider.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), od.ChannelTest, 1, 2, runtime.Version(), time.Now()); err != nil || auth.TesterMembershipID != f.membership.MembershipID().String() {
			t.Fatal("initial OAuth", err)
		}
		var before bson.M
		if err := f.db.Collection(oauthClientCredentialsCollectionName).FindOne(t.Context(), bson.M{"clientId": f.confidentialID.String()}).Decode(&before); err != nil {
			t.Fatal(err)
		}
		repo := NewApplicationTesterMembershipRepository(f.db)
		if r, err := repo.Leave(t.Context(), f.seed.applicationID, f.membership.MembershipID(), f.membership.TesterAuthID(), time.Now()); err != nil || !r.Left() {
			t.Fatal(err)
		}
		if auth, err := provider.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), od.ChannelTest, 1, 2, runtime.Version(), time.Now()); auth != nil || !errors.Is(err, op.ErrRuntimeUnavailable) {
			t.Fatal("OAuth consumed removed", err)
		}
		next := joinIntegrationTesterMembership(t, repo, f.joinLink, newIntegrationTesterMembership(t, f.joinLink, f.membership.TesterAuthID())).Membership()
		if _, err := repo.Leave(t.Context(), f.seed.applicationID, f.membership.MembershipID(), f.membership.TesterAuthID(), time.Now()); err != nil {
			t.Fatal(err)
		}
		if auth, err := provider.ResolveAuthorizationContext(t.Context(), f.publicID, f.membership.TesterAuthID(), od.ChannelTest, 1, 2, runtime.Version(), time.Now()); err != nil || auth.TesterMembershipID != next.MembershipID().String() {
			t.Fatal("OAuth new episode", err)
		}
		var after bson.M
		if err := f.db.Collection(oauthClientCredentialsCollectionName).FindOne(t.Context(), bson.M{"clientId": f.confidentialID.String()}).Decode(&after); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("exit modified OAuth credentials")
		}
	})
}
