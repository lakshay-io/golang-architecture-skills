// Command server is the process entrypoint. It owns only what a process owns:
// signals, config loading and the exit code. Everything else is built in
// internal/app, so the whole service can also be started from tests.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"example.com/goservice/internal/app"
	"example.com/goservice/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// SIGTERM is what Kubernetes, ECS and systemd send; SIGINT is Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	a, err := app.New(cfg)
	if err != nil {
		return fmt.Errorf("build app: %w", err)
	}
	return a.Run(ctx)
}
