-- track_id records the LightRAG track_id assigned on a successful CreateMemory.
-- A non-empty value lets processItem short-circuit on retry, preventing
-- duplicate LightRAG insertions when an item is reprocessed after a crash.
ALTER TABLE ingest_job_items ADD COLUMN IF NOT EXISTS track_id text NOT NULL DEFAULT '';
