package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// ErrNotFound is returned when the requested memory, entity, or edge does not exist.
var ErrNotFound = errors.New("memory: not found")

// ErrUpstream is returned when the LightRAG backend returns an unexpected error.
var ErrUpstream = errors.New("memory: upstream error")

// ErrTransient is returned when the LightRAG backend is temporarily unavailable.
var ErrTransient = errors.New("memory: transient upstream error")

// ErrInvalid is returned when the caller supplies a malformed identifier or payload.
var ErrInvalid = errors.New("memory: invalid input")

// tombstoner is the minimal interface Service needs from TombstoneStore.
type tombstoner interface {
	Mark(ctx context.Context, trackID string) error
	IsDeleted(ctx context.Context, trackID string) (bool, error)
}

// sourceIndex is the minimal interface Service needs from SourceStore: list and
// purge the track_ids produced from a repo/file. May be nil (delete-by-source
// becomes a no-op returning 0).
type sourceIndex interface {
	TrackIDs(ctx context.Context, repo, filePath string) ([]string, error)
	DeleteByFile(ctx context.Context, repo, filePath string) (int64, error)
}

// Service provides memory CRUD and retrieval operations backed by LightRAG.
type Service struct {
	lr      lightrag.Client
	tomb    tombstoner
	sources sourceIndex
	now     func() time.Time
	log     *slog.Logger
	ops     *prometheus.CounterVec
}

// NewService returns a Service backed by the given LightRAG client.
// tomb may be nil; if nil, tombstone checks are skipped (no-op).
func NewService(lr lightrag.Client, tomb tombstoner) *Service {
	return &Service{lr: lr, tomb: tomb, now: time.Now, log: slog.Default(), ops: newServiceOps(nil)}
}

// NewServiceWithSources is NewService plus a sources index that backs
// DeleteMemoriesBySource. sources may be nil (delete-by-source is a no-op).
func NewServiceWithSources(lr lightrag.Client, tomb tombstoner, sources sourceIndex) *Service {
	return &Service{lr: lr, tomb: tomb, sources: sources, now: time.Now, log: slog.Default(), ops: newServiceOps(nil)}
}

// WithLogger sets the logger on the service (functional option on the pointer).
func (s *Service) WithLogger(l *slog.Logger) *Service { s.log = l; return s }

// WithMetrics registers tatara_memory_op_total{op,result} in reg.
func (s *Service) WithMetrics(reg prometheus.Registerer) *Service {
	s.ops = newServiceOps(reg)
	return s
}

func newServiceOps(reg prometheus.Registerer) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tatara_memory_op_total",
		Help: "Count of memory service operations by op and result.",
	}, []string{"op", "result"})
	if reg != nil {
		reg.MustRegister(c)
	}
	for _, op := range []string{"create", "delete", "delete_by_source", "patch_entity", "create_edge", "delete_edge", "delete_by_sources"} {
		for _, r := range []string{"success", "error"} {
			c.WithLabelValues(op, r)
		}
	}
	return c
}

func (s *Service) incOp(op string, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	s.ops.WithLabelValues(op, result).Inc()
}

func wrapUpstream(err error) error {
	if err == nil {
		return nil
	}
	var he *lightrag.HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == http.StatusNotFound:
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case he.Status >= 500:
			return fmt.Errorf("%w: %v", ErrTransient, err)
		default:
			return fmt.Errorf("%w: %v", ErrUpstream, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}

// CreateMemory submits m to LightRAG and returns it with track_id as ID.
// Ingest is asynchronous; the returned Memory's Text is what was submitted,
// not what LightRAG will eventually summarise.
func (s *Service) CreateMemory(ctx context.Context, m Memory) (Memory, error) {
	start := time.Now()
	resp, err := s.lr.InsertText(ctx, ToInsertText(m))
	if err != nil {
		s.incOp("create", err)
		return Memory{}, wrapUpstream(err)
	}
	// Treat a non-success status or an empty track_id as a logical upstream failure.
	// LightRAG may return HTTP 200 with status="failure" when ingest is rejected
	// (e.g. string_too_short). An empty track_id makes the returned Memory unusable.
	if resp.Status != "success" || resp.TrackID == "" {
		logicalErr := fmt.Errorf("%w: insert returned status=%q track_id=%q",
			ErrUpstream, resp.Status, resp.TrackID)
		s.incOp("create", logicalErr)
		return Memory{}, logicalErr
	}
	m.ID = resp.TrackID
	m.CreatedAt = s.now()
	s.incOp("create", nil)
	s.log.InfoContext(ctx, "memory.create",
		"action", "create_memory",
		"resource_id", m.ID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return m, nil
}

// GetMemory retrieves a memory by track_id.
// Returns a Memory derived from the first document associated with the track.
// LightRAG does not expose original document text; Text holds content_summary.
func (s *Service) GetMemory(ctx context.Context, trackID string) (Memory, error) {
	if s.tomb != nil {
		deleted, err := s.tomb.IsDeleted(ctx, trackID)
		if err != nil {
			return Memory{}, fmt.Errorf("tombstone check: %w", err)
		}
		if deleted {
			return Memory{}, ErrNotFound
		}
	}
	ts, err := s.lr.TrackStatus(ctx, trackID)
	if err != nil {
		return Memory{}, wrapUpstream(err)
	}
	if len(ts.Documents) == 0 {
		return Memory{}, fmt.Errorf("%w: track %s has no documents", ErrNotFound, trackID)
	}
	return FromDocStatus(trackID, ts.Documents[0]), nil
}

// DeleteMemory removes all documents associated with the given track_id.
func (s *Service) DeleteMemory(ctx context.Context, trackID string) error {
	start := time.Now()
	ts, err := s.lr.TrackStatus(ctx, trackID)
	if err != nil {
		s.incOp("delete", err)
		return wrapUpstream(err)
	}
	docIDs := make([]string, 0, len(ts.Documents))
	for _, d := range ts.Documents {
		docIDs = append(docIDs, d.ID)
	}
	if len(docIDs) == 0 {
		err := fmt.Errorf("%w: track %s has no documents", ErrNotFound, trackID)
		s.incOp("delete", err)
		return err
	}
	// Mark tombstone BEFORE the upstream delete so GET-after-DELETE returns
	// ErrNotFound immediately even if DeleteDocs succeeds but the caller retries.
	// If DeleteDocs subsequently fails the tombstone is still valid (logically
	// deleted) and the reaper's 24h TTL reconciles it.
	if s.tomb != nil {
		if err := s.tomb.Mark(ctx, trackID); err != nil {
			s.incOp("delete", err)
			return fmt.Errorf("tombstone: %w", err)
		}
	}
	if _, err := s.lr.DeleteDocs(ctx, lightrag.DeleteDocRequest{DocIDs: docIDs}); err != nil {
		s.incOp("delete", err)
		return wrapUpstream(err)
	}
	s.incOp("delete", nil)
	s.log.InfoContext(ctx, "memory.delete",
		"action", "delete_memory",
		"resource_id", trackID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// DeleteMemoriesBySources purges every memory produced from each file in files
// for the given repo, calling DeleteMemoriesBySource once per file. Returns the
// total count of track_ids purged across all files. A nil sources index is a
// no-op returning 0.
func (s *Service) DeleteMemoriesBySources(ctx context.Context, repo string, files []string) (int, error) {
	start := time.Now()
	total := 0
	for _, f := range files {
		n, err := s.DeleteMemoriesBySource(ctx, repo, f)
		if err != nil {
			s.incOp("delete_by_sources", err)
			return total, fmt.Errorf("delete memories for %s/%s: %w", repo, f, err)
		}
		total += n
	}
	s.incOp("delete_by_sources", nil)
	s.log.InfoContext(ctx, "memory.delete_by_sources",
		"action", "delete_memories_by_sources",
		"resource_id", repo,
		"repo", repo,
		"files_count", len(files),
		"total_purged", total,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return total, nil
}

// DeleteMemoriesBySource purges every memory produced from (repo, filePath):
// it deletes each indexed track_id via DeleteMemory (lightrag DeleteDocs +
// tombstone), then clears the source-index rows. Idempotent; returns the count
// of track_ids purged. A nil sources index is a no-op returning 0.
func (s *Service) DeleteMemoriesBySource(ctx context.Context, repo, filePath string) (int, error) {
	start := time.Now()
	if s.sources == nil {
		return 0, nil
	}
	ids, err := s.sources.TrackIDs(ctx, repo, filePath)
	if err != nil {
		s.incOp("delete_by_source", err)
		return 0, fmt.Errorf("source track_ids: %w", err)
	}
	purged := 0
	for _, id := range ids {
		if err := s.DeleteMemory(ctx, id); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // already gone upstream; index cleanup below still runs
			}
			s.incOp("delete_by_source", err)
			// Return purged (work done so far), not 0, so callers can account for
			// partial progress. The source-index is not cleaned up on mid-loop failure;
			// a retry will re-read the remaining ids (ErrNotFound entries are skipped).
			return purged, fmt.Errorf("delete memory %s for %s/%s: %w", id, repo, filePath, err)
		}
		purged++
	}
	if _, err := s.sources.DeleteByFile(ctx, repo, filePath); err != nil {
		s.incOp("delete_by_source", err)
		return purged, fmt.Errorf("purge source index %s/%s: %w", repo, filePath, err)
	}
	s.incOp("delete_by_source", nil)
	s.log.InfoContext(ctx, "memory.delete_by_source",
		"action", "delete_memories_by_source",
		"repo", repo,
		"file_path", filePath,
		"count", purged,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return purged, nil
}

func t(b bool) *bool { return &b }

// Query retrieves context references for the given query.
// LightRAG's /query returns references rather than ranked matches;
// Match.Score is zero in this mapping. include_references must be set
// or LightRAG omits the reference list entirely and Matches comes back
// empty even when only_need_context is true.
func (s *Service) Query(ctx context.Context, q Query) (QueryResult, error) {
	if !q.Mode.Valid() {
		return QueryResult{}, fmt.Errorf("invalid query mode: %s", q.Mode)
	}
	resp, err := s.lr.Query(ctx, lightrag.QueryRequest{
		Mode:              lightrag.QueryMode(q.Mode),
		Query:             q.Text,
		TopK:              q.TopK,
		OnlyNeedContext:   t(true),
		IncludeReferences: t(true),
		Stream:            t(false),
	})
	if err != nil {
		return QueryResult{}, wrapUpstream(err)
	}
	return QueryResultFromQuery(*resp), nil
}

// Describe returns a generative answer plus source file paths.
func (s *Service) Describe(ctx context.Context, q Query) (DescribeResult, error) {
	if !q.Mode.Valid() {
		return DescribeResult{}, fmt.Errorf("invalid query mode: %s", q.Mode)
	}
	resp, err := s.lr.Query(ctx, lightrag.QueryRequest{
		Mode:              lightrag.QueryMode(q.Mode),
		Query:             q.Text,
		TopK:              q.TopK,
		IncludeReferences: t(true),
		Stream:            t(false),
	})
	if err != nil {
		return DescribeResult{}, wrapUpstream(err)
	}
	return DescribeResultFromQuery(*resp), nil
}

// GetEntity retrieves an entity by name (Entity.ID == Entity.Name in this model).
func (s *Service) GetEntity(ctx context.Context, name string) (Entity, error) {
	g, err := s.lr.Graph(ctx, name, 1, 1)
	if err != nil {
		return Entity{}, wrapUpstream(err)
	}
	for _, n := range g.Nodes {
		if n.ID == name {
			return EntityFromGraphNode(n), nil
		}
	}
	return Entity{}, fmt.Errorf("%w: entity %s", ErrNotFound, name)
}

// SearchEntities returns entities matching q by label.
// Labels carry only names; other fields are zero.
func (s *Service) SearchEntities(ctx context.Context, q string) ([]Entity, error) {
	labels, err := s.lr.LabelSearch(ctx, q)
	if err != nil {
		return nil, wrapUpstream(err)
	}
	out := make([]Entity, 0, len(labels))
	for _, l := range labels {
		out = append(out, Entity{ID: l, Name: l})
	}
	return out, nil
}

// PatchEntity applies a partial update to the entity identified by name.
func (s *Service) PatchEntity(ctx context.Context, name string, patch Entity) (Entity, error) {
	start := time.Now()
	data := EntityUpdatePayload(patch)
	allowRename := patch.Name != "" && patch.Name != name
	_, err := s.lr.UpdateEntity(ctx, lightrag.EntityUpdateRequest{
		EntityName:  name,
		UpdatedData: data,
		AllowRename: allowRename,
	})
	if err != nil {
		s.incOp("patch_entity", err)
		return Entity{}, wrapUpstream(err)
	}
	final := name
	if allowRename {
		final = patch.Name
	}
	out, err := s.GetEntity(ctx, final)
	if err != nil {
		s.incOp("patch_entity", err)
		return Entity{}, err
	}
	s.incOp("patch_entity", nil)
	s.log.InfoContext(ctx, "memory.patch_entity",
		"action", "patch_entity",
		"resource_id", name,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return out, nil
}

// ListEdges returns all edges by iterating every known label and collecting its incident edges.
// See MEMORY.md for the O(N) graph-read trade-off rationale.
func (s *Service) ListEdges(ctx context.Context) ([]Edge, error) {
	labels, err := s.lr.LabelSearch(ctx, "")
	if err != nil {
		return nil, wrapUpstream(err)
	}
	seen := map[string]struct{}{}
	out := []Edge{}
	for _, l := range labels {
		g, err := s.lr.Graph(ctx, l, 1, 0)
		if err != nil {
			var he *lightrag.HTTPError
			if errors.As(err, &he) && he.Status == http.StatusNotFound {
				continue
			}
			return nil, wrapUpstream(err)
		}
		for _, e := range g.Edges {
			// Edges are directed: deduplicate only on (source, target) so that
			// A->B and B->A (distinct relations) both surface.
			id := EncodeEdgeID(e.Source, e.Target)
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, EdgeFromGraphEdge(e))
		}
	}
	return out, nil
}

// CreateEdge stores a new relation between two existing entities.
func (s *Service) CreateEdge(ctx context.Context, e Edge) (Edge, error) {
	start := time.Now()
	if _, err := s.lr.CreateRelation(ctx, RelationCreatePayload(e)); err != nil {
		s.incOp("create_edge", err)
		return Edge{}, wrapUpstream(err)
	}
	created := e
	created.ID = EncodeEdgeID(e.From, e.To)
	s.incOp("create_edge", nil)
	s.log.InfoContext(ctx, "memory.create_edge",
		"action", "create_edge",
		"resource_id", created.ID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return created, nil
}

// DeleteEdge removes an edge by opaque ID produced by EncodeEdgeID.
func (s *Service) DeleteEdge(ctx context.Context, id string) error {
	start := time.Now()
	from, to, err := DecodeEdgeID(id)
	if err != nil {
		s.incOp("delete_edge", err)
		return fmt.Errorf("%w: invalid edge id %q", ErrInvalid, id)
	}
	if err := s.lr.DeleteRelation(ctx, lightrag.DeleteRelationRequest{
		SourceEntity: from,
		TargetEntity: to,
	}); err != nil {
		s.incOp("delete_edge", err)
		return wrapUpstream(err)
	}
	s.incOp("delete_edge", nil)
	s.log.InfoContext(ctx, "memory.delete_edge",
		"action", "delete_edge",
		"resource_id", id,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}
