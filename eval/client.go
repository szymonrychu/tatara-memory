package eval

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
	"strings"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// maxRespBody caps how much of a response body the client reads (defensive
// against a misbehaving upstream); query/job responses are small.
const maxRespBody = 8 << 20 // 8 MiB

// Client is a thin HTTP client for a live tatara-memory deployment, used by the
// eval runner to seed the corpus and issue golden queries. It is not a general
// SDK; it only covers /memories:bulk, /ingest-jobs/{id}, and /queries:data.
type Client struct {
	baseURL      string
	token        string
	http         *http.Client
	log          *slog.Logger
	pollInterval time.Duration
	jobTimeout   time.Duration
}

// ClientConfig configures a Client. Token is a pre-fetched OIDC bearer token
// (obtained out-of-band and passed via env); the client does not run an OIDC
// flow. Zero-valued optional fields fall back to sensible defaults.
type ClientConfig struct {
	BaseURL      string
	Token        string
	HTTPClient   *http.Client
	Logger       *slog.Logger
	PollInterval time.Duration
	JobTimeout   time.Duration
}

// NewClient validates the config and returns a ready Client.
func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("eval: base url required")
	}
	c := &Client{
		baseURL:      strings.TrimRight(cfg.BaseURL, "/"),
		token:        cfg.Token,
		http:         cfg.HTTPClient,
		log:          cfg.Logger,
		pollInterval: cfg.PollInterval,
		jobTimeout:   cfg.JobTimeout,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 60 * time.Second}
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	if c.pollInterval <= 0 {
		c.pollInterval = 2 * time.Second
	}
	if c.jobTimeout <= 0 {
		c.jobTimeout = 5 * time.Minute
	}
	return c, nil
}

// BulkIngest submits the seed items to /memories:bulk and returns the async
// ingest job id to poll with WaitJob.
func (c *Client) BulkIngest(ctx context.Context, items []SeedItem) (string, error) {
	if len(items) == 0 {
		return "", errors.New("eval: no seed items to ingest")
	}
	var job memory.IngestJob
	if err := c.doJSON(ctx, http.MethodPost, "/memories:bulk", httpapi.BulkMemoriesRequest{Items: items}, &job); err != nil {
		return "", err
	}
	if job.ID == "" {
		return "", errors.New("eval: bulk ingest returned empty job id")
	}
	return job.ID, nil
}

// WaitJob polls /ingest-jobs/{id} until the job reaches a terminal state or the
// bounded deadline elapses. The terminal job is returned (the caller decides how
// to treat failed/partial).
func (c *Client) WaitJob(ctx context.Context, id string) (memory.IngestJob, error) {
	deadline := time.Now().Add(c.jobTimeout)
	for {
		var job memory.IngestJob
		if err := c.doJSON(ctx, http.MethodGet, "/ingest-jobs/"+url.PathEscape(id), nil, &job); err != nil {
			return memory.IngestJob{}, err
		}
		if job.Status.Terminal() {
			return job, nil
		}
		if time.Now().After(deadline) {
			return job, fmt.Errorf("eval: job %s not terminal within %s (last status %q)", id, c.jobTimeout, job.Status)
		}
		select {
		case <-ctx.Done():
			return memory.IngestJob{}, ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

// Query issues a retrieval against /queries:data (the structured, score-ranked
// path) and decodes the result. The eval uses the scored path so MRR and
// recall@k reflect LightRAG's chunk retrieval order rather than the unscored
// /query reference arrival order.
func (c *Client) Query(ctx context.Context, q memory.Query) (memory.QueryResult, error) {
	var res memory.QueryResult
	err := c.doJSON(ctx, http.MethodPost, "/queries:data", q, &res)
	return res, err
}

func (c *Client) doJSON(ctx context.Context, method, p string, reqBody, respOut any) error {
	var rdr io.Reader
	if reqBody != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("eval: marshal %s %s: %w", method, p, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+p, rdr)
	if err != nil {
		return fmt.Errorf("eval: request %s %s: %w", method, p, err)
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
		return fmt.Errorf("eval: %s %s: %w", method, p, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBody))
	if err != nil {
		return fmt.Errorf("eval: read %s %s: %w", method, p, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.log.ErrorContext(ctx, "eval.http_error",
			"action", "memory_request",
			"method", method,
			"path", p,
			"status", resp.StatusCode,
			"body", strings.TrimSpace(string(body)),
		)
		return fmt.Errorf("eval: %s %s: status %d: %s", method, p, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if respOut != nil {
		if err := json.Unmarshal(body, respOut); err != nil {
			return fmt.Errorf("eval: decode %s %s: %w", method, p, err)
		}
	}
	return nil
}
