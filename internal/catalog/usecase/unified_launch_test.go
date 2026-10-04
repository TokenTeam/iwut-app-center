package usecase

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"iwut-app-center/internal/catalog/domain"
	"iwut-app-center/internal/catalog/port"
	"iwut-app-center/internal/shared"
)

type unifiedResolverFunc func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error)

func (function unifiedResolverFunc) Resolve(ctx context.Context, applicationID shared.ApplicationID, authID shared.AuthID, major int32, capabilities []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error) {
	return function(ctx, applicationID, authID, major, capabilities)
}

func unifiedFixture(t *testing.T, channel domain.LaunchChannel) *domain.LaunchTargetDescriptor {
	t.Helper()
	descriptor, err := domain.NewLaunchTargetDescriptor(shared.ApplicationID(appID), "018f0000-0000-7000-8000-000000000002", 7, channel, 3, "018f0000-0000-7000-8000-000000000003", "v1", "https://example.edu", 3, 4, []domain.CapabilityName{"camera.read.v1"}, []string{"profile.basic"}, []string{"schedule.read"})
	if err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func TestBR_RUN_011_012_017_018_020_OptionalIdentityAndReusableNormalizedQuery(t *testing.T) {
	query := ResolveLaunchTargetQuery{ApplicationID: strings.ToUpper(appID), HostRPCAPIMajor: 3, HostCapabilities: []string{"user.profile.v1", "camera.read.v1", "camera.read.v1"}}
	for _, authID := range []shared.AuthID{"", "ordinary-user"} {
		t.Run(authID.String(), func(t *testing.T) {
			calls := 0
			handler := NewResolveLaunchTarget(unifiedResolverFunc(func(_ context.Context, applicationID shared.ApplicationID, gotAuthID shared.AuthID, major int32, capabilities []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error) {
				calls++
				if applicationID.String() != appID || gotAuthID != authID || major != 3 || !reflect.DeepEqual(capabilities, []domain.CapabilityName{"camera.read.v1", "user.profile.v1"}) {
					t.Fatalf("incorrect resolver input: %s %q %d %v", applicationID, gotAuthID, major, capabilities)
				}
				return unifiedFixture(t, domain.LaunchChannelStable), nil
			}))
			result, err := handler.Execute(context.Background(), shared.AuthenticatedUserIdentity{AuthID: authID}, query)
			if err != nil || result.Channel() != domain.LaunchChannelStable || calls != 1 {
				t.Fatalf("result=%v error=%v calls=%d", result, err, calls)
			}
		})
	}
}

func TestBR_RUN_012_InvalidQueryDoesNotRead(t *testing.T) {
	for _, test := range []struct {
		name  string
		query ResolveLaunchTargetQuery
		want  error
	}{
		{"application", ResolveLaunchTargetQuery{ApplicationID: "bad", HostRPCAPIMajor: 1}, domain.ErrInvalidApplicationID},
		{"major", ResolveLaunchTargetQuery{ApplicationID: appID}, domain.ErrInvalidHostRPCAPIMajor},
		{"capability", ResolveLaunchTargetQuery{ApplicationID: appID, HostRPCAPIMajor: 1, HostCapabilities: []string{"invalid"}}, domain.ErrInvalidHostCapabilities},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewResolveLaunchTarget(unifiedResolverFunc(func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error) {
				t.Fatal("resolver called")
				return nil, nil
			}))
			if result, err := handler.Execute(context.Background(), shared.AuthenticatedUserIdentity{}, test.query); result != nil || !errors.Is(err, test.want) {
				t.Fatalf("result=%v error=%v", result, err)
			}
		})
	}
}

func TestBR_RUN_015_016_UnifiedResolverErrorsFailClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		source error
		want   error
	}{
		{"application", port.ErrApplicationNotFound, domain.ErrApplicationNotFound},
		{"unavailable", port.ErrApplicationLaunchTargetUnavailable, domain.ErrApplicationLaunchTargetUnavailable},
		{"inconsistent", port.ErrApplicationRuntimeStateInconsistent, domain.ErrApplicationRuntimeStateInconsistent},
		{"unknown", errors.New("driver details"), domain.ErrInternal},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewResolveLaunchTarget(unifiedResolverFunc(func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error) {
				return nil, test.source
			}))
			result, err := handler.Execute(context.Background(), shared.AuthenticatedUserIdentity{}, ResolveLaunchTargetQuery{ApplicationID: appID, HostRPCAPIMajor: 3})
			if result != nil || !errors.Is(err, test.want) || strings.Contains(err.Error(), "driver") {
				t.Fatalf("result=%v error=%v", result, err)
			}
		})
	}
}

func TestBR_RUN_016_ResolverCannotReturnIncompatibleOrMixedDescriptor(t *testing.T) {
	for _, result := range []*domain.LaunchTargetDescriptor{
		unifiedFixture(t, domain.LaunchChannelTest),
		func() *domain.LaunchTargetDescriptor {
			value, err := domain.NewLaunchTargetDescriptor("018f0000-0000-7000-8000-000000000099", "018f0000-0000-7000-8000-000000000002", 7, domain.LaunchChannelStable, 3, "018f0000-0000-7000-8000-000000000003", "v1", "https://example.edu", 3, 4, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			return value
		}(),
	} {
		handler := NewResolveLaunchTarget(unifiedResolverFunc(func(context.Context, shared.ApplicationID, shared.AuthID, int32, []domain.CapabilityName) (*domain.LaunchTargetDescriptor, error) {
			return result, nil
		}))
		query := ResolveLaunchTargetQuery{ApplicationID: appID, HostRPCAPIMajor: 3}
		if result.Channel() == domain.LaunchChannelTest {
			query.HostCapabilities = nil
		}
		got, err := handler.Execute(context.Background(), shared.AuthenticatedUserIdentity{}, query)
		if got != nil || !errors.Is(err, domain.ErrApplicationRuntimeStateInconsistent) {
			t.Fatalf("got=%v error=%v", got, err)
		}
	}
}
