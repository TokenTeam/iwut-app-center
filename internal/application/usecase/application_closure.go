package usecase

import (
	"context"
	"errors"

	"iwut-app-center/internal/application/domain"
	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/shared"
)

type CloseApplicationCommand struct {
	ApplicationID             shared.ApplicationID
	ExpectedOwnershipRevision int64
	ExpectedLifecycleRevision int64
	Confirmation              string
	Proof                     domain.ApplicationCloseProof
	ProofStale                bool
}

type ApplicationClosureHandlers struct {
	repository port.ApplicationClosureRepository
	directory  port.DeveloperLifecycleDirectory
	ids        port.ApplicationClosureIDGenerator
	clock      port.Clock
}

func NewApplicationClosureHandlers(repository port.ApplicationClosureRepository, directory port.DeveloperLifecycleDirectory, ids port.ApplicationClosureIDGenerator, clock port.Clock) *ApplicationClosureHandlers {
	return &ApplicationClosureHandlers{repository: repository, directory: directory, ids: ids, clock: clock}
}

func (h *ApplicationClosureHandlers) Preview(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID) (domain.ApplicationClosurePreview, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationClosurePreview{}, domain.ErrDeveloperIdentityRequired
	}
	if !applicationID.IsValid() {
		return domain.ApplicationClosurePreview{}, domain.ErrInvalidApplicationID
	}
	return h.repository.Preview(ctx, applicationID, identity.AuthID, h.clock.Now().UTC())
}

func (h *ApplicationClosureHandlers) Get(ctx context.Context, identity shared.DeveloperIdentity, applicationID shared.ApplicationID) (domain.ApplicationClosure, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationClosure{}, domain.ErrDeveloperIdentityRequired
	}
	if !applicationID.IsValid() {
		return domain.ApplicationClosure{}, domain.ErrInvalidApplicationID
	}
	return h.repository.Get(ctx, applicationID, identity.AuthID)
}

func (h *ApplicationClosureHandlers) Close(ctx context.Context, identity shared.DeveloperIdentity, command CloseApplicationCommand) (domain.ApplicationClosure, error) {
	if !identity.AuthID.IsValid() {
		return domain.ApplicationClosure{}, domain.ErrDeveloperIdentityRequired
	}
	if !command.ApplicationID.IsValid() {
		return domain.ApplicationClosure{}, domain.ErrInvalidApplicationID
	}
	if command.ExpectedOwnershipRevision < 1 {
		return domain.ApplicationClosure{}, domain.ErrInvalidOwnershipRevision
	}
	if command.ExpectedLifecycleRevision < 1 {
		return domain.ApplicationClosure{}, domain.ErrInvalidLifecycleRevision
	}
	if command.Confirmation != "CLOSE_APPLICATION" {
		return domain.ApplicationClosure{}, domain.ErrInvalidCloseConfirmation
	}
	if command.Proof.Subject != identity.AuthID || command.Proof.ApplicationID != command.ApplicationID || command.Proof.JTI == "" {
		return domain.ApplicationClosure{}, domain.ErrHighRiskProofInvalid
	}
	existing, getErr := h.repository.Get(ctx, command.ApplicationID, identity.AuthID)
	if getErr == nil {
		if existing.HighRiskProofJTI == command.Proof.JTI {
			return existing, nil
		}
		if command.ProofStale {
			return domain.ApplicationClosure{}, domain.ErrHighRiskProofInvalid
		}
		return domain.ApplicationClosure{}, domain.ErrApplicationLifecycleChanged
	}
	if command.ProofStale {
		if !errors.Is(getErr, domain.ErrApplicationClosureNotFound) && !errors.Is(getErr, domain.ErrApplicationNotFound) {
			return domain.ApplicationClosure{}, getErr
		}
		return domain.ApplicationClosure{}, domain.ErrHighRiskProofInvalid
	}
	if !errors.Is(getErr, domain.ErrApplicationClosureNotFound) && !errors.Is(getErr, domain.ErrApplicationNotFound) {
		return domain.ApplicationClosure{}, getErr
	}
	if identity.DeveloperStatus != shared.DeveloperStatusApproved {
		return domain.ApplicationClosure{}, domain.ErrDeveloperApprovalRequired
	}
	statuses, err := h.directory.GetFresh(ctx, []shared.AuthID{identity.AuthID})
	if errors.Is(err, port.ErrDeveloperLifecycleNotFound) || err != nil || len(statuses) != 1 || statuses[0].AuthID != identity.AuthID {
		return domain.ApplicationClosure{}, domain.ErrDeveloperStatusUnavailable
	}
	if !statuses[0].Eligible() {
		return domain.ApplicationClosure{}, domain.ErrDeveloperApprovalRequired
	}
	id, err := h.ids.NewUUIDv7()
	if err != nil || !id.IsValid() {
		return domain.ApplicationClosure{}, domain.NewInternalError(err)
	}
	return h.repository.Start(ctx, command.ApplicationID, identity.AuthID, command.ExpectedOwnershipRevision, command.ExpectedLifecycleRevision, command.Proof, id, h.clock.Now().UTC())
}

func (h *ApplicationClosureHandlers) ReconcileOne(ctx context.Context, auth port.AuthApplicationClosure) error {
	now := h.clock.Now().UTC()
	closure, err := h.repository.NextPending(ctx, now)
	if errors.Is(err, domain.ErrApplicationClosureNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	receipt, applyErr := auth.Apply(ctx, closure)
	if errors.Is(applyErr, port.ErrAuthApplicationClosureConflict) {
		if scheduleErr := h.repository.ScheduleRetry(ctx, closure.ClosureID, h.clock.Now().UTC()); scheduleErr != nil {
			return scheduleErr
		}
		return domain.ErrApplicationClosureStateInconsistent
	}
	if applyErr != nil {
		receipt, err = auth.Get(ctx, closure)
	}
	if err != nil {
		if scheduleErr := h.repository.ScheduleRetry(ctx, closure.ClosureID, h.clock.Now().UTC()); scheduleErr != nil {
			return scheduleErr
		}
		if errors.Is(err, port.ErrAuthApplicationClosureConflict) {
			return domain.ErrApplicationClosureStateInconsistent
		}
		return nil
	}
	if receipt.ReceiptID == "" || receipt.AppliedAt.IsZero() {
		return domain.ErrApplicationClosureStateInconsistent
	}
	_, err = h.repository.Complete(ctx, closure.ClosureID, receipt.ReceiptID, receipt.AppliedAt)
	return err
}
