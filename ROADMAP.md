# ROADMAP.md

Component-local roadmap for tatara-memory. Phase-level platform roadmap
lives in `~/Documents/tatara/ROADMAP.md`.

Statuses: `planned`, `in progress`, `shipped`.

---

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
