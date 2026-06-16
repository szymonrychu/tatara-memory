# ROADMAP.md

Component-local roadmap for tatara-memory. Phase-level platform roadmap
lives in `~/Documents/tatara/ROADMAP.md`.

Statuses: `planned`, `in progress`, `shipped`.

---

## v0.2 - Code graph (phase 3 sub-project A)

**Status:** in progress 2026-06-06.

code_entities/code_edges schema, POST /code-graph:bulk (synchronous,
file-granular replace), GET /code/* traversal (entities search, entity,
neighbors, callers, callees, dependents, dependencies, resource-graph,
file-imports) via recursive CTEs. Migrations wired at startup in run()
(also fixes the latent ingest_jobs migration gap). Consumed by
tatara-memory-repo-ingester (B) and tatara-cli code-graph MCP tools (C).
Spec: parent-repo `docs/superpowers/specs/2026-06-05-tatara-code-ingestion-design.md`.

## Retrieval-quality eval harness (issue #41)

**Status:** shipped 2026-06-16.

The measurement layer for memory recall: a versioned golden set
(`eval/golden/*.json`) + seed corpus (`eval/seed/*.json`), a `cmd/eval`
runner that seeds a live deployment, queries each case, and computes
recall@k + MRR, and a `make eval` target (opt-in, needs
`MEMORY_BASE_URL`/`MEMORY_TOKEN`, not part of `make test`). Since
`QueryMatch.Score` is unavailable, a hit is a substring/ID match over the
returned Matches and ranking is match-order only; the runner exits
non-zero below a configurable recall floor so CI can gate. Delivers only
measurement: reranker / ranking / hybrid-weighting changes, any
`Score`/LightRAG change, Grafana dashboards, and in-cluster cron eval stay
out of scope (each a later issue, now unblocked by this gate).

## Ingest worker hardening (post-0.2.2)

**Status:** planned. Found during the 0.2.2 pool-wiring fix (see MEMORY).
Pre-existing, not triggered by the single-notify-per-job invariant today:

- [x] `runJob` Done/Failed counter is a non-atomic read-modify-write
      (`GetJob` -> `cur.Done++` -> `UpdateJob`). Done: replaced with
      `JobStore.IncrementJobProgress`, an atomic `UPDATE ... SET done = done + 1`
      (failure path locks the row to bump `failed` and append the capped error
      in one critical section). Closes szymonrychu/tatara-memory#2.
- [x] Crash mid-item leaves the item `running`; `ClaimNextItem` claims only
      `pending`, so `Resume` re-runs the job but skips the orphan and drains to
      a wrong count. Done: `JobStore.ResetRunningItems` resets `running` items
      of unfinished jobs back to `pending`, called at the top of `Resume` before
      workers claim. Closes szymonrychu/tatara-memory#4.
- [x] No per-item timeout: `processItem` -> `CreateMemory` blocks a worker
      indefinitely on a hung LightRAG call. Done: `Pool.WithItemTimeout` option
      wraps each item in a `context.WithTimeout` (0 disables); config
      `IngestItemTimeout` (env `INGEST_ITEM_TIMEOUT` / `--ingest-item-timeout`,
      default 60s) wires it in `app.go`. A fired deadline fails the item with the
      context error and the worker moves on. Closes szymonrychu/tatara-memory#25
      and the duplicate szymonrychu/tatara-memory#27.
      (Pairs with the ingester-side chunk-poll timeout tracked in the parent ROADMAP.)

## v1.0 - Phase 1 ship

**Status:** v0.1.2 deployed 2026-05-27, end-to-end smoke green
(POST/GET/DELETE/Query against real lightrag with OIDC token).

See `docs/superpowers/specs/2026-05-24-tatara-memory-phase1-impl.md`
and `docs/superpowers/plans/2026-05-24-tatara-memory-phase1.md`.

v0.1.1 followups landed:
- LightRAG wire format rewritten to match real v1.4.16 OpenAPI.
- Integration test against real lightrag pod (`-tags integration`).
- NetworkPolicy re-enabled with correct cnpg/neo4j selectors.

v0.1.2: fix DocStatusResponse.Metadata to map[string]any (LightRAG
returns mixed-type values).

v0.1.3 (2026-05-27): drop `/v1` route prefix; enable ingress at
`tatara.szymonrichert.pl/api/v1/memory` with nginx rewrite. Smoke
green end-to-end via the new external URL.

## v1.1 - Follow-ups

**Status:** planned

- ~~GitHub Actions CI (lint, test, build, push image + chart).~~ (replaced by argo tatara-memory-tag CWT)
- docker-compose integration tests with real lightrag + postgres + neo4j.
- ~~Helm chart push to harbor OCI via CI.~~ (replaced by argo tatara-memory-tag CWT)
- Fix stale helm-unittest tests for UPPER_SNAKE configmap keys (boy-scout, pre-existing).
- Verify DeleteMemory is eventually consistent: GET-after-DELETE
  currently returns 200 until lightrag's async deletion reindex catches up.
