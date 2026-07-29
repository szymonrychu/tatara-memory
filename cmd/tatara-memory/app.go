package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/szymonrychu/tatara-memory/internal/auth"
	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
	"github.com/szymonrychu/tatara-memory/internal/obs"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
}

// shutdown drains the HTTP server, stops the ingest pool, and closes the DB.
// Caps the total wait at 30 s.
func (a *app) shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if a.analyticsCancel != nil {
		a.analyticsCancel()
	}
	if a.reaperCancel != nil {
		a.reaperCancel()
	}
	var errs []error
	if a.server != nil {
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("server shutdown: %w", err))
		}
	}
	if a.pool != nil {
		a.pool.Stop()
	}
	if a.db != nil {
		if err := a.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("db close: %w", err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		a.log.Warn("shutdown completed with errors", "error", err)
		return err
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

// buildObs initialises the logger and Prometheus registry.
func buildObs(_ context.Context, cfg config) (*slog.Logger, *prometheus.Registry, error) {
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
	slog.SetDefault(logger)
	reg := obs.PromRegistry()
	return logger, reg, nil
}

// openDB opens a pgx-backed *sql.DB carrying the server-side session timeouts
// from cfg. Pool limits and connection lifetimes are NOT set here: they are
// applied by newAppWithDeps from config, so they land in exactly one place
// regardless of which dbOpener produced the handle.
//
// The session timeouts exist because of tatara-memory#89, where
// `tatara_memory` backends on mem-mtg-pg grew monotonically from 4 to 87 over
// 5.5h and never came back, exhausting all 97 non-superuser connection slots
// (SQLSTATE 53300) and taking the project's memory API down for 13h+.
// statement_timeout / idle_in_transaction_session_timeout are the server-side
// half of the connection-hold guarantee, and the only half that still works
// when the client is gone entirely: a Postgres backend does not notice a client
// disconnect while it is mid-statement, so without these a backend orphaned by
// a client-side close survives indefinitely - exactly the state the incident
// found (84 backends in state=active, waiting=0, oldest transaction age growing
// 1s per 1s of wall clock).
//
// Defaults are deliberately generous rather than tight: /code-graph:bulk's
// single Reconcile transaction legitimately runs 50-194s+ on a large repo, so
// these are backstops against pathology, not latency budgets.
func openDB(dsn string, cfg config) (*sql.DB, error) {
	connCfg, err := pgConnConfig(dsn, cfg)
	if err != nil {
		return nil, err
	}
	return stdlib.OpenDB(*connCfg), nil
}

// pgConnConfig parses the DSN and layers the server-side session timeouts onto
// it as startup RuntimeParams. An explicit setting already in the DSN always
// wins: the deploying cluster is entitled to override these without a rebuild.
func pgConnConfig(dsn string, cfg config) (*pgx.ConnConfig, error) {
	connCfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	if connCfg.RuntimeParams == nil {
		connCfg.RuntimeParams = map[string]string{}
	}
	if cfg.PGStatementTimeout > 0 {
		if _, ok := connCfg.RuntimeParams["statement_timeout"]; !ok {
			connCfg.RuntimeParams["statement_timeout"] = pgTimeoutMillis(cfg.PGStatementTimeout)
		}
	}
	if cfg.PGIdleInTxTimeout > 0 {
		if _, ok := connCfg.RuntimeParams["idle_in_transaction_session_timeout"]; !ok {
			connCfg.RuntimeParams["idle_in_transaction_session_timeout"] = pgTimeoutMillis(cfg.PGIdleInTxTimeout)
		}
	}
	return connCfg, nil
}

// pgTimeoutMillis renders a duration as the integer millisecond string Postgres
// expects for statement_timeout / idle_in_transaction_session_timeout.
func pgTimeoutMillis(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
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
	logger, reg, err := buildObs(ctx, cfg)
	if err != nil {
		return nil, err
	}

	db, err := d.openDB(cfg.PGDSN)
	if err != nil {
		return nil, err
	}

	// One pool serves everything that touches Postgres: both bulk write routes,
	// every /code/* read, the ingest worker pool, the analytics worker and
	// /readyz. Its size is the budget the admission-control limits are sized
	// against (config.validate enforces that relationship). The previous
	// hard-coded 10 was too small for that budget - 4 ingest workers alone can
	// hold 4 of it, and a single /code-graph:bulk reconcile holds one
	// connection for the whole 50-194s of its transaction (tatara-memory#82),
	// while #87 measured Postgres itself healthy at 44/100 connections during a
	// shedding burst, i.e. the binding constraint was this client-side cap and
	// not the server. Non-positive values leave database/sql's own defaults in
	// place, which is what the fake-DB unit tests rely on.
	if cfg.DBMaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	}
	if cfg.DBMaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	}

	// Bounded connection lifetimes, the client-side half of the same guarantee
	// (tatara-memory#89). Without them a pooled connection is reused forever, so
	// a client-side connection whose server-side backend has become wedged is
	// never recycled. A lifetime bound means every backend this process owns is
	// torn down and re-established on a fixed cadence, which is the only
	// client-side mechanism that puts a ceiling on how long any one backend can
	// be held. 0 means unlimited, database/sql's own default, so both are safe
	// to apply unconditionally.
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.DBConnMaxIdleTime)

	// Pool saturation had no signal at all before tatara-memory#89: the incident
	// had to be reconstructed from the database side (cnpg_backends_total) because
	// this process published nothing about its own pool. This exports
	// go_sql_{open,in_use,idle,max_open}_connections plus the wait counters, so
	// "the pool is full and callers are queuing" is visible here, ~10 minutes
	// after onset rather than 6h later at the ceiling.
	reg.MustRegister(collectors.NewDBStatsCollector(db, "tatara_memory"))

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

	store := ingest.NewPGStore(db, ingest.WithCreateJobTimeout(cfg.IngestCreateJobTimeout))
	tomb := memory.NewTombstoneStore(db)
	srcStore := memory.NewSourceStore(db)
	memSvc := memory.NewServiceWithSources(lrc, tomb, srcStore).WithLogger(logger).WithMetrics(reg)
	pool := ingest.NewPoolWithSources(store, memSvc, cfg.WorkerPoolSize, srcStore, ingest.WithItemTimeout(cfg.IngestItemTimeout), ingest.WithMetrics(reg), ingest.WithLogger(logger))
	pool.Start(ctx)
	if n, err := pool.Resume(ctx); err != nil {
		logger.Error("ingest pool resume failed", "error", err)
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
		Logger:              logger,
		Registerer:          reg,
		BetweennessMaxNodes: cfg.BetweennessMaxNodes,
		MaxConcurrency:      cfg.AnalyticsMaxConcurrency,
		RecomputeTimeout:    cfg.AnalyticsRecomputeTimeout,
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
		Verify:     auth.MiddlewareWithMetricsAndLogger(verifier, reg, logger),
		Logger:     logger,
		Registry:   reg,
		ReadyCheck: readyFn,

		MemoriesBulkMaxInFlight:  cfg.MemoriesBulkMaxInFlight,
		CodeGraphBulkMaxInFlight: cfg.CodeGraphBulkMaxInFlight,
		AdmissionWait:            cfg.AdmissionWait,
		AdmissionRetryAfter:      cfg.AdmissionRetryAfter,
	})

	// WriteTimeout does NOT bound the handler: net/http arms it as a raw
	// SetWriteDeadline on the connection once headers are read, before the
	// handler runs (net/http/server.go's readRequest). It does not cancel
	// r.Context(), does not abort a parked handler, does not close the
	// connection while the handler is running, and emits no HTTP status -
	// it only makes the eventual response write fail once the deadline has
	// passed, at which point the connection is torn down. Had this been set
	// before tatara-memory#85/#86, the 18 hung requests would have behaved
	// identically server-side (same 300s park, same held pool slot, same
	// zero log lines); the only difference is the connection eventually
	// dropping instead of staying open forever. The actual, load-bearing
	// fix for that hang is ingest.WithCreateJobTimeout above, which fails
	// fast on DB pool exhaustion and returns a clean 503 well before this
	// fires. WriteTimeout here is only a best-effort backstop against a
	// connection whose response write genuinely never completes (e.g. a
	// client that stops reading); handlePostCodeGraph explicitly opts out
	// of it (see internal/httpapi/codegraph.go) because /code-graph:bulk's
	// legitimate single-transaction reconcile can run well past any value
	// chosen here.
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      cfg.HTTPWriteTimeout,
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
	}, nil
}

// newApp wires the application with the real Postgres driver.
func newApp(ctx context.Context, cfg config) (*app, error) {
	return newAppWithDeps(ctx, cfg, realDeps{cfg: cfg})
}

// realDeps satisfies dbOpener using the pgx driver. It carries the config so
// openDB can apply the pool and connection-lifetime settings without widening
// the dbOpener interface that tests implement.
type realDeps struct{ cfg config }

func (d realDeps) openDB(dsn string) (*sql.DB, error) { return openDB(dsn, d.cfg) }
