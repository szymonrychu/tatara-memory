package lightrag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HTTPConfig holds constructor parameters for HTTPClient.
type HTTPConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	Logger     *slog.Logger
	Registry   prometheus.Registerer
}

// HTTPClient is a context-aware, instrumented HTTP client for LightRAG.
type HTTPClient struct {
	base    string
	http    *http.Client
	log     *slog.Logger
	metrics *metrics
}

// NewHTTPClient constructs an HTTPClient from cfg. BaseURL is required.
func NewHTTPClient(cfg HTTPConfig) (*HTTPClient, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("lightrag: BaseURL is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &HTTPClient{
		base:    cfg.BaseURL,
		http:    cfg.HTTPClient,
		log:     cfg.Logger,
		metrics: newMetrics(cfg.Registry),
	}, nil
}

// HTTPError is returned when LightRAG responds with a 4xx or 5xx status.
type HTTPError struct {
	Status int
	Body   string
	Path   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("lightrag: %s -> %d: %s", e.Path, e.Status, e.Body)
}

// do is the shared instrumented round-trip. Endpoint methods call it.
func (c *HTTPClient) do(ctx context.Context, op, method, path string, body io.Reader, out any) error {
	start := time.Now()
	err := c.roundTrip(ctx, method, path, body, out)
	dur := time.Since(start).Seconds()
	c.metrics.observe(op, dur, err)
	c.log.LogAttrs(ctx, levelFor(err), "lightrag_call",
		slog.String("op", op),
		slog.String("method", method),
		slog.String("path", path),
		slog.Float64("duration_s", dur),
		slog.Any("error", err),
	)
	return err
}

func levelFor(err error) slog.Level {
	if err != nil {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

func (c *HTTPClient) roundTrip(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("lightrag: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lightrag: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return &HTTPError{Status: resp.StatusCode, Body: string(buf), Path: path}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("lightrag: decode response: %w", err)
	}
	return nil
}

func encodeJSON(v any) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, fmt.Errorf("lightrag: encode body: %w", err)
	}
	return buf, nil
}

// InsertDocument inserts one or more documents into LightRAG.
func (c *HTTPClient) InsertDocument(ctx context.Context, req InsertRequest) (*InsertResponse, error) {
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out InsertResponse
	if err := c.do(ctx, OpInsertDocument, http.MethodPost, "/documents", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDocument retrieves a document by ID.
func (c *HTTPClient) GetDocument(ctx context.Context, id string) (*Document, error) {
	var out Document
	if err := c.do(ctx, OpGetDocument, http.MethodGet, "/documents/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDocument removes a document by ID.
func (c *HTTPClient) DeleteDocument(ctx context.Context, id string) error {
	return c.do(ctx, OpDeleteDocument, http.MethodDelete, "/documents/"+url.PathEscape(id), nil, nil)
}

// Query executes a retrieval query against LightRAG.
func (c *HTTPClient) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	if !req.Mode.Valid() {
		c.metrics.incError(OpQuery)
		return nil, fmt.Errorf("lightrag: invalid query mode %q", req.Mode)
	}
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out QueryResponse
	if err := c.do(ctx, OpQuery, http.MethodPost, "/query", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QueryDescribe executes a generative describe query against LightRAG.
func (c *HTTPClient) QueryDescribe(ctx context.Context, req QueryRequest) (*DescribeResponse, error) {
	if !req.Mode.Valid() {
		c.metrics.incError(OpQueryDescribe)
		return nil, fmt.Errorf("lightrag: invalid query mode %q", req.Mode)
	}
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out DescribeResponse
	if err := c.do(ctx, OpQueryDescribe, http.MethodPost, "/query/describe", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEntities returns entities matching the optional query string.
func (c *HTTPClient) ListEntities(ctx context.Context, q string) ([]Entity, error) {
	path := "/entities"
	if q != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	var out []Entity
	if err := c.do(ctx, OpListEntities, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetEntity retrieves an entity by ID.
func (c *HTTPClient) GetEntity(ctx context.Context, id string) (*Entity, error) {
	var out Entity
	if err := c.do(ctx, OpGetEntity, http.MethodGet, "/entities/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateEntity applies a partial update to an entity.
func (c *HTTPClient) UpdateEntity(ctx context.Context, id string, upd EntityUpdate) (*Entity, error) {
	body, err := encodeJSON(upd)
	if err != nil {
		return nil, err
	}
	var out Entity
	if err := c.do(ctx, OpUpdateEntity, http.MethodPatch, "/entities/"+url.PathEscape(id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEdges returns all edges in the knowledge graph.
func (c *HTTPClient) ListEdges(ctx context.Context) ([]Edge, error) {
	var out []Edge
	if err := c.do(ctx, OpListEdges, http.MethodGet, "/edges", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateEdge creates a new directed edge between two entities.
func (c *HTTPClient) CreateEdge(ctx context.Context, e Edge) (*Edge, error) {
	body, err := encodeJSON(e)
	if err != nil {
		return nil, err
	}
	var out Edge
	if err := c.do(ctx, OpCreateEdge, http.MethodPost, "/edges", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEdge removes an edge by ID.
func (c *HTTPClient) DeleteEdge(ctx context.Context, id string) error {
	return c.do(ctx, OpDeleteEdge, http.MethodDelete, "/edges/"+url.PathEscape(id), nil, nil)
}

// Health checks the LightRAG service health endpoint.
func (c *HTTPClient) Health(ctx context.Context) error {
	return c.do(ctx, OpHealth, http.MethodGet, "/health", nil, nil)
}
