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

const (
	testApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"
	testVersionID     = domain.ApplicationVersionID("01890f5a-e810-7cc3-98c8-8c6d5d8b4c22")
)

type fakeScopeCatalog struct {
	events   *[]string
	revision port.ScopeCatalogRevision
	err      error
	calls    int
	scopes   []domain.ScopeName
}

func (fake *fakeScopeCatalog) EnsureAllRequestable(_ context.Context, scopes []domain.ScopeName) (port.ScopeCatalogRevision, error) {
	fake.calls++
	fake.scopes = append([]domain.ScopeName{}, scopes...)
	appendVersionEvent(fake.events, "scope-catalog")
	return fake.revision, fake.err
}

type fakeOAuthRedirectPolicy struct{ err error }

func (fake *fakeOAuthRedirectPolicy) EnsureCanonical(_, _ []string) error { return fake.err }

type fakeVersionIDGenerator struct {
	events *[]string
	id     domain.ApplicationVersionID
	err    error
	calls  int
}

func (fake *fakeVersionIDGenerator) NewUUIDv7() (domain.ApplicationVersionID, error) {
	fake.calls++
	appendVersionEvent(fake.events, "id")
	return fake.id, fake.err
}

type fakeVersionClock struct {
	events *[]string
	now    time.Time
	calls  int
}

func (fake *fakeVersionClock) Now() time.Time {
	fake.calls++
	appendVersionEvent(fake.events, "clock")
	return fake.now
}

type fakeVersionRepository struct {
	events          *[]string
	err             error
	result          *domain.ApplicationVersion
	returnNil       bool
	calls           int
	expectedAdminID shared.AuthID
	draft           *domain.DraftApplicationVersion
}

func (fake *fakeVersionRepository) CreateDraft(
	_ context.Context,
	expectedAdminID shared.AuthID,
	draft *domain.DraftApplicationVersion,
) (*domain.ApplicationVersion, error) {
	fake.calls++
	fake.expectedAdminID = expectedAdminID
	fake.draft = draft
	appendVersionEvent(fake.events, "repository")
	if fake.returnNil {
		return nil, nil
	}
	if fake.err != nil || fake.result != nil {
		return fake.result, fake.err
	}
	return domain.NewApplicationVersion(draft, 1)
}

func (fake *fakeVersionRepository) ReplaceDraft(
	context.Context,
	shared.ApplicationID,
	domain.ApplicationVersionID,
	shared.AuthID,
	int64,
	domain.DraftApplicationVersionReplacement,
	time.Time,
) (*domain.ApplicationVersion, error) {
	return nil, errors.New("ReplaceDraft is not configured on create fake")
}

func appendVersionEvent(events *[]string, event string) {
	if events != nil {
		*events = append(*events, event)
	}
}

func validCommand() CreateApplicationVersionCommand {
	return CreateApplicationVersionCommand{
		ApplicationID:             testApplicationID,
		VersionLabel:              "v1.0.0",
		LaunchURL:                 "https://example.edu/app",
		RPCApiMinVersion:          2,
		RPCApiMaxVersionExclusive: 4,
		RequiredCapabilities:      []string{"user.profile.v1", "camera.read.v1"},
		RequiredScopes:            []string{"profile.basic"},
		OptionalScopes:            []string{"schedule.read"},
	}
}

func approvedIdentity() DeveloperIdentity {
	return DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved}
}

func TestCreateApplicationVersion_BR_VER_001_002_003_004_005_006_007_008_009_Success(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 4)
	createdAt := time.Date(2026, time.September, 19, 20, 0, 0, 123, time.FixedZone("CST", 8*60*60))
	scopeCatalog := &fakeScopeCatalog{events: &events, revision: 19}
	idGenerator := &fakeVersionIDGenerator{events: &events, id: testVersionID}
	clock := &fakeVersionClock{events: &events, now: createdAt}
	repository := &fakeVersionRepository{events: &events}
	handler := NewCreateApplicationVersionHandler(scopeCatalog, &fakeOAuthRedirectPolicy{}, idGenerator, clock, repository)

	version, err := handler.Handle(context.Background(), approvedIdentity(), validCommand())
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if version == nil || repository.draft == nil {
		t.Fatal("Handle() returned no version or repository received no draft")
	}
	if want := []string{"scope-catalog", "id", "clock", "repository"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("dependency order = %v, want %v", events, want)
	}
	if want := []domain.ScopeName{"profile.basic", "schedule.read"}; !reflect.DeepEqual(scopeCatalog.scopes, want) {
		t.Fatalf("catalog scopes = %v, want globally sorted %v", scopeCatalog.scopes, want)
	}
	if repository.expectedAdminID != approvedIdentity().AuthID {
		t.Fatalf("expected admin = %q, want trusted auth ID", repository.expectedAdminID)
	}
	if version.ID() != testVersionID || version.ApplicationID().String() != testApplicationID || version.Sequence() != 1 {
		t.Fatalf("version identity = (%s, %s, %d), want generated IDs and repository sequence", version.ID(), version.ApplicationID(), version.Sequence())
	}
	if version.VersionLabel().String() != "v1.0.0" || version.LaunchURL().String() != "https://example.edu/app" {
		t.Fatalf("version content = (%q, %q), want command content", version.VersionLabel(), version.LaunchURL())
	}
	if want := []domain.CapabilityName{"camera.read.v1", "user.profile.v1"}; !reflect.DeepEqual(version.RequiredCapabilities(), want) {
		t.Fatalf("capabilities = %v, want sorted %v", version.RequiredCapabilities(), want)
	}
	if version.ReviewStatus() != domain.ReviewStatusDraft || version.Revision() != 1 || version.CreatedBy() != "auth-1" || version.UpdatedBy() != "auth-1" {
		t.Fatalf("server lifecycle/audit fields are incorrect: status=%s revision=%d createdBy=%s updatedBy=%s", version.ReviewStatus(), version.Revision(), version.CreatedBy(), version.UpdatedBy())
	}
	if !version.CreatedAt().Equal(createdAt) || !version.UpdatedAt().Equal(version.CreatedAt()) || version.CreatedAt().Location() != time.UTC {
		t.Fatalf("audit time = (%v, %v), want generated UTC instant", version.CreatedAt(), version.UpdatedAt())
	}
}

func TestCreateApplicationVersion_BR_VER_002_OnlyApprovedIdentityReachesValidationOrDependencies(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		identity DeveloperIdentity
		want     error
	}{
		{name: "missing identity", identity: DeveloperIdentity{DeveloperStatus: DeveloperStatusApproved}, want: domain.ErrDeveloperIdentityRequired},
		{name: "pending", identity: DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusPending}, want: domain.ErrDeveloperApprovalRequired},
		{name: "rejected", identity: DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusRejected}, want: domain.ErrDeveloperApprovalRequired},
		{name: "suspended", identity: DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusSuspended}, want: domain.ErrDeveloperApprovalRequired},
		{name: "unknown", identity: DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: "UNKNOWN"}, want: domain.ErrDeveloperApprovalRequired},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			scopeCatalog := &fakeScopeCatalog{}
			idGenerator := &fakeVersionIDGenerator{id: testVersionID}
			clock := &fakeVersionClock{now: time.Now()}
			repository := &fakeVersionRepository{}
			handler := NewCreateApplicationVersionHandler(scopeCatalog, &fakeOAuthRedirectPolicy{}, idGenerator, clock, repository)
			version, err := handler.Handle(context.Background(), testCase.identity, validCommand())
			if version != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, testCase.want)
			}
			if scopeCatalog.calls != 0 || idGenerator.calls != 0 || clock.calls != 0 || repository.calls != 0 {
				t.Fatal("dependency called for unauthorized identity")
			}
		})
	}
}

func TestCreateApplicationVersion_BR_VER_003_004_005_006_007_LocalValidationStopsBeforeCatalog(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		mutate func(*CreateApplicationVersionCommand)
		want   error
	}{
		{name: "invalid application ID", mutate: func(command *CreateApplicationVersionCommand) { command.ApplicationID = "bad" }, want: domain.ErrInvalidApplicationID},
		{name: "invalid label", mutate: func(command *CreateApplicationVersionCommand) { command.VersionLabel = " v1" }, want: domain.ErrInvalidVersionLabel},
		{name: "invalid launch URL", mutate: func(command *CreateApplicationVersionCommand) { command.LaunchURL = "http://example.edu" }, want: domain.ErrInvalidApplicationLaunchURL},
		{name: "invalid RPC range", mutate: func(command *CreateApplicationVersionCommand) {
			command.RPCApiMaxVersionExclusive = command.RPCApiMinVersion
		}, want: domain.ErrInvalidRPCApiRange},
		{name: "invalid capability", mutate: func(command *CreateApplicationVersionCommand) {
			command.RequiredCapabilities = []string{"Camera.read.v1"}
		}, want: domain.ErrInvalidRequiredCapability},
		{name: "duplicate scope", mutate: func(command *CreateApplicationVersionCommand) {
			command.RequiredScopes = []string{"profile.basic", "profile.basic"}
		}, want: domain.ErrInvalidApplicationScope},
		{name: "cross-list scope", mutate: func(command *CreateApplicationVersionCommand) { command.OptionalScopes = []string{"profile.basic"} }, want: domain.ErrInvalidApplicationScope},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			command := validCommand()
			testCase.mutate(&command)
			scopeCatalog := &fakeScopeCatalog{}
			idGenerator := &fakeVersionIDGenerator{id: testVersionID}
			clock := &fakeVersionClock{now: time.Now()}
			repository := &fakeVersionRepository{}
			handler := NewCreateApplicationVersionHandler(scopeCatalog, &fakeOAuthRedirectPolicy{}, idGenerator, clock, repository)
			version, err := handler.Handle(context.Background(), approvedIdentity(), command)
			if version != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, testCase.want)
			}
			if scopeCatalog.calls != 0 || idGenerator.calls != 0 || clock.calls != 0 || repository.calls != 0 {
				t.Fatal("dependency called after local validation failed")
			}
		})
	}
}

func TestCreateApplicationVersion_BR_VER_007_CatalogOutcomesFailClosedBeforeSystemFields(t *testing.T) {
	t.Parallel()

	unavailableCause := errors.New("auth connection failed")
	testCases := []struct {
		name       string
		catalogErr error
		want       error
		category   domain.ErrorCategory
		cause      error
	}{
		{name: "unknown scope", catalogErr: port.ErrScopeNotRequestable, want: domain.ErrInvalidApplicationScope, category: domain.ErrorCategoryValidation},
		{name: "catalog unavailable", catalogErr: errors.Join(port.ErrScopeCatalogUnavailable, unavailableCause), want: domain.ErrScopeCatalogUnavailable, category: domain.ErrorCategoryDependencyUnavailable, cause: unavailableCause},
		{name: "unexpected catalog failure", catalogErr: unavailableCause, want: domain.ErrInternal, category: domain.ErrorCategoryInternal, cause: unavailableCause},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			scopeCatalog := &fakeScopeCatalog{err: testCase.catalogErr}
			idGenerator := &fakeVersionIDGenerator{id: testVersionID}
			clock := &fakeVersionClock{now: time.Now()}
			repository := &fakeVersionRepository{}
			handler := NewCreateApplicationVersionHandler(scopeCatalog, &fakeOAuthRedirectPolicy{}, idGenerator, clock, repository)
			version, err := handler.Handle(context.Background(), approvedIdentity(), validCommand())
			if version != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, testCase.want)
			}
			if got := versionErrorCategory(t, err); got != testCase.category {
				t.Fatalf("error category = %s, want %s", got, testCase.category)
			}
			if testCase.cause != nil && !errors.Is(err, testCase.cause) {
				t.Fatalf("error %v does not preserve cause %v", err, testCase.cause)
			}
			if scopeCatalog.calls != 1 || idGenerator.calls != 0 || clock.calls != 0 || repository.calls != 0 {
				t.Fatalf("calls = catalog:%d id:%d clock:%d repository:%d, want 1/0/0/0", scopeCatalog.calls, idGenerator.calls, clock.calls, repository.calls)
			}
		})
	}
}

func TestCreateApplicationVersion_BR_VER_001_009_RepositoryOutcomesMapToStableErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		repositoryErr error
		want          error
		category      domain.ErrorCategory
	}{
		{name: "application missing", repositoryErr: port.ErrApplicationNotFound, want: domain.ErrApplicationNotFound, category: domain.ErrorCategoryNotFound},
		{name: "not current admin", repositoryErr: port.ErrApplicationAdminRequired, want: domain.ErrApplicationAdminRequired, category: domain.ErrorCategoryAuthorization},
		{name: "label conflict", repositoryErr: port.ErrApplicationVersionLabelAlreadyExists, want: domain.ErrApplicationVersionLabelAlreadyExists, category: domain.ErrorCategoryConflict},
		{name: "wrapped label conflict", repositoryErr: errors.Join(errors.New("duplicate key"), port.ErrApplicationVersionLabelAlreadyExists), want: domain.ErrApplicationVersionLabelAlreadyExists, category: domain.ErrorCategoryConflict},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			repository := &fakeVersionRepository{err: testCase.repositoryErr}
			handler := NewCreateApplicationVersionHandler(
				&fakeScopeCatalog{},
				&fakeOAuthRedirectPolicy{},
				&fakeVersionIDGenerator{id: testVersionID},
				&fakeVersionClock{now: time.Now()},
				repository,
			)
			version, err := handler.Handle(context.Background(), approvedIdentity(), validCommand())
			if version != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", version, err, testCase.want)
			}
			if got := versionErrorCategory(t, err); got != testCase.category {
				t.Fatalf("error category = %s, want %s", got, testCase.category)
			}
			if repository.calls != 1 {
				t.Fatalf("repository calls = %d, want one atomic call", repository.calls)
			}
		})
	}
}

func TestCreateApplicationVersion_BR_VER_001_009_DependencyFailuresReturnNoPartialResultAndRetainCause(t *testing.T) {
	t.Parallel()

	idCause := errors.New("random source unavailable")
	repositoryCause := errors.New("database unavailable")
	testCases := []struct {
		name        string
		idGenerator *fakeVersionIDGenerator
		clock       *fakeVersionClock
		repository  *fakeVersionRepository
		cause       error
	}{
		{name: "ID generation", idGenerator: &fakeVersionIDGenerator{err: idCause}, clock: &fakeVersionClock{now: time.Now()}, repository: &fakeVersionRepository{}, cause: idCause},
		{name: "invalid generated ID", idGenerator: &fakeVersionIDGenerator{id: "not-a-uuid"}, clock: &fakeVersionClock{now: time.Now()}, repository: &fakeVersionRepository{}},
		{name: "zero clock", idGenerator: &fakeVersionIDGenerator{id: testVersionID}, clock: &fakeVersionClock{}, repository: &fakeVersionRepository{}},
		{name: "persistence", idGenerator: &fakeVersionIDGenerator{id: testVersionID}, clock: &fakeVersionClock{now: time.Now()}, repository: &fakeVersionRepository{err: repositoryCause}, cause: repositoryCause},
		{name: "nil repository result", idGenerator: &fakeVersionIDGenerator{id: testVersionID}, clock: &fakeVersionClock{now: time.Now()}, repository: &fakeVersionRepository{returnNil: true}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			handler := NewCreateApplicationVersionHandler(&fakeScopeCatalog{}, &fakeOAuthRedirectPolicy{}, testCase.idGenerator, testCase.clock, testCase.repository)
			version, err := handler.Handle(context.Background(), approvedIdentity(), validCommand())
			if version != nil || !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("Handle() = (%v, %v), want nil Internal", version, err)
			}
			if testCase.cause != nil && !errors.Is(err, testCase.cause) {
				t.Fatalf("error %v does not preserve cause %v", err, testCase.cause)
			}
		})
	}
}

func TestCreateApplicationVersion_BR_VER_008_RequestCannotSpecifyServerFields(t *testing.T) {
	t.Parallel()

	commandType := reflect.TypeOf(CreateApplicationVersionCommand{})
	wantFields := []string{
		"ApplicationID",
		"VersionLabel",
		"LaunchURL",
		"RPCApiMinVersion",
		"RPCApiMaxVersionExclusive",
		"RequiredCapabilities",
		"RequiredScopes",
		"OptionalScopes",
		"PKCERedirectURIs",
		"ConfidentialRedirectURIs",
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

func TestCreateApplicationVersion_NilDependenciesReturnInternalFailure(t *testing.T) {
	t.Parallel()

	validCatalog := func() port.ScopeCatalog { return &fakeScopeCatalog{} }
	validID := func() port.ApplicationVersionIDGenerator { return &fakeVersionIDGenerator{id: testVersionID} }
	validClock := func() port.Clock { return &fakeVersionClock{now: time.Now()} }
	validRepository := func() port.ApplicationVersionRepository { return &fakeVersionRepository{} }

	testCases := []struct {
		name    string
		handler *CreateApplicationVersionHandler
	}{
		{name: "nil handler"},
		{name: "nil catalog", handler: NewCreateApplicationVersionHandler(nil, &fakeOAuthRedirectPolicy{}, validID(), validClock(), validRepository())},
		{name: "nil OAuth policy", handler: NewCreateApplicationVersionHandler(validCatalog(), nil, validID(), validClock(), validRepository())},
		{name: "nil ID generator", handler: NewCreateApplicationVersionHandler(validCatalog(), &fakeOAuthRedirectPolicy{}, nil, validClock(), validRepository())},
		{name: "nil clock", handler: NewCreateApplicationVersionHandler(validCatalog(), &fakeOAuthRedirectPolicy{}, validID(), nil, validRepository())},
		{name: "nil repository", handler: NewCreateApplicationVersionHandler(validCatalog(), &fakeOAuthRedirectPolicy{}, validID(), validClock(), nil)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			version, err := testCase.handler.Handle(context.Background(), approvedIdentity(), validCommand())
			if version != nil || !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("Handle() = (%v, %v), want nil Internal", version, err)
			}
		})
	}
}

func versionErrorCategory(t *testing.T, err error) domain.ErrorCategory {
	t.Helper()
	var versionError *domain.Error
	if !errors.As(err, &versionError) {
		t.Fatalf("error %v is not a version domain Error", err)
	}
	return versionError.Category()
}
