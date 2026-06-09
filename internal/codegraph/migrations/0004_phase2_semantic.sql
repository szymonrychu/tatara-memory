-- Phase 2: origin-scoped reconcile (extractor), semantic extraction cache,
-- community/analytics persistence, and the debounced analytics trigger state.

ALTER TABLE code_edges
    ADD COLUMN IF NOT EXISTS extractor text NOT NULL DEFAULT 'ast';
ALTER TABLE code_entities
    ADD COLUMN IF NOT EXISTS extractor text NOT NULL DEFAULT 'ast';
ALTER TABLE code_hyperedges
    ADD COLUMN IF NOT EXISTS extractor text NOT NULL DEFAULT 'ast';

CREATE INDEX IF NOT EXISTS code_edges_repo_extractor ON code_edges (repo, extractor);
CREATE INDEX IF NOT EXISTS code_entities_repo_extractor ON code_entities (repo, extractor);
CREATE INDEX IF NOT EXISTS code_hyperedges_repo_extractor ON code_hyperedges (repo, extractor);

CREATE TABLE IF NOT EXISTS semantic_extractions (
    repo         text NOT NULL,
    file_path    text NOT NULL,
    content_sha  text NOT NULL,
    extracted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (repo, file_path)
);

CREATE TABLE IF NOT EXISTS code_communities (
    repo      text NOT NULL,
    community int  NOT NULL,
    label     text NOT NULL DEFAULT '',
    cohesion  real NOT NULL DEFAULT 0,
    size      int  NOT NULL DEFAULT 0,
    PRIMARY KEY (repo, community)
);

CREATE TABLE IF NOT EXISTS repo_analytics_state (
    repo          text PRIMARY KEY,
    dirty         boolean NOT NULL DEFAULT false,
    reconciled_at timestamptz,
    computed_at   timestamptz
);
