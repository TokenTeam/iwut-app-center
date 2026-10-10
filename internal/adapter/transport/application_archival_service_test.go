package transport

import (
	"context"
	"errors"
	"net"
	"testing"

	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/application_archival"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

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

func archivalServers(t *testing.T, handler ApplicationArchivalHandlers) *Servers {
	t.Helper()
	servers, err := NewServers(
		ServerConfig{}, newTestVerifier(t), NewApplicationService(nil), NewApplicationVersionService(nil, nil),
		NewApplicationReviewService(nil, nil, nil), NewApplicationPublicationService(nil), NewTesterJoinLinkService(nil, nil),
		NewTesterMembershipService(nil, nil), NewCatalogService(nil), NewApplicationProfileRevisionService(nil, nil),
		NewApplicationProfileReviewService(nil, nil), NewOAuthClientService(nil), NewApplicationArchivalService(handler),
	)
	if err != nil {
		t.Fatal(err)
	}
	return servers
}

func archivalGRPCClient(t *testing.T, handler ApplicationArchivalHandlers) pb.ApplicationArchivalServiceClient {
	t.Helper()
	servers := archivalServers(t, handler)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = servers.GRPC.Server.Serve(listener) }()
	t.Cleanup(servers.GRPC.Server.Stop)
	connection, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return pb.NewApplicationArchivalServiceClient(connection)
}

func assertPrivateNoStoreMetadata(t *testing.T, headers metadata.MD) {
	t.Helper()
	values := headers.Get("cache-control")
	if len(values) != 1 || values[0] != "private, no-store" {
		t.Fatalf("Cache-Control metadata=%q", values)
	}
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

func TestApplicationArchivalService_BR_APP_058_GRPCSuccessAndErrorsArePrivateNoStore(t *testing.T) {
	appID, _ := shared.ParseApplicationID("0199b33c-d043-7abc-8abc-123456789012")
	fake := &archivalHandlerFake{state: domain.ApplicationArchiveState{ApplicationID: appID, LifecycleStatus: domain.ApplicationLifecycleActive, PlatformAvailabilityStatus: domain.PlatformAvailabilityAvailable, ArchiveStatus: domain.ApplicationArchiveUnarchived, ArchiveRevision: 1}}
	client := archivalGRPCClient(t, fake)
	identityContext := metadata.NewOutgoingContext(t.Context(), metadata.Pairs(IdentityHeader, ordinaryUserToken(t)))

	var successHeaders metadata.MD
	response, err := client.GetApplicationArchiveState(identityContext, &pb.GetApplicationArchiveStateRequest{ApplicationId: appID.String()}, grpc.Header(&successHeaders))
	if err != nil || response.GetState().GetArchiveRevision() != 1 {
		t.Fatalf("GetApplicationArchiveState()=(%v,%v)", response, err)
	}
	assertPrivateNoStoreMetadata(t, successHeaders)

	fake.err = domain.ErrApplicationArchiveNotFound
	var domainErrorHeaders metadata.MD
	_, err = client.ArchiveApplication(identityContext, &pb.ArchiveApplicationRequest{ApplicationId: appID.String(), Command: &pb.ApplicationArchiveCommand{ExpectedArchiveRevision: 1}}, grpc.Header(&domainErrorHeaders))
	if status.Code(err) != codes.NotFound {
		t.Fatalf("ArchiveApplication() error=%v", err)
	}
	assertPrivateNoStoreMetadata(t, domainErrorHeaders)

	var identityErrorHeaders metadata.MD
	_, err = client.UnarchiveApplication(t.Context(), &pb.UnarchiveApplicationRequest{ApplicationId: appID.String(), Command: &pb.ApplicationArchiveCommand{ExpectedArchiveRevision: 1}}, grpc.Header(&identityErrorHeaders))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("UnarchiveApplication() error=%v", err)
	}
	assertPrivateNoStoreMetadata(t, identityErrorHeaders)
}
