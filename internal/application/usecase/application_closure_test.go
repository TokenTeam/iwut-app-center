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

type closureWorkerRepository struct {
	closure       domain.ApplicationClosure
	scheduled     int
	scheduledAt   time.Time
	completed     int
	completedWith port.AuthApplicationClosureReceipt
	getErr        error
}

func (r *closureWorkerRepository) Preview(context.Context, shared.ApplicationID, shared.AuthID, time.Time) (domain.ApplicationClosurePreview, error) {
	panic("unexpected Preview")
}
func (r *closureWorkerRepository) Get(context.Context, shared.ApplicationID, shared.AuthID) (domain.ApplicationClosure, error) {
	return r.closure, r.getErr
}
func (r *closureWorkerRepository) Start(context.Context, shared.ApplicationID, shared.AuthID, int64, int64, domain.ApplicationCloseProof, domain.ApplicationClosureID, time.Time) (domain.ApplicationClosure, error) {
	panic("unexpected Start")
}
func (r *closureWorkerRepository) NextPending(context.Context, time.Time) (domain.ApplicationClosure, error) {
	return r.closure, nil
}
func (r *closureWorkerRepository) ScheduleRetry(_ context.Context, _ domain.ApplicationClosureID, now time.Time) error {
	r.scheduled++
	r.scheduledAt = now
	return nil
}
func (r *closureWorkerRepository) Complete(_ context.Context, _ domain.ApplicationClosureID, receipt string, appliedAt time.Time) (domain.ApplicationClosure, error) {
	r.completed++
	r.completedWith = port.AuthApplicationClosureReceipt{ReceiptID: receipt, AppliedAt: appliedAt}
	return r.closure, nil
}

type closureWorkerAuth struct {
	applyReceipt port.AuthApplicationClosureReceipt
	applyErr     error
	getReceipt   port.AuthApplicationClosureReceipt
	getErr       error
}

func (a closureWorkerAuth) Apply(context.Context, domain.ApplicationClosure) (port.AuthApplicationClosureReceipt, error) {
	return a.applyReceipt, a.applyErr
}
func (a closureWorkerAuth) Get(context.Context, domain.ApplicationClosure) (port.AuthApplicationClosureReceipt, error) {
	return a.getReceipt, a.getErr
}

type closureWorkerClock struct{ now time.Time }

func (c closureWorkerClock) Now() time.Time { return c.now }

type closureWorkerSequenceClock struct {
	times []time.Time
	next  int
}

func (c *closureWorkerSequenceClock) Now() time.Time {
	value := c.times[c.next]
	c.next++
	return value
}

func TestApplicationClosureReconcileRecoversLostApplyResponseFromGet(t *testing.T) {
	now := time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)
	receipt := port.AuthApplicationClosureReceipt{ReceiptID: "receipt", AppliedAt: now.Add(-time.Second)}
	repository := &closureWorkerRepository{closure: workerClosure(t)}
	handlers := &ApplicationClosureHandlers{repository: repository, clock: closureWorkerClock{now: now}}

	if err := handlers.ReconcileOne(t.Context(), closureWorkerAuth{applyErr: errors.New("response lost"), getReceipt: receipt}); err != nil {
		t.Fatalf("ReconcileOne() error = %v", err)
	}
	if repository.completed != 1 || repository.scheduled != 0 || repository.completedWith != receipt {
		t.Fatalf("completed=%d scheduled=%d receipt=%#v", repository.completed, repository.scheduled, repository.completedWith)
	}
}

func TestApplicationClosureCloseAllowsBoundProofRetryAfterProofExpires(t *testing.T) {
	existing := workerClosure(t)
	existing.HighRiskProofJTI = "018f0f4e-7b2a-7def-8f5d-f38c817a1c22"
	repository := &closureWorkerRepository{closure: existing}
	handlers := &ApplicationClosureHandlers{repository: repository}
	identity := shared.DeveloperIdentity{AuthID: shared.AuthID("auth-close")}
	command := CloseApplicationCommand{ApplicationID: existing.ApplicationID, ExpectedOwnershipRevision: 1, ExpectedLifecycleRevision: 1, Confirmation: "CLOSE_APPLICATION", Proof: domain.ApplicationCloseProof{Subject: identity.AuthID, ApplicationID: existing.ApplicationID, JTI: existing.HighRiskProofJTI}, ProofStale: true}

	closure, err := handlers.Close(t.Context(), identity, command)
	if err != nil || closure.ClosureID != existing.ClosureID {
		t.Fatalf("Close() = %#v, %v", closure, err)
	}
	command.Proof.JTI = "018f0f4e-7b2a-7def-8f5d-f38c817a1c23"
	if _, err := handlers.Close(t.Context(), identity, command); !errors.Is(err, domain.ErrHighRiskProofInvalid) {
		t.Fatalf("unbound stale Close() error = %v", err)
	}
}

func TestApplicationClosureCloseDoesNotHideCorruptionBehindStaleProof(t *testing.T) {
	existing := workerClosure(t)
	repository := &closureWorkerRepository{getErr: domain.ErrApplicationClosureStateInconsistent}
	handlers := &ApplicationClosureHandlers{repository: repository}
	identity := shared.DeveloperIdentity{AuthID: shared.AuthID("auth-close")}
	command := CloseApplicationCommand{ApplicationID: existing.ApplicationID, ExpectedOwnershipRevision: 1, ExpectedLifecycleRevision: 1, Confirmation: "CLOSE_APPLICATION", Proof: domain.ApplicationCloseProof{Subject: identity.AuthID, ApplicationID: existing.ApplicationID, JTI: "018f0f4e-7b2a-7def-8f5d-f38c817a1c22"}, ProofStale: true}

	if _, err := handlers.Close(t.Context(), identity, command); !errors.Is(err, domain.ErrApplicationClosureStateInconsistent) {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestApplicationClosureReconcileRetriesSameClosureAfterAuthNotFound(t *testing.T) {
	repository := &closureWorkerRepository{closure: workerClosure(t)}
	handlers := &ApplicationClosureHandlers{repository: repository, clock: closureWorkerClock{now: time.Now().UTC()}}

	err := handlers.ReconcileOne(t.Context(), closureWorkerAuth{applyErr: errors.New("unavailable"), getErr: port.ErrAuthApplicationClosureNotFound})
	if err != nil || repository.scheduled != 1 || repository.completed != 0 {
		t.Fatalf("error=%v scheduled=%d completed=%d", err, repository.scheduled, repository.completed)
	}
}

func TestApplicationClosureReconcileSchedulesFromRPCCompletionTime(t *testing.T) {
	selectedAt := time.Date(2026, 10, 6, 1, 2, 3, 0, time.UTC)
	failedAt := selectedAt.Add(10 * time.Second)
	repository := &closureWorkerRepository{closure: workerClosure(t)}
	clock := &closureWorkerSequenceClock{times: []time.Time{selectedAt, failedAt}}
	handlers := &ApplicationClosureHandlers{repository: repository, clock: clock}

	err := handlers.ReconcileOne(t.Context(), closureWorkerAuth{applyErr: errors.New("unavailable"), getErr: port.ErrAuthApplicationClosureNotFound})
	if err != nil || repository.scheduledAt != failedAt {
		t.Fatalf("error=%v scheduledAt=%v, want %v", err, repository.scheduledAt, failedAt)
	}
}

func TestApplicationClosureReconcileKeepsClosingAndAlertsOnBindingConflict(t *testing.T) {
	repository := &closureWorkerRepository{closure: workerClosure(t)}
	handlers := &ApplicationClosureHandlers{repository: repository, clock: closureWorkerClock{now: time.Now().UTC()}}
	receipt := port.AuthApplicationClosureReceipt{ReceiptID: "must-not-complete", AppliedAt: time.Now().UTC()}

	err := handlers.ReconcileOne(t.Context(), closureWorkerAuth{applyErr: port.ErrAuthApplicationClosureConflict, getReceipt: receipt})
	if !errors.Is(err, domain.ErrApplicationClosureStateInconsistent) || repository.scheduled != 1 || repository.completed != 0 {
		t.Fatalf("error=%v scheduled=%d completed=%d", err, repository.scheduled, repository.completed)
	}
}

func workerClosure(t *testing.T) domain.ApplicationClosure {
	t.Helper()
	closureID, err := domain.ParseApplicationClosureID("0199b813-aec7-70e3-8fb8-79482ea24c9c")
	if err != nil {
		t.Fatal(err)
	}
	applicationID, ok := shared.ParseApplicationID("0199b813-aec7-70e3-8fb8-79482ea24c9d")
	if !ok {
		t.Fatal("invalid application fixture")
	}
	return domain.ApplicationClosure{ClosureID: closureID, ApplicationID: applicationID, Status: domain.ApplicationClosureClosing}
}
