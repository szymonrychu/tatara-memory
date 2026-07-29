package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

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

	// Admission control for the two bulk write routes. Each is its own budget
	// so a /code-graph:bulk burst cannot consume the slots (and, through them,
	// the shared Postgres connections) that /memories:bulk needs. 0 disables a
	// budget; both default to 0 so callers that do not opt in are unaffected.
	MemoriesBulkMaxInFlight  int
	CodeGraphBulkMaxInFlight int
	// AdmissionWait is how long a bulk request may queue for a slot before it
	// is shed with 429. AdmissionRetryAfter is the base Retry-After advertised
	// on a shed response (jittered per response).
	AdmissionWait       time.Duration
	AdmissionRetryAfter time.Duration
}

// Admission-control class names, used as the metric/log label for each budget.
const (
	classMemoriesBulk  = "memories_bulk"
	classCodeGraphBulk = "code_graph_bulk"
)

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
	r.Use(WithLogger(cfg.Logger))
	r.Use(RecoverWithLogger(cfg.Logger, metrics.PanicCounter()))
	r.Use(AccessLog(cfg.Logger))
	r.Use(metrics.Middleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if cfg.ReadyCheck != nil {
			if err := cfg.ReadyCheck(req.Context()); err != nil {
				cfg.Logger.WarnContext(req.Context(), "readyz failed", "error", err, "request_id", RequestIDFromContext(req.Context()))
				WriteError(w, http.StatusServiceUnavailable, "not ready", RequestIDFromContext(req.Context()))
				return
			}
		}
		w.WriteHeader(200)
	})
	r.Handle("/metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{}))

	adm := NewAdmissionMetrics(cfg.Registry, classMemoriesBulk, classCodeGraphBulk)
	limiters := bulkLimiters{
		memories: NewAdmissionLimiter(AdmissionConfig{
			Class:       classMemoriesBulk,
			MaxInFlight: cfg.MemoriesBulkMaxInFlight,
			Wait:        cfg.AdmissionWait,
			RetryAfter:  cfg.AdmissionRetryAfter,
			Logger:      cfg.Logger,
			Metrics:     adm,
		}),
		codeGraph: NewAdmissionLimiter(AdmissionConfig{
			Class:       classCodeGraphBulk,
			MaxInFlight: cfg.CodeGraphBulkMaxInFlight,
			Wait:        cfg.AdmissionWait,
			RetryAfter:  cfg.AdmissionRetryAfter,
			Logger:      cfg.Logger,
			Metrics:     adm,
		}),
	}

	r.Group(func(r chi.Router) {
		if cfg.Verify != nil {
			r.Use(cfg.Verify)
		}
		mountV1(r, cfg, limiters)
	})
	return r
}

// bulkLimiters carries the per-route admission budgets into mountV1.
type bulkLimiters struct {
	memories  *AdmissionLimiter
	codeGraph *AdmissionLimiter
}

func mountV1(r chi.Router, cfg Config, lim bulkLimiters) {
	r.Post("/memories", handlePostMemory(cfg))
	r.Get("/memories/{id}", handleGetMemory(cfg))
	r.Delete("/memories/{id}", handleDeleteMemory(cfg))

	if cfg.Ingest != nil {
		r.With(lim.memories.Middleware).Post("/memories:bulk", handleBulkIngest(cfg))
		r.Get("/ingest-jobs/{id}", handleGetJob(cfg))
	}

	r.Post("/queries", handlePostQuery(cfg))
	r.Post("/queries:data", handlePostQueryData(cfg))
	r.Post("/queries:describe", handlePostQueryDescribe(cfg))

	r.Get("/entities", handleSearchEntities(cfg))
	r.Get("/entities/{id}", handleGetEntity(cfg))
	r.Patch("/entities/{id}", handlePatchEntity(cfg))

	r.Get("/edges", handleListEdges(cfg))
	r.Post("/edges", handleCreateEdge(cfg))
	r.Delete("/edges/{id}", handleDeleteEdge(cfg))

	if cfg.CodeGraph != nil {
		r.With(lim.codeGraph.Middleware).Post("/code-graph:bulk", handlePostCodeGraph(cfg))
		r.Get("/code/entities", handleSearchCodeEntities(cfg))
		r.Get("/code/entity", handleGetCodeEntity(cfg))
		r.Get("/code/neighbors", handleNeighbors(cfg))
		r.Get("/code/callers", handleCallers(cfg))
		r.Get("/code/callees", handleCallees(cfg))
		r.Get("/code/dependents", handleDependents(cfg))
		r.Get("/code/dependencies", handleDependencies(cfg))
		r.Get("/code/resource-graph", handleResourceGraph(cfg))
		r.Get("/code/file-imports", handleFileImports(cfg))
		r.Get("/code/cross-repo", handleCrossRepo(cfg))
		r.Get("/code-graph/path", handleShortestPath(cfg))
		r.Get("/code-graph/important", handleImportantEntities(cfg))
		r.Get("/code-graph/stats", handleStats(cfg))
		r.Get("/code-graph/ambiguous", handleAmbiguousEdges(cfg))
		r.Get("/code-graph/explain", handleEntityExplain(cfg))
		r.Get("/code-graph/related", handleRelated(cfg))
		r.Get("/code-graph/hyperedges", handleHyperedges(cfg))
		r.Get("/code-graph/hyperedge", handleHyperedge(cfg))
		r.Post("/code-graph/semantic-misses", handleSemanticMisses(cfg))
		r.Get("/code-graph/communities", handleCommunities(cfg))
		r.Get("/code-graph/community", handleCommunity(cfg))
		r.Get("/code-graph/bridges", handleBridges(cfg))
	}
}
