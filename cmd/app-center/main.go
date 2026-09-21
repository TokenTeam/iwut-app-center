package main

import (
	"fmt"
	"log"
	"os"

	"iwut-app-center/internal/config"
)

const (
	commandServe   = "serve"
	commandMigrate = "migrate"
)

func main() {
	command := commandServe
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case commandServe:
		if err := runServe(); err != nil {
			log.Fatalf("app-center serve: %v", err)
		}
	case commandMigrate:
		if err := runMigrate(); err != nil {
			log.Fatalf("app-center migrate: %v", err)
		}
	default:
		log.Fatalf("app-center: unknown command %q; want %q or %q", command, commandServe, commandMigrate)
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
