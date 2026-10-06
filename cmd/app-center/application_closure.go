package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"iwut-app-center/internal/application/port"
	"iwut-app-center/internal/application/usecase"
)

type applicationClosureWorker struct {
	handlers *usecase.ApplicationClosureHandlers
	auth     port.AuthApplicationClosure
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func provideApplicationClosureWorker(handlers *usecase.ApplicationClosureHandlers, auth port.AuthApplicationClosure) *applicationClosureWorker {
	return &applicationClosureWorker{handlers: handlers, auth: auth}
}

func (w *applicationClosureWorker) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if err := w.handlers.ReconcileOne(ctx, w.auth); err != nil && ctx.Err() == nil {
					slog.Error("application closure reconciliation unavailable")
				}
			}
		}
	}()
	return nil
}
func (w *applicationClosureWorker) Stop(context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	return nil
}
