package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	applicationv1 "iwut-app-center/api/gen/go/app_center/v1/application"
	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/usecase"
	"iwut-app-center/internal/shared"
)

const testApplicationID = "01890a5d-ac96-774b-bcce-b302099a8057"

type fakeCreateApplicationHandler struct {
	calls    int
	identity usecase.DeveloperIdentity
	command  usecase.CreateApplicationCommand
	result   *domain.Application
	err      error
}

func (handler *fakeCreateApplicationHandler) Handle(
	_ context.Context,
	identity usecase.DeveloperIdentity,
	command usecase.CreateApplicationCommand,
) (*domain.Application, error) {
	handler.calls++
	handler.identity = identity
	handler.command = command
	return handler.result, handler.err
}

func newApplication(t *testing.T, adminID shared.AuthID, createdAt time.Time) *domain.Application {
	t.Helper()
	name, err := domain.NewApplicationName("Course_Table")
	if err != nil {
		t.Fatalf("NewApplicationName() error = %v", err)
	}
	application, err := domain.NewApplication(domain.ApplicationID(testApplicationID), name, adminID, createdAt)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	return application
}

func contextWithIdentity(identity shared.DeveloperIdentity) context.Context {
	return withDeveloperIdentity(context.Background(), identity)
}

func TestApplicationService_CreateApplicationMapsSuccess(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	identity := shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved}
	handler := &fakeCreateApplicationHandler{result: newApplication(t, identity.AuthID, createdAt)}
	service := NewApplicationService(handler)

	response, err := service.CreateApplication(
		contextWithIdentity(identity),
		&applicationv1.CreateApplicationRequest{Name: "Course_Table"},
	)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}
	if handler.calls != 1 {
		t.Fatalf("handler calls = %d, want 1", handler.calls)
	}
	if handler.identity.AuthID != identity.AuthID || handler.identity.DeveloperStatus != shared.DeveloperStatusApproved {
		t.Fatalf("handler identity = %#v", handler.identity)
	}
	if handler.command.Name != "Course_Table" {
		t.Fatalf("handler command = %#v", handler.command)
	}
	if response.GetId() != testApplicationID {
		t.Fatalf("response id = %q", response.GetId())
	}
	if response.GetName() != "Course_Table" {
		t.Fatalf("response name = %q", response.GetName())
	}
	if response.GetAdminId() != "auth-123" {
		t.Fatalf("response adminId = %q", response.GetAdminId())
	}
	if !response.GetCreatedAt().AsTime().Equal(createdAt) {
		t.Fatalf("response createdAt = %v, want %v", response.GetCreatedAt().AsTime(), createdAt)
	}
}

func TestApplicationService_RequiresVerifiedIdentityInContext(t *testing.T) {
	t.Parallel()

	handler := &fakeCreateApplicationHandler{result: newApplication(t, "auth-123", time.Now())}
	service := NewApplicationService(handler)

	_, err := service.CreateApplication(context.Background(), &applicationv1.CreateApplicationRequest{Name: "x"})
	assertTransportError(t, err, codes.Unauthenticated, ReasonDeveloperIdentityRequired)
	if handler.calls != 0 {
		t.Fatalf("handler calls = %d, want 0", handler.calls)
	}
}

func TestApplicationService_MapsStableDomainErrors(t *testing.T) {
	t.Parallel()

	identity := shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved}
	testCases := []struct {
		name       string
		domainErr  error
		wantCode   codes.Code
		wantReason string
	}{
		{name: "invalid name", domainErr: domain.ErrInvalidApplicationName, wantCode: codes.InvalidArgument, wantReason: ReasonInvalidApplicationName},
		{name: "approval required", domainErr: domain.ErrDeveloperApprovalRequired, wantCode: codes.PermissionDenied, wantReason: ReasonDeveloperApprovalRequired},
		{name: "name conflict", domainErr: domain.ErrApplicationNameAlreadyExists, wantCode: codes.AlreadyExists, wantReason: ReasonApplicationNameAlreadyExists},
		{name: "quota exhausted", domainErr: domain.ErrApplicationQuotaExceeded, wantCode: codes.ResourceExhausted, wantReason: ReasonApplicationQuotaExceeded},
		{name: "identity required", domainErr: domain.ErrDeveloperIdentityRequired, wantCode: codes.Unauthenticated, wantReason: ReasonDeveloperIdentityRequired},
		{name: "internal", domainErr: domain.NewInternalError(errors.New("database down")), wantCode: codes.Internal, wantReason: ReasonInternal},
		{name: "unknown", domainErr: errors.New("boom"), wantCode: codes.Internal, wantReason: ReasonInternal},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			service := NewApplicationService(&fakeCreateApplicationHandler{err: testCase.domainErr})
			_, err := service.CreateApplication(
				contextWithIdentity(identity),
				&applicationv1.CreateApplicationRequest{Name: "x"},
			)
			assertTransportError(t, err, testCase.wantCode, testCase.wantReason)
		})
	}
}

func TestApplicationService_NilHandlerFailsInternal(t *testing.T) {
	t.Parallel()

	service := NewApplicationService(nil)
	_, err := service.CreateApplication(
		contextWithIdentity(shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: shared.DeveloperStatusApproved}),
		&applicationv1.CreateApplicationRequest{Name: "x"},
	)
	assertTransportError(t, err, codes.Internal, ReasonInternal)
}

func TestApplicationService_IdentityCannotBeOverriddenByBody(t *testing.T) {
	t.Parallel()

	identity := shared.DeveloperIdentity{AuthID: "trusted-admin", DeveloperStatus: shared.DeveloperStatusApproved}
	handler := &fakeCreateApplicationHandler{result: newApplication(t, identity.AuthID, time.Now())}
	service := NewApplicationService(handler)

	request := &applicationv1.CreateApplicationRequest{Name: "Course_Table"}
	response, err := service.CreateApplication(contextWithIdentity(identity), request)
	if err != nil {
		t.Fatalf("CreateApplication() error = %v", err)
	}
	if handler.identity.AuthID != "trusted-admin" {
		t.Fatalf("handler identity = %q, want trusted-admin", handler.identity.AuthID)
	}
	if response.GetAdminId() != "trusted-admin" {
		t.Fatalf("response adminId = %q, want trusted-admin", response.GetAdminId())
	}
}
