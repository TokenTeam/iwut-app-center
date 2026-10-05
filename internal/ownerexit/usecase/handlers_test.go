package usecase

import (
	"context"
	"errors"
	d "iwut-app-center/internal/ownerexit/domain"
	"testing"
	"time"
)

type fakeRepository struct {
	jobs     []d.Work
	finishes []d.Finish
	retries  int
	cleanup  int
}

func (r *fakeRepository) Prepare(context.Context, d.Prepare, string, time.Time) (d.Preparation, error) {
	return d.Preparation{}, nil
}
func (r *fakeRepository) Finish(_ context.Context, f d.Finish, _ time.Time) (d.Status, error) {
	r.finishes = append(r.finishes, f)
	return d.Status{}, nil
}
func (r *fakeRepository) Get(context.Context, d.Key) (d.Status, error)          { return d.Status{}, nil }
func (r *fakeRepository) Due(context.Context, time.Time, int) ([]d.Work, error) { return r.jobs, nil }
func (r *fakeRepository) CleanupBatch(context.Context, d.Key, int) error        { r.cleanup++; return nil }
func (r *fakeRepository) Retry(context.Context, d.Key, time.Time, int) error    { r.retries++; return nil }

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Unix(1000, 0) }

type fakeDecisions struct {
	status d.Status
	err    error
}

func (f fakeDecisions) Get(context.Context, d.Key) (d.Status, error) { return f.status, f.err }
func TestBR_APP_009_ReconcileNeverInfersCancellation(t *testing.T) {
	base := d.Status{Prepare: d.Prepare{Key: d.Key{AuthID: "user", OperationID: "d8046b8e-ff67-42c0-9a2b-395174606e23"}, Purpose: d.Closure}, ReceiptID: "019314e9-9037-7b00-8123-abcdeffedcba", Decision: d.Pending, Cleanup: d.NotRequired}
	for _, decision := range []fakeDecisions{{status: base}, {err: errors.New("not found")}, {err: errors.New("unavailable")}, {status: d.Status{Prepare: base.Prepare, ReceiptID: "wrong", Decision: d.Committed}}} {
		r := &fakeRepository{jobs: []d.Work{{Status: base}}}
		h := NewHandlers(r, fakeClock{}, nil, decision)
		if e := h.Reconcile(t.Context()); e != nil || len(r.finishes) != 0 || r.retries != 1 {
			t.Fatalf("%+v %v", r, e)
		}
	}
	terminal := base
	terminal.Decision = d.Committed
	r := &fakeRepository{jobs: []d.Work{{Status: base}}}
	h := NewHandlers(r, fakeClock{}, nil, fakeDecisions{status: terminal})
	if e := h.Reconcile(t.Context()); e != nil || len(r.finishes) != 1 || r.finishes[0].Decision != d.Committed {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestBR_APP_009_CancelWithoutLostReceipt(t *testing.T) {
	p := d.Prepare{Key: d.Key{AuthID: "user", OperationID: "d8046b8e-ff67-42c0-9a2b-395174606e23"}, Purpose: d.Closure}
	job := d.Status{Prepare: p, ReceiptID: "019314e9-9037-7b00-8123-abcdeffedcba", Decision: d.Pending, Cleanup: d.NotRequired}
	r := &fakeRepository{jobs: []d.Work{{Status: job}}}
	h := NewHandlers(r, fakeClock{}, nil, fakeDecisions{status: d.Status{Prepare: p, Decision: d.Cancelled}})
	if e := h.Reconcile(t.Context()); e != nil || len(r.finishes) != 1 || r.finishes[0].Decision != d.Cancelled {
		t.Fatalf("%+v %v", r, e)
	}
}
