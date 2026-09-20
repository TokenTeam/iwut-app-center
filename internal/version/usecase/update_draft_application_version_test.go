package usecase

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/shared"
	"iwut-app-center/internal/version/domain"
	"iwut-app-center/internal/version/port"
)

type fakeUpdateVersionRepository struct {
	events           *[]string
	err              error
	result           *domain.ApplicationVersion
	returnNil        bool
	calls            int
	applicationID    shared.ApplicationID
	versionID        domain.ApplicationVersionID
	expectedAdminID  shared.AuthID
	expectedRevision int64
	replacement      domain.DraftApplicationVersionReplacement
	updatedAt        time.Time
}

func (fake *fakeUpdateVersionRepository) CreateDraft(
	context.Context,
	shared.AuthID,
	*domain.DraftApplicationVersion,
) (*domain.ApplicationVersion, error) {
	return nil, errors.New("CreateDraft is not configured on update fake")
}

func (fake *fakeUpdateVersionRepository) ReplaceDraft(
	_ context.Context,
	applicationID shared.ApplicationID,
	versionID domain.ApplicationVersionID,
	expectedAdminID shared.AuthID,
	expectedRevision int64,
	replacement domain.DraftApplicationVersionReplacement,
	updatedAt time.Time,
) (*domain.ApplicationVersion, error) {
	fake.calls++
	fake.applicationID = applicationID
	fake.versionID = versionID
	fake.expectedAdminID = expectedAdminID
	fake.expectedRevision = expectedRevision
	fake.replacement = replacement
	fake.updatedAt = updatedAt
	appendVersionEvent(fake.events, "repository")
	if fake.returnNil {
		return nil, nil
	}
	return fake.result, fake.err
}

func TestUpdateDraftApplicationVersion_BR_VER_010_011_012_013_014_015_016_017_Success(t *testing.T) {
	t.Parallel()
	events := make([]string, 0, 3)
	updatedAt := time.Date(2026, time.September, 20, 20, 0, 0, 123, time.FixedZone("CST", 8*60*60))
	result := usecaseVersion(t, 2, "auth-1", updatedAt.UTC())
	repository := &fakeUpdateVersionRepository{events: &events, result: result}
	catalog := &fakeScopeCatalog{events: &events}
	clock := &fakeVersionClock{events: &events, now: updatedAt}
	handler := NewUpdateDraftApplicationVersionHandler(catalog, clock, repository)

	version, err := handler.Handle(t.Context(), approvedIdentity(), usecaseApplicationID(t), testVersionID, validUpdateCommand())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if version != result {
		t.Fatal("Handle() did not return repository result")
	}
	if want := []string{"scope-catalog", "clock", "repository"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("dependency order = %v, want %v", events, want)
	}
	if want := []domain.ScopeName{"email.read", "profile.basic", "schedule.read"}; !reflect.DeepEqual(catalog.scopes, want) {
		t.Fatalf("catalog scopes = %v, want %v", catalog.scopes, want)
	}
	if repository.applicationID != usecaseApplicationID(t) || repository.versionID != testVersionID ||
		repository.expectedAdminID != approvedIdentity().AuthID || repository.expectedRevision != 1 {
		t.Fatal("repository did not receive trusted identity and path/concurrency preconditions")
	}
	if !repository.updatedAt.Equal(updatedAt) || repository.updatedAt.Location() != time.UTC {
		t.Fatalf("updatedAt = %v, want clock UTC instant", repository.updatedAt)
	}
	if repository.replacement.VersionLabel().String() != "v2.0.0" || repository.replacement.LaunchURL().String() != "http://localhost:3000/app" {
		t.Fatal("repository did not receive complete replacement")
	}
	if want := []domain.CapabilityName{"camera.read.v1", "user.profile.v2"}; !reflect.DeepEqual(repository.replacement.RequiredCapabilities(), want) {
		t.Fatalf("replacement capabilities = %v, want %v", repository.replacement.RequiredCapabilities(), want)
	}
}

func TestUpdateDraftApplicationVersion_BR_VER_013_ApprovedIdentityAndRevisionRequiredBeforeDependencies(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		identity DeveloperIdentity
		mutate   func(*UpdateDraftApplicationVersionCommand)
		want     error
	}{
		{name: "missing identity", identity: DeveloperIdentity{DeveloperStatus: DeveloperStatusApproved}, want: domain.ErrDeveloperIdentityRequired},
		{name: "pending", identity: DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusPending}, want: domain.ErrDeveloperApprovalRequired},
		{name: "missing revision", identity: approvedIdentity(), mutate: func(command *UpdateDraftApplicationVersionCommand) { command.ExpectedRevision = 0 }, want: domain.ErrApplicationVersionRevisionRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validUpdateCommand()
			if test.mutate != nil {
				test.mutate(&command)
			}
			catalog := &fakeScopeCatalog{}
			clock := &fakeVersionClock{now: time.Now()}
			repository := &fakeUpdateVersionRepository{}
			version, err := NewUpdateDraftApplicationVersionHandler(catalog, clock, repository).Handle(
				t.Context(), test.identity, usecaseApplicationID(t), testVersionID, command,
			)
			if version != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, test.want)
			}
			if catalog.calls != 0 || clock.calls != 0 || repository.calls != 0 {
				t.Fatal("dependency called before identity/revision validation completed")
			}
		})
	}
}

func TestUpdateDraftApplicationVersion_BR_VER_014_015_FieldValidationStopsBeforeCatalog(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*UpdateDraftApplicationVersionCommand)
		want   error
	}{
		{name: "invalid label", mutate: func(command *UpdateDraftApplicationVersionCommand) { command.VersionLabel = " v2" }, want: domain.ErrInvalidVersionLabel},
		{name: "invalid URL", mutate: func(command *UpdateDraftApplicationVersionCommand) { command.LaunchURL = "http://example.edu" }, want: domain.ErrInvalidApplicationLaunchURL},
		{name: "invalid RPC range", mutate: func(command *UpdateDraftApplicationVersionCommand) {
			command.RPCApiMaxVersionExclusive = command.RPCApiMinVersion
		}, want: domain.ErrInvalidRPCApiRange},
		{name: "duplicate capability", mutate: func(command *UpdateDraftApplicationVersionCommand) {
			command.RequiredCapabilities = []string{"camera.read.v1", "camera.read.v1"}
		}, want: domain.ErrInvalidRequiredCapability},
		{name: "duplicate scope", mutate: func(command *UpdateDraftApplicationVersionCommand) {
			command.RequiredScopes = []string{"profile.basic", "profile.basic"}
		}, want: domain.ErrInvalidApplicationScope},
		{name: "crossed scope", mutate: func(command *UpdateDraftApplicationVersionCommand) {
			command.OptionalScopes = []string{"profile.basic"}
		}, want: domain.ErrInvalidApplicationScope},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validUpdateCommand()
			test.mutate(&command)
			catalog := &fakeScopeCatalog{}
			clock := &fakeVersionClock{now: time.Now()}
			repository := &fakeUpdateVersionRepository{}
			version, err := NewUpdateDraftApplicationVersionHandler(catalog, clock, repository).Handle(
				t.Context(), approvedIdentity(), usecaseApplicationID(t), testVersionID, command,
			)
			if version != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, test.want)
			}
			if catalog.calls != 0 || clock.calls != 0 || repository.calls != 0 {
				t.Fatal("dependency called after local validation failed")
			}
		})
	}
}

func TestUpdateDraftApplicationVersion_BR_VER_015_CatalogFailureStopsBeforeClockAndRepository(t *testing.T) {
	t.Parallel()
	cause := errors.New("auth unavailable")
	tests := []struct {
		name       string
		catalogErr error
		want       error
	}{
		{name: "scope not requestable", catalogErr: port.ErrScopeNotRequestable, want: domain.ErrInvalidApplicationScope},
		{name: "catalog unavailable", catalogErr: errors.Join(port.ErrScopeCatalogUnavailable, cause), want: domain.ErrScopeCatalogUnavailable},
		{name: "unexpected failure", catalogErr: cause, want: domain.ErrInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := &fakeScopeCatalog{err: test.catalogErr}
			clock := &fakeVersionClock{now: time.Now()}
			repository := &fakeUpdateVersionRepository{}
			version, err := NewUpdateDraftApplicationVersionHandler(catalog, clock, repository).Handle(
				t.Context(), approvedIdentity(), usecaseApplicationID(t), testVersionID, validUpdateCommand(),
			)
			if version != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, test.want)
			}
			if clock.calls != 0 || repository.calls != 0 {
				t.Fatal("clock or repository called after catalog failure")
			}
		})
	}
}

func TestUpdateDraftApplicationVersion_BR_VER_010_013_016_RepositoryErrorsMapWithoutPartialResult(t *testing.T) {
	t.Parallel()
	cause := errors.New("database unavailable")
	tests := []struct {
		name          string
		repositoryErr error
		want          error
	}{
		{name: "not found", repositoryErr: port.ErrApplicationVersionNotFound, want: domain.ErrApplicationVersionNotFound},
		{name: "not admin", repositoryErr: port.ErrApplicationAdminRequired, want: domain.ErrApplicationAdminRequired},
		{name: "not draft", repositoryErr: port.ErrApplicationVersionNotDraft, want: domain.ErrApplicationVersionNotDraft},
		{name: "revision conflict", repositoryErr: port.ErrApplicationVersionRevisionConflict, want: domain.ErrApplicationVersionRevisionConflict},
		{name: "label conflict", repositoryErr: port.ErrApplicationVersionLabelAlreadyExists, want: domain.ErrApplicationVersionLabelAlreadyExists},
		{name: "infrastructure", repositoryErr: cause, want: domain.ErrInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeUpdateVersionRepository{err: test.repositoryErr}
			version, err := NewUpdateDraftApplicationVersionHandler(
				&fakeScopeCatalog{}, &fakeVersionClock{now: time.Now()}, repository,
			).Handle(t.Context(), approvedIdentity(), usecaseApplicationID(t), testVersionID, validUpdateCommand())
			if version != nil || !errors.Is(err, test.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, test.want)
			}
			if repository.calls != 1 {
				t.Fatalf("repository calls = %d, want one atomic call", repository.calls)
			}
			if test.repositoryErr == cause && !errors.Is(err, cause) {
				t.Fatalf("internal error did not retain cause: %v", err)
			}
		})
	}
}

func TestUpdateDraftApplicationVersion_BR_VER_011_CommandContainsOnlyCompleteEditableReplacement(t *testing.T) {
	t.Parallel()
	commandType := reflect.TypeOf(UpdateDraftApplicationVersionCommand{})
	wantFields := []string{
		"ExpectedRevision", "VersionLabel", "LaunchURL", "RPCApiMinVersion", "RPCApiMaxVersionExclusive",
		"RequiredCapabilities", "RequiredScopes", "OptionalScopes",
	}
	if commandType.NumField() != len(wantFields) {
		t.Fatalf("command has %d fields, want %d", commandType.NumField(), len(wantFields))
	}
	for index, want := range wantFields {
		if got := commandType.Field(index).Name; got != want {
			t.Fatalf("command field %d = %q, want %q", index, got, want)
		}
	}
}

func validUpdateCommand() UpdateDraftApplicationVersionCommand {
	return UpdateDraftApplicationVersionCommand{
		ExpectedRevision:          1,
		VersionLabel:              "v2.0.0",
		LaunchURL:                 "http://localhost:3000/app",
		RPCApiMinVersion:          2,
		RPCApiMaxVersionExclusive: 5,
		RequiredCapabilities:      []string{"user.profile.v2", "camera.read.v1"},
		RequiredScopes:            []string{"schedule.read", "profile.basic"},
		OptionalScopes:            []string{"email.read"},
	}
}

func usecaseApplicationID(t *testing.T) shared.ApplicationID {
	t.Helper()
	id, ok := shared.ParseApplicationID(testApplicationID)
	if !ok {
		t.Fatal("parse test application ID")
	}
	return id
}

func usecaseVersion(t *testing.T, revision int64, updatedBy shared.AuthID, updatedAt time.Time) *domain.ApplicationVersion {
	t.Helper()
	label, _ := domain.NewVersionLabel("v2.0.0")
	launchURL, _ := domain.NewLaunchURL("http://localhost:3000/app")
	rpcRange, _ := domain.NewRPCApiRange(2, 5)
	capabilities, _ := domain.NewCapabilitySet([]string{"camera.read.v1", "user.profile.v2"})
	scopes, _ := domain.NewScopeRequest([]string{"profile.basic", "schedule.read"}, []string{"email.read"})
	version, err := domain.RestoreApplicationVersion(
		testVersionID, usecaseApplicationID(t), 1, label, launchURL, rpcRange, capabilities, scopes,
		domain.ReviewStatusDraft, "auth-created", time.Date(2026, time.September, 19, 0, 0, 0, 0, time.UTC),
		revision, updatedBy, updatedAt,
	)
	if err != nil {
		t.Fatalf("restore use-case version: %v", err)
	}
	return version
}
