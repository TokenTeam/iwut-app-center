package usecase

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
)

const generatedApplicationID domain.ApplicationID = "01890f5a-e810-7cc3-98c8-8c6d5d8b4c21"

type fakeIDGenerator struct {
	events *[]string
	id     domain.ApplicationID
	err    error
	calls  int
}

func (fake *fakeIDGenerator) NewUUIDv7() (domain.ApplicationID, error) {
	fake.calls++
	appendEvent(fake.events, "id")
	return fake.id, fake.err
}

type fakeClock struct {
	events *[]string
	now    time.Time
	calls  int
}

func (fake *fakeClock) Now() time.Time {
	fake.calls++
	appendEvent(fake.events, "clock")
	return fake.now
}

type fakeApplicationRepository struct {
	events       *[]string
	err          error
	calls        int
	application  *domain.Application
	initialLimit int32
}

func (fake *fakeApplicationRepository) CreateWithinQuota(
	_ context.Context,
	application *domain.Application,
	initialLimit int32,
) error {
	fake.calls++
	fake.application = application
	fake.initialLimit = initialLimit
	appendEvent(fake.events, "repository")
	return fake.err
}

func appendEvent(events *[]string, event string) {
	if events != nil {
		*events = append(*events, event)
	}
}

func TestCreateApplication_BR_APP_001_002_003_005_006_007_Success(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 3)
	generatedAt := time.Date(2026, time.September, 19, 8, 9, 10, 11, time.FixedZone("CST", 8*60*60))
	idGenerator := &fakeIDGenerator{events: &events, id: generatedApplicationID}
	clock := &fakeClock{events: &events, now: generatedAt}
	repository := &fakeApplicationRepository{events: &events}
	handler := NewCreateApplicationHandler(idGenerator, clock, repository)

	application, err := handler.Handle(
		context.Background(),
		DeveloperIdentity{AuthID: "trusted-auth-id", DeveloperStatus: DeveloperStatusApproved},
		CreateApplicationCommand{Name: "Course_App"},
	)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if application == nil {
		t.Fatal("Handle() application is nil")
	}
	if application.ID() != generatedApplicationID {
		t.Fatalf("ID() = %q, want generated ID", application.ID())
	}
	if application.Name().String() != "Course_App" || application.Name().Key() != "course_app" {
		t.Fatalf("Name() = %#v, want preserved value and lowercase key", application.Name())
	}
	if application.AdminID() != "trusted-auth-id" {
		t.Fatalf("AdminID() = %q, want trusted identity", application.AdminID())
	}
	if !application.CreatedAt().Equal(generatedAt) || application.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt() = %v, want generated instant in UTC", application.CreatedAt())
	}
	if repository.application != application || repository.initialLimit != domain.InitialDeveloperApplicationQuotaLimit || repository.calls != 1 {
		t.Fatalf("repository call = (%p, %d, %d), want application, initial limit 10, exactly one call", repository.application, repository.initialLimit, repository.calls)
	}
	if want := []string{"id", "clock", "repository"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("dependency order = %v, want %v", events, want)
	}
}

func TestCreateApplication_BR_APP_002_OnlyApprovedDevelopersCanCreate(t *testing.T) {
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

			idGenerator := &fakeIDGenerator{id: generatedApplicationID}
			clock := &fakeClock{now: time.Now()}
			repository := &fakeApplicationRepository{}
			handler := NewCreateApplicationHandler(idGenerator, clock, repository)

			application, err := handler.Handle(context.Background(), testCase.identity, CreateApplicationCommand{Name: "app"})
			if application != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", application, err, testCase.want)
			}
			if idGenerator.calls != 0 || clock.calls != 0 || repository.calls != 0 {
				t.Fatal("dependencies were called for an unauthorized request")
			}
		})
	}
}

func TestCreateApplication_BR_APP_003_InvalidNameStopsBeforeDependencies(t *testing.T) {
	t.Parallel()

	idGenerator := &fakeIDGenerator{id: generatedApplicationID}
	clock := &fakeClock{now: time.Now()}
	repository := &fakeApplicationRepository{}
	handler := NewCreateApplicationHandler(idGenerator, clock, repository)

	application, err := handler.Handle(
		context.Background(),
		DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved},
		CreateApplicationCommand{Name: "not valid"},
	)
	if application != nil || !errors.Is(err, domain.ErrInvalidApplicationName) {
		t.Fatalf("Handle() = (%v, %v), want nil and InvalidApplicationName", application, err)
	}
	if idGenerator.calls != 0 || clock.calls != 0 || repository.calls != 0 {
		t.Fatal("dependencies were called for an invalid name")
	}
}

func TestCreateApplication_BR_APP_004_CommandDoesNotAcceptPublicProfile(t *testing.T) {
	t.Parallel()

	commandType := reflect.TypeOf(CreateApplicationCommand{})
	if commandType.NumField() != 1 || commandType.Field(0).Name != "Name" {
		t.Fatalf("CreateApplicationCommand fields = %v, want only Name", commandType)
	}
}

func TestCreateApplication_BR_APP_005_006_RepositoryOutcomesMapToBusinessErrors(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		repositoryError error
		want            error
	}{
		{name: "name conflict", repositoryError: port.ErrApplicationNameAlreadyExists, want: domain.ErrApplicationNameAlreadyExists},
		{name: "wrapped name conflict", repositoryError: errors.Join(errors.New("transaction"), port.ErrApplicationNameAlreadyExists), want: domain.ErrApplicationNameAlreadyExists},
		{name: "quota exceeded", repositoryError: port.ErrApplicationQuotaExceeded, want: domain.ErrApplicationQuotaExceeded},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			repository := &fakeApplicationRepository{err: testCase.repositoryError}
			handler := NewCreateApplicationHandler(
				&fakeIDGenerator{id: generatedApplicationID},
				&fakeClock{now: time.Now()},
				repository,
			)

			application, err := handler.Handle(
				context.Background(),
				DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved},
				CreateApplicationCommand{Name: "app"},
			)
			if application != nil || !errors.Is(err, testCase.want) {
				t.Fatalf("Handle() = (%v, %v), want nil and %v", application, err, testCase.want)
			}
			if repository.calls != 1 {
				t.Fatalf("repository calls = %d, want exactly one atomic call", repository.calls)
			}
		})
	}
}

func TestCreateApplication_BR_APP_001_005_006_DependencyFailuresReturnNoPartialResult(t *testing.T) {
	t.Parallel()

	idFailure := errors.New("id generator unavailable")
	repositoryFailure := errors.New("database unavailable")

	testCases := []struct {
		name        string
		idGenerator *fakeIDGenerator
		repository  *fakeApplicationRepository
		cause       error
	}{
		{
			name:        "ID generation",
			idGenerator: &fakeIDGenerator{err: idFailure},
			repository:  &fakeApplicationRepository{},
			cause:       idFailure,
		},
		{
			name:        "persistence",
			idGenerator: &fakeIDGenerator{id: generatedApplicationID},
			repository:  &fakeApplicationRepository{err: repositoryFailure},
			cause:       repositoryFailure,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			handler := NewCreateApplicationHandler(
				testCase.idGenerator,
				&fakeClock{now: time.Now()},
				testCase.repository,
			)
			application, err := handler.Handle(
				context.Background(),
				DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved},
				CreateApplicationCommand{Name: "app"},
			)

			if application != nil || !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("Handle() = (%v, %v), want nil and Internal", application, err)
			}
			if !errors.Is(err, testCase.cause) {
				t.Fatalf("Handle() error does not retain dependency cause %v", testCase.cause)
			}
		})
	}
}

func TestCreateApplication_BR_APP_001_InvalidGeneratedIDIsInternalFailure(t *testing.T) {
	t.Parallel()

	repository := &fakeApplicationRepository{}
	handler := NewCreateApplicationHandler(
		&fakeIDGenerator{id: "not-a-uuid"},
		&fakeClock{now: time.Now()},
		repository,
	)

	application, err := handler.Handle(
		context.Background(),
		DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved},
		CreateApplicationCommand{Name: "app"},
	)
	if application != nil || !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("Handle() = (%v, %v), want nil and Internal", application, err)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want zero", repository.calls)
	}
}

func TestCreateApplication_BR_APP_001_007_InvalidClockValueIsInternalFailure(t *testing.T) {
	t.Parallel()

	clock := &fakeClock{}
	repository := &fakeApplicationRepository{}
	handler := NewCreateApplicationHandler(
		&fakeIDGenerator{id: generatedApplicationID},
		clock,
		repository,
	)

	application, err := handler.Handle(
		context.Background(),
		DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved},
		CreateApplicationCommand{Name: "app"},
	)
	if application != nil || !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("Handle() = (%v, %v), want nil and Internal", application, err)
	}
	if clock.calls != 1 {
		t.Fatalf("clock calls = %d, want one", clock.calls)
	}
	if repository.calls != 0 {
		t.Fatalf("repository calls = %d, want zero", repository.calls)
	}
}

func TestCreateApplication_NilDependenciesReturnInternalFailure(t *testing.T) {
	t.Parallel()

	validIDGenerator := func() port.ApplicationIDGenerator {
		return &fakeIDGenerator{id: generatedApplicationID}
	}
	validClock := func() port.Clock {
		return &fakeClock{now: time.Now()}
	}
	validRepository := func() port.ApplicationRepository {
		return &fakeApplicationRepository{}
	}

	testCases := []struct {
		name    string
		handler *CreateApplicationHandler
	}{
		{name: "nil handler"},
		{name: "nil ID generator", handler: NewCreateApplicationHandler(nil, validClock(), validRepository())},
		{name: "nil clock", handler: NewCreateApplicationHandler(validIDGenerator(), nil, validRepository())},
		{name: "nil repository", handler: NewCreateApplicationHandler(validIDGenerator(), validClock(), nil)},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			application, err := testCase.handler.Handle(
				context.Background(),
				DeveloperIdentity{AuthID: "auth-1", DeveloperStatus: DeveloperStatusApproved},
				CreateApplicationCommand{Name: "app"},
			)
			if application != nil || !errors.Is(err, domain.ErrInternal) {
				t.Fatalf("Handle() = (%v, %v), want nil and Internal", application, err)
			}
		})
	}
}
