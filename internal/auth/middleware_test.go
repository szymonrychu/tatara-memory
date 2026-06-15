package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth"
	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
)

func TestMiddleware_ValidTokenInjectsClaims(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(auth.Middleware(v))
	r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
		c, ok := auth.ClaimsFromContext(req.Context())
		require.True(t, ok)
		_, _ = w.Write([]byte(c.Subject))
	})

	tok := srv.SignTypedToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "user-1", rec.Body.String())
}

func TestMiddleware_MissingTokenReturns401(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(auth.Middleware(v))
	r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, `Bearer realm="tatara-memory"`, rec.Header().Get("WWW-Authenticate"))
}

func TestMiddleware_InvalidTokenReturns401(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(auth.Middleware(v))
	r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, `Bearer realm="tatara-memory"`, rec.Header().Get("WWW-Authenticate"))
}

// authCounterFor returns the auth_attempts_total counter for the given result label.
func authCounterFor(t *testing.T, reg *prometheus.Registry, result string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != "auth_attempts_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "result" && lp.GetValue() == result {
					return m.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
}

// TestMiddlewareWithMetrics_CountsAttempts verifies auth_attempts_total is
// incremented for success, missing_token, and invalid_token paths (finding 12).
func TestMiddlewareWithMetrics_CountsAttempts(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	mw := auth.MiddlewareWithMetrics(v, reg)

	r := chi.NewRouter()
	r.Use(mw)
	r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
		c, ok := auth.ClaimsFromContext(req.Context())
		require.True(t, ok)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(c.Subject))
	})

	// missing token -> missing_token
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.InDelta(t, 1.0, authCounterFor(t, reg, "missing_token"), 0.0001)

	// invalid token -> invalid_token
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.InDelta(t, 1.0, authCounterFor(t, reg, "invalid_token"), 0.0001)

	// valid token -> success
	tok := srv.SignToken(map[string]interface{}{
		"sub": "u1", "aud": "tatara-memory",
	})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.InDelta(t, 1.0, authCounterFor(t, reg, "success"), 0.0001)
}

// TestMiddlewareWithMetrics_InvalidScheme verifies invalid_scheme path is counted.
func TestMiddlewareWithMetrics_InvalidScheme(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	reg := prometheus.NewRegistry()
	r := chi.NewRouter()
	r.Use(auth.MiddlewareWithMetrics(v, reg))
	r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz") // not Bearer
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.InDelta(t, 1.0, authCounterFor(t, reg, "invalid_scheme"), 0.0001)
}

// logEntry is used to decode JSON log lines in logger-injection tests.
type logEntry struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	User  string `json:"user"`
}

// TestMiddlewareWithLogger_UsesInjectedLogger verifies that the injected logger
// receives rejection WARNs and success INFOs (finding 2: rule-11 JSON logger; finding 1: success log).
func TestMiddlewareWithLogger_UsesInjectedLogger(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := chi.NewRouter()
	r.Use(auth.MiddlewareWithLogger(v, logger))
	r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
		c, ok := auth.ClaimsFromContext(req.Context())
		require.True(t, ok)
		_, _ = w.Write([]byte(c.Subject))
	})

	// rejection -> WARN in injected logger
	buf.Reset()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	var warnEntry logEntry
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &warnEntry))
	require.Equal(t, "WARN", warnEntry.Level)
	require.Equal(t, "auth: rejected", warnEntry.Msg)

	// success -> INFO in injected logger with user field
	buf.Reset()
	tok := srv.SignToken(map[string]interface{}{
		"sub": "alice", "aud": "tatara-memory",
	})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var infoEntry logEntry
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &infoEntry))
	require.Equal(t, "INFO", infoEntry.Level)
	require.Equal(t, "auth: accepted", infoEntry.Msg)
	require.Equal(t, "alice", infoEntry.User)
}

// TestMiddlewareWithMetricsAndLogger_CountsAndLogs is the production-path test:
// both metrics and the injected logger fire correctly.
func TestMiddlewareWithMetricsAndLogger_CountsAndLogs(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := prometheus.NewRegistry()

	r := chi.NewRouter()
	r.Use(auth.MiddlewareWithMetricsAndLogger(v, reg, logger))
	r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
		c, ok := auth.ClaimsFromContext(req.Context())
		require.True(t, ok)
		_, _ = w.Write([]byte(c.Subject))
	})

	// valid token -> success counter + INFO log
	tok := srv.SignToken(map[string]interface{}{
		"sub": "bob", "aud": "tatara-memory",
	})
	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.InDelta(t, 1.0, authCounterFor(t, reg, "success"), 0.0001)

	var entry logEntry
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	require.Equal(t, "INFO", entry.Level)
	require.Equal(t, "auth: accepted", entry.Msg)
	require.Equal(t, "bob", entry.User)
}
