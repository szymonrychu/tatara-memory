package codegraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

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

// Reconcile deletes the prior graph owned by p.Files then inserts p.Entities and
// p.Edges, all in a single transaction.
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
	}

	for _, e := range p.Entities {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_entities(repo, id, name, type, description, file_path, properties)
			VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
			ON CONFLICT (repo, id) DO UPDATE SET
				name=EXCLUDED.name, type=EXCLUDED.type, description=EXCLUDED.description,
				file_path=EXCLUDED.file_path, properties=EXCLUDED.properties`,
			p.Repo, e.ID, e.Name, e.Type, e.Description, e.FilePath, marshalProps(e.Properties)); err != nil {
			return PushResult{}, err
		}
	}

	for _, e := range p.Edges {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO code_edges(repo, from_id, to_id, relation, src_file, properties)
			VALUES ($1,$2,$3,$4,$5,$6::jsonb)
			ON CONFLICT (repo, from_id, to_id, relation) DO UPDATE SET
				src_file=EXCLUDED.src_file, properties=EXCLUDED.properties`,
			p.Repo, e.From, e.To, e.Relation, e.SrcFile, marshalProps(e.Properties)); err != nil {
			return PushResult{}, err
		}
	}

	for _, s := range p.Symbols {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cross_repo_symbols(repo, symbol, lang, kind, role, entity_id, src_file)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (repo, symbol, role, entity_id) DO UPDATE SET
			    lang=EXCLUDED.lang, kind=EXCLUDED.kind, src_file=EXCLUDED.src_file`,
			p.Repo, s.Symbol, s.Lang, s.Kind, s.Role, s.EntityID, s.SrcFile); err != nil {
			return PushResult{}, err
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
// fragment and optional exact type, ordered by name.
func (s *PGStore) SearchEntities(ctx context.Context, repo, q, typ string, limit int) ([]Entity, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, type, description, file_path, properties
		FROM code_entities
		WHERE repo=$1
		  AND ($2='' OR name ILIKE '%'||$2||'%' OR description ILIKE '%'||$2||'%')
		  AND ($3='' OR type=$3)
		ORDER BY name
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

// Neighbors walks edges of the given relations from id, in the given direction
// ("out" follows from->to, "in" follows to->from), up to depth hops.
func (s *PGStore) Neighbors(ctx context.Context, repo, id string, relations []string, dir string, depth int) ([]PathNode, error) {
	query := neighborsOutQuery
	if dir == "in" {
		query = neighborsInQuery
	}
	rows, err := s.db.QueryContext(ctx, query, repo, id, strings.Join(relations, ","), depth)
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
