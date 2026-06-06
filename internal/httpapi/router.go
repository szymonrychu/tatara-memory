package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config holds all dependencies for the HTTP router.
type Config struct {
	Service    MemoryService
	Ingest     IngestService
	CodeGraph  CodeGraphService
	Verify     func(http.Handler) http.Handler // auth middleware; nil disables auth (tests only)
	Logger     *slog.Logger
	Registry   *prometheus.Registry
	ReadyCheck func(context.Context) error
}

// NewRouter builds and returns the chi router with the full middleware stack.
// Middleware order: request-id -> recoverer -> access-log -> metrics -> (auth on API routes).
// /healthz, /readyz, and /metrics are excluded from auth.
func NewRouter(cfg Config) *chi.Mux {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
	metrics := NewMetrics(cfg.Registry)

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(Recover)
	r.Use(AccessLog(cfg.Logger))
	r.Use(metrics.Middleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if cfg.ReadyCheck != nil {
			if err := cfg.ReadyCheck(req.Context()); err != nil {
				WriteError(w, http.StatusServiceUnavailable, "not ready", RequestIDFromContext(req.Context()))
				return
			}
		}
		w.WriteHeader(200)
	})
	r.Handle("/metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{}))

	r.Group(func(r chi.Router) {
		if cfg.Verify != nil {
			r.Use(cfg.Verify)
		}
		mountV1(r, cfg)
	})
	return r
}

func mountV1(r chi.Router, cfg Config) {
	r.Post("/memories", handlePostMemory(cfg))
	r.Get("/memories/{id}", handleGetMemory(cfg))
	r.Delete("/memories/{id}", handleDeleteMemory(cfg))

	if cfg.Ingest != nil {
		r.Post("/memories:bulk", handleBulkIngest(cfg))
		r.Get("/ingest-jobs/{id}", handleGetJob(cfg))
	}

	r.Post("/queries", handlePostQuery(cfg))
	r.Post("/queries:describe", handlePostQueryDescribe(cfg))

	r.Get("/entities", handleSearchEntities(cfg))
	r.Get("/entities/{id}", handleGetEntity(cfg))
	r.Patch("/entities/{id}", handlePatchEntity(cfg))

	r.Get("/edges", handleListEdges(cfg))
	r.Post("/edges", handleCreateEdge(cfg))
	r.Delete("/edges/{id}", handleDeleteEdge(cfg))

	if cfg.CodeGraph != nil {
		r.Post("/code-graph:bulk", handlePostCodeGraph(cfg))
		r.Get("/code/entities", handleSearchCodeEntities(cfg))
		r.Get("/code/entity", handleGetCodeEntity(cfg))
		r.Get("/code/neighbors", handleNeighbors(cfg))
		r.Get("/code/callers", handleCallers(cfg))
		r.Get("/code/callees", handleCallees(cfg))
		r.Get("/code/dependents", handleDependents(cfg))
		r.Get("/code/dependencies", handleDependencies(cfg))
		r.Get("/code/resource-graph", handleResourceGraph(cfg))
		r.Get("/code/file-imports", handleFileImports(cfg))
	}
}
