package main

import (
	"context"
	"fmt"
	"log/slog"
	"os/signal"
	"syscall"

	mongoadapter "iwut-app-center/internal/adapter/mongo"
	"iwut-app-center/internal/config"
)

// runMigrate loads only Mongo configuration, applies every pending migration
// and exits. It never loads identity or server settings, and the serve command
// never migrates automatically.
func runMigrate() error {
	configuration, err := config.LoadMongoFromEnvironment()
	if err != nil {
		return fmt.Errorf("load mongo configuration: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := mongoadapter.NewClient(configuration.URI)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), mongoShutdownTimeout)
		defer cancel()
		_ = client.Disconnect(shutdownCtx)
	}()
	if err := mongoadapter.VerifyTransactionTopology(ctx, client); err != nil {
		return err
	}

	database, err := mongoadapter.NewDatabase(client, configuration.Database)
	if err != nil {
		return err
	}
	if err := mongoadapter.NewMigrator(database).Migrate(ctx); err != nil {
		return err
	}

	slog.Info("app-center migrations applied", "component", "migration")
	return nil
}
