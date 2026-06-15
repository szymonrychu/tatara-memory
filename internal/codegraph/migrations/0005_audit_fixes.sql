-- Audit fixes 2026-06-15.

-- Finding 1: add extractor to code_edges primary key so each extractor owns
-- its own rows and the extractor-scoped per-file DELETE can reclaim them.
-- Without this a semantic push of the same (from,to,relation) tuple overwrites
-- the AST row's extractor tag and the AST reconcile's DELETE WHERE extractor='ast'
-- never matches it, leaking the edge forever.
ALTER TABLE code_edges DROP CONSTRAINT IF EXISTS code_edges_pkey;
ALTER TABLE code_edges ADD PRIMARY KEY (repo, from_id, to_id, relation, extractor);

-- Finding 12: drop the never-written, never-read cohesion column from
-- code_entities (cohesion belongs on code_communities, where it already lives).
ALTER TABLE code_entities DROP COLUMN IF EXISTS cohesion;
