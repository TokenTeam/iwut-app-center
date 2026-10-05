package main

import (
	"context"
	"fmt"
	pb "github.com/TokenTeam/iwut-api-proto/gen/go/app_center/v1/account_owner_exit"
	"github.com/go-kratos/kratos/v2"
	m "go.mongodb.org/mongo-driver/v2/mongo"
	"google.golang.org/grpc"
	auth "iwut-app-center/internal/adapter/auth"
	"iwut-app-center/internal/adapter/generator"
	mongo "iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/adapter/transport"
	"iwut-app-center/internal/config"
	u "iwut-app-center/internal/ownerexit/usecase"
	"log/slog"
	"sync"
	"time"
)

type ownerExitWorker struct {
	handlers *u.Handlers
	enabled  bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func provideOwnerExitHandlers(db *m.Database, conn *grpc.ClientConn) *u.Handlers {
	return u.NewHandlers(mongo.NewAccountOwnerExitRepository(db), generator.NewSystemClock(), generator.NewUUIDv7Generator(), auth.NewAccountOwnerExitDecisions(conn))
}
func provideOwnerExitWorker(c config.Config, h *u.Handlers) (*ownerExitWorker, error) {
	if c.OwnerExitEnabled {
		required := map[string]bool{"app.account-owner-exit.prepare": false, "app.account-owner-exit.finish": false, "app.account-owner-exit.read": false}
		for _, caller := range c.ServiceCallers {
			if caller.ServiceID == "iwut-auth-center" && caller.Status == "ACTIVE" {
				for _, p := range caller.Permissions {
					if _, ok := required[p]; ok {
						required[p] = true
					}
				}
			}
		}
		for p, ok := range required {
			if !ok {
				return nil, fmt.Errorf("account owner exit requires Auth caller permission %s", p)
			}
		}
	}
	return &ownerExitWorker{handlers: h, enabled: c.OwnerExitEnabled}, nil
}
func (w *ownerExitWorker) Start(context.Context) error {
	if !w.enabled {
		return nil
	}
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
				if err := w.handlers.Reconcile(ctx); err != nil && ctx.Err() == nil {
					slog.Error("account owner exit reconciliation unavailable")
				}
			}
		}
	}()
	return nil
}
func (w *ownerExitWorker) Stop(context.Context) error {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	return nil
}
func provideAppWithOwnerExit(servers *transport.Servers, w *ownerExitWorker) *kratos.App {
	if w.enabled {
		pb.RegisterAccountOwnerExitServiceServer(servers.GRPC, transport.NewAccountOwnerExitService(w.handlers))
	}
	return kratos.New(kratos.Name("iwut-app-center"), kratos.Server(servers.HTTP, servers.GRPC, w))
}
