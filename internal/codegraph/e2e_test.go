//go:build integration

package codegraph_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/codegraph"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestCodeGraphE2E(t *testing.T) {
	s, _ := freshStore(t)
	svc := codegraph.NewService(s, codegraph.NewMetrics(prometheus.NewRegistry()))

	router := httpapi.NewRouter(httpapi.Config{
		Service:   nil, // memory endpoints unused here
		CodeGraph: svc,
		Registry:  prometheus.NewRegistry(),
	})

	// Push a small graph.
	body := `{"repo":"e2e","files":["a.go","b.go"],
		"entities":[
			{"id":"go:func:e2e/a.A","name":"A","type":"go_func","file_path":"a.go"},
			{"id":"go:func:e2e/b.B","name":"B","type":"go_func","file_path":"b.go"}],
		"edges":[{"from":"go:func:e2e/a.A","to":"go:func:e2e/b.B","relation":"calls","src_file":"a.go"}]}`
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/code-graph:bulk", strings.NewReader(body)))
	require.Equal(t, http.StatusOK, w.Code)

	// Callees of A includes B.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code/callees?repo=e2e&id=go:func:e2e/a.A", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "go:func:e2e/b.B")

	// Callers of B includes A.
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/code/callers?repo=e2e&id=go:func:e2e/b.B", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "go:func:e2e/a.A")
}
