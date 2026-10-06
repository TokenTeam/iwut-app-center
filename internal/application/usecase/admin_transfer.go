package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type ApplicationAdminTransferHandlers struct {
	repository port.ApplicationAdminTransferRepository
	directory  port.DeveloperLifecycleDirectory
	ids        port.ApplicationAdminTransferIDGenerator
	clock      port.Clock
}

func NewApplicationAdminTransferHandlers(repository port.ApplicationAdminTransferRepository, directory port.DeveloperLifecycleDirectory, ids port.ApplicationAdminTransferIDGenerator, clock port.Clock) *ApplicationAdminTransferHandlers {
	return &ApplicationAdminTransferHandlers{repository: repository, directory: directory, ids: ids, clock: clock}
}

func transferIdentity(identity shared.DeveloperIdentity, requireApproved bool) error {
	if !identity.AuthID.IsValid() {
		return domain.ErrDeveloperIdentityRequired
	}
	if requireApproved && identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return domain.ErrDeveloperApprovalRequired
	}
	return nil
}

func (h *ApplicationAdminTransferHandlers) GetOwnership(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID) (domain.ApplicationOwnership, error) {
	if err := transferIdentity(identity, false); err != nil {
		return domain.ApplicationOwnership{}, err
	}
	if !applicationID.IsValid() {
		return domain.ApplicationOwnership{}, domain.ErrInvalidApplicationID
	}
	return h.repository.GetOwnership(ctx, applicationID, identity.AuthID, h.clock.Now().UTC())
}

func (h *ApplicationAdminTransferHandlers) Initiate(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID, toAuthID shared.AuthID, expected int64) (domain.ApplicationAdminTransfer, error) {
	if err := transferIdentity(identity, true); err != nil {
		return domain.ApplicationAdminTransfer{}, err
	}
	if !applicationID.IsValid() {
		return domain.ApplicationAdminTransfer{}, domain.ErrInvalidApplicationID
	}
	if !toAuthID.IsValid() || toAuthID == identity.AuthID {
		return domain.ApplicationAdminTransfer{}, domain.ErrInvalidTargetAuthID
	}
	if expected < 1 {
		return domain.ApplicationAdminTransfer{}, domain.ErrInvalidOwnershipRevision
	}
	statuses, err := h.directory.GetFresh(ctx, []shared.AuthID{toAuthID})
	if errors.Is(err, port.ErrDeveloperLifecycleNotFound) {
		return domain.ApplicationAdminTransfer{}, domain.ErrTargetDeveloperIneligible
	}
	if err != nil || len(statuses) != 1 || statuses[0].AuthID != toAuthID {
		return domain.ApplicationAdminTransfer{}, domain.ErrDeveloperStatusUnavailable
	}
	if !statuses[0].Eligible() {
		return domain.ApplicationAdminTransfer{}, domain.ErrTargetDeveloperIneligible
	}
	id, err := h.ids.NewUUIDv7()
	if err != nil || !id.IsValid() {
		return domain.ApplicationAdminTransfer{}, domain.NewInternalError(err)
	}
	return h.repository.Initiate(ctx, applicationID, identity.AuthID, toAuthID, expected, id, h.clock.Now().UTC())
}

func (h *ApplicationAdminTransferHandlers) Get(ctx context.Context, identity shared.DeveloperIdentity, transferID domain.ApplicationAdminTransferID) (domain.ApplicationAdminTransfer, error) {
	if err := transferIdentity(identity, false); err != nil {
		return domain.ApplicationAdminTransfer{}, err
	}
	if !transferID.IsValid() {
		return domain.ApplicationAdminTransfer{}, domain.ErrInvalidApplicationAdminTransferID
	}
	return h.repository.Get(ctx, transferID, identity.AuthID, h.clock.Now().UTC())
}

func (h *ApplicationAdminTransferHandlers) Accept(ctx context.Context, identity shared.DeveloperIdentity, transferID domain.ApplicationAdminTransferID, handling domain.ConfidentialCredentialHandling) (domain.AcceptApplicationAdminTransferResult, error) {
	if err := transferIdentity(identity, false); err != nil {
		return domain.AcceptApplicationAdminTransferResult{}, err
	}
	if !transferID.IsValid() {
		return domain.AcceptApplicationAdminTransferResult{}, domain.ErrInvalidApplicationAdminTransferID
	}
	if handling != domain.ConfidentialCredentialKeep && handling != domain.ConfidentialCredentialRotate {
		return domain.AcceptApplicationAdminTransferResult{}, domain.ErrInvalidConfidentialCredentialHandling
	}
	transfer, err := h.repository.Get(ctx, transferID, identity.AuthID, h.clock.Now().UTC())
	if err != nil {
		return domain.AcceptApplicationAdminTransferResult{}, err
	}
	if transfer.Status == domain.ApplicationAdminTransferAccepted {
		return domain.AcceptApplicationAdminTransferResult{Transfer: transfer}, nil
	}
	statuses, err := h.directory.GetFresh(ctx, []shared.AuthID{transfer.FromAdminID, transfer.ToAdminID})
	if errors.Is(err, port.ErrDeveloperLifecycleNotFound) {
		return domain.AcceptApplicationAdminTransferResult{}, domain.ErrTargetDeveloperIneligible
	}
	if err != nil || len(statuses) != 2 || statuses[0].AuthID != transfer.FromAdminID || statuses[1].AuthID != transfer.ToAdminID {
		return domain.AcceptApplicationAdminTransferResult{}, domain.ErrDeveloperStatusUnavailable
	}
	if !statuses[0].Eligible() {
		return domain.AcceptApplicationAdminTransferResult{}, domain.ErrSourceDeveloperIneligible
	}
	if !statuses[1].Eligible() {
		return domain.AcceptApplicationAdminTransferResult{}, domain.ErrTargetDeveloperIneligible
	}
	return h.repository.Accept(ctx, transferID, identity.AuthID, handling, h.clock.Now().UTC())
}

func (h *ApplicationAdminTransferHandlers) Reject(ctx context.Context, identity shared.DeveloperIdentity, transferID domain.ApplicationAdminTransferID) (domain.ApplicationAdminTransfer, error) {
	if err := transferIdentity(identity, false); err != nil {
		return domain.ApplicationAdminTransfer{}, err
	}
	if !transferID.IsValid() {
		return domain.ApplicationAdminTransfer{}, domain.ErrInvalidApplicationAdminTransferID
	}
	return h.repository.Reject(ctx, transferID, identity.AuthID, h.clock.Now().UTC())
}

func (h *ApplicationAdminTransferHandlers) Cancel(ctx context.Context, identity shared.DeveloperIdentity, transferID domain.ApplicationAdminTransferID) (domain.ApplicationAdminTransfer, error) {
	if err := transferIdentity(identity, false); err != nil {
		return domain.ApplicationAdminTransfer{}, err
	}
	if !transferID.IsValid() {
		return domain.ApplicationAdminTransfer{}, domain.ErrInvalidApplicationAdminTransferID
	}
	return h.repository.Cancel(ctx, transferID, identity.AuthID, h.clock.Now().UTC())
}
