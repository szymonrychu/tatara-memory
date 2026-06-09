package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

func handlePostCodeGraph(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p codegraph.GraphPush
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		res, err := cfg.CodeGraph.Push(r.Context(), p)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	}
}

func reqRepo(w http.ResponseWriter, r *http.Request) (string, bool) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		WriteError(w, http.StatusBadRequest, "repo query parameter required", RequestIDFromContext(r.Context()))
		return "", false
	}
	return repo, true
}

func reqIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.URL.Query().Get("id")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "id query parameter required", RequestIDFromContext(r.Context()))
		return "", false
	}
	return id, true
}

func depthParam(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("depth"))
	return n
}

// confidenceFilter parses optional min_confidence and tier query params.
// Returns 400 and false if tier is present but not a valid tier value.
func confidenceFilter(w http.ResponseWriter, r *http.Request) (codegraph.ConfidenceFilter, bool) {
	cf := codegraph.ConfidenceFilter{}
	if s := r.URL.Query().Get("min_confidence"); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "min_confidence must be a number", RequestIDFromContext(r.Context()))
			return codegraph.ConfidenceFilter{}, false
		}
		cf.MinConfidence = v
	}
	if t := r.URL.Query().Get("tier"); t != "" {
		if !codegraph.ValidTiers[t] {
			WriteError(w, http.StatusBadRequest, "tier must be EXTRACTED, INFERRED, or AMBIGUOUS", RequestIDFromContext(r.Context()))
			return codegraph.ConfidenceFilter{}, false
		}
		cf.Tier = t
	}
	return cf, true
}

func handleSearchCodeEntities(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		es, err := cfg.CodeGraph.Search(r.Context(), repo, r.URL.Query().Get("q"), r.URL.Query().Get("type"), limit)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entities": es})
	}
}

func handleGetCodeEntity(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		det, err := cfg.CodeGraph.Entity(r.Context(), repo, id)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, det)
	}
}

func writeNodes(w http.ResponseWriter, r *http.Request, nodes []codegraph.PathNode, err error) {
	if err != nil {
		mapServiceError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"nodes": nodes})
}

func handleNeighbors(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		rel := r.URL.Query().Get("relation")
		if rel == "" {
			WriteError(w, http.StatusBadRequest, "relation query parameter required", RequestIDFromContext(r.Context()))
			return
		}
		cf, ok := confidenceFilter(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Neighbors(r.Context(), repo, id, strings.Split(rel, ","), r.URL.Query().Get("direction"), depthParam(r), cf)
		writeNodes(w, r, nodes, err)
	}
}

func handleCallers(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		cf, ok := confidenceFilter(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Callers(r.Context(), repo, id, depthParam(r), cf)
		writeNodes(w, r, nodes, err)
	}
}

func handleCallees(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		cf, ok := confidenceFilter(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Callees(r.Context(), repo, id, depthParam(r), cf)
		writeNodes(w, r, nodes, err)
	}
}

func handleDependents(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		cf, ok := confidenceFilter(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Dependents(r.Context(), repo, id, depthParam(r), cf)
		writeNodes(w, r, nodes, err)
	}
}

func handleDependencies(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		cf, ok := confidenceFilter(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Dependencies(r.Context(), repo, id, depthParam(r), cf)
		writeNodes(w, r, nodes, err)
	}
}

func handleResourceGraph(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		cf, ok := confidenceFilter(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.ResourceGraph(r.Context(), repo, id, depthParam(r), cf)
		writeNodes(w, r, nodes, err)
	}
}

func handleFileImports(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			WriteError(w, http.StatusBadRequest, "path query parameter required", RequestIDFromContext(r.Context()))
			return
		}
		edges, err := cfg.CodeGraph.FileImports(r.Context(), repo, path)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"edges": edges})
	}
}

func handleCrossRepo(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		links, err := cfg.CodeGraph.CrossRepo(r.Context(), repo, id)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, links)
	}
}

func handleShortestPath(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		from := r.URL.Query().Get("from")
		if from == "" {
			WriteError(w, http.StatusBadRequest, "from query parameter required", RequestIDFromContext(r.Context()))
			return
		}
		to := r.URL.Query().Get("to")
		if to == "" {
			WriteError(w, http.StatusBadRequest, "to query parameter required", RequestIDFromContext(r.Context()))
			return
		}
		var relations []string
		if rel := r.URL.Query().Get("relations"); rel != "" {
			relations = strings.Split(rel, ",")
		}
		maxDepth, _ := strconv.Atoi(r.URL.Query().Get("max_depth"))
		chain, err := cfg.CodeGraph.ShortestPath(r.Context(), repo, from, to, relations, maxDepth)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if chain == nil {
			chain = []codegraph.Entity{}
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"path": chain})
	}
}

func handleImportantEntities(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		entities, err := cfg.CodeGraph.ImportantEntities(r.Context(), repo, limit)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if entities == nil {
			entities = []codegraph.EntityDegree{}
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entities": entities})
	}
}

func handleStats(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		stats, err := cfg.CodeGraph.Stats(r.Context(), repo)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, stats)
	}
}

func handleAmbiguousEdges(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		edges, err := cfg.CodeGraph.AmbiguousEdges(r.Context(), repo, limit)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if edges == nil {
			edges = []codegraph.Edge{}
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"edges": edges})
	}
}

func handleEntityExplain(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		ex, err := cfg.CodeGraph.EntityExplain(r.Context(), repo, id)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, ex)
	}
}
