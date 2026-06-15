package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type entStub struct {
	stubService
	e memory.Entity
	q []memory.Entity
}

func (s *entStub) GetEntity(_ context.Context, _ string) (memory.Entity, error) { return s.e, nil }
func (s *entStub) SearchEntities(_ context.Context, _ string) ([]memory.Entity, error) {
	return s.q, nil
}
func (s *entStub) PatchEntity(_ context.Context, _ string, p memory.Entity) (memory.Entity, error) {
	return p, nil
}

func TestGetEntity200(t *testing.T) {
	srv := newSrv(t, &entStub{e: memory.Entity{ID: "e1", Name: "tatara"}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/entities/e1")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestSearchEntities200(t *testing.T) {
	srv := newSrv(t, &entStub{q: []memory.Entity{{ID: "e1"}}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/entities?q=tat")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

func TestSearchEntitiesMissingQ400(t *testing.T) {
	srv := newSrv(t, &entStub{})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/entities")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 400, resp.StatusCode)
}

func TestPatchEntity200(t *testing.T) {
	srv := newSrv(t, &entStub{})
	defer srv.Close()
	body, _ := json.Marshal(memory.Entity{Description: "smelter"})
	req, _ := http.NewRequest("PATCH", srv.URL+"/entities/e1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)
}

// TestPatchEntityLogsActor verifies that PATCH /entities/{id} emits a structured
// INFO log with action=patch_entity and a user field (actor scoping requirement).
func TestPatchEntityLogsActor(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	r := httpapi.NewRouter(httpapi.Config{Service: &entStub{}, Logger: logger})
	srv := httptest.NewServer(r)
	defer srv.Close()

	body, _ := json.Marshal(memory.Entity{Description: "smelter"})
	req, _ := http.NewRequest("PATCH", srv.URL+"/entities/e1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 200, resp.StatusCode)

	var actionLine map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		if m["action"] == "patch_entity" {
			actionLine = m
			break
		}
	}
	require.NotNil(t, actionLine, "patch_entity INFO log not emitted")
	_, hasUser := actionLine["user"]
	require.True(t, hasUser, "patch_entity log must include user field")
}
