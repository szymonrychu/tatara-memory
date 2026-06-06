CREATE TABLE IF NOT EXISTS code_entities (
    repo        text NOT NULL,
    id          text NOT NULL,
    name        text NOT NULL,
    type        text NOT NULL,
    description text  NOT NULL DEFAULT '',
    file_path   text  NOT NULL DEFAULT '',
    properties  jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (repo, id)
);

CREATE INDEX IF NOT EXISTS idx_code_entities_repo_file ON code_entities(repo, file_path);
CREATE INDEX IF NOT EXISTS idx_code_entities_type ON code_entities(repo, type);
CREATE INDEX IF NOT EXISTS idx_code_entities_name ON code_entities(repo, name);

CREATE TABLE IF NOT EXISTS code_edges (
    repo       text NOT NULL,
    from_id    text NOT NULL,
    to_id      text NOT NULL,
    relation   text NOT NULL,
    src_file   text  NOT NULL DEFAULT '',
    properties jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (repo, from_id, to_id, relation)
);

CREATE INDEX IF NOT EXISTS idx_code_edges_to ON code_edges(repo, to_id, relation);
CREATE INDEX IF NOT EXISTS idx_code_edges_from ON code_edges(repo, from_id, relation);
CREATE INDEX IF NOT EXISTS idx_code_edges_src_file ON code_edges(repo, src_file);
