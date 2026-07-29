package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// ErrNotFound is returned when the requested memory, entity, or edge does not exist.
var ErrNotFound = errors.New("memory: not found")

// ErrUpstream is returned when the LightRAG backend returns an unexpected error.
var ErrUpstream = errors.New("memory: upstream error")

// ErrTransient is returned when the LightRAG backend is genuinely unavailable
// (5xx or unreachable). It maps to HTTP 503: a real server-side fault.
var ErrTransient = errors.New("memory: transient upstream error")

// ErrBusy is returned when an upstream refuses work because it is saturated
// rather than broken: LightRAG's pipeline lock is held (delete status="busy"),
// it answered 429, or a call ran out of its time budget under load. It maps to
// HTTP 429 + Retry-After, not 503 - the request is retryable and nothing is
// down, so it must not inflate the 5xx error ratio (tatara-memory#80).
var ErrBusy = errors.New("memory: upstream busy")

// ErrInvalid is returned when the caller supplies a malformed identifier or payload.
var ErrInvalid = errors.New("memory: invalid input")

// tombstoner is the minimal interface Service needs from TombstoneStore.
type tombstoner interface {
	Mark(ctx context.Context, trackID string) error
	Unmark(ctx context.Context, trackID string) error
	IsDeleted(ctx context.Context, trackID string) (bool, error)
}

// sourceIndex is the minimal interface Service needs from SourceStore: list and
// purge the track_ids produced from a repo/file. May be nil (delete-by-source
// becomes a no-op returning 0).
type sourceIndex interface {
	TrackIDs(ctx context.Context, repo, filePath string) ([]string, error)
	DeleteByFile(ctx context.Context, repo, filePath string) (int64, error)
}

// DefaultPurgeBudget bounds one DeleteMemoriesBySources call end to end.
//
// The purge is serial over reconcile files, and a LightRAG document delete
// returns immediately but holds the pipeline lock for the 6-42s its graph
// rebuild takes (41.85s worst case measured, tatara-memory#90 Q9). So file N+1
// waits out the lock file N just took: waiting per file is unbounded in the
// file count and would run a wide reconcile past any client timeout. One budget
// for the whole batch keeps the request bounded; whatever does not fit comes
// back as ErrBusy -> 429 + Retry-After with the completed files already cleared
// from the source index, so the client's retry resumes instead of restarting.
//
// 45s covers a single full lock hold (so no file is shed that a wait would have
// saved) and leaves headroom under tatara-memory-repo-ingester's 60s HTTP
// client default.
const DefaultPurgeBudget = 45 * time.Second

// Service provides memory CRUD and retrieval operations backed by LightRAG.
type Service struct {
	lr          lightrag.Client
	tomb        tombstoner
	sources     sourceIndex
	now         func() time.Time
	log         *slog.Logger
	ops         *prometheus.CounterVec
	purgeBudget time.Duration
}

// NewService returns a Service backed by the given LightRAG client.
// tomb may be nil; if nil, tombstone checks are skipped (no-op).
func NewService(lr lightrag.Client, tomb tombstoner) *Service {
	return &Service{lr: lr, tomb: tomb, now: time.Now, log: slog.Default(), ops: newServiceOps(nil), purgeBudget: DefaultPurgeBudget}
}

// NewServiceWithSources is NewService plus a sources index that backs
// DeleteMemoriesBySource. sources may be nil (delete-by-source is a no-op).
func NewServiceWithSources(lr lightrag.Client, tomb tombstoner, sources sourceIndex) *Service {
	return &Service{lr: lr, tomb: tomb, sources: sources, now: time.Now, log: slog.Default(), ops: newServiceOps(nil), purgeBudget: DefaultPurgeBudget}
}

// WithLogger sets the logger on the service (functional option on the pointer).
func (s *Service) WithLogger(l *slog.Logger) *Service { s.log = l; return s }

// WithPurgeBudget overrides DefaultPurgeBudget. 0 or less disables the bound,
// leaving DeleteMemoriesBySources governed only by the caller's context.
func (s *Service) WithPurgeBudget(d time.Duration) *Service { s.purgeBudget = d; return s }

// WithMetrics registers tatara_memory_op_total{op,class,result} in reg.
func (s *Service) WithMetrics(reg prometheus.Registerer) *Service {
	s.ops = newServiceOps(reg)
	return s
}

// opClassMaintenance are the internal purge/reconcile ops driven by automated
// repo reconciliation (tatara-memory-repo-ingester's reconcile_files path),
// not by a live agent read/write. Separated out (issue #84) so an op-error-
// ratio alert can scope itself to user-facing traffic: a burst of purge
// errors (e.g. LightRAG pipeline-lock contention during a concurrent ingest)
// must not read as user-facing impact when create/get/query were unaffected.
var opClassMaintenance = map[string]bool{
	"delete_by_source":  true,
	"delete_by_sources": true,
}

// opClass returns "maintenance" for internal purge ops and "user" for the
// rest of the op catalog (the live agent-facing CRUD/query surface).
func opClass(op string) string {
	if opClassMaintenance[op] {
		return "maintenance"
	}
	return "user"
}

func newServiceOps(reg prometheus.Registerer) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tatara_memory_op_total",
		Help: "Count of memory service operations by op, class, and result.",
	}, []string{"op", "class", "result"})
	if reg != nil {
		reg.MustRegister(c)
	}
	for _, op := range []string{"create", "delete", "delete_by_source", "patch_entity", "create_edge", "delete_edge", "delete_by_sources", "get", "query", "query_data", "describe", "get_entity", "search_entities", "list_edges"} {
		for _, r := range []string{"success", "error"} {
			c.WithLabelValues(op, opClass(op), r)
		}
	}
	return c
}

func (s *Service) incOp(op string, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	s.ops.WithLabelValues(op, opClass(op), result).Inc()
}

// logPurgeErr logs a purge-path (delete_by_source / delete_by_sources)
// failure with structured fields. Before this, a purge failure only ever
// reached the HTTP layer's mapServiceError, whose backpressure and ErrNotFound
// branches write a response but never call the logger - so the internal purge
// path could fail with zero log lines (issue #84).
//
// The level split follows the error taxonomy, not the call site: ErrBusy is
// expected, self-correcting backpressure (LightRAG's pipeline lock is held by a
// concurrent ingest or by this batch's own previous delete, or the purge budget
// ran out waiting for it), so it logs at WARN. Everything else - including
// ErrTransient - logs at ERROR.
//
// This predicate was ErrTransient when it was written, because ErrTransient was
// then the only non-permanent classification and carried the busy path.
// tatara-memory#97 split the taxonomy: ErrBusy is now "saturated, come back
// later" (429 + Retry-After) and ErrTransient is now reserved for genuine
// unavailability, an upstream 5xx or an unreachable LightRAG (503). Keeping the
// old predicate would have inverted the intent exactly - the one class that is
// routine under load would page at ERROR while a real upstream outage on the
// purge path stayed a WARN.
//
// The predicate deliberately matches mapServiceError's backpressure branch
// (ErrBusy OR a bare context.DeadlineExceeded) rather than just ErrBusy, so the
// level always agrees with the status the caller is handed: WARN iff 429. The
// bare-deadline case is reachable here - the batch purge budget can expire
// inside sources.TrackIDs/DeleteByFile, whose store errors never pass through
// wrapUpstream and so are never reclassified as ErrBusy.
func (s *Service) logPurgeErr(ctx context.Context, msg string, err error, args ...any) {
	args = append(args, "error", err)
	if errors.Is(err, ErrBusy) || errors.Is(err, context.DeadlineExceeded) {
		s.log.WarnContext(ctx, msg, args...)
		return
	}
	s.log.ErrorContext(ctx, msg, args...)
}

func wrapUpstream(err error) error {
	if err == nil {
		return nil
	}
	// A held LightRAG pipeline lock is backpressure, not an upstream failure:
	// LightRAG answers HTTP 200 in milliseconds throughout. Without this case it
	// falls through to ErrUpstream -> 502, which the ingest client treats as
	// permanent (tatara-memory#90, #91).
	//
	// ErrBusy, not ErrTransient: an exhausted purge budget means the work is
	// still doable, we just ran out of time waiting for the lock. The caller
	// should come back later, which is exactly what 429 + Retry-After says;
	// 503 would claim the service is unavailable when nothing is down and would
	// put pure load on the 5xx-ratio alert. Both clients honour Retry-After on
	// 429 (tatara-memory-repo-ingester#32, tatara-operator#484). This also keeps
	// the two busy paths coherent: the single-response `status="busy"` envelope
	// below already maps to ErrBusy (tatara-memory#80), so waiting the full 45s
	// for that same condition must not come back as a worse class of error.
	var busy *lightrag.BusyError
	if errors.As(err, &busy) {
		return fmt.Errorf("%w: %v", ErrBusy, err)
	}
	var he *lightrag.HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == http.StatusNotFound:
			return fmt.Errorf("%w: %v", ErrNotFound, err)
		case he.Status == http.StatusTooManyRequests:
			// Explicit upstream backpressure. Previously fell through to the
			// default branch and became a permanent 502.
			return fmt.Errorf("%w: %v", ErrBusy, err)
		case he.Status >= 500:
			return fmt.Errorf("%w: %v", ErrTransient, err)
		default:
			return fmt.Errorf("%w: %v", ErrUpstream, err)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Ran out of budget under load: retryable backpressure, not a fault.
		return fmt.Errorf("%w: %v", ErrBusy, err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}

// filterTombstonedRefs removes references whose ReferenceID is tombstoned, so
// Query and Describe do not surface content from logically-deleted memories.
func filterTombstonedRefs(ctx context.Context, tomb tombstoner, refs []lightrag.ReferenceItem) []lightrag.ReferenceItem {
	out := refs[:0]
	for _, ref := range refs {
		del, err := tomb.IsDeleted(ctx, ref.ReferenceID)
		if err != nil || !del {
			out = append(out, ref)
		}
	}
	return out
}

// filterTombstonedChunks drops chunks whose reference_id is tombstoned, so
// QueryData does not surface content from logically-deleted memories (the
// structured-path mirror of filterTombstonedRefs).
func filterTombstonedChunks(ctx context.Context, tomb tombstoner, chunks []queryDataChunk) []queryDataChunk {
	out := chunks[:0]
	for _, ch := range chunks {
		del, err := tomb.IsDeleted(ctx, ch.referenceID)
		if err != nil || !del {
			out = append(out, ch)
		}
	}
	return out
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
	// "duplicated" (content already indexed) and "partial_success" are idempotent
	// successes per the LightRAG contract: re-ingesting unchanged content returns
	// "duplicated" with the existing, reusable track_id. Only a genuine "failure"
	// status (e.g. string_too_short) or an empty track_id is a logical upstream
	// error. Treating "duplicated" as a failure made every re-ingest job fail.
	accepted := resp.Status == "success" || resp.Status == "duplicated" || resp.Status == "partial_success"
	if !accepted || resp.TrackID == "" {
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
	start := time.Now()
	if s.tomb != nil {
		deleted, err := s.tomb.IsDeleted(ctx, trackID)
		if err != nil {
			return Memory{}, fmt.Errorf("tombstone check: %w", err)
		}
		if deleted {
			s.incOp("get", ErrNotFound)
			return Memory{}, ErrNotFound
		}
	}
	ts, err := s.lr.TrackStatus(ctx, trackID)
	if err != nil {
		wrapped := wrapUpstream(err)
		s.incOp("get", wrapped)
		return Memory{}, wrapped
	}
	if len(ts.Documents) == 0 {
		notFound := fmt.Errorf("%w: track %s has no documents", ErrNotFound, trackID)
		s.incOp("get", notFound)
		return Memory{}, notFound
	}
	if len(ts.Documents) > 1 {
		// Sort by doc ID (stable, ascending) so selection is deterministic across
		// replicas and calls even when LightRAG returns docs in arbitrary order.
		// Log a WARN so the 1:1 branch-per-track invariant violation is observable.
		sortDocsByID(ts.Documents)
		s.log.WarnContext(ctx, "memory.get: multi-doc track; picking first by doc_id",
			"track_id", trackID, "doc_count", len(ts.Documents))
	}
	m, parseErr := FromDocStatus(trackID, ts.Documents[0])
	if parseErr != nil {
		s.log.WarnContext(ctx, "memory.get: unparseable created_at",
			"track_id", trackID,
			"error", parseErr,
		)
	}
	s.incOp("get", nil)
	s.log.InfoContext(ctx, "memory.get",
		"action", "get_memory",
		"resource_id", trackID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return m, nil
}

// DeleteMemory removes all documents associated with the given track_id.
func (s *Service) DeleteMemory(ctx context.Context, trackID string) error {
	start := time.Now()
	err := s.deleteMemoryRaw(ctx, trackID)
	s.incOp("delete", err)
	if err != nil {
		return err
	}
	s.log.InfoContext(ctx, "memory.delete",
		"action", "delete_memory",
		"resource_id", trackID,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// deleteMemoryRaw is the unmetered core used by DeleteMemory and the source-purge
// loop in DeleteMemoriesBySource, so that each public entry point owns exactly one
// op label increment (finding 6: no double-counting).
func (s *Service) deleteMemoryRaw(ctx context.Context, trackID string) error {
	ts, err := s.lr.TrackStatus(ctx, trackID)
	if err != nil {
		return wrapUpstream(err)
	}
	docIDs := make([]string, 0, len(ts.Documents))
	for _, d := range ts.Documents {
		docIDs = append(docIDs, d.ID)
	}
	if len(docIDs) == 0 {
		return fmt.Errorf("%w: track %s has no documents", ErrNotFound, trackID)
	}
	// Mark tombstone BEFORE the upstream delete so GET-after-DELETE returns
	// ErrNotFound immediately. On failure we roll it back (Unmark) so a caller
	// retry can re-attempt the upstream delete (finding 11).
	if s.tomb != nil {
		if err := s.tomb.Mark(ctx, trackID); err != nil {
			return fmt.Errorf("tombstone: %w", err)
		}
	}
	resp, err := s.lr.DeleteDocs(ctx, lightrag.DeleteDocRequest{DocIDs: docIDs})
	if err != nil {
		if s.tomb != nil {
			_ = s.tomb.Unmark(ctx, trackID)
		}
		return wrapUpstream(err)
	}
	// LightRAG v1.4.16 returns "deletion_started" (async) or "success" (sync) on accepted deletes.
	// "busy" means the pipeline lock is held (LightRAG is mid-ingest): backpressure, so callers
	// must retry (ErrBusy -> 429 + Retry-After), not fail permanently and not be told the
	// service is unavailable. Any other status (e.g. "failure") is a logical upstream
	// rejection even though HTTP returned 200.
	if resp.Status != "deletion_started" && resp.Status != "success" {
		if s.tomb != nil {
			_ = s.tomb.Unmark(ctx, trackID)
		}
		if resp.Status == "busy" {
			return fmt.Errorf("%w: delete returned status=%q", ErrBusy, resp.Status)
		}
		return fmt.Errorf("%w: delete returned status=%q", ErrUpstream, resp.Status)
	}
	return nil
}

// DeleteMemoriesBySources purges every memory produced from each file in files
// for the given repo, calling DeleteMemoriesBySource once per file. Returns the
// total count of track_ids purged across all files. A nil sources index is a
// no-op returning 0.
func (s *Service) DeleteMemoriesBySources(ctx context.Context, repo string, files []string) (int, error) {
	start := time.Now()
	// One budget for the whole batch (see DefaultPurgeBudget): each file's delete
	// may have to wait out the pipeline lock the previous file's delete took, so
	// a per-file bound would scale with the file count. The deadline also
	// propagates into lightrag.DeleteDocs, which sizes its busy wait from it.
	if s.purgeBudget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.purgeBudget)
		defer cancel()
	}
	total := 0
	for _, f := range files {
		n, err := s.DeleteMemoriesBySource(ctx, repo, f)
		if err != nil {
			s.incOp("delete_by_sources", err)
			wrapped := fmt.Errorf("delete memories for %s/%s: %w", repo, f, err)
			s.logPurgeErr(ctx, "memory.delete_by_sources.incomplete", wrapped,
				"action", "delete_memories_by_sources",
				"repo", repo,
				"file_path", f,
				"files_count", len(files),
				"total_purged", total+n,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			// n is the progress DeleteMemoriesBySource made inside the file that
			// failed; drop it and the caller under-counts what is already gone.
			return total + n, wrapped
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
		wrapped := fmt.Errorf("source track_ids: %w", err)
		s.logPurgeErr(ctx, "memory.delete_by_source", wrapped,
			"action", "delete_memories_by_source",
			"repo", repo,
			"file_path", filePath,
			"stage", "track_ids_lookup",
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return 0, wrapped
	}
	purged := 0
	for _, id := range ids {
		if err := s.deleteMemoryRaw(ctx, id); err != nil {
			if errors.Is(err, ErrNotFound) {
				continue // already gone upstream; index cleanup below still runs
			}
			s.incOp("delete_by_source", err)
			// Return purged (work done so far), not 0, so callers can account for
			// partial progress. The source-index is not cleaned up on mid-loop failure;
			// a retry will re-read the remaining ids (ErrNotFound entries are skipped).
			wrapped := fmt.Errorf("delete memory %s for %s/%s: %w", id, repo, filePath, err)
			s.logPurgeErr(ctx, "memory.delete_by_source", wrapped,
				"action", "delete_memories_by_source",
				"repo", repo,
				"file_path", filePath,
				"stage", "delete_track",
				"track_id", id,
				"purged_before_failure", purged,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return purged, wrapped
		}
		purged++
	}
	if _, err := s.sources.DeleteByFile(ctx, repo, filePath); err != nil {
		s.incOp("delete_by_source", err)
		wrapped := fmt.Errorf("purge source index %s/%s: %w", repo, filePath, err)
		s.logPurgeErr(ctx, "memory.delete_by_source", wrapped,
			"action", "delete_memories_by_source",
			"repo", repo,
			"file_path", filePath,
			"stage", "source_index_cleanup",
			"purged", purged,
			"duration_ms", time.Since(start).Milliseconds(),
		)
		return purged, wrapped
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

func sortDocsByID(docs []lightrag.DocStatusResponse) {
	sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
}

// Query retrieves context references for the given query.
// LightRAG's /query returns references rather than ranked matches;
// Match.Score is zero in this mapping. include_references must be set
// or LightRAG omits the reference list entirely and Matches comes back
// empty even when only_need_context is true.
func (s *Service) Query(ctx context.Context, q Query) (QueryResult, error) {
	start := time.Now()
	if !q.Mode.Valid() {
		err := fmt.Errorf("invalid query mode: %s", q.Mode)
		s.incOp("query", err)
		return QueryResult{}, err
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
		wrapped := wrapUpstream(err)
		s.incOp("query", wrapped)
		return QueryResult{}, wrapped
	}
	if s.tomb != nil {
		resp.References = filterTombstonedRefs(ctx, s.tomb, resp.References)
	}
	s.incOp("query", nil)
	s.log.InfoContext(ctx, "memory.query",
		"action", "query",
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return QueryResultFromQuery(*resp), nil
}

// Describe returns a generative answer plus source file paths.
func (s *Service) Describe(ctx context.Context, q Query) (DescribeResult, error) {
	start := time.Now()
	if !q.Mode.Valid() {
		err := fmt.Errorf("invalid query mode: %s", q.Mode)
		s.incOp("describe", err)
		return DescribeResult{}, err
	}
	resp, err := s.lr.Query(ctx, lightrag.QueryRequest{
		Mode:              lightrag.QueryMode(q.Mode),
		Query:             q.Text,
		TopK:              q.TopK,
		IncludeReferences: t(true),
		Stream:            t(false),
	})
	if err != nil {
		wrapped := wrapUpstream(err)
		s.incOp("describe", wrapped)
		return DescribeResult{}, wrapped
	}
	if s.tomb != nil {
		resp.References = filterTombstonedRefs(ctx, s.tomb, resp.References)
	}
	s.incOp("describe", nil)
	s.log.InfoContext(ctx, "memory.describe",
		"action", "describe",
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return DescribeResultFromQuery(*resp), nil
}

// QueryData retrieves ranked, scored matches via LightRAG's structured /query/data
// path. Unlike Query (which maps unranked /query references with Score 0),
// data.chunks come back in retrieval order and are mapped to Score-descending
// Matches (translate.go: queryResultFromChunks). top_k is mirrored into
// chunk_top_k so the requested depth bounds the chunk window (the ranked unit),
// not just entity/relation retrieval. include_chunk_content must be set or
// LightRAG omits chunk text, mirroring the include_references lesson (#21).
func (s *Service) QueryData(ctx context.Context, q Query) (QueryResult, error) {
	start := time.Now()
	if !q.Mode.Valid() {
		err := fmt.Errorf("invalid query mode: %s", q.Mode)
		s.incOp("query_data", err)
		return QueryResult{}, err
	}
	resp, err := s.lr.QueryData(ctx, lightrag.QueryRequest{
		Mode:          lightrag.QueryMode(q.Mode),
		Query:         q.Text,
		TopK:          q.TopK,
		ChunkTopK:     q.TopK,
		IncludeChunks: t(true),
	})
	if err != nil {
		wrapped := wrapUpstream(err)
		s.incOp("query_data", wrapped)
		return QueryResult{}, wrapped
	}
	chunks := chunksFromQueryData(*resp)
	if s.tomb != nil {
		chunks = filterTombstonedChunks(ctx, s.tomb, chunks)
	}
	s.incOp("query_data", nil)
	s.log.InfoContext(ctx, "memory.query_data",
		"action", "query_data",
		"matches", len(chunks),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return queryResultFromChunks(chunks), nil
}

// GetEntity retrieves an entity by name (Entity.ID == Entity.Name in this model).
func (s *Service) GetEntity(ctx context.Context, name string) (Entity, error) {
	start := time.Now()
	g, err := s.lr.Graph(ctx, name, 1, 1)
	if err != nil {
		wrapped := wrapUpstream(err)
		s.incOp("get_entity", wrapped)
		return Entity{}, wrapped
	}
	for _, n := range g.Nodes {
		if n.ID == name {
			s.incOp("get_entity", nil)
			s.log.InfoContext(ctx, "memory.get_entity",
				"action", "get_entity",
				"resource_id", name,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return EntityFromGraphNode(n), nil
		}
	}
	notFound := fmt.Errorf("%w: entity %s", ErrNotFound, name)
	s.incOp("get_entity", notFound)
	return Entity{}, notFound
}

// SearchEntities returns entities matching q by label.
// Labels carry only names; other fields are zero.
func (s *Service) SearchEntities(ctx context.Context, q string) ([]Entity, error) {
	start := time.Now()
	labels, err := s.lr.LabelSearch(ctx, q)
	if err != nil {
		wrapped := wrapUpstream(err)
		s.incOp("search_entities", wrapped)
		return nil, wrapped
	}
	out := make([]Entity, 0, len(labels))
	for _, l := range labels {
		out = append(out, Entity{ID: l, Name: l})
	}
	s.incOp("search_entities", nil)
	s.log.InfoContext(ctx, "memory.search_entities",
		"action", "search_entities",
		"count", len(out),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return out, nil
}

// PatchEntity applies a partial update to the entity identified by name.
// The returned Entity is built from UpdateEntity's response data to avoid a
// second upstream round-trip (N+1) and the TOCTOU window that existed when
// the previous implementation called GetEntity after the update.
func (s *Service) PatchEntity(ctx context.Context, name string, patch Entity) (Entity, error) {
	start := time.Now()
	data := EntityUpdatePayload(patch)
	allowRename := patch.Name != "" && patch.Name != name
	resp, err := s.lr.UpdateEntity(ctx, lightrag.EntityUpdateRequest{
		EntityName:  name,
		UpdatedData: data,
		AllowRename: allowRename,
	})
	if err != nil {
		s.incOp("patch_entity", err)
		return Entity{}, wrapUpstream(err)
	}
	// Build the result from the response data without a second upstream call.
	final := name
	if allowRename {
		final = patch.Name
	}
	out := entityFromUpdateResponse(resp, final)
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
	start := time.Now()
	labels, err := s.lr.LabelSearch(ctx, "")
	if err != nil {
		wrapped := wrapUpstream(err)
		s.incOp("list_edges", wrapped)
		return nil, wrapped
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
			wrapped := wrapUpstream(err)
			s.incOp("list_edges", wrapped)
			return nil, wrapped
		}
		for _, e := range g.Edges {
			edge := EdgeFromGraphEdge(e)
			// Deduplicate on (source, target, relation): A->B with "owns" and A->B
			// with "manages" are distinct edges. A->B and B->A are always distinct.
			dedup := edge.ID + "\x00" + edge.Relation
			if _, ok := seen[dedup]; ok {
				continue
			}
			seen[dedup] = struct{}{}
			out = append(out, edge)
		}
	}
	s.incOp("list_edges", nil)
	s.log.InfoContext(ctx, "memory.list_edges",
		"action", "list_edges",
		"labels_scanned", len(labels),
		"edges_found", len(out),
		"duration_ms", time.Since(start).Milliseconds(),
	)
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
