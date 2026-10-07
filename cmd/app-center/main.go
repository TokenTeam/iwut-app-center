package main

import (
	"fmt"
	"log/slog"
	"os"

	"iwut-app-center/internal/config"
)

const (
	commandServe   = "serve"
	commandMigrate = "migrate"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service.name", serviceName))
	command := commandServe
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case commandServe:
		if err := runServe(); err != nil {
			slog.Error("app-center serve failed", "error.type", "startup")
			os.Exit(1)
		}
	case commandMigrate:
		if err := runMigrate(); err != nil {
			slog.Error("app-center migrate failed", "error.type", "migration")
			os.Exit(1)
		}
	default:
		slog.Error("app-center command invalid", "command", command)
		os.Exit(2)
	}
}

func runServe() error {
	configuration, err := config.LoadFromEnvironment()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	app, cleanup, err := wireApp(configuration)
	if err != nil {
		return fmt.Errorf("build composition: %w", err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		return fmt.Errorf("run: %w", err)
	}
	return nil
}
