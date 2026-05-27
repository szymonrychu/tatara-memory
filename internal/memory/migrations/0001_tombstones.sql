CREATE TABLE IF NOT EXISTS deleted_memories (
    track_id   TEXT PRIMARY KEY,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS deleted_memories_deleted_at_idx
    ON deleted_memories (deleted_at);
