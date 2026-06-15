package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

const maxBulkBody = 32 << 20 // 32 MiB

// BulkMemoriesRequest is the /memories:bulk body. ReconcileFiles is the touched
// file set whose prior memories are purged before the items are inserted. Repo
// identifies the repository these items belong to; required when ReconcileFiles
// is non-empty (the purge-before-insert path). It may also be inferred from
// item metadata for back-compat. A legacy bare JSON array of items is still
// accepted (decoded into Items).
type BulkMemoriesRequest struct {
	Repo           string              `json:"repo,omitempty"`
	ReconcileFiles []string            `json:"reconcile_files,omitempty"`
	Items          []memory.IngestItem `json:"items"`
}

// decodeBulk accepts either the BulkMemoriesRequest object or a bare
// []IngestItem (back-compat). A leading '[' selects the array form.
func decodeBulk(body []byte) (BulkMemoriesRequest, error) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []memory.IngestItem
		if err := json.Unmarshal(body, &items); err != nil {
			return BulkMemoriesRequest{}, err
		}
		return BulkMemoriesRequest{Items: items}, nil
	}
	var req BulkMemoriesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return BulkMemoriesRequest{}, err
	}
	return req, nil
}

// repoFromItems returns the repo metadata shared by the items (first non-empty).
func repoFromItems(items []memory.IngestItem) string {
	for _, it := range items {
		if r := it.Metadata["repo"]; r != "" {
			return r
		}
	}
	return ""
}

func readAllLimited(r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBulkBody))
}

func handleBulkIngest(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := readAllLimited(r)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		req, err := decodeBulk(body)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if len(req.Items) == 0 && len(req.ReconcileFiles) == 0 {
			WriteError(w, http.StatusBadRequest, "items must not be empty", RequestIDFromContext(r.Context()))
			return
		}

		// Purge-before-insert: for every reconcile file, drop its prior memories.
		// repo is taken from the explicit top-level field first; item metadata is
		// the back-compat fallback for callers that do not yet send the field.
		if len(req.ReconcileFiles) > 0 {
			repo := req.Repo
			if repo == "" {
				repo = repoFromItems(req.Items)
			}
			if repo == "" {
				WriteError(w, http.StatusBadRequest, "repo is required when reconcile_files is set", RequestIDFromContext(r.Context()))
				return
			}
			totalPurged, err := cfg.Service.DeleteMemoriesBySources(r.Context(), repo, req.ReconcileFiles)
			if err != nil {
				mapServiceError(w, r, err)
				return
			}
			cfg.Logger.InfoContext(r.Context(), "memories.reconcile.purge",
				"action", "reconcile_purge",
				"request_id", RequestIDFromContext(r.Context()),
				"user", claimSubject(r),
				"repo", repo,
				"files", len(req.ReconcileFiles),
				"deleted", totalPurged,
			)
		}

		if len(req.Items) == 0 {
			// Pure deletion reconcile (deleted files only): nothing to enqueue.
			WriteJSON(w, http.StatusAccepted, memory.IngestJob{Status: memory.JobStatusSucceeded})
			return
		}

		job, err := cfg.Ingest.Enqueue(r.Context(), req.Items)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, job)
	}
}

func handleGetJob(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, err := cfg.Ingest.GetJob(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, job)
	}
}
