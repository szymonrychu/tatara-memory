package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/auth"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
	"github.com/szymonrychu/tatara-memory/internal/obs"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// app holds all runtime dependencies for tatara-memory.
type app struct {
	log     *slog.Logger
	reg     *prometheus.Registry
	db      *sql.DB
	lrc     lightrag.Client
	pool    *ingest.Pool
	server  *http.Server
	stopOTL func(context.Context) error
}

// shutdown drains the HTTP server, stops the ingest pool, closes the DB, and
// flushes OTLP spans. It caps the total wait at 30 s.
func (a *app) shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if a.server != nil {
		_ = a.server.Shutdown(shutdownCtx)
	}
	if a.pool != nil {
		a.pool.Stop()
	}
	if a.db != nil {
		_ = a.db.Close()
	}
	if a.stopOTL != nil {
		_ = a.stopOTL(shutdownCtx)
	}
	return nil
}

// buildObs initialises the logger, Prometheus registry, and tracer provider.
// When cfg.OTLPEndpoint is empty, a noop tracer is used.
func buildObs(ctx context.Context, cfg config) (*slog.Logger, *prometheus.Registry, func(context.Context) error, error) {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	logger := obs.NewLogger(os.Stdout, level)
	reg := obs.PromRegistry()
	_, stop, err := obs.TracerProvider(ctx, cfg.OTLPEndpoint, "tatara-memory")
	if err != nil {
		return nil, nil, nil, err
	}
	return logger, reg, stop, nil
}

// openDB opens a pgx-backed *sql.DB with conservative connection limits.
func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	return db, nil
}

// dbOpener is the interface used by newAppWithDeps to allow test injection.
type dbOpener interface {
	openDB(string) (*sql.DB, error)
}

// newAppWithDeps wires all layers and returns a ready-to-serve app. deps is
// injectable so tests can substitute a fake DB without a real Postgres.
func newAppWithDeps(ctx context.Context, cfg config, d dbOpener) (*app, error) {
	logger, reg, stop, err := buildObs(ctx, cfg)
	if err != nil {
		return nil, err
	}

	db, err := d.openDB(cfg.PGDSN)
	if err != nil {
		return nil, err
	}

	lrc, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  cfg.LightRAGBaseURL,
		Logger:   logger,
		Registry: reg,
	})
	if err != nil {
		return nil, err
	}

	if err := ingest.Migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("ingest migrate: %w", err)
	}
	if err := memory.Migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("memory migrate: %w", err)
	}

	store := ingest.NewPGStore(db)
	tomb := memory.NewTombstoneStore(db)
	memSvc := memory.NewService(lrc, tomb)
	pool := ingest.NewPool(store, memSvc, cfg.WorkerPoolSize)
	pool.Start(ctx)

	enqueuer := ingest.NewEnqueuer(store)

	verifier, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   cfg.OIDCIssuer,
		Audience: cfg.OIDCAudience,
	})
	if err != nil {
		return nil, err
	}

	readyFn := readyzFunc(db, lrc)
	router := httpapi.NewRouter(httpapi.Config{
		Service:    memSvc,
		Ingest:     enqueuer,
		Verify:     auth.Middleware(verifier),
		Logger:     logger,
		Registry:   reg,
		ReadyCheck: readyFn,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &app{
		log:     logger,
		reg:     reg,
		db:      db,
		lrc:     lrc,
		pool:    pool,
		server:  srv,
		stopOTL: stop,
	}, nil
}

// newApp wires the application with the real Postgres driver.
func newApp(ctx context.Context, cfg config) (*app, error) {
	return newAppWithDeps(ctx, cfg, realDeps{})
}

// realDeps satisfies dbOpener using the pgx driver.
type realDeps struct{}

func (realDeps) openDB(dsn string) (*sql.DB, error) { return openDB(dsn) }
