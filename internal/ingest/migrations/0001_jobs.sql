CREATE TABLE IF NOT EXISTS ingest_jobs (
    id          text PRIMARY KEY,
    status      text NOT NULL,
    total       int  NOT NULL DEFAULT 0,
    done        int  NOT NULL DEFAULT 0,
    failed      int  NOT NULL DEFAULT 0,
    errors_json text NOT NULL DEFAULT '[]',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ingest_job_items (
    id              text PRIMARY KEY,
    job_id          text NOT NULL REFERENCES ingest_jobs(id) ON DELETE CASCADE,
    idempotency_key text NOT NULL,
    status          text NOT NULL DEFAULT 'pending',
    error           text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_job_items_unique_key
    ON ingest_job_items(job_id, idempotency_key);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON ingest_jobs(status);
CREATE INDEX IF NOT EXISTS idx_job_items_pending ON ingest_job_items(job_id, status);
