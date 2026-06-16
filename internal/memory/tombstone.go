package memory

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// TombstoneStore tracks soft-deleted memory IDs so that GET-after-DELETE
// returns 404 immediately, without waiting for the upstream lightrag
// reindex to finish.
type TombstoneStore struct {
	db          *sql.DB
	markCounter func()
}

// NewTombstoneStore returns a TombstoneStore backed by db.
func NewTombstoneStore(db *sql.DB) *TombstoneStore {
	return &TombstoneStore{db: db}
}

// SetMarkCounter registers a callback invoked each time Mark succeeds.
// Used to increment tatara_memory_tombstone_total{op="created"}.
func (s *TombstoneStore) SetMarkCounter(fn func()) {
	s.markCounter = fn
}

// Mark records a track_id as deleted. Idempotent: re-marking refreshes
// the deleted_at timestamp.
func (s *TombstoneStore) Mark(ctx context.Context, trackID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deleted_memories (track_id) VALUES ($1)
		 ON CONFLICT (track_id) DO UPDATE SET deleted_at = now()`,
		trackID)
	if err != nil {
		return fmt.Errorf("tombstone mark: %w", err)
	}
	if s.markCounter != nil {
		s.markCounter()
	}
	return nil
}

// IsDeleted reports whether the given track_id has been tombstoned.
func (s *TombstoneStore) IsDeleted(ctx context.Context, trackID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM deleted_memories WHERE track_id = $1)`,
		trackID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("tombstone check: %w", err)
	}
	return exists, nil
}

// Unmark removes the tombstone for the given track_id, reversing a Mark call.
// Used by Service.deleteMemoryRaw to roll back a pre-marked tombstone when
// the upstream DeleteDocs call fails permanently, so a retry can re-attempt.
func (s *TombstoneStore) Unmark(ctx context.Context, trackID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deleted_memories WHERE track_id = $1`, trackID)
	if err != nil {
		return fmt.Errorf("tombstone unmark: %w", err)
	}
	return nil
}

// Delete removes the tombstone for the given track_id. Used by the
// reaper after lightrag confirms the document is gone.
func (s *TombstoneStore) Delete(ctx context.Context, trackID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deleted_memories WHERE track_id = $1`, trackID)
	if err != nil {
		return fmt.Errorf("tombstone delete: %w", err)
	}
	return nil
}

// ListOlderThan returns up to limit track_ids of tombstones older than age,
// oldest first. Passing TombstoneReapBatchSize as limit mirrors the fast path's
// cap so a backlog never causes a single tick to load an unbounded set.
func (s *TombstoneStore) ListOlderThan(ctx context.Context, age time.Duration, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT track_id FROM deleted_memories
		 WHERE deleted_at < now() - ($1 * interval '1 second')
		 ORDER BY deleted_at ASC
		 LIMIT $2`,
		int64(age.Seconds()), limit)
	if err != nil {
		return nil, fmt.Errorf("tombstone list older: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("tombstone list older scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// List returns up to `limit` tombstones, oldest first.
func (s *TombstoneStore) List(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT track_id FROM deleted_memories ORDER BY deleted_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("tombstone list: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("tombstone list scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
