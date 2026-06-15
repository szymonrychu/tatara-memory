package httpapi_test

import (
	"encoding/json"
	"net/http"
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

// unencodable is a type that cannot be JSON-marshalled.
type unencodable struct {
	Ch chan int
}

// TestWriteJSONMarshalFailureFallsBackTo500 verifies that WriteJSON returns a
// 500 error envelope when the body cannot be marshalled, rather than silently
// discarding the error after committing a 200 status code.
func TestWriteJSONMarshalFailureFallsBackTo500(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.WriteJSON(rr, http.StatusOK, unencodable{Ch: make(chan int)})

	// Must be 500, not 200, because the marshal failed before WriteHeader was called.
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	// Body must be a valid JSON error envelope.
	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.NotEmpty(t, body["error"])
}

// TestWriteJSONSuccess verifies the happy path is unchanged.
func TestWriteJSONSuccess(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.WriteJSON(rr, http.StatusCreated, map[string]string{"key": "value"})
	require.Equal(t, http.StatusCreated, rr.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "value", body["key"])
}
