-- Phase 0: graphify-forward-compatible capture. Confidence is promoted to typed
-- columns on code_edges; analytics + provenance columns are reserved on
-- code_entities (compute later); empty hyperedge tables reserve the n-ary shape.
ALTER TABLE code_edges
    ADD COLUMN IF NOT EXISTS confidence_score real NOT NULL DEFAULT 1.0,
    ADD COLUMN IF NOT EXISTS confidence_tier  text NOT NULL DEFAULT 'EXTRACTED';
CREATE INDEX IF NOT EXISTS code_edges_repo_tier ON code_edges (repo, confidence_tier);

ALTER TABLE code_entities
    ADD COLUMN IF NOT EXISTS community    int,
    ADD COLUMN IF NOT EXISTS cohesion     real,
    ADD COLUMN IF NOT EXISTS degree       int,
    ADD COLUMN IF NOT EXISTS betweenness  real,
    ADD COLUMN IF NOT EXISTS source_url   text,
    ADD COLUMN IF NOT EXISTS author       text,
    ADD COLUMN IF NOT EXISTS captured_at  timestamptz,
    ADD COLUMN IF NOT EXISTS line_start   int,
    ADD COLUMN IF NOT EXISTS line_end     int;

CREATE TABLE IF NOT EXISTS code_hyperedges (
    repo             text NOT NULL,
    id               text NOT NULL,
    label            text NOT NULL,
    relation         text NOT NULL,
    confidence_score real NOT NULL DEFAULT 1.0,
    src_file         text NOT NULL,
    properties       jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (repo, id)
);
CREATE TABLE IF NOT EXISTS code_hyperedge_members (
    repo         text NOT NULL,
    hyperedge_id text NOT NULL,
    entity_id    text NOT NULL,
    PRIMARY KEY (repo, hyperedge_id, entity_id)
);
CREATE INDEX IF NOT EXISTS code_hyperedges_src ON code_hyperedges (repo, src_file);
