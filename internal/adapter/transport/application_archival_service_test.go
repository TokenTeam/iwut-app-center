package transport

import (
	"context"
	"errors"
	"testing"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_archival"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

type archivalHandlerFake struct {
	state    domain.ApplicationArchiveState
	err      error
	target   domain.ApplicationArchiveStatus
	expected int64
}

func (f *archivalHandlerFake) Get(context.Context, shared.AuthenticatedUserIdentity, shared.ApplicationID) (domain.ApplicationArchiveState, error) {
	return f.state, f.err
}
func (f *archivalHandlerFake) Archive(_ context.Context, _ shared.AuthenticatedUserIdentity, command usecase.ApplicationArchiveCommand) (domain.ApplicationArchiveState, error) {
	f.target, f.expected = domain.ApplicationArchiveArchived, command.ExpectedArchiveRevision
	return f.state, f.err
}
func (f *archivalHandlerFake) Unarchive(_ context.Context, _ shared.AuthenticatedUserIdentity, command usecase.ApplicationArchiveCommand) (domain.ApplicationArchiveState, error) {
	f.target, f.expected = domain.ApplicationArchiveUnarchived, command.ExpectedArchiveRevision
	return f.state, f.err
}

func TestApplicationArchivalService_BR_APP_051_052_058_MapsResourceAndErrors(t *testing.T) {
	appID, _ := shared.ParseApplicationID("0199b33c-d043-7abc-8abc-123456789012")
	fake := &archivalHandlerFake{state: domain.ApplicationArchiveState{ApplicationID: appID, LifecycleStatus: domain.ApplicationLifecycleActive, PlatformAvailabilityStatus: domain.PlatformAvailabilitySuspended, ArchiveStatus: domain.ApplicationArchiveArchived, ArchiveRevision: 2}}
	service := NewApplicationArchivalService(fake)
	ctx := withTrustedIdentity(t.Context(), shared.TrustedIdentity{AuthID: "admin"})
	response, err := service.ArchiveApplication(ctx, &pb.ArchiveApplicationRequest{ApplicationId: appID.String(), Command: &pb.ApplicationArchiveCommand{ExpectedArchiveRevision: 2}})
	if err != nil || response.GetState().GetArchiveStatus() != pb.ApplicationArchiveStatus_APPLICATION_ARCHIVE_STATUS_ARCHIVED || response.GetState().GetPlatformAvailabilityStatus() != pb.PlatformAvailabilityStatus_PLATFORM_AVAILABILITY_STATUS_SUSPENDED || fake.expected != 2 {
		t.Fatalf("response=%#v fake=%#v error=%v", response, fake, err)
	}

	for source, want := range map[error]codes.Code{
		domain.ErrInvalidApplicationArchiveRequest: codes.InvalidArgument,
		domain.ErrApplicationArchiveNotFound:       codes.NotFound,
		domain.ErrApplicationArchiveConflict:       codes.Aborted,
		domain.ErrApplicationNotActive:             codes.FailedPrecondition,
		domain.ErrApplicationArchiveInconsistent:   codes.Internal,
		domain.ErrApplicationArchiveUnavailable:    codes.Unavailable,
	} {
		if got := status.Code(archivalError(source)); got != want {
			t.Fatalf("source=%v code=%s want=%s", source, got, want)
		}
	}
	if _, err = service.GetApplicationArchiveState(t.Context(), &pb.GetApplicationArchiveStateRequest{ApplicationId: appID.String()}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing identity error=%v", err)
	}
	fake.err = errors.New("secret storage detail")
	if _, err = service.UnarchiveApplication(ctx, &pb.UnarchiveApplicationRequest{ApplicationId: appID.String(), Command: &pb.ApplicationArchiveCommand{ExpectedArchiveRevision: 2}}); status.Code(err) != codes.Internal || status.Convert(err).Message() == fake.err.Error() {
		t.Fatalf("unknown error leaked: %v", err)
	}
}
