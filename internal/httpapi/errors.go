package httpapi

import (
	"encoding/json"
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
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: msg, RequestID: reqID})
}

// WriteJSON serialises body to JSON and writes it with the given status code.
func WriteJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
