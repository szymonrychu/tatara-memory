-- The async ingest worker needs each item's payload (the chunk text it sends to
-- LightRAG and the metadata it stamps on the Memory). 0001 stored only the
-- idempotency key, so the worker sent empty text. Add the payload columns.
ALTER TABLE ingest_job_items ADD COLUMN IF NOT EXISTS text     text  NOT NULL DEFAULT '';
ALTER TABLE ingest_job_items ADD COLUMN IF NOT EXISTS metadata jsonb NOT NULL DEFAULT '{}';
