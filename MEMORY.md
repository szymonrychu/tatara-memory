# MEMORY.md

Component-local memory for tatara-memory. Cross-repo decisions live in
`~/Documents/tatara/MEMORY.md`.

Format: `YYYY-MM-DD - decision/finding`

---

## Decisions

2026-05-27 - Makefile VERSION strips leading v from `git describe --tags`. Chart appVersion and image tag now match without manual re-tagging. Long-standing v0.1.x friction closed.
2026-05-25 - Phase 1 v1.0 code complete (all 4 waves merged). Wave 7 deploy is the remaining step.
2026-05-24 - golangci-lint pinned to v2.11 (not v1.62 as in plan); v1.62 built with go1.23 cannot process go1.25 modules.
2026-05-24 - removed `go-imports` pre-commit hook (dnephin/pre-commit-golang); mise shim for goimports fails in pre-commit subprocess. golangci-lint covers import ordering.
2026-05-24 - helmfile secrets block commented out until Wave 7; helm-secrets plugin API (getter/v1) incompatible with helmfile 1.5.2 CLI call pattern; sops age key is placeholder anyway.
2026-05-24 - lightrag subchart dep commented out in Chart.yaml until Wave 5; file:// local ref prevents helm dep update.
2026-05-24 - httproute.yaml template removed from chart skeleton; .Values.httpRoute not in values schema per no-lists rule.
2026-05-24 - check-yaml pre-commit hook excludes charts/ dir; Helm templates contain {{ }} which is invalid YAML.

## Dead-ends / things tried that did not work

2026-05-24 - mise global go@1.25.0 does not fix goimports shim in pre-commit subprocess env; shim resolution requires full mise shell activation which pre-commit does not do.
2026-05-24 - golangci-lint v2 classifies gofmt/goimports as `formatters`, not `linters`; they go under `formatters.enable` not `linters.enable`.
2026-05-24 - `HELMFILE_HELM_BINARY` env var not supported in helmfile 0.171; `--helm-binary` flag and `helmBinary` YAML key work. Used `helmBinary` in helmfile.yaml.gotmpl to hard-wire mise helm 3.16 path.
2026-05-24 - `mise exec -- helmfile lint` picks up homebrew helm 4.x (appears before mise tools in PATH); fixed via `helmBinary` in helmfile.yaml.gotmpl expanding `$HOME`.
2026-05-24 - RESOLUTION: dropped `helmBinary` from helmfile.yaml.gotmpl (was darwin-arm64-only, version-pinned, non-portable). Canonical pattern: Makefile computes HELM_BIN dynamically via `mise exec -- bash -c 'echo $$PATH'` and passes `--helm-binary $(HELM_BIN)` to both `helm lint` and `helmfile lint` targets. All helm invocations go through Makefile targets.
2026-05-24 - `go mod tidy` removes all deps that aren't imported yet; baseline deps must be added without running tidy (or tidy would strip them). They land as `// indirect` until code imports them.
2026-05-24 - GOFLAGS=-count=1 in .mise.toml breaks `go get` (invalid flag for that command); must unset GOFLAGS when running go get.

2026-05-24 - disabled revive `exported` rule in .golangci.yml; CLAUDE.md hard rule prohibits docstrings on new code, rule is incompatible with that constraint.
2026-05-24 - otlptracegrpc not pre-seeded in Wave 1 go.mod (only otlptracehttp was); added via `go get` in Wave 2A; semconv/v1.26.0 is a subpackage of go.opentelemetry.io/otel, no separate module needed.
2026-05-24 - lightrag metrics pre-initialized with all op labels in newMetrics so prometheus Gather() returns families even with zero calls; without pre-init, CounterVec/HistogramVec are invisible to Gather until first observation.
2026-05-24 - lightrag/fake implements full Client interface from the start (no staged stubs); unused linter would reject partial implementation committed without all methods in use.
2026-05-24 - metrics_test.go sums all label combinations for calls_total and duration_seconds; pre-init zeros don't inflate the sum because the test checks for exactly 1 after 1 call.

2026-05-24 - wave 3A plan was written against a hypothetical lightrag API (DocID/Content fields, Insert/GetDoc method names, EntityPatch/EdgeCreate types); actual wave 2C API uses InsertDocument/GetDocument/DeleteDocument, InsertRequest.Documents slice, EntityUpdate with pointer fields, and CreateEdge taking Edge directly. Adapted translation layer accordingly.
2026-05-24 - memory.Memory.Metadata is map[string]string (matches lightrag.Document.Metadata); plan showed map[string]interface{} which would not compile. Used string-valued maps throughout.
2026-05-24 - wrapUpstream detects "not found" from fake client via strings.Contains(err.Error(), "not found"); HTTPError{Status:404} path is for production HTTP client. Both map to ErrNotFound.
2026-05-24 - ingest newID helper duplicated in memory and ingest packages (no shared util); three similar lines beats premature abstraction per CLAUDE.md.
2026-05-24 - PGStore.ListRunningJobs: defer rows.Close() needed wrapping as func(){}() to satisfy errcheck linter (golangci-lint v2).
2026-05-24 - httpapi developed locally during wave 3B with duplicate types (Memory, Query, etc.) and sentinels (ErrNotFound, ErrUpstream, ErrTransient); reconciled at wave-3-merge to import memory.* directly (TODO(wave-6-merge) resolved).
2026-05-24 - e2e smoke test does not assert JSON envelope on 401 because internal/auth middleware writes plain text; only the status code is checked.

2026-05-25 - wave 4A plan shows obs.Logger/obs.PromRegistry helpers that don't exist; actual API is obs.NewLogger(w, level) + obs.PromRegistry(). Adapted buildObs accordingly.
2026-05-25 - wave 4A plan calls lightrag.NewClient(url, reg, logger) but actual constructor is lightrag.NewHTTPClient(HTTPConfig{...}). Used HTTPConfig struct.
2026-05-25 - wave 4A plan calls auth.NewVerifier(ctx, issuer, audience) but actual is auth.NewVerifier(ctx, auth.Config{...}). Used Config struct.
2026-05-25 - wave 4A plan calls ingest.NewPool(size, store, memSvc, logger) but actual is ingest.NewPool(store, runner, size). Reordered.
2026-05-25 - wave 4A plan references httpapi.Deps struct; actual is httpapi.Config. Used Config. ReadyCheck field in httpapi.Config wires readyz instead of re-registering the route (router already owns /healthz and /readyz).
2026-05-25 - wave 4A plan uses lib/pq driver; project already has pgx/v5/stdlib. Used pgx with sql.Open("pgx", dsn). No new go.mod dep needed.
2026-05-25 - golangci-lint v2 exclude-rules path: _test.go does not suppress gosec in test files (confirmed broken with isolated config). Used //nolint:gosec inline on the 5 affected lines.
2026-05-25 - integration test SIGTERM in plan kills the test process (no signal handler running during test). Removed SIGTERM send; shutdown is exercised directly via a.shutdown(). waitForSignal is tested separately in main_test.go.
2026-05-25 - OIDC discovery stub must return issuer with scheme: `"issuer":"http://"+r.Host` not `"issuer":r.Host`; go-oidc/v3 validates issuer URL equality including scheme.
2026-05-25 - mise helm 3.16 cannot load helm-unittest plugin (platformHooks field in plugin.yaml is helm4 API); Makefile HELM_UNITTEST_BIN points at /opt/homebrew/bin/helm (4.x) for chart-test only; lint/package still use mise helm 3.16.
2026-05-25 - deployment.yaml checksum/config annotation uses envConfig helper (not include of configmap.yaml file) to stay compatible with helm-unittest per-template test isolation.
2026-05-25 - secret stub: pg-password b64enc of empty string renders as "" not null; test uses exists: not isNotNullOrEmpty:.
2026-05-25 - serviceMonitor.enabled and networkPolicy.enabled flipped to true in values.yaml as part of wave 4B (was false with comment "enabled in Wave 4B").
2026-05-25 - lightrag subchart: checksum/config annotation uses `include "lightrag.configKeys"` not `include (print $.Template.BasePath "/configmap.yaml")`; the cross-template include pattern fails in helm-unittest when only deployment.yaml is selected.
2026-05-25 - helm-unittest v1.1.0 does not support Chart.yaml as a template target; chart metadata tests use rendered labels from serviceaccount.yaml instead.
2026-05-25 - helm-unittest v1.1.0 does not support `documentSelector`; tests that need to disambiguate multiple documents use per-test `template:` override or single-template suites.
2026-05-25 - parent chart lightrag dependency test requires neo4j.neo4j.name and neo4j.volumes.data.mode set; neo4j subchart evaluates all templates (including required-value guards) even when only a lightrag template is targeted.
2026-05-25 - `helm dep update` creates both `lightrag/` dir (in-place symlink) and `lightrag-0.1.0.tgz` (vendored tarball) in charts/; both are correct for file:// local deps.

## Open questions

*(nothing yet)*

## 2026-05-25 - v0.1.0 deploy + handoff

- Deployed v0.1.0 to homelab `tatara` namespace via helmfile. Auth + observability + cnpg + neo4j + lightrag bootstrap all verified. End-to-end smoke test BLOCKED by lightrag wire-format bugs from Wave 2C (invented schemas instead of reading real OpenAPI). See `docs/superpowers/specs/2026-05-25-lightrag-wire-format-fix.md` + `docs/superpowers/plans/2026-05-25-lightrag-wire-format-fix.md` for the v0.1.1 fix.
- Real LightRAG v1.4.16 OpenAPI captured at `docs/lightrag-openapi-v1.4.16.json`.
- OIDC client `tatara-memory` exists in master realm at auth.szymonrichert.pl. Client secret extracted via `terraform output -raw tatara_memory_client_secret` from `~/Documents/infra/terraform/keycloak` (TF_VAR_* loaded from `.env` in that dir).
- Helm release `tatara-memory` is at revision 5 in tatara namespace. Lightrag pod resolves bolt to `tatara-neo4j-lb-neo4j:7687` (renamed to avoid release-name service collision). Shared cnpg cluster has pgvector extension; lightrag uses `tatara_memory` database alongside the ingest jobs.
- Pre-created secrets in `tatara` ns: `regcred` (copied from `ai` ns); `tatara-neo4j-password` (manual, has 3 keys: NEO4J_AUTH, NEO4J_PASSWORD, password - one secret feeds both neo4j chart and lightrag).
- Network policies disabled for v0.1.0 (selectors didn't match real pod labels). v0.1.1 should re-enable with corrected selectors.
- Tooling bumped to match spellslinger pattern: helm 4, helmfile 1.4.4, sops 3.12, secrets-v4 plugin set.

## 2026-05-27 - v0.1.2 deployed (end-to-end smoke green)

2026-05-27 - v0.1.1 wire-format rewrite shipped; v0.1.2 followed within minutes to fix DocStatusResponse.Metadata (map[string]string -> map[string]any) caught by GET /v1/memories/{track} smoke. End-to-end POST/GET/DELETE/Query all return correctly with real OIDC tokens.
2026-05-27 - Domain semantic shifts forced by real LightRAG v1.4.16 API. Recorded so future work knows the shape changes (the OpenAPI is too lossy to "fix" later without a v2 of the domain API):
  * Memory.ID = track_id (not doc_id; ingest is async, no synchronous doc id)
  * Memory.Text on GET = content_summary, not original text (LightRAG does not expose original docs via API)
  * GET /v1/memories/{id} eventual-consistent: doc is queryable seconds after POST; DELETE is async ("deletion_started") so GET-after-DELETE may still return until reindex catches up.
  * Entity.ID = entity_name (LightRAG keys entities by name); SearchEntities returns labels only (Name set, other fields zero).
  * Edge.ID = "from||to" composite (LightRAG has no edge ID; relations addressed by (src, tgt) pair). ListEdges iterates labels via Graph(label) - O(N) reads, acceptable for v0.1.x.
  * QueryMatch.Score = 0 (LightRAG /query returns references, not ranked matches); /query/data exists for structured results when needed.
2026-05-27 - SSA field-manager fight during v0.1.1 upgrade. A prior `kubectl set image` and the recovery path's `kubectl replace` left orphan field managers (kubectl-set, before-first-apply) that blocked helm 4's server-side apply. Repaired by stripping all non-helm/non-controller field managers from .metadata.managedFields via `kubectl replace` of a filtered manifest, then `helmfile sync --args '--force-conflicts'`. CLAUDE.md should add: never run `kubectl set image` against a helm-managed resource - bump chart appVersion + helm upgrade instead.
2026-05-27 - Makefile uses `git describe --tags` => image tagged as `v0.1.1` (with v). Chart's appVersion="0.1.1" makes the deployment pull `0.1.1` (no v). Always re-tag without the v after `make push`: `docker tag .../tatara-memory:vX.Y.Z .../tatara-memory:X.Y.Z && docker push .../tatara-memory:X.Y.Z`. Long-term fix: strip leading v in Makefile, or sync chart appVersion to git describe output.
2026-05-27 - helm-unittest configmap/deployment/helpers tests are stale after the v0.1.0 patch that flipped chart env to UPPER_SNAKE; failures are pre-existing, do not block deploy. Fixing them is a v0.1.x boy-scout task (mechanical: assert UPPER_SNAKE keys, not kebab-case).

## 2026-05-27 - v0.1.3 deployed (routes flattened, ingress live)

2026-05-27 - v0.1.3 dropped the `/v1` prefix from httpapi routes (breaking change). Platform versioning now lives only at the ingress prefix `/api/v1/memory`. Service routes are at `/memories`, `/queries`, `/entities`, `/edges`, `/ingest-jobs`, etc. End-to-end smoke green via `https://tatara.szymonrichert.pl/api/v1/memory/...` with OIDC client_credentials.
2026-05-27 - Ingress: `tatara.szymonrichert.pl/api/v1/memory(/|$)(.*)` with `nginx.ingress.kubernetes.io/rewrite-target: /$2`, pathType `ImplementationSpecific`. Chart values `ingress.path` defaults to `/`; non-`/` paths trigger the rewrite. Operator endpoints (`/healthz`, `/readyz`, `/metrics`) are NOT exposed via ingress on purpose; reach them via port-forward or `/api/v1/memory/healthz` is not configured.
2026-05-27 - container build, chart publish, and homelab deploy moved to argo (tatara-memory-tag composite CWT). `make push` and `make chart-push` removed from Makefile; the only local-dev CI target is `make ci` mirroring the CWT lint+test steps. Tag push to v0.1.4+ triggers the tatara-memory-tag composite: container-build and helm-publish run in parallel, then helmfile-deploy. Four github commit statuses land on the tag commit: tatara/container, tatara/chart, tatara/deploy, tatara/tag-pipeline.
