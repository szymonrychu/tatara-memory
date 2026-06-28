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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/ctxkeys"
)

const (
	// maxBodyBytes caps error and success body reads to prevent OOM from a misbehaving server.
	maxBodyBytes = 4 * 1024 * 1024 // 4 MiB
	// maxErrBodyDisplay caps the Body string stored in HTTPError (and thus log lines).
	maxErrBodyDisplay = 512
	// retryMax is the maximum number of retry attempts after the initial request.
	retryMax = 3
	// retryBaseDelay is the initial backoff delay before the first retry.
	retryBaseDelay = 200 * time.Millisecond
	// maxRetryAfter caps the server-directed Retry-After sleep so a misbehaving
	// or compromised upstream cannot wedge a worker indefinitely with a huge value.
	maxRetryAfter = 30 * time.Second
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

// LogicalError is returned when LightRAG responds HTTP 200 but the response
// envelope Status field indicates a logical failure (e.g. "failure").
type LogicalError struct {
	Op      string
	Status  string
	Message string
}

func (e *LogicalError) Error() string {
	return fmt.Sprintf("lightrag: %s: logical failure: status=%q message=%q", e.Op, e.Status, e.Message)
}

func (c *HTTPClient) do(ctx context.Context, op, method, path string, body io.Reader, out any) error {
	start := time.Now()
	err := c.roundTrip(ctx, method, path, body, out)
	dur := time.Since(start).Seconds()
	c.metrics.observe(op, dur, err)
	c.logCall(ctx, op, method, path, dur, err)
	return err
}

// doAndObserve executes the round-trip and returns the duration so the caller
// can observe once with the true outcome (e.g. after an envelope check that may
// convert an HTTP-200 into a logical error). The transport-level error (if any)
// is returned; the caller must pass the final err to metrics.observe.
func (c *HTTPClient) doAndObserve(ctx context.Context, op, method, path string, body io.Reader, out any) (float64, error) {
	start := time.Now()
	err := c.roundTrip(ctx, method, path, body, out)
	return time.Since(start).Seconds(), err
}

// requestIDFromCtx retrieves the inbound HTTP request ID placed in ctx by the
// httpapi RequestID middleware (via internal/ctxkeys). Returns empty string when
// not in an HTTP request context (e.g. background tasks).
func requestIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxkeys.RequestID).(string)
	return v
}

func (c *HTTPClient) logCall(ctx context.Context, op, method, path string, dur float64, err error) {
	reqID := requestIDFromCtx(ctx)
	attrs := []slog.Attr{
		slog.String("op", op),
		slog.String("method", method),
		slog.String("path", path),
		slog.Float64("duration_s", dur),
	}
	if reqID != "" {
		attrs = append(attrs, slog.String("request_id", reqID))
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}
	c.log.LogAttrs(ctx, levelFor(op, err), "lightrag_call", attrs...)
}

func levelFor(op string, err error) slog.Level {
	if err != nil {
		return slog.LevelWarn
	}
	if op == OpHealth {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// isRetryable returns true for transport errors and 5xx/429 responses that
// are safe to retry (the caller ensures the body is re-readable per attempt).
func isRetryable(statusCode int, transportErr error) bool {
	if transportErr != nil {
		return true
	}
	return statusCode == http.StatusTooManyRequests || statusCode >= 500
}

func (c *HTTPClient) roundTrip(ctx context.Context, method, path string, body io.Reader, out any) error {
	// Capture body bytes once so each retry attempt can rebuild the reader.
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return fmt.Errorf("lightrag: read body: %w", err)
		}
	}

	var lastErr error
	// skipBackoff is set when the previous iteration already slept (Retry-After),
	// so the exponential backoff is not stacked on top of the server-directed wait.
	skipBackoff := false
	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 && !skipBackoff {
			delay := retryBaseDelay * (1 << (attempt - 1)) // 200ms, 400ms, 800ms
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		skipBackoff = false

		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, c.base+path, reqBody)
		if err != nil {
			return fmt.Errorf("lightrag: build request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		if reqBody != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("lightrag: do request: %w", err)
			if isRetryable(0, err) {
				c.log.LogAttrs(ctx, slog.LevelDebug, "lightrag_retry",
					slog.String("path", path),
					slog.Int("attempt", attempt+1),
					slog.Any("error", err),
				)
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 400 {
			buf, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
			_ = resp.Body.Close()
			body := string(buf)
			if len(body) > maxErrBodyDisplay {
				body = body[:maxErrBodyDisplay] + "...(truncated)"
			}
			httpErr := &HTTPError{Status: resp.StatusCode, Body: body, Path: path}
			if isRetryable(resp.StatusCode, nil) && attempt < retryMax {
				lastErr = httpErr
				// Honour Retry-After if present: sleep the server-directed
				// duration now and skip the next iteration's exponential
				// backoff (do not stack the two waits, do not burn an attempt).
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					var wait time.Duration
					if secs, parseErr := strconv.Atoi(ra); parseErr == nil {
						wait = time.Duration(secs) * time.Second
					} else if t, parseErr := http.ParseTime(ra); parseErr == nil && t.After(time.Now()) {
						wait = time.Until(t)
					}
					if wait > maxRetryAfter {
						wait = maxRetryAfter
					}
					if wait > 0 {
						select {
						case <-ctx.Done():
							return ctx.Err()
						case <-time.After(wait):
						}
					}
					skipBackoff = true
				}
				c.log.LogAttrs(ctx, slog.LevelDebug, "lightrag_retry",
					slog.String("path", path),
					slog.Int("attempt", attempt+1),
					slog.Int("status", resp.StatusCode),
				)
				continue
			}
			return httpErr
		}

		if out == nil || resp.StatusCode == http.StatusNoContent {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return nil
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(out)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return fmt.Errorf("lightrag: decode response: %w", decodeErr)
		}
		return nil
	}
	return lastErr
}

func encodeJSON(v any) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, fmt.Errorf("lightrag: encode body: %w", err)
	}
	return buf, nil
}

// InsertText submits text for async ingest. Returns status + track_id.
func (c *HTTPClient) InsertText(ctx context.Context, req InsertTextRequest) (*InsertResponse, error) {
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out InsertResponse
	if err := c.do(ctx, OpInsertText, http.MethodPost, "/documents/text", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TrackStatus returns the per-doc statuses for the given track_id.
func (c *HTTPClient) TrackStatus(ctx context.Context, trackID string) (*TrackStatusResponse, error) {
	var out TrackStatusResponse
	if err := c.do(ctx, OpTrackStatus, http.MethodGet,
		"/documents/track_status/"+url.PathEscape(trackID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDocs deletes documents by their IDs (background-processed).
func (c *HTTPClient) DeleteDocs(ctx context.Context, req DeleteDocRequest) (*DeleteDocByIdResponse, error) {
	// Encode once to bytes and hand each attempt a fresh reader: roundTrip drains
	// the reader via io.ReadAll, so reusing one *bytes.Buffer across the busy-retry
	// loop would resend an empty body (LightRAG 422 input:null) on every retry.
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("lightrag: encode body: %w", err)
	}
	// status="busy" is a logical 200 (the pipeline lock is held mid-ingest), so the
	// HTTP-level retry in roundTrip does not see it. Retry it here on the same
	// backoff schedule (no Retry-After to honour on a 200); if still busy after
	// retryMax attempts, return the busy response so the memory service maps it to
	// ErrTransient. doAndObserve is used so the whole busy-retry sequence records a
	// single metric + log line with the final outcome, instead of one per attempt.
	var out DeleteDocByIdResponse
	var totalDur float64
	for attempt := 0; attempt <= retryMax; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay * (1 << (attempt - 1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		out = DeleteDocByIdResponse{}
		dur, transportErr := c.doAndObserve(ctx, OpDeleteDocs, http.MethodDelete, "/documents/delete_document", bytes.NewReader(body), &out)
		totalDur += dur
		if transportErr != nil {
			c.metrics.observe(OpDeleteDocs, totalDur, transportErr)
			c.logCall(ctx, OpDeleteDocs, http.MethodDelete, "/documents/delete_document", totalDur, transportErr)
			return nil, transportErr
		}
		if out.Status != "busy" {
			c.metrics.observe(OpDeleteDocs, totalDur, nil)
			c.logCall(ctx, OpDeleteDocs, http.MethodDelete, "/documents/delete_document", totalDur, nil)
			return &out, nil
		}
		c.log.LogAttrs(ctx, slog.LevelDebug, "lightrag_delete_busy_retry",
			slog.Int("attempt", attempt+1),
		)
	}
	c.metrics.observe(OpDeleteDocs, totalDur, nil)
	c.logCall(ctx, OpDeleteDocs, http.MethodDelete, "/documents/delete_document", totalDur, nil)
	return &out, nil
}

// Query executes a retrieval query and returns the generated response.
// Unlike QueryData, /query returns a plain {response, references} body with no
// status/failure envelope (LightRAG v1.4.16), so no LogicalError check is needed.
func (c *HTTPClient) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	if req.Mode != "" && !req.Mode.Valid() {
		c.log.WarnContext(ctx, "lightrag: invalid query mode",
			"op", OpQuery,
			"mode", string(req.Mode),
		)
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

// QueryData executes a structured query and returns entities, relationships, and chunks.
// A HTTP-200 response with a non-success Status envelope is treated as a LogicalError.
// Finding 2: do() is bypassed so that exactly one of success/error is recorded per
// call. do() would have recorded success for the HTTP-200 transport leg, and then the
// logical-failure branch would have added a second error increment - double-counting.
// Instead doAndObserve is used: it runs the round-trip and returns the duration without
// recording any metric, then QueryData calls metrics.observe once with the true outcome.
func (c *HTTPClient) QueryData(ctx context.Context, req QueryRequest) (*QueryDataResponse, error) {
	if req.Mode != "" && !req.Mode.Valid() {
		c.log.WarnContext(ctx, "lightrag: invalid query mode",
			"op", OpQueryData,
			"mode", string(req.Mode),
		)
		c.metrics.incError(OpQueryData)
		return nil, fmt.Errorf("lightrag: invalid query mode %q", req.Mode)
	}
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out QueryDataResponse
	dur, transportErr := c.doAndObserve(ctx, OpQueryData, http.MethodPost, "/query/data", body, &out)
	if transportErr != nil {
		c.metrics.observe(OpQueryData, dur, transportErr)
		c.logCall(ctx, OpQueryData, http.MethodPost, "/query/data", dur, transportErr)
		return nil, transportErr
	}
	if out.Status != "success" {
		logicalErr := &LogicalError{Op: OpQueryData, Status: out.Status, Message: out.Message}
		c.metrics.observe(OpQueryData, dur, logicalErr)
		c.log.WarnContext(ctx, "lightrag: query_data logical failure",
			"op", OpQueryData,
			"status", out.Status,
			"message", out.Message,
			"duration_s", dur,
		)
		return nil, logicalErr
	}
	c.metrics.observe(OpQueryData, dur, nil)
	c.logCall(ctx, OpQueryData, http.MethodPost, "/query/data", dur, nil)
	return &out, nil
}

// EntityExists checks whether an entity by name is present in the graph.
func (c *HTTPClient) EntityExists(ctx context.Context, name string) (bool, error) {
	var out EntityExistsResponse
	path := "/graph/entity/exists?name=" + url.QueryEscape(name)
	if err := c.do(ctx, OpEntityExists, http.MethodGet, path, nil, &out); err != nil {
		return false, err
	}
	return out.Exists, nil
}

// CreateEntity inserts a new entity into the knowledge graph.
func (c *HTTPClient) CreateEntity(ctx context.Context, req EntityCreateRequest) (*EntityResponse, error) {
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out EntityResponse
	if err := c.do(ctx, OpCreateEntity, http.MethodPost, "/graph/entity/create", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateEntity edits an existing entity in the knowledge graph.
func (c *HTTPClient) UpdateEntity(ctx context.Context, req EntityUpdateRequest) (*EntityResponse, error) {
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out EntityResponse
	if err := c.do(ctx, OpUpdateEntity, http.MethodPost, "/graph/entity/edit", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEntity removes an entity (and its incident relations) from the graph.
func (c *HTTPClient) DeleteEntity(ctx context.Context, req DeleteEntityRequest) error {
	body, err := encodeJSON(req)
	if err != nil {
		return err
	}
	return c.do(ctx, OpDeleteEntity, http.MethodDelete, "/documents/delete_entity", body, nil)
}

// LabelSearch returns labels matching q via /graph/label/search.
func (c *HTTPClient) LabelSearch(ctx context.Context, q string) ([]string, error) {
	path := "/graph/label/search?q=" + url.QueryEscape(q)
	var out []string
	if err := c.do(ctx, OpLabelSearch, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Graph returns a connected subgraph rooted at label.
// maxDepth and maxNodes are optional; pass 0 to omit.
func (c *HTTPClient) Graph(ctx context.Context, label string, maxDepth, maxNodes int) (*KnowledgeGraph, error) {
	q := url.Values{}
	q.Set("label", label)
	if maxDepth > 0 {
		q.Set("max_depth", strconv.Itoa(maxDepth))
	}
	if maxNodes > 0 {
		q.Set("max_nodes", strconv.Itoa(maxNodes))
	}
	var out KnowledgeGraph
	if err := c.do(ctx, OpGraph, http.MethodGet, "/graphs?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateRelation adds an undirected relation between two existing entities.
func (c *HTTPClient) CreateRelation(ctx context.Context, req RelationCreateRequest) (*RelationResponse, error) {
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out RelationResponse
	if err := c.do(ctx, OpCreateRelation, http.MethodPost, "/graph/relation/create", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRelation removes the relation between source_entity and target_entity.
func (c *HTTPClient) DeleteRelation(ctx context.Context, req DeleteRelationRequest) error {
	body, err := encodeJSON(req)
	if err != nil {
		return err
	}
	return c.do(ctx, OpDeleteRelation, http.MethodDelete, "/documents/delete_relation", body, nil)
}

// Health checks the LightRAG service health endpoint.
func (c *HTTPClient) Health(ctx context.Context) error {
	return c.do(ctx, OpHealth, http.MethodGet, "/health", nil, nil)
}
