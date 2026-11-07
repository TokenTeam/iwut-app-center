package main

import (
	"context"
	"iwut-app-center/internal/script/usecase"
	"log/slog"
	"sync"
	"time"
)

type scriptCascadeWorker struct {
	handler *usecase.CascadeHandler
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func provideScriptCascadeWorker(handler *usecase.CascadeHandler) *scriptCascadeWorker {
	return &scriptCascadeWorker{handler: handler}
}
func (w *scriptCascadeWorker) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				attempt, cancelAttempt := context.WithTimeout(ctx, 20*time.Second)
				err := w.handler.Reconcile(attempt)
				cancelAttempt()
				if err != nil && ctx.Err() == nil {
					slog.Error("script rejection cascade reconciliation unavailable")
				}
			}
		}
	}()
	return nil
}
func (w *scriptCascadeWorker) Stop(context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	return nil
}
