package cgeval

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
	"strconv"
	"strings"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
)

// maxRespBody caps how much of a response body the client reads (defensive
// against a misbehaving upstream); traversal responses on the fixture are small.
const maxRespBody = 8 << 20 // 8 MiB

// Client is a thin HTTP client for a live tatara-memory deployment, used by the
// code-graph eval runner to push the fixture and run the golden traversal cases.
// It only covers /code-graph:bulk and the read-only /code/* endpoints the harness
// exercises.
type Client struct {
	baseURL string
	repo    string
	token   string
	http    *http.Client
	log     *slog.Logger
}

// ClientConfig configures a Client. Token is a pre-fetched OIDC bearer token
// (passed via env); the client does not run an OIDC flow.
type ClientConfig struct {
	BaseURL    string
	Repo       string
	Token      string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// NewClient validates the config and returns a ready Client.
func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("cgeval: base url required")
	}
	if strings.TrimSpace(cfg.Repo) == "" {
		return nil, errors.New("cgeval: repo required")
	}
	c := &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		repo:    cfg.Repo,
		token:   cfg.Token,
		http:    cfg.HTTPClient,
		log:     cfg.Logger,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 60 * time.Second}
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	return c, nil
}

// Push reconciles the fixture graph via POST /code-graph:bulk. The push is
// synchronous and file-granular replace, so re-running is idempotent.
func (c *Client) Push(ctx context.Context, p codegraph.GraphPush) (codegraph.PushResult, error) {
	p.Repo = c.repo
	var res codegraph.PushResult
	if err := c.do(ctx, http.MethodPost, "/code-graph:bulk", nil, p, &res); err != nil {
		return codegraph.PushResult{}, err
	}
	return res, nil
}

// Run issues the case's endpoint request and extracts the ordered results.
func (c *Client) Run(ctx context.Context, gc GoldenCase) ([]Result, error) {
	switch gc.Kind {
	case KindSearch:
		return c.search(ctx, gc)
	case KindEntity:
		return c.entity(ctx, gc)
	case KindNeighbors:
		return c.nodes(ctx, "/code/neighbors", gc, c.neighborQuery(gc))
	case KindCallers:
		return c.nodes(ctx, "/code/callers", gc, c.idDepthQuery(gc))
	case KindCallees:
		return c.nodes(ctx, "/code/callees", gc, c.idDepthQuery(gc))
	case KindDependents:
		return c.nodes(ctx, "/code/dependents", gc, c.idDepthQuery(gc))
	case KindDependencies:
		return c.nodes(ctx, "/code/dependencies", gc, c.idDepthQuery(gc))
	case KindResourceGraph:
		return c.nodes(ctx, "/code/resource-graph", gc, c.idDepthQuery(gc))
	case KindFileImports:
		return c.fileImports(ctx, gc)
	case KindPath:
		return c.path(ctx, gc)
	default:
		return nil, fmt.Errorf("cgeval: unknown case kind %q", gc.Kind)
	}
}

func (c *Client) baseQuery() url.Values {
	q := url.Values{}
	q.Set("repo", c.repo)
	return q
}

func (c *Client) idDepthQuery(gc GoldenCase) url.Values {
	q := c.baseQuery()
	q.Set("id", gc.ID)
	if gc.Depth > 0 {
		q.Set("depth", strconv.Itoa(gc.Depth))
	}
	return q
}

func (c *Client) neighborQuery(gc GoldenCase) url.Values {
	q := c.idDepthQuery(gc)
	q.Set("relation", gc.Relation)
	if gc.Direction != "" {
		q.Set("direction", gc.Direction)
	}
	return q
}

func (c *Client) search(ctx context.Context, gc GoldenCase) ([]Result, error) {
	q := c.baseQuery()
	q.Set("q", gc.Q)
	if gc.Type != "" {
		q.Set("type", gc.Type)
	}
	var env struct {
		Entities []codegraph.Entity `json:"entities"`
	}
	if err := c.do(ctx, http.MethodGet, "/code/entities", q, nil, &env); err != nil {
		return nil, err
	}
	return entityResults(env.Entities), nil
}

func (c *Client) entity(ctx context.Context, gc GoldenCase) ([]Result, error) {
	q := c.baseQuery()
	q.Set("id", gc.ID)
	var det codegraph.EntityDetail
	if err := c.do(ctx, http.MethodGet, "/code/entity", q, nil, &det); err != nil {
		return nil, err
	}
	// The entity-detail result set is the node itself plus its immediate
	// neighbours (out-edge targets and in-edge sources).
	out := []Result{{Key: det.ID, Name: det.Name}}
	for _, e := range det.OutEdges {
		out = append(out, Result{Key: e.To})
	}
	for _, e := range det.InEdges {
		out = append(out, Result{Key: e.From})
	}
	return out, nil
}

func (c *Client) nodes(ctx context.Context, path string, _ GoldenCase, q url.Values) ([]Result, error) {
	var env struct {
		Nodes []codegraph.PathNode `json:"nodes"`
	}
	if err := c.do(ctx, http.MethodGet, path, q, nil, &env); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(env.Nodes))
	for _, n := range env.Nodes {
		out = append(out, Result{Key: n.ID, Name: n.Name})
	}
	return out, nil
}

func (c *Client) fileImports(ctx context.Context, gc GoldenCase) ([]Result, error) {
	q := c.baseQuery()
	q.Set("path", gc.Path)
	var env struct {
		Edges []codegraph.Edge `json:"edges"`
	}
	if err := c.do(ctx, http.MethodGet, "/code/file-imports", q, nil, &env); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(env.Edges))
	for _, e := range env.Edges {
		out = append(out, Result{Key: e.From + "->" + e.To})
	}
	return out, nil
}

func (c *Client) path(ctx context.Context, gc GoldenCase) ([]Result, error) {
	q := c.baseQuery()
	q.Set("from", gc.From)
	q.Set("to", gc.To)
	if gc.Relation != "" {
		q.Set("relations", gc.Relation)
	}
	if gc.MaxDepth > 0 {
		q.Set("max_depth", strconv.Itoa(gc.MaxDepth))
	}
	var env struct {
		Path []codegraph.Entity `json:"path"`
	}
	if err := c.do(ctx, http.MethodGet, "/code-graph/path", q, nil, &env); err != nil {
		return nil, err
	}
	return entityResults(env.Path), nil
}

func entityResults(es []codegraph.Entity) []Result {
	out := make([]Result, 0, len(es))
	for _, e := range es {
		out = append(out, Result{Key: e.ID, Name: e.Name})
	}
	return out
}

func (c *Client) do(ctx context.Context, method, p string, query url.Values, reqBody, respOut any) error {
	var rdr io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("cgeval: marshal %s %s: %w", method, p, err)
		}
		rdr = bytes.NewReader(b)
	}
	u := c.baseURL + p
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return fmt.Errorf("cgeval: request %s %s: %w", method, p, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cgeval: %s %s: %w", method, p, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBody))
	if err != nil {
		return fmt.Errorf("cgeval: read %s %s: %w", method, p, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.log.ErrorContext(ctx, "cgeval.http_error",
			"action", "code_request",
			"method", method,
			"path", p,
			"status", resp.StatusCode,
			"body", strings.TrimSpace(string(body)),
		)
		return fmt.Errorf("cgeval: %s %s: status %d: %s", method, p, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if respOut != nil {
		if err := json.Unmarshal(body, respOut); err != nil {
			return fmt.Errorf("cgeval: decode %s %s: %w", method, p, err)
		}
	}
	return nil
}
