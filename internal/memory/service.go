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

// ErrTransient is returned when the LightRAG backend is temporarily unavailable (5xx, timeout, cancellation).
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

// CreateMemory stores m in LightRAG and returns it with a generated ID and timestamp.
func (s *Service) CreateMemory(ctx context.Context, m Memory) (Memory, error) {
	if m.ID == "" {
		m.ID = newID("mem")
	}
	m.CreatedAt = s.now()
	if _, err := s.lr.InsertDocument(ctx, ToLightragInsert(m)); err != nil {
		return Memory{}, wrapUpstream(err)
	}
	return m, nil
}

// GetMemory retrieves a memory by ID.
func (s *Service) GetMemory(ctx context.Context, id string) (Memory, error) {
	doc, err := s.lr.GetDocument(ctx, id)
	if err != nil {
		return Memory{}, wrapUpstream(err)
	}
	return Memory{ID: doc.ID, Text: doc.Content, Metadata: doc.Metadata}, nil
}

// DeleteMemory removes a memory by ID.
func (s *Service) DeleteMemory(ctx context.Context, id string) error {
	return wrapUpstream(s.lr.DeleteDocument(ctx, id))
}

// Query retrieves ranked matches for the given query.
func (s *Service) Query(ctx context.Context, q Query) (QueryResult, error) {
	if !q.Mode.Valid() {
		return QueryResult{}, fmt.Errorf("invalid query mode: %s", q.Mode)
	}
	resp, err := s.lr.Query(ctx, lightrag.QueryRequest{
		Mode:  lightrag.QueryMode(q.Mode),
		Query: q.Text,
		TopK:  q.TopK,
	})
	if err != nil {
		return QueryResult{}, wrapUpstream(err)
	}
	return FromLightragQuery(*resp), nil
}

// Describe returns a generative answer for the given query.
func (s *Service) Describe(ctx context.Context, q Query) (DescribeResult, error) {
	if !q.Mode.Valid() {
		return DescribeResult{}, fmt.Errorf("invalid query mode: %s", q.Mode)
	}
	resp, err := s.lr.QueryDescribe(ctx, lightrag.QueryRequest{
		Mode:  lightrag.QueryMode(q.Mode),
		Query: q.Text,
		TopK:  q.TopK,
	})
	if err != nil {
		return DescribeResult{}, wrapUpstream(err)
	}
	return DescribeResult{Response: resp.Response, Sources: resp.Sources}, nil
}

// GetEntity retrieves an entity by ID.
func (s *Service) GetEntity(ctx context.Context, id string) (Entity, error) {
	e, err := s.lr.GetEntity(ctx, id)
	if err != nil {
		return Entity{}, wrapUpstream(err)
	}
	return FromLightragEntity(*e), nil
}

// SearchEntities returns entities whose names match q.
func (s *Service) SearchEntities(ctx context.Context, q string) ([]Entity, error) {
	es, err := s.lr.ListEntities(ctx, q)
	if err != nil {
		return nil, wrapUpstream(err)
	}
	out := make([]Entity, 0, len(es))
	for _, e := range es {
		out = append(out, FromLightragEntity(e))
	}
	return out, nil
}

// PatchEntity applies a partial update to the entity identified by id.
func (s *Service) PatchEntity(ctx context.Context, id string, patch Entity) (Entity, error) {
	e, err := s.lr.UpdateEntity(ctx, id, ToLightragEntityUpdate(patch))
	if err != nil {
		return Entity{}, wrapUpstream(err)
	}
	return FromLightragEntity(*e), nil
}

// ListEdges returns all edges in the knowledge graph.
func (s *Service) ListEdges(ctx context.Context) ([]Edge, error) {
	es, err := s.lr.ListEdges(ctx)
	if err != nil {
		return nil, wrapUpstream(err)
	}
	out := make([]Edge, 0, len(es))
	for _, e := range es {
		out = append(out, FromLightragEdge(e))
	}
	return out, nil
}

// CreateEdge stores a new directed relationship in the knowledge graph.
func (s *Service) CreateEdge(ctx context.Context, e Edge) (Edge, error) {
	created, err := s.lr.CreateEdge(ctx, ToLightragEdge(e))
	if err != nil {
		return Edge{}, wrapUpstream(err)
	}
	return FromLightragEdge(*created), nil
}

// DeleteEdge removes an edge by ID.
func (s *Service) DeleteEdge(ctx context.Context, id string) error {
	return wrapUpstream(s.lr.DeleteEdge(ctx, id))
}
