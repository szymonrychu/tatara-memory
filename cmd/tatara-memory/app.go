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
	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
	"github.com/szymonrychu/tatara-memory/internal/obs"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// app holds all runtime dependencies for tatara-memory.
type app struct {
	log             *slog.Logger
	reg             *prometheus.Registry
	db              *sql.DB
	lrc             lightrag.Client
	pool            *ingest.Pool
	server          *http.Server
	reaper          *memory.Reaper
	reaperCancel    context.CancelFunc
	analyticsCancel context.CancelFunc
	stopOTL         func(context.Context) error
}

// shutdown drains the HTTP server, stops the ingest pool, closes the DB, and
// flushes OTLP spans. It caps the total wait at 30 s.
func (a *app) shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if a.analyticsCancel != nil {
		a.analyticsCancel()
	}
	if a.reaperCancel != nil {
		a.reaperCancel()
	}
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

// migrate applies all embedded schema migrations. It is idempotent
// (CREATE TABLE IF NOT EXISTS) and runs at startup before serving.
func (a *app) migrate(ctx context.Context) error {
	if err := ingest.Migrate(ctx, a.db); err != nil {
		return fmt.Errorf("migrate ingest schema: %w", err)
	}
	if err := memory.Migrate(ctx, a.db); err != nil {
		return fmt.Errorf("migrate memory schema: %w", err)
	}
	if err := codegraph.Migrate(ctx, a.db); err != nil {
		return fmt.Errorf("migrate codegraph schema: %w", err)
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

// waitForDB retries ping until it succeeds or timeout elapses.
// A transient postgres restart retries every interval instead of aborting startup.
func waitForDB(ctx context.Context, ping func(context.Context) error, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ping(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("database not reachable within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
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

	if err := waitForDB(ctx, db.PingContext, 60*time.Second, 2*time.Second); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("wait for postgres: %w", err)
	}

	lrc, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  cfg.LightRAGBaseURL,
		Logger:   logger,
		Registry: reg,
	})
	if err != nil {
		return nil, err
	}

	store := ingest.NewPGStore(db)
	tomb := memory.NewTombstoneStore(db)
	srcStore := memory.NewSourceStore(db)
	memSvc := memory.NewServiceWithSources(lrc, tomb, srcStore)
	pool := ingest.NewPoolWithSources(store, memSvc, cfg.WorkerPoolSize, srcStore)
	pool.Start(ctx)
	if n, err := pool.Resume(ctx); err != nil {
		logger.Warn("ingest pool resume failed", "error", err)
	} else if n > 0 {
		logger.Info("ingest pool resumed unfinished jobs", "jobs", n)
	}

	enqueuer := ingest.NewEnqueuer(store, pool)

	cgStore := codegraph.NewPGStore(db)
	cgMetrics := codegraph.NewMetrics(reg)
	cgSvc := codegraph.NewService(cgStore, cgMetrics)

	// Build community labeler; nil when OPENAI_API_KEY is unset so the worker
	// falls back to the first-member-name heuristic.
	var labeler codegraph.CommunityLabeler
	if l := codegraph.NewOpenAILabelerFromEnv(); l != nil {
		labeler = l
	}
	analyticsCtx, analyticsCancel := context.WithCancel(context.Background())
	analyticsWorker := codegraph.NewAnalyticsWorker(cgStore, labeler, codegraph.AnalyticsWorkerConfig{
		Logger: logger,
	})
	go analyticsWorker.Run(analyticsCtx)

	verifier, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   cfg.OIDCIssuer,
		Audience: cfg.OIDCAudience,
	})
	if err != nil {
		analyticsCancel()
		return nil, err
	}

	reaper := memory.NewReaper(tomb, lrc, logger, reg)
	tomb.SetMarkCounter(reaper.IncCreated)
	reaperCtx, reaperCancel := context.WithCancel(context.Background())
	go reaper.Run(reaperCtx)

	readyFn := readyzFunc(db, lrc)
	router := httpapi.NewRouter(httpapi.Config{
		Service:    memSvc,
		Ingest:     enqueuer,
		CodeGraph:  cgSvc,
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
		log:             logger,
		reg:             reg,
		db:              db,
		lrc:             lrc,
		pool:            pool,
		server:          srv,
		reaper:          reaper,
		reaperCancel:    reaperCancel,
		analyticsCancel: analyticsCancel,
		stopOTL:         stop,
	}, nil
}

// newApp wires the application with the real Postgres driver.
func newApp(ctx context.Context, cfg config) (*app, error) {
	return newAppWithDeps(ctx, cfg, realDeps{})
}

// realDeps satisfies dbOpener using the pgx driver.
type realDeps struct{}

func (realDeps) openDB(dsn string) (*sql.DB, error) { return openDB(dsn) }
