package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/szymonrychu/tatara-memory/internal/version"
)

// waitForSignal blocks until SIGTERM or SIGINT is received, or ctx is cancelled.
func waitForSignal(ctx context.Context) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(ch)
	select {
	case <-ch:
	case <-ctx.Done():
	}
	return nil
}

func run(ctx context.Context, args []string) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	a, err := newApp(ctx, cfg)
	if err != nil {
		return err
	}
	if err := a.migrate(ctx); err != nil {
		return err
	}
	a.log.Info("starting", "action", "service_start", "version", version.Version, "addr", cfg.HTTPAddr)

	ln, err := newListener(cfg.HTTPAddr)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- serve(a.server, ln) }()

	done := make(chan struct{})
	go func() {
		_ = waitForSignal(ctx)
		close(done)
	}()

	select {
	case err := <-errCh:
		_ = a.shutdown(context.Background())
		return err
	case <-done:
	}

	a.log.Info("shutdown signal received", "action", "service_shutdown")
	return a.shutdown(context.Background())
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
