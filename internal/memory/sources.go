package memory

import (
	"context"
	"database/sql"
	"fmt"
)

// SourceStore indexes which track_ids were produced from a given repo/file, so a
// per-file reconcile can purge exactly that file's memories. Mirrors the
// per-file ownership the code-graph already has.
type SourceStore struct {
	db *sql.DB
}

// NewSourceStore returns a SourceStore backed by db.
func NewSourceStore(db *sql.DB) *SourceStore {
	return &SourceStore{db: db}
}

// Add records that track_id originated from (repo, filePath). Idempotent.
func (s *SourceStore) Add(ctx context.Context, repo, filePath, trackID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO memory_sources (repo, file_path, track_id) VALUES ($1,$2,$3)
		 ON CONFLICT (repo, file_path, track_id) DO NOTHING`,
		repo, filePath, trackID)
	if err != nil {
		return fmt.Errorf("memory_sources add: %w", err)
	}
	return nil
}

// TrackIDs returns the track_ids indexed for (repo, filePath).
func (s *SourceStore) TrackIDs(ctx context.Context, repo, filePath string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT track_id FROM memory_sources WHERE repo=$1 AND file_path=$2`, repo, filePath)
	if err != nil {
		return nil, fmt.Errorf("memory_sources select: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("memory_sources scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteByFile removes every source row for (repo, filePath) and returns the count.
func (s *SourceStore) DeleteByFile(ctx context.Context, repo, filePath string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_sources WHERE repo=$1 AND file_path=$2`, repo, filePath)
	if err != nil {
		return 0, fmt.Errorf("memory_sources delete: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
