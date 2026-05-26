# tatara-memory v0.1.1 - LightRAG wire-format fix

Date: 2026-05-25
Status: Ready for plan
Author: Szymon (with Claude)

## Background

v0.1.0 deployed to the homelab in the `tatara` namespace and verified:
- OIDC end-to-end (token mint from `tatara-memory` Keycloak client, JWT verification in service)
- All 6 pods 1/1 Running (app, lightrag, neo4j, postgres x3)
- `/healthz`, `/readyz`, `/metrics` all 200
- cnpg cluster with pgvector extension auto-installed
- Neo4j Bolt over ClusterIP service `tatara-neo4j-lb-neo4j:7687`
- LightRAG application bootstrapped its `tatara_memory` database with HNSW indexes

What does NOT work end-to-end: the LightRAG HTTP client (`internal/lightrag/`) sends paths and bodies that do not match the LightRAG v1.4.16 API. The Wave 2C subagent invented a domain-shaped wire format instead of reading LightRAG's actual OpenAPI spec. Unit tests pass because the canned `httptest.Server` responses use the same wrong shapes.

## Observed gaps (from real cluster traffic)

1. **Path: `POST /documents` (405 Method Not Allowed)**
   Fixed in v0.1.0 patch: now `POST /documents/text`.
2. **Body: `{"documents":[{"id":"...","content":"..."}]}` (422 missing `text`)**
   LightRAG v1.4.16 `POST /documents/text` expects `InsertTextRequest`:
   ```json
   { "text": "string" }
   ```
3. **Likely additional mismatches** on: `Query`, `QueryDescribe`, entity get/list/update, edge list/create/delete, response decoding for all of the above.

## Real LightRAG v1.4.16 API surface

Canonical reference saved at `docs/lightrag-openapi-v1.4.16.json` (live spec captured from the running pod via `curl /openapi.json`).

Inventory of methods our service uses, by old name -> real LightRAG endpoint:

| Our op             | LightRAG path                           | Real method | Real request schema      | Real response schema       |
| ------------------ | --------------------------------------- | ----------- | ------------------------ | -------------------------- |
| InsertDocument     | `/documents/text`                       | POST        | `InsertTextRequest{text}` | `InsertResponse`          |
| (bulk insert)      | `/documents/texts`                      | POST        | `InsertTextsRequest{texts:[]}` | `InsertResponse`     |
| GetDocument        | `/documents/track_status/{track_id}` OR `/documents/paginated` | GET/POST | track_id path / paginated body | per-doc status |
| DeleteDocument     | `/documents/delete_document`            | POST        | `DeleteDocByIdRequest`   | `DeleteDocByIdResponse`    |
| Query              | `/query`                                | POST        | `QueryRequest`           | `QueryResponse`            |
| QueryDescribe      | `/query/data` (or `/query` with flag)   | POST        | `QueryRequest`           | structured response        |
| ListEntities       | `/graph/label/list` or `/graph/label/search` | GET/POST | label name + filters | label list                 |
| GetEntity          | `/graph/entity/exists`                  | POST        | `EntityExistsRequest`    | bool + entity              |
| UpdateEntity       | `/graph/entity/edit`                    | POST        | `EntityUpdateRequest`    | updated entity             |
| (CreateEntity)     | `/graph/entity/create`                  | POST        | `EntityCreateRequest`    | created entity             |
| ListEdges          | `/graphs` (returns whole graph) or label-driven | GET | n/a       | graph object               |
| CreateEdge         | `/graph/relation/create`                | POST        | `RelationCreateRequest`  | created relation           |
| (UpdateEdge)       | `/graph/relation/edit`                  | POST        | `RelationEditRequest`    | updated relation           |
| DeleteEdge         | `/documents/delete_relation`            | POST        | `DeleteRelationRequest`  | response                   |
| Health             | `/health`                               | GET         | n/a                      | `HealthResponse`           |

Confirm each schema's exact fields from `docs/lightrag-openapi-v1.4.16.json` -> `components.schemas`.

## Scope of this spec

- Rewrite `internal/lightrag/types.go` so structs match the real LightRAG schemas exactly (field names, json tags, required vs optional).
- Rewrite `internal/lightrag/http.go` paths + methods + body construction to match the OpenAPI.
- Update `internal/lightrag/fake/fake.go` to return canned responses in the real wire shape.
- Update `internal/lightrag/http_test.go` so the `httptest.Server` returns real-shape canned responses; tests must assert the request body Claude code is sending matches the real schema's `required` fields.
- Update `internal/memory/translate.go` adapters so the domain types still round-trip cleanly (domain side does not change).
- Keep `Client` interface stable where possible; only break signatures where the real API has fundamentally different shape (e.g. ingest is async via background_tasks, so InsertDocument may need to return a `track_id` instead of synchronous `IDs`).
- Add one integration test that hits a real LightRAG instance (build-tagged `integration`, `LIGHTRAG_BASE_URL` env-driven); use the same approach as the postgres integration test from Wave 3A.

## Out of scope

- Domain API (`/v1/*` HTTP surface) shape changes - those endpoints stay as-spec.
- Helm chart / cnpg / neo4j changes - v0.1.0 already correct.
- Auth / observability / job pool internals - all verified working in v0.1.0.

## Constraints

- LightRAG v1.4.16 is the pinned version. Do not switch to v1.5.0rc.
- The `tatara_memory` database already has lightrag's schema; no migration concerns.
- Keep prom metrics (`lightrag_calls_total`, `lightrag_call_duration_seconds`) wired across every method.
- Continue using inline `env:` for the lightrag-specific secret key remap (deployment.yaml).
- TDD per task. golangci-lint clean. JSON logs via slog only.

## Deliverables for v0.1.1

1. Updated client passes a new `httptest`-backed unit suite that uses **real** LightRAG schemas (canned bodies must match what we'd see in production).
2. New optional integration test (`-tags=integration`) that points at a live LightRAG and exercises the smoke-test path.
3. Rebuild + repush image as `harbor.szymonrichert.pl/containers/tatara-memory:0.1.1`.
4. `helm -n tatara upgrade` redeploys the new image.
5. Smoke test from `Documents/tatara/docs/superpowers/specs/2026-05-24-tatara-phases-0-1-design.md` Wave 7 succeeds end-to-end (POST/GET/Query/Bulk/DELETE all green).
6. Tag `v0.1.1` on the tatara-memory repo, push origin.
7. Update `~/Documents/tatara/ROADMAP.md` to mark Phase 1 shipped.

## Open questions

- The `EntityUpdate` shape in our code uses pointer fields per Wave 3A's adaptation. Need to check whether LightRAG's `EntityUpdateRequest` actually supports partial updates or expects a full replacement.
- `QueryDescribe` may not be a separate LightRAG endpoint at all; might just be a `mode=describe` flag on `/query`. Confirm before keeping two distinct methods.
- Async ingest: LightRAG appears to enqueue documents via `BackgroundTasks` and exposes a `track_status` endpoint. This aligns naturally with our `ingest.Pool` if we change `InsertDocument` to return a track_id and have the Pool poll status. Worth evaluating - or keep synchronous and live with the latency.
