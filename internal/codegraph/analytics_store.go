package codegraph

import (
	"context"
	"time"

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
	// Compute telemetry forwarded from analytics.Result so the worker can
	// record per-compute metrics and a structured INFO log (findings 3, 6, 8).
	NodeCount          int
	EdgeCount          int
	ComputeDurationMs  int64
	BetweennessSkipped bool
}

// RecomputeAnalytics loads the repo's graph, computes signals via gonum, persists
// them to code_entities + code_communities, labels communities (via labeler or
// first non-empty member name when labeler is nil), and clears the dirty flag.
// betweennessMaxNodes is forwarded to analytics.Config.MaxNodes; 0 means no
// limit (not recommended in production - pass a sane cap via AnalyticsWorkerConfig).
//
// The dirty-flag clear is guarded: it only fires when reconciled_at has not
// advanced since the snapshot was taken (finding 1). A push that lands after the
// snapshot but before the commit keeps dirty=true so the next tick recomputes.
func (s *PGStore) RecomputeAnalytics(ctx context.Context, repo string, labeler CommunityLabeler, betweennessMaxNodes int) (RecomputeResult, error) {
	// Capture the snapshot timestamp BEFORE loading the graph so the guard can
	// detect a concurrent push (finding 1).
	var snapshotReconciled time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(reconciled_at, '-infinity'::timestamptz) FROM repo_analytics_state WHERE repo=$1`, repo).
		Scan(&snapshotReconciled)
	if err != nil {
		return RecomputeResult{}, err
	}

	ids, names, err := s.loadEntityIDs(ctx, repo)
	if err != nil {
		return RecomputeResult{}, err
	}
	edges, err := s.loadEdgePairs(ctx, repo)
	if err != nil {
		return RecomputeResult{}, err
	}

	res := analytics.Compute(ids, edges, analytics.Config{MaxNodes: betweennessMaxNodes})

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecomputeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Batch-update all node signals in one statement using an unnest approach to
	// avoid N round-trips (one per entity). degree is omitted: the column was
	// dropped in migration 0006 (finding 4); degree is cheap to compute live and
	// is already computed live in ImportantEntities/ImportantEntitiesBy.
	nodeIDs := make([]string, len(res.Nodes))
	communities := make([]int, len(res.Nodes))
	betweennesses := make([]float64, len(res.Nodes))
	for i, n := range res.Nodes {
		nodeIDs[i] = n.ID
		communities[i] = n.Community
		betweennesses[i] = n.Betweenness
	}
	if len(nodeIDs) > 0 {
		if res.BetweennessSkipped {
			// When betweenness was skipped (graph exceeded BetweennessMaxNodes),
			// every node's Betweenness is 0.0. Writing those zeros would clobber
			// any previously-computed real betweenness values in the column
			// (findings 2, 4). Update only community so prior betweenness is
			// preserved until the graph shrinks back below the cap.
			if _, err := tx.ExecContext(ctx, `
				UPDATE code_entities AS e
				SET community = u.community
				FROM (SELECT unnest($2::text[]) AS id,
				             unnest($3::int[])  AS community) AS u
				WHERE e.repo = $1 AND e.id = u.id`,
				repo, nodeIDs, communities); err != nil {
				return RecomputeResult{}, err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `
				UPDATE code_entities AS e
				SET community = u.community, betweenness = u.betweenness
				FROM (SELECT unnest($2::text[]) AS id,
				             unnest($3::int[])  AS community,
				             unnest($4::float8[]) AS betweenness) AS u
				WHERE e.repo = $1 AND e.id = u.id`,
				repo, nodeIDs, communities, betweennesses); err != nil {
				return RecomputeResult{}, err
			}
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

	// Guard: only clear dirty when reconciled_at has not advanced since the
	// snapshot (finding 1). A push that races in leaves dirty=true so the next
	// tick recomputes with the fresh graph.
	if _, err := tx.ExecContext(ctx, `
		UPDATE repo_analytics_state
		SET dirty=false, computed_at=now()
		WHERE repo=$1
		  AND COALESCE(reconciled_at, '-infinity'::timestamptz) = $2`,
		repo, snapshotReconciled); err != nil {
		return RecomputeResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return RecomputeResult{}, err
	}
	return RecomputeResult{
		Entities:           len(res.Nodes),
		Communities:        len(res.Communities),
		NodeCount:          res.NodeCount,
		EdgeCount:          res.EdgeCount,
		ComputeDurationMs:  res.DurationMs,
		BetweennessSkipped: res.BetweennessSkipped,
	}, nil
}

// labelCommunity returns an LLM label when a labeler is set and succeeds,
// otherwise the first non-empty member name.
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

// loadEdgePairsQuery selects only AST-extractor edges for community/betweenness
// computation. Mixing semantic (LLM-inferred) edges with structural AST edges
// corrupts community and god-node signals with noisy LLM associations (finding 3).
const loadEdgePairsQuery = `SELECT from_id, to_id FROM code_edges WHERE repo=$1 AND extractor='ast'`

func (s *PGStore) loadEdgePairs(ctx context.Context, repo string) ([]analytics.Edge, error) {
	rows, err := s.db.QueryContext(ctx, loadEdgePairsQuery, repo)
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
