# tatara-memory phase 1 - implementation strategy

Date: 2026-05-24
Status: Approved for planning
Author: Szymon (with Claude)

## Relationship to parent spec

This sub-spec assumes the parent spec
`~/Documents/tatara/docs/superpowers/specs/2026-05-24-tatara-phases-0-1-design.md`
(sections "Phase 1 - tatara-memory" through "Testing strategy") as the
contract for what tatara-memory does. This document covers only the
"how to ship it":

- Wave-based workstream decomposition for parallel sonnet subagents.
- Resolved open questions (lightrag image pin, cnpg/neo4j chart pins).
- Dev tooling defaults (mise, pre-commit, golangci-lint, Makefile).
- Explicit v1.0 vs v1.1 cut.

Anything not listed here defers to the parent spec.

## v1.0 ship cut

**Full parent spec in one phase.** All endpoints (memories CRUD,
queries, queries:describe, entities, entities PATCH, edges, bulk
ingest, ingest jobs), full observability, helm chart with subcharts,
deploy to homelab.

**Deferred to v1.1 (explicit):**

- GitHub Actions CI. For v1.0, `pre-commit run --all-files` locally and
  `make build push` from laptop. CI lands in v1.1.
- docker-compose integration tests with real lightrag + postgres +
  neo4j. v1.0 ships with the in-memory `internal/lightrag/fake` and
  unit-test coverage only. Real-deps integration tests are v1.1.
- Helm chart push to harbor OCI registry via CI. Manual `helm push`
  from laptop for v1.0.

Everything else from the parent spec ships in v1.0.

## Pinned versions

| Component             | Pin                                                                          | Source                                   |
| --------------------- | ---------------------------------------------------------------------------- | ---------------------------------------- |
| Go toolchain          | newest stable 1.25.x (set exact minor in `go.mod`)                           | golang.org                               |
| LightRAG image        | `ghcr.io/hkuds/lightrag:v1.4.16@sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d` | `ghcr.io/v2/hkuds/lightrag/tags/list` (skipping v1.5.0 rc1/rc2) |
| cnpg/cluster chart    | `0.6.1`                                                                      | `cnpg` helm repo (latest, matches tatara-old) |
| neo4j/neo4j chart     | `2026.4.0`                                                                   | `neo4j` helm repo (CalVer line; old SemVer 5.x deprecated) |

Refresh policy: bump deliberately during follow-up work, never via
floating tags.

## Wave-based decomposition

Each wave runs in worktrees off `main`, dispatched to sonnet
implementation subagents in parallel where the wave allows. A single
opus merge subagent integrates each wave back to `main` before the next
wave starts. Build and deploy run only from `main` after wave 7.

### Wave 1 - Bootstrap (sequential, single sonnet subagent)

Foundational files that everything else depends on. Cannot be
parallelised.

- `go.mod` (Go 1.25.x, baseline deps: `github.com/go-chi/chi/v5`,
  `github.com/coreos/go-oidc/v3`, `github.com/golang-jwt/jwt/v5`,
  `github.com/prometheus/client_golang`, `github.com/stretchr/testify`,
  `go.opentelemetry.io/otel`).
- `.mise.toml` pinning go, helm, helmfile, helm-diff, helm-secrets,
  sops, golangci-lint, pre-commit, helm-unittest.
- `.pre-commit-config.yaml` with gofmt, golangci-lint, gitleaks,
  end-of-file-fixer, trailing-whitespace, yamllint, helm-lint hooks.
  Pinned revisions.
- `.golangci.yml` (default + `errcheck`, `gofmt`, `goimports`,
  `gosec`, `govet`, `ineffassign`, `revive`, `staticcheck`,
  `unconvert`, `unused`).
- `Makefile` targets: `lint`, `test`, `build`, `image`, `push`,
  `chart-lint`, `chart-test`, `chart-push`.
- `Dockerfile` multi-stage: `golang:1.25-alpine` builder, distroless
  static final, non-root.
- `CLAUDE.md` copied verbatim from `~/Documents/tatara/CLAUDE.md`.
- `MEMORY.md`, `ROADMAP.md`, `README.md` stubs.
- `.gitignore` (go binaries, coverage, .env, helm-secrets decrypted
  files, etc).
- `helm create charts/tatara-memory` then edit: delete `hpa.yaml`,
  `tests/`, `NOTES.txt`. Keep `_helpers.tpl`, `deployment.yaml`,
  `service.yaml`, `serviceaccount.yaml`, `ingress.yaml` as starting
  shells (real bodies in wave 4).
- `charts/tatara-memory/Chart.yaml` declares deps: cnpg/cluster 0.6.1,
  neo4j/neo4j 2026.4.0, local `lightrag` subchart (file://charts/lightrag).
- `charts/tatara-memory/values.yaml` stripped to the camelCase scalars
  the workload will read.
- `helmfile.yaml.gotmpl` at repo root, mirroring tatara-old structure,
  single `default` environment, releases: `tatara-memory` (which pulls
  in subcharts via dependencies, so only one release needed).
- `cmd/tatara-memory/main.go` minimal: prints version, exits 0. Proves
  the build wires together.
- `internal/version/version.go` with `Version`, `Commit`, `Date`
  populated by `-ldflags` at build time.

Verification gate: `make lint test build` passes; `helm dep update &&
helm lint charts/tatara-memory` passes; `helmfile lint` passes.

### Wave 2 - Foundation packages (3 parallel sonnet subagents, TDD)

All independent, no cross-deps. Each in its own worktree.

- **2A: `internal/obs`**
  - `Logger() *slog.Logger` returning JSON handler with stable
    field set (`request_id`, `user`, `route`, `method`, `status`,
    `duration_ms`).
  - `PromRegistry() *prometheus.Registry` with default go + process
    collectors.
  - `TracerProvider(ctx, endpoint)` returning OTLP exporter when
    endpoint is non-empty, no-op otherwise.
  - Tests: logger emits valid JSON with expected keys; registry
    exposes default collectors; tracer no-ops without endpoint.

- **2B: `internal/auth`**
  - `Verifier` wrapping `go-oidc` with issuer + audience config,
    JWKS caching delegated to go-oidc.
  - `Middleware(verifier, claimsKey)` for chi.
  - `internal/auth/testjwks` helper: gen RSA key, run httptest JWKS
    server, sign tokens with arbitrary claims.
  - Tests: valid; expired; wrong issuer; wrong audience; bad
    signature; missing required claim.

- **2C: `internal/lightrag` + `internal/lightrag/fake`**
  - `Client` interface covering every LightRAG endpoint we use
    (insert, query, query/describe, get/update/delete docs, entity
    get/update, edge list/create/delete).
  - HTTP implementation with typed request/response structs,
    `context.Context`-aware, `slog`-instrumented, prom metrics
    (`lightrag_calls_total`, `lightrag_call_duration_seconds`).
  - `fake.Client` implementing the interface against in-memory maps
    for tests.
  - Tests: HTTP client against `httptest.Server` returning canned
    responses for each op; fake client basic round-trip.

Verification gate per subagent: `go test ./internal/...` green in
its own worktree.

### Wave 3 - Domain and HTTP (2 parallel sonnet subagents, TDD)

After wave 2 merges. Both consume wave-2 outputs through interfaces.

- **3A: `internal/memory` + `internal/ingest`**
  - `memory.Service` orchestrates lightrag calls behind the domain
    API. Translates domain types to/from lightrag wire types.
  - `ingest.Pool` worker pool, configurable size, drains a postgres
    table. `Job` and `JobItem` structs; idempotency by client-key.
  - Crash-safe: on startup, requeue any `running` jobs.
  - Job status: `queued | running | succeeded | failed | partial`,
    counts, bounded per-item error list (last 50).
  - Tests: memory.Service against `lightrag/fake`; ingest.Pool against
    fake + an in-memory job store (separate interface from postgres
    impl); state-machine transitions; idempotency.
  - Postgres schema migration in `internal/ingest/migrations/` (raw
    SQL files, applied via stdlib `database/sql`; no migrate library
    for v1.0 - one file, one table).

- **3B: `internal/httpapi`**
  - `chi.Router` factory wiring middleware: request-id, slog access
    log, auth (from wave 2), metrics, panic recovery.
  - Handlers for every `/v1/*` endpoint in the parent spec.
  - Error envelope `{"error": "...", "request_id": "..."}`. 4xx for
    client errors, 502 for lightrag upstream, 503 with `Retry-After`
    for transient lightrag errors.
  - Tests: `httptest.Server` end-to-end with chi + fake memory
    service + auth in test mode. One test per status-code path.

Verification gate: `go test ./internal/...` green after merge.

### Wave 4 - Wiring and chart bodies (2 parallel sonnet subagents)

After wave 3 merges. Both build on previous waves.

- **4A: `cmd/tatara-memory/main.go`**
  - Flag/env parsing: HTTP addr, postgres DSN, lightrag base URL,
    OIDC issuer, OIDC audience, worker pool size, log level, OTLP
    endpoint.
  - Wire `obs`, postgres connection, `lightrag.Client`,
    `memory.Service`, `ingest.Pool`, `auth.Verifier`, `httpapi`
    router.
  - Graceful shutdown on SIGTERM/SIGINT: drain workers, close http
    server, close postgres.
  - Healthz: process alive. Readyz: postgres ping + lightrag /health
    OK.

- **4B: chart bodies**
  - `deployment.yaml` consumes config via `envFrom: [configMapRef,
    secretRef]`. No inline `env:`. Sidecar-free; single container.
  - `configmap.yaml` keys kebab-case, populated from camelCase
    `values.yaml` scalars via a single `_helpers.tpl` mapping.
  - `secret.yaml` stub for SOPS, referencing `existingSecret` style
    where the secret is provided externally (cnpg, neo4j passwords,
    OIDC client secret, lightrag llm/embedding keys).
  - `ingress.yaml` cert-manager + nginx, host from values.
  - `servicemonitor.yaml` prometheus-operator selector.
  - `networkpolicy.yaml` allow ingress from ingress-nginx + monitoring
    namespaces; egress to postgres + neo4j + lightrag + OIDC issuer.
  - `serviceaccount.yaml` plain, no IRSA equivalents (this is a
    homelab cluster, no cloud iam).
  - `charts/tatara-memory/tests/` helm-unittest specs covering: every
    template renders; envFrom present in deployment; configmap has
    expected keys; no plaintext secret leaked into rendered output.

Verification gate: `helm template ... | kubeconform` clean,
`helm-unittest charts/tatara-memory` green.

### Wave 5 - LightRAG subchart port (1 sonnet subagent, parallel with wave 4)

Independent of wave 4 (separate chart directory).

- `helm create charts/tatara-memory/charts/lightrag` then port
  templates from `~/Documents/tatara-old/charts/tatara-lightrag/`.
- Image pinned to
  `ghcr.io/hkuds/lightrag:v1.4.16@sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d`.
- Honor the no-lists-in-values rule. Where tatara-old's chart has
  list-shaped values, render those into a templated ConfigMap and
  consume at runtime.
- envFrom-only wiring; no inline `env:` arrays.
- helm-unittest specs.

### Wave 6 - Integration merge (1 opus subagent)

Single merge subagent reconciles all parallel worktrees back into
`main`. Expected conflict points:

- `go.mod` / `go.sum` (additive merges).
- `charts/tatara-memory/Chart.yaml` dependency list (additive).
- `cmd/tatara-memory/main.go` imports.
- `Makefile` if any subagent added targets.

Merge subagent runs `make lint test build chart-lint chart-test`
after each merge to verify integration.

### Wave 7 - Local deploy and smoke test (sequential, from `main`)

Runs only after wave 6 merges to `main`.

1. `helm dep update charts/tatara-memory`.
2. `helmfile diff` (with sops-decrypted env values for OIDC client
   secret, openai/anthropic key, neo4j password).
3. `helmfile apply`.
4. Smoke test via tatara-cli (phase 2 not built yet, so for v1.0:
   `curl` with a bearer token minted from `kcadm.sh` against the
   `tatara-cli` keycloak client's device flow, or via a service-account
   token from the `tatara-memory` confidential client).
   - POST /v1/memories with one chunk -> expect 201, id
   - GET /v1/memories/{id} -> expect 200, body matches
   - POST /v1/queries (mode=hybrid) -> expect 200, matches array
   - POST /v1/memories:bulk with 3 items -> expect 202, job_id
   - GET /v1/ingest-jobs/{job_id} until terminal -> expect succeeded
   - DELETE /v1/memories/{id} -> expect 204
5. Verify Prometheus scrape: target up, sample metrics present.
6. Verify log shape: one INFO per request, no panics, no ERROR.
7. Update parent `~/Documents/tatara/MEMORY.md` and `ROADMAP.md`:
   mark phase 1 shipped, note any dead-ends or non-obvious decisions
   that came out of implementation.

## Dev tooling defaults (v1.0)

- **Tool version manager:** `mise` (per global CLAUDE.md). `.mise.toml`
  pins every tool.
- **Formatter + linter:** `gofmt` + `golangci-lint` via pre-commit.
- **Tests:** `go test ./...` with `testify/require`. Table-driven.
- **Chart lint:** `helm lint` + `helm-unittest`.
- **Secrets:** `sops` with `.sops.yaml` at repo root; encrypted
  values files under `values/<release>/<env>.secrets.yaml`.
- **Pre-commit:** mandatory before every commit. Hooks pinned, no
  ad-hoc scripts.
- **No CI in v1.0.** All gates run locally. CI in v1.1.

## Open items resolved

| Parent-spec open question                  | Decision                                                                                       |
| ------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| lightrag image tag/digest                  | v1.4.16 @ sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d              |
| cnpg chart version                         | 0.6.1                                                                                          |
| neo4j chart version                        | 2026.4.0 (CalVer line)                                                                         |
| tatara-old archival                        | Stay separate, link from README. Out of scope for phase 1 work itself.                         |

## Parallelism summary

| Wave | Subagents | Type   | Sequential gate                     |
| ---- | --------- | ------ | ----------------------------------- |
| 1    | 1         | sonnet | bootstrap lands on `main`           |
| 2    | 3         | sonnet | per-subagent verification + merge   |
| 3    | 2         | sonnet | per-subagent verification + merge   |
| 4    | 2         | sonnet | helm template + unittest gates      |
| 5    | 1         | sonnet | helm template + unittest gates      |
| 6    | 1         | opus   | full `make` gates after each merge  |
| 7    | 0 (human) | -      | smoke test pass, MEMORY/ROADMAP updated |

Waves 4 and 5 run concurrently. Estimated wall-clock saving vs
sequential: ~40 percent.

## Things explicitly NOT in this sub-spec

- Per-endpoint detailed handler logic (parent spec is the contract).
- LightRAG wire-format mapping per endpoint (lives in
  `internal/lightrag` and is owned by wave 2C).
- Keycloak terraform (ships separately in the infra repo, parent
  spec covers it).
- tatara-cli, ingester, wrapper, workflows, tasks, bridge (later
  phases).
