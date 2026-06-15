package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

const (
	// maxListLimit caps unbounded list endpoints (Related, Communities, Community,
	// Hyperedges, FileImports) to avoid full-table response DoS.
	maxListLimit     = 500
	defaultListLimit = 100
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

// depthParam reads the optional "depth" query param. Missing or empty returns
// (0, true) which the service interprets as its default. A non-empty value
// that is not a non-negative integer returns (0, false) and writes 400.
func depthParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	return parsePosInt(w, r, "depth")
}

// limitParam reads the optional "limit" query param. Missing or empty returns
// (0, true) which callers treat as "use default". A non-empty value that is
// not a non-negative integer returns (0, false) and writes 400.
func limitParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	return parsePosInt(w, r, "limit")
}

// parsePosInt reads a named query param as a non-negative integer. Missing or
// empty returns (0, true). Non-empty but unparseable or negative writes 400
// and returns (0, false).
func parsePosInt(w http.ResponseWriter, r *http.Request, key string) (int, bool) {
	s := r.URL.Query().Get(key)
	if s == "" {
		return 0, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		WriteError(w, http.StatusBadRequest, key+" must be a non-negative integer", RequestIDFromContext(r.Context()))
		return 0, false
	}
	return n, true
}

// clampListLimit applies the default when n==0 and caps at maxListLimit.
func clampListLimit(n int) int {
	if n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}

// parseMinConfidence parses the optional "min_confidence" query param.
// Returns (0, true) if absent. Writes 400 and returns false if present but
// invalid.
func parseMinConfidence(w http.ResponseWriter, r *http.Request) (float64, bool) {
	s := r.URL.Query().Get("min_confidence")
	if s == "" {
		return 0, true
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "min_confidence must be a number", RequestIDFromContext(r.Context()))
		return 0, false
	}
	if v < 0 || v > 1 {
		WriteError(w, http.StatusBadRequest, "min_confidence must be between 0 and 1", RequestIDFromContext(r.Context()))
		return 0, false
	}
	return v, true
}

// confidenceFilter parses optional min_confidence and tier query params.
// Returns 400 and false if either is present but invalid.
func confidenceFilter(w http.ResponseWriter, r *http.Request) (codegraph.ConfidenceFilter, bool) {
	cf := codegraph.ConfidenceFilter{}
	v, ok := parseMinConfidence(w, r)
	if !ok {
		return codegraph.ConfidenceFilter{}, false
	}
	cf.MinConfidence = v
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
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteError(w, http.StatusBadRequest, "q query parameter required", RequestIDFromContext(r.Context()))
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		es, err := cfg.CodeGraph.Search(r.Context(), repo, q, r.URL.Query().Get("type"), limit)
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
		depth, ok := depthParam(w, r)
		if !ok {
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Neighbors(r.Context(), repo, id, strings.Split(rel, ","), r.URL.Query().Get("direction"), depth, limit, cf)
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
		depth, ok := depthParam(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Callers(r.Context(), repo, id, depth, cf)
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
		depth, ok := depthParam(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Callees(r.Context(), repo, id, depth, cf)
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
		depth, ok := depthParam(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Dependents(r.Context(), repo, id, depth, cf)
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
		depth, ok := depthParam(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.Dependencies(r.Context(), repo, id, depth, cf)
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
		depth, ok := depthParam(w, r)
		if !ok {
			return
		}
		nodes, err := cfg.CodeGraph.ResourceGraph(r.Context(), repo, id, depth, cf)
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
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		edges, err := cfg.CodeGraph.FileImports(r.Context(), repo, path)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if edges == nil {
			edges = []codegraph.Edge{}
		}
		cap := clampListLimit(limit)
		if len(edges) > cap {
			edges = edges[:cap]
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
		maxDepth, ok := parsePosInt(w, r, "max_depth")
		if !ok {
			return
		}
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
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		by := r.URL.Query().Get("by")
		var entities []codegraph.EntityDegree
		var err error
		if by != "" {
			entities, err = cfg.CodeGraph.ImportantEntitiesBy(r.Context(), repo, by, limit)
		} else {
			entities, err = cfg.CodeGraph.ImportantEntities(r.Context(), repo, limit)
		}
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

func handleRelated(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		var relations []string
		if rel := r.URL.Query().Get("relations"); rel != "" {
			relations = strings.Split(rel, ",")
		}
		minConf, ok := parseMinConfidence(w, r)
		if !ok {
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		results, err := cfg.CodeGraph.Related(r.Context(), repo, id, relations, minConf)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if results == nil {
			results = []codegraph.RelatedResult{}
		}
		cap := clampListLimit(limit)
		if len(results) > cap {
			results = results[:cap]
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"related": results})
	}
}

func handleHyperedges(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		hes, err := cfg.CodeGraph.Hyperedges(r.Context(), repo, r.URL.Query().Get("entity"))
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if hes == nil {
			hes = []codegraph.Hyperedge{}
		}
		cap := clampListLimit(limit)
		if len(hes) > cap {
			hes = hes[:cap]
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"hyperedges": hes})
	}
}

func handleHyperedge(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		id, ok := reqIDParam(w, r)
		if !ok {
			return
		}
		he, err := cfg.CodeGraph.Hyperedge(r.Context(), repo, id)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, he)
	}
}

type semanticMissesRequest struct {
	Repo  string              `json:"repo"`
	Files []codegraph.FileSHA `json:"files"`
}

const maxSemanticMissesFiles = 1000

func handleSemanticMisses(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 4 MiB
		var req semanticMissesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if req.Repo == "" {
			WriteError(w, http.StatusBadRequest, "repo required", RequestIDFromContext(r.Context()))
			return
		}
		if len(req.Files) == 0 {
			WriteError(w, http.StatusBadRequest, "files must not be empty", RequestIDFromContext(r.Context()))
			return
		}
		if len(req.Files) > maxSemanticMissesFiles {
			WriteError(w, http.StatusBadRequest, "files exceeds maximum allowed count", RequestIDFromContext(r.Context()))
			return
		}
		misses, err := cfg.CodeGraph.SemanticMisses(r.Context(), req.Repo, req.Files)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if misses == nil {
			misses = []string{}
		}
		WriteJSON(w, http.StatusOK, misses)
	}
}

func handleCommunities(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		comms, err := cfg.CodeGraph.Communities(r.Context(), repo)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if comms == nil {
			comms = []codegraph.CommunityRow{}
		}
		cap := clampListLimit(limit)
		if len(comms) > cap {
			comms = comms[:cap]
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"communities": comms})
	}
}

func handleCommunity(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		cstr := r.URL.Query().Get("community")
		if cstr == "" {
			WriteError(w, http.StatusBadRequest, "community query parameter required", RequestIDFromContext(r.Context()))
			return
		}
		cid, err := strconv.Atoi(cstr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "community must be an integer", RequestIDFromContext(r.Context()))
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		members, err := cfg.CodeGraph.Community(r.Context(), repo, cid)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if members == nil {
			members = []codegraph.Entity{}
		}
		cap := clampListLimit(limit)
		if len(members) > cap {
			members = members[:cap]
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entities": members})
	}
}

func handleBridges(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		repo, ok := reqRepo(w, r)
		if !ok {
			return
		}
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
		bridges, err := cfg.CodeGraph.Bridges(r.Context(), repo, limit)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		if bridges == nil {
			bridges = []codegraph.Bridge{}
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"bridges": bridges})
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
		limit, ok := limitParam(w, r)
		if !ok {
			return
		}
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
