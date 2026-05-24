package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestWriteErrorEnvelope(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.WriteError(rr, 400, "bad input", "req-123")
	require.Equal(t, 400, rr.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "bad input", body["error"])
	require.Equal(t, "req-123", body["request_id"])
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}
