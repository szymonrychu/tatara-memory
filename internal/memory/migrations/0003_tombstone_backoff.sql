ALTER TABLE deleted_memories
    ADD COLUMN IF NOT EXISTS force_reap_attempts INT NOT NULL DEFAULT 0;

ALTER TABLE deleted_memories
    ADD COLUMN IF NOT EXISTS next_force_check_at TIMESTAMPTZ;
