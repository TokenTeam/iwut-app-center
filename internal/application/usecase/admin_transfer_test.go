package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type transferDirectoryFake struct{ err error }

func (fake *transferDirectoryFake) GetFresh(context.Context, []shared.AuthID) ([]port.DeveloperLifecycle, error) {
	return nil, fake.err
}

type transferIDGeneratorFake struct{}

func (*transferIDGeneratorFake) NewUUIDv7() (domain.ApplicationAdminTransferID, error) {
	return domain.ParseApplicationAdminTransferID("01890f47-0000-7000-8000-000000000201")
}

type transferRepositoryFake struct {
	transfer domain.ApplicationAdminTransfer
	called   bool
}

func (*transferRepositoryFake) GetOwnership(context.Context, shared.ApplicationID, shared.AuthID, time.Time) (domain.ApplicationOwnership, error) {
	return domain.ApplicationOwnership{}, nil
}
func (fake *transferRepositoryFake) Initiate(context.Context, shared.ApplicationID, shared.AuthID, shared.AuthID, int64, domain.ApplicationAdminTransferID, time.Time) (domain.ApplicationAdminTransfer, error) {
	fake.called = true
	return domain.ApplicationAdminTransfer{}, nil
}
func (fake *transferRepositoryFake) Get(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, time.Time) (domain.ApplicationAdminTransfer, error) {
	return fake.transfer, nil
}
func (fake *transferRepositoryFake) Accept(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, domain.ConfidentialCredentialHandling, time.Time) (domain.AcceptApplicationAdminTransferResult, error) {
	fake.called = true
	return domain.AcceptApplicationAdminTransferResult{}, nil
}
func (*transferRepositoryFake) Reject(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, time.Time) (domain.ApplicationAdminTransfer, error) {
	return domain.ApplicationAdminTransfer{}, nil
}
func (*transferRepositoryFake) Cancel(context.Context, domain.ApplicationAdminTransferID, shared.AuthID, time.Time) (domain.ApplicationAdminTransfer, error) {
	return domain.ApplicationAdminTransfer{}, nil
}

func TestApplicationAdminTransfer_BR_APP_014_NotFoundIsIneligible(t *testing.T) {
	t.Parallel()

	applicationID, _ := shared.ParseApplicationID("01890f47-0000-7000-8000-000000000202")
	transferID, _ := domain.ParseApplicationAdminTransferID("01890f47-0000-7000-8000-000000000203")
	now := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	directory := &transferDirectoryFake{err: port.ErrDeveloperLifecycleNotFound}
	repository := &transferRepositoryFake{transfer: domain.ApplicationAdminTransfer{
		TransferID: transferID, ApplicationID: applicationID, FromAdminID: "auth-source", ToAdminID: "auth-target",
		SourceOwnershipRevision: 1, Status: domain.ApplicationAdminTransferPending, RequestedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour),
	}}
	handler := NewApplicationAdminTransferHandlers(repository, directory, &transferIDGeneratorFake{}, &fakeClock{now: now})

	_, err := handler.Initiate(t.Context(), shared.DeveloperIdentity{AuthID: "auth-source", DeveloperStatus: shared.DeveloperStatusApproved}, applicationID, "auth-target", 1)
	if !errors.Is(err, domain.ErrTargetDeveloperIneligible) || repository.called {
		t.Fatalf("Initiate() = %v, called=%v; want target ineligible before repository", err, repository.called)
	}
	_, err = handler.Accept(t.Context(), shared.DeveloperIdentity{AuthID: "auth-target"}, transferID, domain.ConfidentialCredentialKeep)
	if !errors.Is(err, domain.ErrTargetDeveloperIneligible) || repository.called {
		t.Fatalf("Accept() = %v, called=%v; want target ineligible before repository", err, repository.called)
	}
}
