package codegraph

import (
	"context"

	"github.com/szymonrychu/tatara-memory/internal/analytics"
)

// DirtyRepos returns repos whose analytics are dirty and whose last reconcile is
// older than debounceSecs (so a settling repo is not recomputed on every edit).
func (s *PGStore) DirtyRepos(ctx context.Context, debounceSecs int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT repo FROM repo_analytics_state
		WHERE dirty=true
		  AND reconciled_at IS NOT NULL
		  AND reconciled_at < now() - make_interval(secs => $1)`, debounceSecs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecomputeResult summarizes a completed recompute for the worker's INFO log
// and metrics. Counts reflect what was persisted in this run.
type RecomputeResult struct {
	Entities    int
	Communities int
}

// RecomputeAnalytics loads the repo's graph, computes signals via gonum, persists
// them to code_entities + code_communities, labels communities (via labeler or
// first non-empty member name (deterministic, Louvain order; not degree-sorted)
// when labeler is nil), and clears the dirty flag.
func (s *PGStore) RecomputeAnalytics(ctx context.Context, repo string, labeler CommunityLabeler) (RecomputeResult, error) {
	ids, names, err := s.loadEntityIDs(ctx, repo)
	if err != nil {
		return RecomputeResult{}, err
	}
	edges, err := s.loadEdgePairs(ctx, repo)
	if err != nil {
		return RecomputeResult{}, err
	}

	res := analytics.Compute(ids, edges)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecomputeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, n := range res.Nodes {
		if _, err := tx.ExecContext(ctx, `
			UPDATE code_entities SET community=$3, degree=$4, betweenness=$5
			WHERE repo=$1 AND id=$2`,
			repo, n.ID, n.Community, n.Degree, n.Betweenness); err != nil {
			return RecomputeResult{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM code_communities WHERE repo=$1`, repo); err != nil {
		return RecomputeResult{}, err
	}
	for _, c := range res.Communities {
		label := labelCommunity(ctx, labeler, c, names)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_communities(repo, community, label, cohesion, size)
			VALUES ($1,$2,$3,$4,$5)`,
			repo, c.Community, label, c.Cohesion, c.Size); err != nil {
			return RecomputeResult{}, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO repo_analytics_state(repo, dirty, computed_at)
		VALUES ($1, false, now())
		ON CONFLICT (repo) DO UPDATE SET dirty=false, computed_at=now()`, repo); err != nil {
		return RecomputeResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return RecomputeResult{}, err
	}
	return RecomputeResult{Entities: len(res.Nodes), Communities: len(res.Communities)}, nil
}

// labelCommunity returns an LLM label when a labeler is set and succeeds,
// otherwise the first non-empty member name (deterministic, Louvain order;
// not degree-sorted).
func labelCommunity(ctx context.Context, labeler CommunityLabeler, c analytics.CommunitySignal, names map[string]string) string {
	memberNames := make([]string, 0, len(c.Members))
	for _, id := range c.Members {
		memberNames = append(memberNames, names[id])
	}
	if labeler != nil {
		if lbl, err := labeler.Label(ctx, memberNames); err == nil && lbl != "" {
			return lbl
		}
	}
	// Fallback: first member name.
	for _, id := range c.Members {
		if names[id] != "" {
			return names[id]
		}
	}
	return ""
}

func (s *PGStore) loadEntityIDs(ctx context.Context, repo string) ([]string, map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM code_entities WHERE repo=$1 ORDER BY id`, repo)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	names := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		names[id] = name
	}
	return ids, names, rows.Err()
}

func (s *PGStore) loadEdgePairs(ctx context.Context, repo string) ([]analytics.Edge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT from_id, to_id FROM code_edges WHERE repo=$1`, repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []analytics.Edge
	for rows.Next() {
		var e analytics.Edge
		if err := rows.Scan(&e.From, &e.To); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
