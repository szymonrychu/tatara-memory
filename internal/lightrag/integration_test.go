//go:build integration

package lightrag_test

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func newIntegrationClient(t *testing.T) *lightrag.HTTPClient {
	t.Helper()
	base := os.Getenv("LIGHTRAG_BASE_URL")
	if base == "" {
		t.Skip("LIGHTRAG_BASE_URL not set; skipping integration test")
	}
	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:    strings.TrimRight(base, "/"),
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	})
	require.NoError(t, err)
	return c
}

func TestIntegration_Health(t *testing.T) {
	c := newIntegrationClient(t)
	require.NoError(t, c.Health(context.Background()))
}

func TestIntegration_InsertTextThenTrackStatus(t *testing.T) {
	c := newIntegrationClient(t)
	ctx := context.Background()

	resp, err := c.InsertText(ctx, lightrag.InsertTextRequest{
		Text:       "Tatara is a traditional Japanese smelting furnace used for producing steel.",
		FileSource: "tatara-memory-integration-test",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.TrackID, "track_id required on insert response")
	require.Contains(t, []string{"success", "duplicated", "partial_success"}, resp.Status)

	deadline := time.Now().Add(60 * time.Second)
	var ts *lightrag.TrackStatusResponse
	for time.Now().Before(deadline) {
		ts, err = c.TrackStatus(ctx, resp.TrackID)
		require.NoError(t, err)
		if ts.TotalCount > 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NotNil(t, ts)
	require.Greater(t, ts.TotalCount, 0, "track_status should report at least one doc within 60s")

	docIDs := make([]string, 0, ts.TotalCount)
	for _, d := range ts.Documents {
		docIDs = append(docIDs, d.ID)
	}
	_, err = c.DeleteDocs(ctx, lightrag.DeleteDocRequest{DocIDs: docIDs})
	require.NoError(t, err)
}

func TestIntegration_QueryRoundTrip(t *testing.T) {
	c := newIntegrationClient(t)
	ctx := context.Background()

	resp, err := c.Query(ctx, lightrag.QueryRequest{
		Query: "what is tatara",
		Mode:  lightrag.QueryModeHybrid,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Response, "/query must return a response field")
}

func TestIntegration_LabelSearch(t *testing.T) {
	c := newIntegrationClient(t)
	ctx := context.Background()

	_, err := c.LabelSearch(ctx, "")
	require.NoError(t, err)
}
