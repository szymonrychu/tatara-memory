package lightrag_test

// Tests for obs-scaffold round-3 findings 2, 3, 8, 11 in internal/lightrag.
// Finding 2: QueryData logical failure must NOT double-count (success+error for one call).
// Finding 3: QueryData logical failure must emit WARN log, not INFO.
// Finding 8: Invalid query mode must emit a WARN log (not just an error counter bump).
// Finding 11: lightrag_call logs must carry request_id from ctx when available.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ctxkeys"
	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

// Finding 2: QueryData HTTP-200 logical failure must not double-count.
// Before the fix: do() recorded success, then incError added another error -> total=2.
// After the fix: exactly one of success/error per call.
func TestHTTPClient_QueryData_LogicalFailure_ExactlyOneCount(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(lightrag.QueryDataResponse{
			Status:  "failure",
			Message: "backend error",
		})
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Registry: reg})
	require.NoError(t, err)

	_, err = c.QueryData(context.Background(), lightrag.QueryRequest{Query: "x"})
	require.Error(t, err)

	mfs, _ := reg.Gather()

	successCount := counterFor(t, mfs, "query_data", "success")
	errorCount := counterFor(t, mfs, "query_data", "error")

	// Exactly one result per call; success must be 0 for a logical failure.
	require.InDelta(t, 0.0, successCount, 0.001,
		"QueryData logical failure must NOT record success (finding 2: no double-count)")
	require.InDelta(t, 1.0, errorCount, 0.001,
		"QueryData logical failure must record exactly one error")
}

// Finding 3: QueryData logical failure must emit a WARN log.
func TestHTTPClient_QueryData_LogicalFailure_EmitsWarnLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(lightrag.QueryDataResponse{
			Status:  "failure",
			Message: "something wrong",
		})
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	_, err = c.QueryData(context.Background(), lightrag.QueryRequest{Query: "x"})
	require.Error(t, err)

	// At least one WARN line mentioning the logical failure.
	var warnFound bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["level"] == "WARN" {
			warnFound = true
			break
		}
	}
	require.True(t, warnFound,
		"QueryData logical failure must emit a WARN log line (finding 3)")
}

// Finding 8: invalid query mode on Query must emit a WARN log.
func TestHTTPClient_Query_InvalidMode_EmitsWarnLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server must not be called for invalid mode")
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	_, err = c.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bogus"})
	require.Error(t, err)

	var warnFound bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["level"] == "WARN" {
			warnFound = true
			break
		}
	}
	require.True(t, warnFound,
		"Query invalid mode must emit a WARN log line (finding 8)")
}

// Finding 8: invalid query mode on QueryData must emit a WARN log.
func TestHTTPClient_QueryData_InvalidMode_EmitsWarnLog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server must not be called for invalid mode")
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	_, err = c.QueryData(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bad"})
	require.Error(t, err)

	var warnFound bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["level"] == "WARN" {
			warnFound = true
			break
		}
	}
	require.True(t, warnFound,
		"QueryData invalid mode must emit a WARN log line (finding 8)")
}

// Finding 11: lightrag_call log must carry request_id from ctx when set.
func TestHTTPClient_Do_LogCarriesRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	// Inject request_id into context using the shared ctxkeys key.
	ctx := context.WithValue(context.Background(), ctxkeys.RequestID, "req-abc123")
	require.NoError(t, c.Health(ctx))

	var logLine map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["msg"] == "lightrag_call" {
			logLine = entry
			break
		}
	}
	require.NotNil(t, logLine, "lightrag_call log line must be emitted")
	require.Equal(t, "req-abc123", logLine["request_id"],
		"lightrag_call log must carry request_id from ctx (finding 11)")
}

// Finding 11: when no request_id in ctx, the log line must not have the field.
func TestHTTPClient_Do_LogOmitsRequestIDWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL, Logger: logger})
	require.NoError(t, err)

	require.NoError(t, c.Health(context.Background()))

	var logLine map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry["msg"] == "lightrag_call" {
			logLine = entry
			break
		}
	}
	require.NotNil(t, logLine, "lightrag_call log line must be emitted")
	_, hasReqID := logLine["request_id"]
	require.False(t, hasReqID,
		"lightrag_call log must omit request_id when not in ctx")
}
