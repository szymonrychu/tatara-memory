package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// ErrNotFound is returned when the requested memory, entity, or edge does not exist.
var ErrNotFound = errors.New("memory: not found")

// ErrUpstream is returned when the LightRAG backend returns an unexpected error.
var ErrUpstream = errors.New("memory: upstream error")

// ErrTransient is returned when the LightRAG backend is temporarily unavailable.
var ErrTransient = errors.New("memory: transient upstream error")

// Service provides memory CRUD and retrieval operations backed by LightRAG.
type Service struct {
	lr  lightrag.Client
	now func() time.Time
}

// NewService returns a Service backed by the given LightRAG client.
func NewService(lr lightrag.Client) *Service {
	return &Service{lr: lr, now: time.Now}
}

func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
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
	if m.ID == "" {
		m.ID = newID("mem")
	}
	resp, err := s.lr.InsertText(ctx, ToInsertText(m))
	if err != nil {
		return Memory{}, wrapUpstream(err)
	}
	m.ID = resp.TrackID
	m.CreatedAt = s.now()
	return m, nil
}

// GetMemory retrieves a memory by track_id.
// Returns a Memory derived from the first document associated with the track.
// LightRAG does not expose original document text; Text holds content_summary.
func (s *Service) GetMemory(ctx context.Context, trackID string) (Memory, error) {
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
	if _, err := s.lr.DeleteDocs(ctx, lightrag.DeleteDocRequest{DocIDs: docIDs}); err != nil {
		return wrapUpstream(err)
	}
	return nil
}

func t(b bool) *bool { return &b }

// Query retrieves context references for the given query.
// LightRAG's /query returns references rather than ranked matches;
// Match.Score is zero in this mapping.
func (s *Service) Query(ctx context.Context, q Query) (QueryResult, error) {
	if !q.Mode.Valid() {
		return QueryResult{}, fmt.Errorf("invalid query mode: %s", q.Mode)
	}
	resp, err := s.lr.Query(ctx, lightrag.QueryRequest{
		Mode:            lightrag.QueryMode(q.Mode),
		Query:           q.Text,
		TopK:            q.TopK,
		OnlyNeedContext: t(true),
		Stream:          t(false),
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
	data := EntityUpdatePayload(patch)
	allowRename := patch.Name != "" && patch.Name != name
	_, err := s.lr.UpdateEntity(ctx, lightrag.EntityUpdateRequest{
		EntityName:  name,
		UpdatedData: data,
		AllowRename: allowRename,
	})
	if err != nil {
		return Entity{}, wrapUpstream(err)
	}
	final := name
	if allowRename {
		final = patch.Name
	}
	return s.GetEntity(ctx, final)
}

// ListEdges returns all edges by iterating every known label and collecting its incident edges.
// O(N) graph reads; acceptable for v0.1.1, slated for revisit in v0.2.
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
			id := MakeEdgeID(e.Source, e.Target)
			rev := MakeEdgeID(e.Target, e.Source)
			if _, ok := seen[id]; ok {
				continue
			}
			if _, ok := seen[rev]; ok {
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
	if _, err := s.lr.CreateRelation(ctx, RelationCreatePayload(e)); err != nil {
		return Edge{}, wrapUpstream(err)
	}
	created := e
	created.ID = MakeEdgeID(e.From, e.To)
	return created, nil
}

// DeleteEdge removes an edge by composite ID ("from||to").
func (s *Service) DeleteEdge(ctx context.Context, id string) error {
	from, to, ok := ParseEdgeID(id)
	if !ok {
		return fmt.Errorf("%w: invalid edge id %q", ErrNotFound, id)
	}
	if err := s.lr.DeleteRelation(ctx, lightrag.DeleteRelationRequest{
		SourceEntity: from,
		TargetEntity: to,
	}); err != nil {
		return wrapUpstream(err)
	}
	return nil
}
