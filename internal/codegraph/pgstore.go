package codegraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// maxImportCycleDepth is the maximum recursion depth used when detecting import
// cycles in Stats. Keeps the CTE bounded on large graphs with long dependency
// chains; cycles deeper than this are not counted.
const maxImportCycleDepth = 20

// PGStore is a PostgreSQL-backed implementation of Store.
type PGStore struct {
	db *sql.DB
}

// NewPGStore returns a PGStore backed by the given database connection.
func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

// DB returns the underlying database connection (for testing).
func (s *PGStore) DB() *sql.DB {
	return s.db
}

func marshalProps(p map[string]string) string {
	if len(p) == 0 {
		return "{}"
	}
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func scanProps(raw []byte) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// Reconcile deletes the prior graph owned by p.Files then inserts p.Entities,
// p.Edges, p.Symbols, and p.Hyperedges, all in a single transaction.
func (s *PGStore) Reconcile(ctx context.Context, p GraphPush) (PushResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PushResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, f := range p.Files {
		if _, err := tx.ExecContext(ctx, `DELETE FROM code_edges WHERE repo=$1 AND src_file=$2`, p.Repo, f); err != nil {
			return PushResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM code_entities WHERE repo=$1 AND file_path=$2`, p.Repo, f); err != nil {
			return PushResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cross_repo_symbols WHERE repo=$1 AND src_file=$2`, p.Repo, f); err != nil {
			return PushResult{}, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM code_hyperedge_members WHERE repo=$1 AND hyperedge_id IN (
				SELECT id FROM code_hyperedges WHERE repo=$1 AND src_file=$2)`, p.Repo, f); err != nil {
			return PushResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM code_hyperedges WHERE repo=$1 AND src_file=$2`, p.Repo, f); err != nil {
			return PushResult{}, err
		}
	}

	for _, e := range p.Entities {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_entities(repo, id, name, type, description, file_path, properties,
				line_start, line_end, source_url, author, captured_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,$12)
			ON CONFLICT (repo, id) DO UPDATE SET
				name=EXCLUDED.name, type=EXCLUDED.type, description=EXCLUDED.description,
				file_path=EXCLUDED.file_path, properties=EXCLUDED.properties,
				line_start=EXCLUDED.line_start, line_end=EXCLUDED.line_end,
				source_url=EXCLUDED.source_url, author=EXCLUDED.author, captured_at=EXCLUDED.captured_at`,
			p.Repo, e.ID, e.Name, e.Type, e.Description, e.FilePath, marshalProps(e.Properties),
			nullInt(e.LineStart), nullInt(e.LineEnd), nullStr(e.SourceURL), nullStr(e.Author), nullTime(e.CapturedAt)); err != nil {
			return PushResult{}, err
		}
	}

	for _, e := range p.Edges {
		score := e.ConfidenceScore
		tier := e.ConfidenceTier
		if score == 0 && tier == "" {
			score, tier = 1.0, TierExtracted
		} else if tier == "" {
			tier = TierFor(score)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_edges(repo, from_id, to_id, relation, src_file, properties, confidence_score, confidence_tier)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb,$7,$8)
			ON CONFLICT (repo, from_id, to_id, relation) DO UPDATE SET
				src_file=EXCLUDED.src_file, properties=EXCLUDED.properties,
				confidence_score=EXCLUDED.confidence_score, confidence_tier=EXCLUDED.confidence_tier`,
			p.Repo, e.From, e.To, e.Relation, e.SrcFile, marshalProps(e.Properties), score, tier); err != nil {
			return PushResult{}, err
		}
	}

	for _, sym := range p.Symbols {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cross_repo_symbols(repo, symbol, lang, kind, role, entity_id, src_file)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (repo, symbol, role, entity_id) DO UPDATE SET
			    lang=EXCLUDED.lang, kind=EXCLUDED.kind, src_file=EXCLUDED.src_file`,
			p.Repo, sym.Symbol, sym.Lang, sym.Kind, sym.Role, sym.EntityID, sym.SrcFile); err != nil {
			return PushResult{}, err
		}
	}

	for _, h := range p.Hyperedges {
		score := h.ConfidenceScore
		if score == 0 {
			score = 1.0
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_hyperedges(repo, id, label, relation, confidence_score, src_file, properties)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
			ON CONFLICT (repo, id) DO UPDATE SET
				label=EXCLUDED.label, relation=EXCLUDED.relation,
				confidence_score=EXCLUDED.confidence_score, src_file=EXCLUDED.src_file, properties=EXCLUDED.properties`,
			p.Repo, h.ID, h.Label, h.Relation, score, h.SrcFile, marshalProps(h.Properties)); err != nil {
			return PushResult{}, err
		}
		for _, m := range h.Members {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO code_hyperedge_members(repo, hyperedge_id, entity_id)
				VALUES ($1,$2,$3)
				ON CONFLICT (repo, hyperedge_id, entity_id) DO NOTHING`,
				p.Repo, h.ID, m); err != nil {
				return PushResult{}, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return PushResult{}, err
	}
	return PushResult{
		Repo:             p.Repo,
		Files:            len(p.Files),
		EntitiesUpserted: len(p.Entities),
		EdgesUpserted:    len(p.Edges),
	}, nil
}

// CountEntities returns the number of entities stored for a repo.
func (s *PGStore) CountEntities(ctx context.Context, repo string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM code_entities WHERE repo=$1`, repo).Scan(&n)
	return n, err
}

// SearchEntities returns entities in repo matching an optional name/description
// fragment and optional exact type, ordered by relevance (exact name=0,
// name prefix=1, name substring=2, description substring=3), tie-broken by name.
func (s *PGStore) SearchEntities(ctx context.Context, repo, q, typ string, limit int) ([]Entity, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, description, file_path, properties
		FROM code_entities
		WHERE repo=$1
		  AND ($2='' OR name ILIKE '%'||$2||'%' OR description ILIKE '%'||$2||'%')
		  AND ($3='' OR type=$3)
		ORDER BY
		  CASE
		    WHEN $2='' THEN 4
		    WHEN name ILIKE $2 THEN 0
		    WHEN name ILIKE $2||'%' THEN 1
		    WHEN name ILIKE '%'||$2||'%' THEN 2
		    ELSE 3
		  END,
		  name
		LIMIT $4`, repo, q, typ, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetEntity returns one entity plus its immediate outgoing and incoming edges.
// Edges are returned as stored and may reference targets absent from this repo's
// entities (orphans from a later file change); traversal queries filter those.
func (s *PGStore) GetEntity(ctx context.Context, repo, id string) (EntityDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, type, description, file_path, properties
		FROM code_entities WHERE repo=$1 AND id=$2`, repo, id)
	e, err := scanEntity(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EntityDetail{}, ErrEntityNotFound
		}
		return EntityDetail{}, err
	}
	out, err := s.queryEdges(ctx,
		`SELECT from_id, to_id, relation, src_file, properties FROM code_edges WHERE repo=$1 AND from_id=$2`,
		repo, id)
	if err != nil {
		return EntityDetail{}, err
	}
	in, err := s.queryEdges(ctx,
		`SELECT from_id, to_id, relation, src_file, properties FROM code_edges WHERE repo=$1 AND to_id=$2`,
		repo, id)
	if err != nil {
		return EntityDetail{}, err
	}
	return EntityDetail{Entity: e, OutEdges: out, InEdges: in}, nil
}

// neighborsOutQuery walks from->to (forward). neighborsInQuery walks to->from
// (reverse). Two fixed queries avoid building SQL by string concatenation
// (gosec G202). Orphan targets are dropped by the join to code_entities.
const neighborsOutQuery = `
	WITH RECURSIVE walk(id, depth) AS (
		SELECT $2::text, 0
		UNION
		SELECT e.to_id, w.depth + 1
		FROM walk w
		JOIN code_edges e ON e.repo=$1 AND e.from_id=w.id
		 AND e.relation = ANY(string_to_array($3, ','))
		WHERE w.depth < $4
	)
	SELECT DISTINCT ON (en.id) en.id, en.name, en.type, en.description, en.file_path, en.properties, w.depth
	FROM walk w
	JOIN code_entities en ON en.repo=$1 AND en.id=w.id
	WHERE w.depth > 0
	ORDER BY en.id, w.depth`

const neighborsInQuery = `
	WITH RECURSIVE walk(id, depth) AS (
		SELECT $2::text, 0
		UNION
		SELECT e.from_id, w.depth + 1
		FROM walk w
		JOIN code_edges e ON e.repo=$1 AND e.to_id=w.id
		 AND e.relation = ANY(string_to_array($3, ','))
		WHERE w.depth < $4
	)
	SELECT DISTINCT ON (en.id) en.id, en.name, en.type, en.description, en.file_path, en.properties, w.depth
	FROM walk w
	JOIN code_entities en ON en.repo=$1 AND en.id=w.id
	WHERE w.depth > 0
	ORDER BY en.id, w.depth`

const neighborsOutCFQuery = `
	WITH RECURSIVE walk(id, depth) AS (
		SELECT $2::text, 0
		UNION
		SELECT e.to_id, w.depth + 1
		FROM walk w
		JOIN code_edges e ON e.repo=$1 AND e.from_id=w.id
		 AND e.relation = ANY(string_to_array($3, ','))
		 AND e.confidence_score >= $5
		 AND ($6='' OR e.confidence_tier=$6)
		WHERE w.depth < $4
	)
	SELECT DISTINCT ON (en.id) en.id, en.name, en.type, en.description, en.file_path, en.properties, w.depth
	FROM walk w
	JOIN code_entities en ON en.repo=$1 AND en.id=w.id
	WHERE w.depth > 0
	ORDER BY en.id, w.depth`

const neighborsInCFQuery = `
	WITH RECURSIVE walk(id, depth) AS (
		SELECT $2::text, 0
		UNION
		SELECT e.from_id, w.depth + 1
		FROM walk w
		JOIN code_edges e ON e.repo=$1 AND e.to_id=w.id
		 AND e.relation = ANY(string_to_array($3, ','))
		 AND e.confidence_score >= $5
		 AND ($6='' OR e.confidence_tier=$6)
		WHERE w.depth < $4
	)
	SELECT DISTINCT ON (en.id) en.id, en.name, en.type, en.description, en.file_path, en.properties, w.depth
	FROM walk w
	JOIN code_entities en ON en.repo=$1 AND en.id=w.id
	WHERE w.depth > 0
	ORDER BY en.id, w.depth`

// Neighbors walks edges of the given relations from id, in the given direction
// ("out" follows from->to, "in" follows to->from), up to depth hops.
// cf is an optional confidence filter; zero value means no filtering.
func (s *PGStore) Neighbors(ctx context.Context, repo, id string, relations []string, dir string, depth int, cf ConfidenceFilter) ([]PathNode, error) {
	var rows *sql.Rows
	var err error
	relStr := strings.Join(relations, ",")
	if cf.MinConfidence > 0 || cf.Tier != "" {
		query := neighborsOutCFQuery
		if dir == "in" {
			query = neighborsInCFQuery
		}
		rows, err = s.db.QueryContext(ctx, query, repo, id, relStr, depth, cf.MinConfidence, cf.Tier)
	} else {
		query := neighborsOutQuery
		if dir == "in" {
			query = neighborsInQuery
		}
		rows, err = s.db.QueryContext(ctx, query, repo, id, relStr, depth)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PathNode
	for rows.Next() {
		var n PathNode
		var raw []byte
		if err := rows.Scan(&n.ID, &n.Name, &n.Type, &n.Description, &n.FilePath, &raw, &n.Depth); err != nil {
			return nil, err
		}
		n.Properties = scanProps(raw)
		out = append(out, n)
	}
	return out, rows.Err()
}

// shortestPathQuery uses a recursive CTE to find the shortest path from $2 to $5.
// Cycle detection uses a text[] array so that membership is exact (no false positives
// when one entity ID is a prefix/substring of another).
// The path array is converted to a '|'-separated string for easy scanning.
const shortestPathQuery = `
	WITH RECURSIVE walk(id, path_arr, depth) AS (
		SELECT $2::text, ARRAY[$2::text], 0
		UNION ALL
		SELECT e.to_id,
		       walk.path_arr || e.to_id,
		       walk.depth + 1
		FROM walk
		JOIN code_edges e ON e.repo=$1 AND e.from_id=walk.id
		  AND ($3='' OR e.relation = ANY(string_to_array($3, ',')))
		WHERE walk.depth < $4
		  AND e.to_id <> ALL(walk.path_arr)
	)
	SELECT array_to_string(path_arr, '|') FROM walk WHERE id=$5 ORDER BY depth LIMIT 1`

// ShortestPath returns the ordered entity chain from fromID to toID (inclusive),
// or an empty slice if unreachable within maxDepth hops.
func (s *PGStore) ShortestPath(ctx context.Context, repo, fromID, toID string, relations []string, maxDepth int) ([]Entity, error) {
	relStr := strings.Join(relations, ",")
	row := s.db.QueryRowContext(ctx, shortestPathQuery, repo, fromID, relStr, maxDepth, toID)
	var pathStr string
	if err := row.Scan(&pathStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return []Entity{}, nil
		}
		return nil, err
	}
	ids := strings.Split(pathStr, "|")
	out := make([]Entity, 0, len(ids))
	for _, pid := range ids {
		r := s.db.QueryRowContext(ctx,
			`SELECT id, name, type, description, file_path, properties FROM code_entities WHERE repo=$1 AND id=$2`,
			repo, pid)
		e, err := scanEntity(r)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// ImportantEntities returns entities ranked by degree (in+out edge count) DESC.
func (s *PGStore) ImportantEntities(ctx context.Context, repo string, limit int) ([]EntityDegree, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT en.id, en.name, en.type, en.description, en.file_path, en.properties,
		       COALESCE(out_c.cnt, 0) + COALESCE(in_c.cnt, 0) AS degree
		FROM code_entities en
		LEFT JOIN (
			SELECT from_id AS id, count(*) AS cnt
			FROM code_edges WHERE repo=$1 GROUP BY from_id
		) out_c ON out_c.id=en.id
		LEFT JOIN (
			SELECT to_id AS id, count(*) AS cnt
			FROM code_edges WHERE repo=$1 GROUP BY to_id
		) in_c ON in_c.id=en.id
		WHERE en.repo=$1
		ORDER BY degree DESC
		LIMIT $2`, repo, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []EntityDegree
	for rows.Next() {
		var ed EntityDegree
		var raw []byte
		if err := rows.Scan(&ed.ID, &ed.Name, &ed.Type, &ed.Description, &ed.FilePath, &raw, &ed.Degree); err != nil {
			return nil, err
		}
		ed.Properties = scanProps(raw)
		out = append(out, ed)
	}
	return out, rows.Err()
}

// Stats returns aggregate counts for a repo's code graph.
func (s *PGStore) Stats(ctx context.Context, repo string) (GraphStats, error) {
	var gs GraphStats

	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM code_entities WHERE repo=$1`, repo).Scan(&gs.Entities); err != nil {
		return GraphStats{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM code_edges WHERE repo=$1`, repo).Scan(&gs.Edges); err != nil {
		return GraphStats{}, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT type, count(*) FROM code_entities WHERE repo=$1 GROUP BY type`, repo)
	if err != nil {
		return GraphStats{}, err
	}
	gs.EntitiesByType = make(map[string]int)
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return GraphStats{}, err
		}
		gs.EntitiesByType[k] = v
	}
	if err := rows.Close(); err != nil {
		return GraphStats{}, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT relation, count(*) FROM code_edges WHERE repo=$1 GROUP BY relation`, repo)
	if err != nil {
		return GraphStats{}, err
	}
	gs.EdgesByRelation = make(map[string]int)
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return GraphStats{}, err
		}
		gs.EdgesByRelation[k] = v
	}
	if err := rows.Close(); err != nil {
		return GraphStats{}, err
	}

	rows, err = s.db.QueryContext(ctx, `SELECT confidence_tier, count(*) FROM code_edges WHERE repo=$1 GROUP BY confidence_tier`, repo)
	if err != nil {
		return GraphStats{}, err
	}
	gs.EdgesByTier = make(map[string]int)
	for rows.Next() {
		var k string
		var v int
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return GraphStats{}, err
		}
		gs.EdgesByTier[k] = v
	}
	if err := rows.Close(); err != nil {
		return GraphStats{}, err
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*) FROM code_entities en
		WHERE en.repo=$1
		  AND NOT EXISTS (
		      SELECT 1 FROM code_edges e
		      WHERE e.repo=$1 AND (e.from_id=en.id OR e.to_id=en.id)
		  )`, repo).Scan(&gs.IsolatedEntities); err != nil {
		return GraphStats{}, err
	}

	// Import cycles: count distinct start nodes that can reach themselves via 'imports'.
	if err := s.db.QueryRowContext(ctx, `
		WITH RECURSIVE cycle_check(start_id, cur_id, depth) AS (
			SELECT e.from_id, e.to_id, 1
			FROM code_edges e
			WHERE e.repo=$1 AND e.relation='imports'
			UNION ALL
			SELECT cc.start_id, e.to_id, cc.depth + 1
			FROM cycle_check cc
			JOIN code_edges e ON e.repo=$1 AND e.relation='imports' AND e.from_id=cc.cur_id
			WHERE cc.depth < $2 AND cc.cur_id <> cc.start_id
		)
		SELECT count(DISTINCT start_id) FROM cycle_check WHERE cur_id=start_id`,
		repo, maxImportCycleDepth).Scan(&gs.ImportCycles); err != nil {
		return GraphStats{}, err
	}

	return gs, nil
}

// AmbiguousEdges returns edges where confidence_tier='AMBIGUOUS' OR confidence_score<=ambiguousScoreThreshold.
func (s *PGStore) AmbiguousEdges(ctx context.Context, repo string, limit int) ([]Edge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_id, to_id, relation, src_file, properties, confidence_score, confidence_tier
		FROM code_edges
		WHERE repo=$1 AND (confidence_tier='AMBIGUOUS' OR confidence_score<=$3)
		ORDER BY confidence_score
		LIMIT $2`, repo, limit, ambiguousScoreThreshold)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEdgesWithConfidence(rows)
}

// EntityExplain returns EntityDetail plus labeled in/out neighbor entities.
func (s *PGStore) EntityExplain(ctx context.Context, repo, id string) (EntityExplain, error) {
	det, err := s.GetEntity(ctx, repo, id)
	if err != nil {
		return EntityExplain{}, err
	}
	outNeighbors, err := s.queryNeighborEntities(ctx, repo, id, true)
	if err != nil {
		return EntityExplain{}, err
	}
	inNeighbors, err := s.queryNeighborEntities(ctx, repo, id, false)
	if err != nil {
		return EntityExplain{}, err
	}
	if outNeighbors == nil {
		outNeighbors = []NeighborEntity{}
	}
	if inNeighbors == nil {
		inNeighbors = []NeighborEntity{}
	}
	return EntityExplain{EntityDetail: det, OutNeighbors: outNeighbors, InNeighbors: inNeighbors}, nil
}

func (s *PGStore) queryNeighborEntities(ctx context.Context, repo, id string, outbound bool) ([]NeighborEntity, error) {
	var q string
	if outbound {
		q = `SELECT en.id, en.name, en.type, en.file_path, en.line_start, en.line_end
			 FROM code_edges e
			 JOIN code_entities en ON en.repo=e.repo AND en.id=e.to_id
			 WHERE e.repo=$1 AND e.from_id=$2
			 ORDER BY en.name`
	} else {
		q = `SELECT en.id, en.name, en.type, en.file_path, en.line_start, en.line_end
			 FROM code_edges e
			 JOIN code_entities en ON en.repo=e.repo AND en.id=e.from_id
			 WHERE e.repo=$1 AND e.to_id=$2
			 ORDER BY en.name`
	}
	rows, err := s.db.QueryContext(ctx, q, repo, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NeighborEntity
	for rows.Next() {
		var ne NeighborEntity
		var ls, le sql.NullInt64
		if err := rows.Scan(&ne.ID, &ne.Name, &ne.Type, &ne.FilePath, &ls, &le); err != nil {
			return nil, err
		}
		if ls.Valid {
			ne.LineStart = int(ls.Int64)
		}
		if le.Valid {
			ne.LineEnd = int(le.Int64)
		}
		out = append(out, ne)
	}
	return out, rows.Err()
}

func scanEdgesWithConfidence(rows *sql.Rows) ([]Edge, error) {
	var out []Edge
	for rows.Next() {
		var e Edge
		var raw []byte
		if err := rows.Scan(&e.From, &e.To, &e.Relation, &e.SrcFile, &raw, &e.ConfidenceScore, &e.ConfidenceTier); err != nil {
			return nil, err
		}
		e.Properties = scanProps(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}

// FileImports returns the import edges that originate in the given file.
func (s *PGStore) FileImports(ctx context.Context, repo, path string) ([]Edge, error) {
	return s.queryEdges(ctx,
		`SELECT from_id, to_id, relation, src_file, properties FROM code_edges WHERE repo=$1 AND src_file=$2 AND relation='imports'`,
		repo, path)
}

// Consumers: others that REQUIRE a symbol this entity PROVIDES.
const crossConsumersQuery = `
SELECT r.repo, r.entity_id, r.symbol, r.lang
FROM cross_repo_symbols p
JOIN cross_repo_symbols r
  ON r.symbol = p.symbol AND r.lang = p.lang AND r.role = 'requires'
WHERE p.repo = $1 AND p.entity_id = $2 AND p.role = 'provides' AND r.repo <> $1
ORDER BY r.repo, r.entity_id`

// Providers: others that PROVIDE a symbol this entity REQUIRES.
const crossProvidersQuery = `
SELECT q.repo, q.entity_id, q.symbol, q.lang
FROM cross_repo_symbols rq
JOIN cross_repo_symbols q
  ON q.symbol = rq.symbol AND q.lang = rq.lang AND q.role = 'provides'
WHERE rq.repo = $1 AND rq.entity_id = $2 AND rq.role = 'requires' AND q.repo <> $1
ORDER BY q.repo, q.entity_id`

// CrossRepo returns the cross-repo consumers and providers for an entity.
func (s *PGStore) CrossRepo(ctx context.Context, repo, id string) (CrossRepoLinks, error) {
	consumers, err := s.queryCrossRefs(ctx, crossConsumersQuery, repo, id)
	if err != nil {
		return CrossRepoLinks{}, err
	}
	providers, err := s.queryCrossRefs(ctx, crossProvidersQuery, repo, id)
	if err != nil {
		return CrossRepoLinks{}, err
	}
	if consumers == nil {
		consumers = []CrossRef{}
	}
	if providers == nil {
		providers = []CrossRef{}
	}
	return CrossRepoLinks{Consumers: consumers, Providers: providers}, nil
}

func (s *PGStore) queryCrossRefs(ctx context.Context, query, repo, id string) ([]CrossRef, error) {
	rows, err := s.db.QueryContext(ctx, query, repo, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CrossRef
	for rows.Next() {
		var c CrossRef
		if err := rows.Scan(&c.Repo, &c.EntityID, &c.Symbol, &c.Lang); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PGStore) queryEdges(ctx context.Context, query string, args ...any) ([]Edge, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEdges(rows)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEntity(r rowScanner) (Entity, error) {
	var e Entity
	var raw []byte
	if err := r.Scan(&e.ID, &e.Name, &e.Type, &e.Description, &e.FilePath, &raw); err != nil {
		return Entity{}, err
	}
	e.Properties = scanProps(raw)
	return e, nil
}

func scanEdges(rows *sql.Rows) ([]Edge, error) {
	var out []Edge
	for rows.Next() {
		var e Edge
		var raw []byte
		if err := rows.Scan(&e.From, &e.To, &e.Relation, &e.SrcFile, &raw); err != nil {
			return nil, err
		}
		e.Properties = scanProps(raw)
		out = append(out, e)
	}
	return out, rows.Err()
}

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullTime(v string) any {
	if v == "" {
		return nil
	}
	return v
}
