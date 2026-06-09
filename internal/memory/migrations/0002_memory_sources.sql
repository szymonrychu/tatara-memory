CREATE TABLE IF NOT EXISTS memory_sources (
    repo      text NOT NULL,
    file_path text NOT NULL,
    track_id  text NOT NULL,
    PRIMARY KEY (repo, file_path, track_id)
);
CREATE INDEX IF NOT EXISTS memory_sources_repo_file
    ON memory_sources (repo, file_path);
