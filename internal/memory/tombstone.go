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

// ForceCandidate pairs a tombstone's track_id with its current
// force_reap_attempts count, so the reaper can compute the next backoff
// interval without a second round trip per row.
type ForceCandidate struct {
	TrackID  string
	Attempts int
}

// ListOlderThan returns up to limit force-reap candidates for tombstones older
// than age, oldest first: track_ids whose next_force_check_at has elapsed (or
// was never set), paired with their current attempt count. Passing
// TombstoneReapBatchSize as limit mirrors the fast path's cap so a backlog
// never causes a single tick to load an unbounded set.
func (s *TombstoneStore) ListOlderThan(ctx context.Context, age time.Duration, limit int) ([]ForceCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT track_id, force_reap_attempts FROM deleted_memories
		 WHERE deleted_at < now() - ($1 * interval '1 second')
		   AND (next_force_check_at IS NULL OR next_force_check_at <= now())
		 ORDER BY deleted_at ASC
		 LIMIT $2`,
		int64(age.Seconds()), limit)
	if err != nil {
		return nil, fmt.Errorf("tombstone list older: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ForceCandidate
	for rows.Next() {
		var c ForceCandidate
		if err := rows.Scan(&c.TrackID, &c.Attempts); err != nil {
			return nil, fmt.Errorf("tombstone list older scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordForceCheckStillPresent bumps force_reap_attempts and sets
// next_force_check_at, deferring the next force-reap verification of trackID
// until nextCheckAt. Called by the reaper's forced-reap path when lightrag
// still reports the doc present, so a permanently-stuck upstream delete backs
// off instead of being re-verified on every tick.
func (s *TombstoneStore) RecordForceCheckStillPresent(ctx context.Context, trackID string, nextCheckAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deleted_memories
		 SET force_reap_attempts = force_reap_attempts + 1, next_force_check_at = $2
		 WHERE track_id = $1`,
		trackID, nextCheckAt)
	if err != nil {
		return fmt.Errorf("tombstone record force check: %w", err)
	}
	return nil
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
