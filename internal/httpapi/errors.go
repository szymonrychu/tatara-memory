package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
)

type errorEnvelope struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id"`
}

// WriteError writes a JSON error envelope with the given status code, message, and request ID.
func WriteError(w http.ResponseWriter, status int, msg, reqID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(errorEnvelope{Error: msg, RequestID: reqID}); err != nil {
		slog.Warn("WriteError: failed to encode error envelope", "request_id", reqID, "err", err)
	}
}

// WriteJSON marshals body to JSON first; if marshalling succeeds it writes the
// given status code and the payload. If marshalling fails it falls back to a
// 500 error envelope so the committed status code is never misleading.
func WriteJSON(w http.ResponseWriter, status int, body interface{}) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		slog.Warn("WriteJSON: failed to marshal response body", "err", err)
		WriteError(w, http.StatusInternalServerError, "internal error", "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}
