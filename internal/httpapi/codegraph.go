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
		nodes, err := cfg.CodeGraph.Neighbors(r.Context(), repo, id, strings.Split(rel, ","), r.URL.Query().Get("direction"), depthParam(r))
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
		nodes, err := cfg.CodeGraph.Callers(r.Context(), repo, id, depthParam(r))
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
		nodes, err := cfg.CodeGraph.Callees(r.Context(), repo, id, depthParam(r))
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
		nodes, err := cfg.CodeGraph.Dependents(r.Context(), repo, id, depthParam(r))
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
		nodes, err := cfg.CodeGraph.Dependencies(r.Context(), repo, id, depthParam(r))
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
		nodes, err := cfg.CodeGraph.ResourceGraph(r.Context(), repo, id, depthParam(r))
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
