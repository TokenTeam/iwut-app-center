package transport

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	applicationversionv1 "iwut-app-center/api/gen/go/app_center/v1/application_version"
	"iwut-app-center/internal/shared"
	versiondomain "iwut-app-center/internal/version/domain"
	versionusecase "iwut-app-center/internal/version/usecase"
)

const testApplicationVersionID = "018f7777-7777-7777-8777-777777777777"

type fakeCreateApplicationVersionHandler struct {
	result   *versiondomain.ApplicationVersion
	err      error
	calls    int
	identity versionusecase.DeveloperIdentity
	command  versionusecase.CreateApplicationVersionCommand
}

type fakeUpdateDraftApplicationVersionHandler struct {
	result        *versiondomain.ApplicationVersion
	err           error
	calls         int
	identity      versionusecase.DeveloperIdentity
	applicationID shared.ApplicationID
	versionID     versiondomain.ApplicationVersionID
	command       versionusecase.UpdateDraftApplicationVersionCommand
}

func (handler *fakeUpdateDraftApplicationVersionHandler) Handle(
	_ context.Context,
	identity versionusecase.DeveloperIdentity,
	applicationID shared.ApplicationID,
	versionID versiondomain.ApplicationVersionID,
	command versionusecase.UpdateDraftApplicationVersionCommand,
) (*versiondomain.ApplicationVersion, error) {
	handler.calls++
	handler.identity = identity
	handler.applicationID = applicationID
	handler.versionID = versionID
	handler.command = command
	return handler.result, handler.err
}

func (handler *fakeCreateApplicationVersionHandler) Handle(
	_ context.Context,
	identity versionusecase.DeveloperIdentity,
	command versionusecase.CreateApplicationVersionCommand,
) (*versiondomain.ApplicationVersion, error) {
	handler.calls++
	handler.identity = identity
	handler.command = command
	return handler.result, handler.err
}

func newApplicationVersion(t *testing.T, adminID string, createdAt time.Time) *versiondomain.ApplicationVersion {
	t.Helper()
	applicationID, ok := shared.ParseApplicationID(testApplicationID)
	if !ok {
		t.Fatal("test application ID is invalid")
	}
	label, _ := versiondomain.NewVersionLabel("v1.0.0")
	launchURL, _ := versiondomain.NewLaunchURL("https://example.edu/app")
	rpcRange, _ := versiondomain.NewRPCApiRange(3, 5)
	capabilities, _ := versiondomain.NewCapabilitySet([]string{"user.profile.v1"})
	scopes, _ := versiondomain.NewScopeRequest([]string{"profile.basic"}, []string{"schedule.read"})
	draft, err := versiondomain.NewDraftApplicationVersion(
		versiondomain.ApplicationVersionID(testApplicationVersionID),
		applicationID,
		label,
		launchURL,
		rpcRange,
		capabilities,
		scopes,
		shared.AuthID(adminID),
		createdAt,
	)
	if err != nil {
		t.Fatalf("NewDraftApplicationVersion() error = %v", err)
	}
	sequence, _ := versiondomain.NewVersionSequence(1)
	version, err := versiondomain.NewApplicationVersion(draft, sequence)
	if err != nil {
		t.Fatalf("NewApplicationVersion() error = %v", err)
	}
	return version
}

func updatedApplicationVersion(t *testing.T, adminID string, createdAt time.Time) *versiondomain.ApplicationVersion {
	t.Helper()
	version := newApplicationVersion(t, adminID, createdAt)
	label, _ := versiondomain.NewVersionLabel("v1.1.0")
	launchURL, _ := versiondomain.NewLaunchURL("http://192.168.1.20:8081/")
	rpcRange, _ := versiondomain.NewRPCApiRange(3, 6)
	capabilities, _ := versiondomain.NewCapabilitySet([]string{"camera.read.v1", "user.profile.v1"})
	scopes, _ := versiondomain.NewScopeRequest([]string{"profile.basic"}, []string{"schedule.read"})
	replacement, _ := versiondomain.NewDraftApplicationVersionReplacement(label, launchURL, rpcRange, capabilities, scopes)
	updated, err := version.ReplaceDraft(1, replacement, shared.AuthID(adminID), createdAt.Add(time.Minute))
	if err != nil {
		t.Fatalf("ReplaceDraft() error = %v", err)
	}
	return updated
}

func versionRequest() *applicationversionv1.CreateApplicationVersionRequest {
	return &applicationversionv1.CreateApplicationVersionRequest{
		ApplicationId:             testApplicationID,
		VersionLabel:              "v1.0.0",
		LaunchUrl:                 "https://example.edu/app",
		RpcApiMinVersion:          3,
		RpcApiMaxVersionExclusive: 5,
		RequiredCapabilities:      []string{"user.profile.v1"},
		RequiredScopes:            []string{"profile.basic"},
		OptionalScopes:            []string{"schedule.read"},
	}
}

func TestApplicationVersionService_UCAPP002_MapsIdentityCommandAndCompleteResponse(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 21, 13, 0, 0, 0, time.UTC)
	handler := &fakeCreateApplicationVersionHandler{result: newApplicationVersion(t, "auth-123", createdAt)}
	service := NewApplicationVersionService(handler, nil)
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: "APPROVED"})

	response, err := service.CreateApplicationVersion(ctx, versionRequest())
	if err != nil {
		t.Fatalf("CreateApplicationVersion() error = %v", err)
	}
	if handler.calls != 1 || handler.identity.AuthID != "auth-123" || handler.command.ApplicationID != testApplicationID {
		t.Fatalf("handler input = calls:%d identity:%#v command:%#v", handler.calls, handler.identity, handler.command)
	}
	if response.GetVersionId() != testApplicationVersionID || response.GetSequence() != 1 || response.GetReviewStatus() != "DRAFT" || response.GetRevision() != 1 {
		t.Fatalf("response identity/state = %v", response)
	}
	if response.GetCreatedBy() != "auth-123" || response.GetUpdatedBy() != "auth-123" || !response.GetCreatedAt().AsTime().Equal(response.GetUpdatedAt().AsTime()) {
		t.Fatalf("response audit = %v", response)
	}
}

func TestApplicationVersionService_UCAPP002_ErrorMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		code   codes.Code
		reason string
	}{
		{name: "invalid application ID", err: versiondomain.ErrInvalidApplicationID, code: codes.InvalidArgument, reason: ReasonInvalidApplicationID},
		{name: "invalid label", err: versiondomain.ErrInvalidVersionLabel, code: codes.InvalidArgument, reason: ReasonInvalidVersionLabel},
		{name: "invalid URL", err: versiondomain.ErrInvalidApplicationLaunchURL, code: codes.InvalidArgument, reason: ReasonInvalidApplicationLaunchURL},
		{name: "invalid RPC range", err: versiondomain.ErrInvalidRPCApiRange, code: codes.InvalidArgument, reason: ReasonInvalidRPCApiRange},
		{name: "invalid capability", err: versiondomain.ErrInvalidRequiredCapability, code: codes.InvalidArgument, reason: ReasonInvalidRequiredCapability},
		{name: "invalid scope", err: versiondomain.ErrInvalidApplicationScope, code: codes.InvalidArgument, reason: ReasonInvalidApplicationScope},
		{name: "approval required", err: versiondomain.ErrDeveloperApprovalRequired, code: codes.PermissionDenied, reason: ReasonDeveloperApprovalRequired},
		{name: "application missing", err: versiondomain.ErrApplicationNotFound, code: codes.NotFound, reason: ReasonApplicationNotFound},
		{name: "administrator required", err: versiondomain.ErrApplicationAdminRequired, code: codes.PermissionDenied, reason: ReasonApplicationAdminRequired},
		{name: "label conflict", err: versiondomain.ErrApplicationVersionLabelAlreadyExists, code: codes.AlreadyExists, reason: ReasonApplicationVersionLabelExists},
		{name: "catalog unavailable", err: versiondomain.NewScopeCatalogUnavailableError(errors.New("auth.internal:9000 secret")), code: codes.Unavailable, reason: ReasonScopeCatalogUnavailable},
	}
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: "APPROVED"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := NewApplicationVersionService(&fakeCreateApplicationVersionHandler{err: test.err}, nil)
			_, err := service.CreateApplicationVersion(ctx, versionRequest())
			current := status.Convert(err)
			if current.Code() != test.code || errorReason(current) != test.reason {
				t.Fatalf("status = (%v, %q, %q)", current.Code(), current.Message(), errorReason(current))
			}
			if current.Message() == "auth.internal:9000 secret" {
				t.Fatal("transport leaked dependency cause")
			}
		})
	}
}

func TestApplicationVersionService_UCAPP002_NilResultFailsInternal(t *testing.T) {
	t.Parallel()
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: "APPROVED"})
	service := NewApplicationVersionService(&fakeCreateApplicationVersionHandler{}, nil)
	_, err := service.CreateApplicationVersion(ctx, versionRequest())
	if current := status.Convert(err); current.Code() != codes.Internal || errorReason(current) != ReasonInternal {
		t.Fatalf("status = (%v, %q)", current.Code(), errorReason(current))
	}
}

func updateVersionRequest() *applicationversionv1.UpdateApplicationVersionRequest {
	return &applicationversionv1.UpdateApplicationVersionRequest{
		ApplicationId:    testApplicationID,
		VersionId:        testApplicationVersionID,
		ExpectedRevision: 1,
		Replacement: &applicationversionv1.DraftApplicationVersionReplacement{
			VersionLabel:              "v1.1.0",
			LaunchUrl:                 "http://192.168.1.20:8081/",
			RpcApiMinVersion:          3,
			RpcApiMaxVersionExclusive: 6,
			RequiredCapabilities:      []string{"user.profile.v1", "camera.read.v1"},
			RequiredScopes:            []string{"profile.basic"},
			OptionalScopes:            []string{"schedule.read"},
		},
	}
}

func TestApplicationVersionService_UCAPP003_MapsGRPCRevisionReplacementAndCompleteResponse(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.September, 21, 13, 0, 0, 0, time.UTC)
	handler := &fakeUpdateDraftApplicationVersionHandler{result: updatedApplicationVersion(t, "auth-123", createdAt)}
	service := NewApplicationVersionService(nil, handler)
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: "APPROVED"})

	response, err := service.UpdateApplicationVersion(ctx, updateVersionRequest())
	if err != nil {
		t.Fatalf("UpdateApplicationVersion() error = %v", err)
	}
	if handler.calls != 1 || handler.command.ExpectedRevision != 1 || handler.applicationID.String() != testApplicationID || handler.versionID.String() != testApplicationVersionID {
		t.Fatalf("handler input = calls:%d application:%s version:%s command:%#v", handler.calls, handler.applicationID, handler.versionID, handler.command)
	}
	if response.GetRevision() != 2 || response.GetVersionLabel() != "v1.1.0" || response.GetUpdatedBy() != "auth-123" {
		t.Fatalf("response = %v", response)
	}
}

func TestApplicationVersionService_UCAPP003_ErrorMappings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		err    error
		code   codes.Code
		reason string
	}{
		{name: "revision required", err: versiondomain.ErrApplicationVersionRevisionRequired, code: codes.InvalidArgument, reason: ReasonApplicationVersionRevisionRequired},
		{name: "version missing", err: versiondomain.ErrApplicationVersionNotFound, code: codes.NotFound, reason: ReasonApplicationVersionNotFound},
		{name: "not draft", err: versiondomain.ErrApplicationVersionNotDraft, code: codes.Aborted, reason: ReasonApplicationVersionNotDraft},
		{name: "revision conflict", err: versiondomain.ErrApplicationVersionRevisionConflict, code: codes.Aborted, reason: ReasonApplicationVersionRevisionConflict},
	}
	ctx := withDeveloperIdentity(context.Background(), shared.DeveloperIdentity{AuthID: "auth-123", DeveloperStatus: "APPROVED"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := NewApplicationVersionService(nil, &fakeUpdateDraftApplicationVersionHandler{err: test.err})
			_, err := service.UpdateApplicationVersion(ctx, updateVersionRequest())
			current := status.Convert(err)
			if current.Code() != test.code || errorReason(current) != test.reason {
				t.Fatalf("status = (%v, %q)", current.Code(), errorReason(current))
			}
		})
	}
}

func errorReason(current *status.Status) string {
	for _, detail := range current.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}
	return ""
}
