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
	db *sql.DB
}

// NewTombstoneStore returns a TombstoneStore backed by db.
func NewTombstoneStore(db *sql.DB) *TombstoneStore {
	return &TombstoneStore{db: db}
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

// Delete removes the tombstone for the given track_id. Used by the
// reaper after lightrag confirms the document is gone.
func (s *TombstoneStore) Delete(ctx context.Context, trackID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deleted_memories WHERE track_id = $1`, trackID)
	if err != nil {
		return fmt.Errorf("tombstone delete: %w", err)
	}
	return nil
}

// ReapOlderThan deletes all tombstones older than the given age. Used
// by the reaper for the 24h belt-and-suspenders cleanup.
func (s *TombstoneStore) ReapOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM deleted_memories WHERE deleted_at < now() - ($1 * interval '1 second')`,
		int64(age.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("tombstone reap: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("tombstone reap rows affected: %w", err)
	}
	return n, nil
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
