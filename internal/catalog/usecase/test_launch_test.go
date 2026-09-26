package usecase

import (
	"context"
	"errors"
	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
	"reflect"
	"strings"
	"testing"
)

const appID = "018f0000-0000-7000-8000-000000000001"

type resolverFunc func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.TestLaunchDescriptor, error)

func (f resolverFunc) ResolveForTester(ctx context.Context, app shared.ApplicationID, auth shared.AuthID, major int32, caps []domain.CapabilityName) (*domain.TestLaunchDescriptor, error) {
	return f(ctx, app, auth, major, caps)
}
func fixture(t *testing.T) *domain.TestLaunchDescriptor {
	t.Helper()
	r, e := domain.NewTestLaunchDescriptor(shared.ApplicationID(appID), "018f0000-0000-7000-8000-000000000002", 7, 3, "018f0000-0000-7000-8000-000000000003", "v1", "https://example.edu", 3, 4, []domain.CapabilityName{"camera.read.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestBR_RUN_001_005_008_010_OnlyAuthenticatedReadPort(t *testing.T) {
	query := ResolveTestLaunchTargetQuery{ApplicationID: strings.ToUpper(appID), HostRPCAPIMajor: 3, HostCapabilities: []string{"user.profile.v1", "camera.read.v1", "camera.read.v1"}}
	calls := 0
	h := NewResolveTestLaunchTarget(resolverFunc(func(_ context.Context, id shared.ApplicationID, auth shared.AuthID, major int32, caps []domain.CapabilityName) (*domain.TestLaunchDescriptor, error) {
		calls++
		if id.String() != appID || auth != "ordinary-user" || major != 3 || !reflect.DeepEqual(caps, []domain.CapabilityName{"camera.read.v1", "user.profile.v1"}) {
			t.Fatal("incorrect port input")
		}
		return fixture(t), nil
	}))
	r, e := h.Execute(context.Background(), shared.AuthenticatedUserIdentity{AuthID: "ordinary-user"}, query)
	if e != nil || r.PublicationRevision() != 7 || calls != 1 {
		t.Fatalf("%v %v calls%d", r, e, calls)
	}
	for _, tc := range []struct {
		name     string
		identity shared.AuthenticatedUserIdentity
		query    ResolveTestLaunchTargetQuery
		err      error
	}{
		{"identity-first", shared.AuthenticatedUserIdentity{}, ResolveTestLaunchTargetQuery{}, domain.ErrAuthenticatedUserRequired},
		{"application-id", shared.AuthenticatedUserIdentity{AuthID: "user"}, ResolveTestLaunchTargetQuery{ApplicationID: "bad"}, domain.ErrInvalidApplicationID},
		{"major", shared.AuthenticatedUserIdentity{AuthID: "user"}, ResolveTestLaunchTargetQuery{ApplicationID: appID, HostRPCAPIMajor: 0}, domain.ErrInvalidHostRPCAPIMajor},
		{"capability", shared.AuthenticatedUserIdentity{AuthID: "user"}, ResolveTestLaunchTargetQuery{ApplicationID: appID, HostRPCAPIMajor: 3, HostCapabilities: []string{"private invalid"}}, domain.ErrInvalidHostCapabilities},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := h.Execute(context.Background(), tc.identity, tc.query)
			if result != nil || !errors.Is(err, tc.err) || calls != 1 {
				t.Fatalf("result %v err %v calls%d", result, err, calls)
			}
		})
	}
}
func TestBR_RUN_001_002_003_004_005_009_ResolverFailuresNoPartialResult(t *testing.T) {
	for _, tc := range []struct {
		name           string
		source, target error
	}{
		{"missing-application", port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{"admin-is-not-tester", port.ErrApplicationTesterRequired, domain.ErrApplicationTesterRequired},
		{"removed-is-not-tester", domain.ErrApplicationTesterRequired, domain.ErrApplicationTesterRequired},
		{"exact-major-and-test-only", port.ErrApplicationTestTargetUnavailable, domain.ErrApplicationTestTargetUnavailable},
		{"dangling-or-drifted", port.ErrApplicationTestPublicationInconsistent, domain.ErrApplicationTestPublicationInconsistent},
		{"missing-caps", domain.NewHostCapabilitiesInsufficientError([]domain.CapabilityName{"user.profile.v1", "camera.read.v1"}), domain.ErrHostCapabilitiesInsufficient},
		{"driver-private-failure", errors.New("private launch/token document"), domain.ErrInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewResolveTestLaunchTarget(resolverFunc(func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.TestLaunchDescriptor, error) {
				return fixture(t), tc.source
			}))
			r, err := h.Execute(context.Background(), shared.AuthenticatedUserIdentity{AuthID: "user"}, ResolveTestLaunchTargetQuery{appID, 3, []string{"camera.read.v1"}})
			if r != nil || !errors.Is(err, tc.target) || strings.Contains(err.Error(), "private") {
				t.Fatalf("result%v err%v", r, err)
			}
			if errors.Is(err, domain.ErrInternal) && errors.Unwrap(err) != nil {
				t.Fatal("driver cause retained")
			}
		})
	}
}
func TestBR_RUN_006_DescriptorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result *domain.TestLaunchDescriptor
		major  int32
		caps   []string
		want   error
	}{
		{"nil", nil, 3, nil, domain.ErrInternal}, {"wrong-major", fixture(t), 4, []string{"camera.read.v1"}, domain.ErrApplicationTestPublicationInconsistent}, {"capability-bypass", fixture(t), 3, nil, domain.ErrApplicationTestPublicationInconsistent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewResolveTestLaunchTarget(resolverFunc(func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.TestLaunchDescriptor, error) {
				return tc.result, nil
			}))
			r, err := h.Execute(context.Background(), shared.AuthenticatedUserIdentity{AuthID: "user"}, ResolveTestLaunchTargetQuery{appID, tc.major, tc.caps})
			if r != nil || !errors.Is(err, tc.want) {
				t.Fatalf("r%v err%v", r, err)
			}
		})
	}
}
