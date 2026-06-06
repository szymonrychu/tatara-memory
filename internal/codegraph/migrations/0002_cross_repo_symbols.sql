CREATE TABLE IF NOT EXISTS cross_repo_symbols (
    repo      text NOT NULL,
    symbol    text NOT NULL,
    lang      text NOT NULL,
    kind      text NOT NULL DEFAULT '',
    role      text NOT NULL,
    entity_id text NOT NULL,
    src_file  text NOT NULL DEFAULT '',
    PRIMARY KEY (repo, symbol, role, entity_id)
);
CREATE INDEX IF NOT EXISTS idx_crs_join ON cross_repo_symbols(symbol, lang, role);
CREATE INDEX IF NOT EXISTS idx_crs_src  ON cross_repo_symbols(repo, src_file);
