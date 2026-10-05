package usecase

import (
	"context"
	"iwut-app-center/internal/ownerexit/domain"
	"iwut-app-center/internal/ownerexit/port"
	"time"
)

type Handlers struct {
	repository port.Repository
	clock      port.Clock
	ids        port.IDs
	decisions  port.Decisions
}

func NewHandlers(r port.Repository, c port.Clock, i port.IDs, d port.Decisions) *Handlers {
	return &Handlers{r, c, i, d}
}
func (h *Handlers) Prepare(ctx context.Context, p domain.Prepare) (domain.Preparation, error) {
	if !p.Valid() {
		return domain.Preparation{}, domain.ErrInvalid
	}
	id, err := h.ids.NewReceiptID()
	if err != nil {
		return domain.Preparation{}, domain.ErrUnavailable
	}
	return h.repository.Prepare(ctx, p, id, h.clock.Now().UTC())
}
func (h *Handlers) Finish(ctx context.Context, f domain.Finish) (domain.Status, error) {
	if !f.Valid() {
		return domain.Status{}, domain.ErrInvalid
	}
	return h.repository.Finish(ctx, f, h.clock.Now().UTC())
}
func (h *Handlers) Get(ctx context.Context, k domain.Key) (domain.Status, error) {
	if !k.Valid() {
		return domain.Status{}, domain.ErrInvalid
	}
	return h.repository.Get(ctx, k)
}

// Reconcile uses persisted due times. Each remote query has an independent deadline;
// no unavailable or unknown decision ever releases a fence.
func (h *Handlers) Reconcile(ctx context.Context) error {
	jobs, err := h.repository.Due(ctx, h.clock.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var workErr error
		if job.Decision == domain.Pending {
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			decision, e := h.decisions.Get(callCtx, job.Key)
			cancel()
			workErr = e
			if e == nil && (decision.Prepare != job.Prepare || (decision.Decision != domain.Cancelled || decision.ReceiptID != "") && decision.ReceiptID != job.ReceiptID) {
				workErr = domain.ErrConflict
			}
			if workErr == nil && decision.Decision != domain.Pending {
				_, workErr = h.Finish(ctx, domain.Finish{Prepare: job.Prepare, ReceiptID: job.ReceiptID, Decision: decision.Decision})
			}
		} else if job.Decision == domain.Committed && job.Cleanup == domain.CleanupPending {
			workErr = h.repository.CleanupBatch(ctx, job.Key, 500)
		}
		// Bounded deterministic jitter avoids a process-local random dependency.
		attempt := job.Attempt + 1
		if attempt > 7 {
			attempt = 7
		}
		delay := time.Second * time.Duration(1<<uint(attempt-1))
		if delay > 60*time.Second {
			delay = 60 * time.Second
		}
		jitter := time.Duration(int(job.OperationID[len(job.OperationID)-1])%200) * time.Millisecond
		if workErr == nil && job.Cleanup == domain.CleanupPending {
			attempt = 0
			delay = time.Second
		}
		if err = h.repository.Retry(ctx, job.Key, h.clock.Now().UTC().Add(delay+jitter), attempt); err != nil {
			return err
		}
	}
	return nil
}
