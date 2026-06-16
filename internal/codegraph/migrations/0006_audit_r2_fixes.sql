-- Audit round-2 fixes 2026-06-16.

-- Finding 4: drop the write-only code_entities.degree column. Degree is cheap
-- to compute live from code_edges (COUNT join) and is already computed live in
-- ImportantEntities and ImportantEntitiesBy; the persisted value went stale
-- between recomputes and was never read by any query.
ALTER TABLE code_entities DROP COLUMN IF EXISTS degree;

-- Finding 11: widen betweenness from real (float32) to double precision (float64)
-- so the stored type matches the Go float64 produced by Brandes and bound via
-- unnest($4::float8[]). The previous real column silently downcast on every
-- write; comparison with a freshly computed float64 would never match exactly.
ALTER TABLE code_entities ALTER COLUMN betweenness TYPE double precision;
