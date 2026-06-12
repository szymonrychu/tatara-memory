package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

// PGStore is a PostgreSQL-backed implementation of JobStore.
type PGStore struct {
	db *sql.DB
}

// NewPGStore returns a PGStore backed by the given database connection.
func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

// CreateJob inserts the job and all its items in a single transaction.
func (s *PGStore) CreateJob(ctx context.Context, j memory.IngestJob, items []memory.IngestItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	errJSON, _ := json.Marshal(j.Errors)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ingest_jobs(id, status, total, done, failed, errors_json, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		j.ID, string(j.Status), j.Total, j.Done, j.Failed, string(errJSON), j.CreatedAt, j.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrJobExists
		}
		return err
	}
	for _, it := range items {
		metaJSON, _ := json.Marshal(it.Metadata)
		if it.Metadata == nil {
			metaJSON = []byte("{}")
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ingest_job_items(id, job_id, idempotency_key, status, error, text, metadata, created_at)
			VALUES ($1,$2,$3,'pending','',$4,$5,now())`,
			newItemID(), j.ID, it.IdempotencyKey, it.Text, string(metaJSON))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetJob retrieves the current state of a job by ID.
func (s *PGStore) GetJob(ctx context.Context, id string) (memory.IngestJob, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, status, total, done, failed, errors_json, created_at, updated_at
		FROM ingest_jobs WHERE id = $1`, id)
	var j memory.IngestJob
	var status, errJSON string
	if err := row.Scan(&j.ID, &status, &j.Total, &j.Done, &j.Failed, &errJSON, &j.CreatedAt, &j.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memory.IngestJob{}, ErrJobNotFound
		}
		return memory.IngestJob{}, err
	}
	j.Status = memory.JobStatus(status)
	_ = json.Unmarshal([]byte(errJSON), &j.Errors)
	return j, nil
}

// UpdateJob replaces the stored job state.
func (s *PGStore) UpdateJob(ctx context.Context, j memory.IngestJob) error {
	errJSON, _ := json.Marshal(j.Errors)
	res, err := s.db.ExecContext(ctx, `
		UPDATE ingest_jobs SET status=$2, total=$3, done=$4, failed=$5, errors_json=$6, updated_at=$7
		WHERE id=$1`,
		j.ID, string(j.Status), j.Total, j.Done, j.Failed, string(errJSON), time.Now())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrJobNotFound
	}
	return nil
}

// ClaimNextItem atomically selects the next pending item and marks it running.
func (s *PGStore) ClaimNextItem(ctx context.Context, jobID string) (memory.IngestItem, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return memory.IngestItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT id, idempotency_key, text, metadata FROM ingest_job_items
		WHERE job_id=$1 AND status='pending'
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, jobID)
	var id, key, text, metaJSON string
	if err := row.Scan(&id, &key, &text, &metaJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memory.IngestItem{}, false, tx.Commit()
		}
		return memory.IngestItem{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_job_items SET status='running' WHERE id=$1`, id); err != nil {
		return memory.IngestItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return memory.IngestItem{}, false, err
	}
	var meta map[string]string
	if metaJSON != "" {
		_ = json.Unmarshal([]byte(metaJSON), &meta)
	}
	return memory.IngestItem{IdempotencyKey: key, Text: text, Metadata: meta}, true, nil
}

// MarkItemDone records the outcome of a processed item.
func (s *PGStore) MarkItemDone(ctx context.Context, jobID, key string, runErr error) error {
	status := "done"
	errStr := ""
	if runErr != nil {
		status = "failed"
		errStr = runErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE ingest_job_items SET status=$3, error=$4
		WHERE job_id=$1 AND idempotency_key=$2`,
		jobID, key, status, errStr)
	return err
}

// IncrementJobProgress atomically bumps done or failed for one processed item.
// On success it is a single counter UPDATE. On failure it locks the job row so
// the failed bump and the capped error append happen as one critical section,
// preventing concurrent workers from losing increments via read-modify-write.
func (s *PGStore) IncrementJobProgress(ctx context.Context, jobID string, itemErr *memory.IngestItemError) error {
	if itemErr == nil {
		res, err := s.db.ExecContext(ctx, `
			UPDATE ingest_jobs SET done = done + 1, updated_at = $2 WHERE id = $1`,
			jobID, time.Now())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrJobNotFound
		}
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `SELECT errors_json FROM ingest_jobs WHERE id = $1 FOR UPDATE`, jobID)
	var errJSON string
	if err := row.Scan(&errJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrJobNotFound
		}
		return err
	}
	var errs []memory.IngestItemError
	_ = json.Unmarshal([]byte(errJSON), &errs)
	if len(errs) < maxErrors {
		errs = append(errs, *itemErr)
	}
	out, _ := json.Marshal(errs)
	if _, err := tx.ExecContext(ctx, `
		UPDATE ingest_jobs SET failed = failed + 1, errors_json = $2, updated_at = $3 WHERE id = $1`,
		jobID, string(out), time.Now()); err != nil {
		return err
	}
	return tx.Commit()
}

// ListUnfinishedJobs returns the IDs of all jobs that are queued or running,
// i.e. enqueued but not yet terminal. Used at startup to resume work that a
// crash or restart left scheduled-but-undrained.
func (s *PGStore) ListUnfinishedJobs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM ingest_jobs WHERE status IN ('queued','running')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(msg, "duplicate key")
}
