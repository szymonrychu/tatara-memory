# tatara-memory phase 1 v1.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development`. Wave 1 is single-subagent sequential; Waves 2, 3, 4+5 dispatch parallel subagents (sonnet) in their own git worktrees; Wave 6 is a single opus merge subagent; Wave 7 is a human-driven smoke test from `main`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the full tatara-memory REST service (parent spec sections "Phase 1 - tatara-memory" through "Testing strategy") to homelab from `main`, gated by JWT bearer tokens from `auth.szymonrichert.pl`, backed by CloudNativePG + Neo4j + LightRAG v1.4.16.

**Architecture:** Go service with thin chi HTTP layer, domain `memory` package that hides LightRAG behind a typed `Client` interface, in-process async ingest worker pool with crash-safe postgres-backed job state, slog JSON logs + Prometheus metrics + OTLP tracing throughout. Single helm chart with cnpg/cluster, neo4j/neo4j, and a local lightrag subchart (ported from tatara-old).

**Tech Stack:** Go 1.25.x, `chi/v5`, `coreos/go-oidc/v3`, `prometheus/client_golang`, stdlib `log/slog`, stdlib `database/sql` + `jackc/pgx/v5`, OpenTelemetry, helm v3, helmfile, helm-unittest, SOPS, mise, pre-commit.

**Specs:**
- Implementation strategy: `docs/superpowers/specs/2026-05-24-tatara-memory-phase1-impl.md` (this repo)
- Parent design: `~/Documents/tatara/docs/superpowers/specs/2026-05-24-tatara-phases-0-1-design.md`

**Hard pins:**

| Component         | Pin                                                                              |
| ----------------- | -------------------------------------------------------------------------------- |
| Go toolchain      | newest stable 1.25.x                                                             |
| LightRAG image    | `ghcr.io/hkuds/lightrag:v1.4.16@sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d` |
| cnpg/cluster      | `0.6.1`                                                                          |
| neo4j/neo4j       | `2026.4.0`                                                                       |

**Wave flow:**

```
Wave 1 (bootstrap, 1 sonnet)
  → Wave 6 merge (opus)
Wave 2A obs ─┐
Wave 2B auth ┼─ 3 parallel sonnet subagents
Wave 2C lightrag ─┘
  → Wave 6 merge (opus)
Wave 3A memory+ingest ─┐
Wave 3B httpapi ───────┴─ 2 parallel sonnet subagents
  → Wave 6 merge (opus)
Wave 4A cmd wiring ─┐
Wave 4B chart bodies┤─ 3 parallel sonnet subagents (4+5 concurrent)
Wave 5  lightrag subchart ─┘
  → Wave 6 merge (opus)
Wave 7 (human smoke test from main)
```

**Worktree convention:** Each wave subagent runs in `~/Documents/tatara/tatara-memory-worktrees/wave-NX-<name>/` off `main`. The merge subagent (Wave 6) consolidates and cleans up after each wave.

**Deferred to v1.1 (out of scope here):**
- GitHub Actions CI
- docker-compose integration tests with real services
- Helm chart push to harbor OCI via CI

---
## Wave 1 — Bootstrap (single sequential sonnet subagent)

Lays the foundation everything else depends on. **Cannot run in parallel with other waves.** Runs in a worktree off `main`. After every task, the subagent commits locally; at the end of the wave the worktree merges back to `main` via the opus merge subagent.

### Task 1.1: Initialize the Go module

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Initialize module**

```bash
cd ~/Documents/tatara/tatara-memory
go mod init github.com/szymonrychu/tatara-memory
```

- [ ] **Step 2: Pin Go toolchain to newest stable**

Run: `go version` and capture the exact `go1.25.x` value, then ensure `go.mod` has:

```
module github.com/szymonrychu/tatara-memory

go 1.25.3
```

(Replace `1.25.3` with the actual newest stable from `go version`.)

- [ ] **Step 3: Add baseline dependencies**

```bash
go get github.com/go-chi/chi/v5@latest
go get github.com/coreos/go-oidc/v3@latest
go get github.com/golang-jwt/jwt/v5@latest
go get github.com/prometheus/client_golang@latest
go get github.com/stretchr/testify@latest
go get go.opentelemetry.io/otel@latest
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp@latest
go get go.opentelemetry.io/otel/sdk/trace@latest
go get github.com/jackc/pgx/v5@latest
go mod tidy
```

- [ ] **Step 4: Verify**

Run: `go build ./...`
Expected: PASS (no packages yet, builds cleanly).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: bootstrap Go module with baseline deps"
```

### Task 1.2: Pin tool versions with mise

**Files:**
- Create: `.mise.toml`

- [ ] **Step 1: Write `.mise.toml`**

```toml
[tools]
go = "1.25"
helm = "3.16"
helmfile = "0.171"
sops = "3.9"
golangci-lint = "1.62"
pre-commit = "4.0"
"aqua:helmfile/helmfile" = "0.171"

[env]
GOFLAGS = "-count=1"
```

- [ ] **Step 2: Install tools**

Run: `mise install`
Expected: every tool installs without error.

- [ ] **Step 3: Add the helm-diff, helm-secrets, helm-unittest helm plugins**

```bash
helm plugin install https://github.com/databus23/helm-diff || true
helm plugin install https://github.com/jkroepke/helm-secrets || true
helm plugin install https://github.com/helm-unittest/helm-unittest || true
```

Expected: plugins install (the `|| true` tolerates already-installed).

- [ ] **Step 4: Commit**

```bash
git add .mise.toml
git commit -m "chore: pin tool versions with mise"
```

### Task 1.3: Configure golangci-lint

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Write `.golangci.yml`**

```yaml
run:
  timeout: 5m
  tests: true

linters:
  disable-all: true
  enable:
    - errcheck
    - gofmt
    - goimports
    - gosec
    - govet
    - ineffassign
    - revive
    - staticcheck
    - unconvert
    - unused

linters-settings:
  goimports:
    local-prefixes: github.com/szymonrychu/tatara-memory
  revive:
    rules:
      - name: exported
        disabled: false

issues:
  exclude-rules:
    - path: _test\.go
      linters:
        - gosec
```

- [ ] **Step 2: Verify**

Run: `golangci-lint run ./...`
Expected: PASS (no Go files yet).

- [ ] **Step 3: Commit**

```bash
git add .golangci.yml
git commit -m "chore: configure golangci-lint"
```

### Task 1.4: Configure pre-commit hooks

**Files:**
- Create: `.pre-commit-config.yaml`

- [ ] **Step 1: Write `.pre-commit-config.yaml`**

```yaml
repos:
  - repo: https://github.com/pre-commit/pre-commit-hooks
    rev: v5.0.0
    hooks:
      - id: end-of-file-fixer
      - id: trailing-whitespace
      - id: check-yaml
        args: [--allow-multiple-documents]
      - id: check-merge-conflict
      - id: check-added-large-files

  - repo: https://github.com/gitleaks/gitleaks
    rev: v8.21.2
    hooks:
      - id: gitleaks

  - repo: https://github.com/adrienverge/yamllint
    rev: v1.35.1
    hooks:
      - id: yamllint
        args: [-c=.yamllint.yaml]

  - repo: https://github.com/dnephin/pre-commit-golang
    rev: v0.5.1
    hooks:
      - id: go-fmt
      - id: go-imports
      - id: golangci-lint

  - repo: https://github.com/gruntwork-io/pre-commit
    rev: v0.1.25
    hooks:
      - id: helmlint
```

- [ ] **Step 2: Write `.yamllint.yaml`**

```yaml
extends: default
rules:
  line-length:
    max: 200
  document-start: disable
  truthy:
    check-keys: false
  comments:
    min-spaces-from-content: 1
  indentation:
    indent-sequences: consistent
ignore: |
  charts/*/templates/
  charts/*/charts/*/templates/
```

- [ ] **Step 3: Install hooks**

Run: `pre-commit install`
Expected: hooks installed in `.git/hooks/pre-commit`.

- [ ] **Step 4: Run hooks**

Run: `pre-commit run --all-files`
Expected: PASS (everything clean; some files may auto-fix on first run; re-run to confirm idempotency).

- [ ] **Step 5: Commit**

```bash
git add .pre-commit-config.yaml .yamllint.yaml
git commit -m "chore: configure pre-commit hooks"
```

### Task 1.5: Write the Dockerfile

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

- [ ] **Step 1: Write `Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w \
      -X github.com/szymonrychu/tatara-memory/internal/version.Version=${VERSION} \
      -X github.com/szymonrychu/tatara-memory/internal/version.Commit=${COMMIT} \
      -X github.com/szymonrychu/tatara-memory/internal/version.Date=${DATE}" \
    -o /out/tatara-memory \
    ./cmd/tatara-memory

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/tatara-memory /tatara-memory
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/tatara-memory"]
```

- [ ] **Step 2: Write `.dockerignore`**

```
.git
.github
.idea
.vscode
charts
docs
test
*.md
*.local.*
```

- [ ] **Step 3: Verify build (after cmd/ exists in 1.10)**

Defer the actual `docker build` to Task 1.11 once `cmd/tatara-memory/main.go` exists. For now just commit.

- [ ] **Step 4: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "chore: add multi-stage Dockerfile"
```

### Task 1.6: Write the Makefile

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write `Makefile`**

```makefile
SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c

REGISTRY ?= harbor.szymonrichert.pl
IMAGE_NAME ?= tatara-memory
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

IMAGE_REF := $(REGISTRY)/$(IMAGE_NAME):$(VERSION)

.PHONY: all lint test build image push chart-lint chart-test chart-push tidy fmt clean

all: lint test build

tidy:
	go mod tidy

fmt:
	gofmt -s -w .
	goimports -w -local github.com/szymonrychu/tatara-memory .

lint:
	golangci-lint run ./...

test:
	go test ./... -race -count=1

build:
	CGO_ENABLED=0 go build \
		-trimpath \
		-ldflags "-s -w \
		  -X github.com/szymonrychu/tatara-memory/internal/version.Version=$(VERSION) \
		  -X github.com/szymonrychu/tatara-memory/internal/version.Commit=$(COMMIT) \
		  -X github.com/szymonrychu/tatara-memory/internal/version.Date=$(DATE)" \
		-o bin/tatara-memory \
		./cmd/tatara-memory

image:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		-t $(IMAGE_REF) \
		.

push: image
	docker push $(IMAGE_REF)

chart-lint:
	helm lint charts/tatara-memory

chart-test:
	helm unittest charts/tatara-memory

chart-push:
	helm package charts/tatara-memory -d dist/
	helm push dist/tatara-memory-*.tgz oci://$(REGISTRY)/charts

clean:
	rm -rf bin dist
```

- [ ] **Step 2: Verify**

Run: `make lint`
Expected: PASS (no Go files yet).

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "chore: add Makefile"
```

### Task 1.7: Write the .gitignore

**Files:**
- Create: `.gitignore`

- [ ] **Step 1: Write `.gitignore`**

```
# Binaries
bin/
dist/

# Coverage
*.out
coverage.html

# Local config / secrets
.env
.env.local
*.local.yaml
*.dec.yaml

# Helm working files
charts/*/charts/*.tgz
charts/*/charts/cnpg-*
charts/*/charts/neo4j-*

# IDE
.idea/
.vscode/
*.swp
.DS_Store

# Mise local override
.mise.local.toml
```

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: add .gitignore"
```

### Task 1.8: Copy CLAUDE.md and seed docs

**Files:**
- Create: `CLAUDE.md`
- Create: `MEMORY.md`
- Create: `ROADMAP.md`
- Create: `README.md`

- [ ] **Step 1: Copy parent CLAUDE.md verbatim**

```bash
cp ~/Documents/tatara/CLAUDE.md CLAUDE.md
```

- [ ] **Step 2: Write `MEMORY.md` stub**

```markdown
# MEMORY.md

Component-local memory for tatara-memory. Cross-repo decisions live in
`~/Documents/tatara/MEMORY.md`.

Format: `YYYY-MM-DD - decision/finding`

---

## Decisions

*(nothing yet)*

## Dead-ends / things tried that did not work

*(nothing yet)*

## Open questions

*(nothing yet)*
```

- [ ] **Step 3: Write `ROADMAP.md` stub**

```markdown
# ROADMAP.md

Component-local roadmap for tatara-memory. Phase-level platform roadmap
lives in `~/Documents/tatara/ROADMAP.md`.

Statuses: `planned`, `in progress`, `shipped`.

---

## v1.0 — Phase 1 ship

**Status:** in progress

See `docs/superpowers/specs/2026-05-24-tatara-memory-phase1-impl.md`
and `docs/superpowers/plans/2026-05-24-tatara-memory-phase1.md`.

## v1.1 — Follow-ups

**Status:** planned

- GitHub Actions CI (lint, test, build, push image + chart).
- docker-compose integration tests with real lightrag + postgres + neo4j.
- Helm chart push to harbor OCI via CI.
```

- [ ] **Step 4: Write `README.md` stub**

```markdown
# tatara-memory

REST memory service over LightRAG, OIDC-gated. Part of the tatara
platform. See `~/Documents/tatara/README.md` for the platform overview.

## Status

Phase 1 v1.0 in active development. See `ROADMAP.md`.

## Layout

```
cmd/tatara-memory/       service entrypoint
internal/                non-exported packages (auth, memory, ingest, lightrag, httpapi, obs, version)
charts/tatara-memory/    helm chart with cnpg, neo4j, lightrag subcharts
helmfile.yaml.gotmpl     single-release helmfile (default env)
docs/superpowers/        specs and plans
```

## Build

```
mise install
make all
```

## Deploy

```
helm dep update charts/tatara-memory
helmfile diff
helmfile apply
```

(Build/deploy only from `main`. See parent `CLAUDE.md`.)

## License

AGPL-3.0
```

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md MEMORY.md ROADMAP.md README.md
git commit -m "docs: seed CLAUDE.md, MEMORY, ROADMAP, README"
```

### Task 1.9: Scaffold the version package

**Files:**
- Create: `internal/version/version.go`

- [ ] **Step 1: Write `internal/version/version.go`**

```go
package version

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
```

- [ ] **Step 2: Verify**

Run: `go build ./internal/version/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/version/
git commit -m "feat(version): add version vars populated via ldflags"
```

### Task 1.10: Scaffold cmd/tatara-memory entrypoint

**Files:**
- Create: `cmd/tatara-memory/main.go`

- [ ] **Step 1: Write `cmd/tatara-memory/main.go`**

```go
package main

import (
	"fmt"
	"os"

	"github.com/szymonrychu/tatara-memory/internal/version"
)

func main() {
	fmt.Fprintf(os.Stdout, "tatara-memory %s (commit %s, built %s)\n",
		version.Version, version.Commit, version.Date)
}
```

- [ ] **Step 2: Verify build**

Run: `make build`
Expected: produces `bin/tatara-memory` with embedded version info.

- [ ] **Step 3: Verify run**

Run: `./bin/tatara-memory`
Expected stdout: `tatara-memory <version> (commit <sha>, built <iso>)`.

- [ ] **Step 4: Commit**

```bash
git add cmd/
git commit -m "feat(cmd): scaffold tatara-memory entrypoint"
```

### Task 1.11: Verify Docker build end-to-end

- [ ] **Step 1: Build image**

Run: `make image`
Expected: image tagged `harbor.szymonrichert.pl/tatara-memory:<version>` exists.

- [ ] **Step 2: Run image**

Run: `docker run --rm harbor.szymonrichert.pl/tatara-memory:$(git describe --tags --always --dirty)` (or whatever VERSION resolves to)
Expected stdout: same version line as `./bin/tatara-memory`.

- [ ] **Step 3: No commit needed** (verification only)

### Task 1.12: Scaffold the helm chart

**Files:**
- Create: `charts/tatara-memory/` (via `helm create`)

- [ ] **Step 1: Run `helm create`**

```bash
mkdir -p charts
helm create charts/tatara-memory
```

- [ ] **Step 2: Strip non-essentials**

```bash
rm charts/tatara-memory/templates/NOTES.txt
rm charts/tatara-memory/templates/hpa.yaml
rm -rf charts/tatara-memory/templates/tests
```

- [ ] **Step 3: Replace `Chart.yaml`**

```yaml
apiVersion: v2
name: tatara-memory
description: Tatara memory service (REST over LightRAG, OIDC-gated)
type: application
version: 0.1.0
appVersion: "0.1.0"

dependencies:
  - name: cluster
    version: 0.6.1
    repository: https://cloudnative-pg.github.io/charts
    alias: postgres
    condition: postgres.enabled
  - name: neo4j
    version: 2026.4.0
    repository: https://helm.neo4j.com/neo4j
    condition: neo4j.enabled
  - name: lightrag
    version: 0.1.0
    repository: file://charts/lightrag
    condition: lightrag.enabled
```

- [ ] **Step 4: Strip `values.yaml` to camelCase scalars only**

```yaml
image:
  repository: harbor.szymonrichert.pl/tatara-memory
  tag: ""
  digest: ""
  pullPolicy: IfNotPresent

imagePullSecrets:
  - name: regcred

replicaCount: 1

httpAddr: ":8080"
logLevel: "info"
workerPoolSize: 4

lightragBaseUrl: "http://tatara-memory-lightrag:9621"

oidcIssuer: "https://auth.szymonrichert.pl/realms/master"
oidcAudience: "tatara-memory"

otlpEndpoint: ""

pgHost: "tatara-memory-postgres-cluster-rw"
pgPort: "5432"
pgDb: "tatara_memory"
pgUser: "tatara_memory"

# Existing-secret references (secrets ship via SOPS-encrypted overrides
# in values/<env>.secrets.yaml, never plaintext here).
existingSecrets:
  pgPasswordSecret: ""
  pgPasswordKey: "password"
  oidcClientSecretSecret: ""
  oidcClientSecretKey: "client-secret"

service:
  type: ClusterIP
  port: 8080

ingress:
  enabled: true
  className: nginx
  host: "tatara-memory.szymonrichert.pl"
  clusterIssuer: "letsencrypt-prod"
  tlsSecretName: "tatara-memory-tls"

serviceMonitor:
  enabled: true
  interval: "30s"

networkPolicy:
  enabled: true

postgres:
  enabled: true

neo4j:
  enabled: true

lightrag:
  enabled: true

serviceAccount:
  create: true
  annotations: {}
  name: ""

podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532

containerSecurityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
  readOnlyRootFilesystem: true

resources:
  requests:
    cpu: "50m"
    memory: "128Mi"
  limits:
    cpu: "1"
    memory: "512Mi"
```

- [ ] **Step 5: Verify**

Run: `helm lint charts/tatara-memory`
Expected: 1 chart(s) linted, 0 chart(s) failed (subchart `lightrag` warning is acceptable until Wave 5 ships).

- [ ] **Step 6: Commit**

```bash
git add charts/tatara-memory/
git commit -m "feat(chart): scaffold tatara-memory chart skeleton with deps"
```

### Task 1.13: Scaffold the helmfile

**Files:**
- Create: `helmfile.yaml.gotmpl`
- Create: `values/common.yaml`
- Create: `values/default.yaml`
- Create: `values/tatara-memory/common.yaml`
- Create: `values/tatara-memory/default.yaml`
- Create: `values/tatara-memory/default.secrets.yaml` (empty stub, SOPS-encrypted later)
- Create: `.sops.yaml`

- [ ] **Step 1: Write `helmfile.yaml.gotmpl`**

```yaml
environments:
  default: {}

---

helmDefaults:
  wait: true
  syncArgs:
    - --rollback-on-failure

templates:
  default: &default
    missingFileHandler: Warn
    values:
      - values/common.yaml
      - values/{{`{{ .Environment.Name }}`}}.yaml
      - values/{{`{{ .Release.Name }}`}}/common.yaml
      - values/{{`{{ .Release.Name }}`}}/{{`{{ .Environment.Name }}`}}.yaml
    secrets:
      - values/{{`{{ .Release.Name }}`}}/{{`{{ .Environment.Name }}`}}.secrets.yaml

repositories:
  - name: cnpg
    url: https://cloudnative-pg.github.io/charts
  - name: neo4j
    url: https://helm.neo4j.com/neo4j

releases:
  - name: tatara-memory
    chart: ./charts/tatara-memory
    namespace: tatara-memory
    createNamespace: true
    labels:
      purpose: tatara
      application: tatara-memory
    <<: *default
```

- [ ] **Step 2: Write `values/common.yaml`** (empty platform-wide override slot)

```yaml
{}
```

- [ ] **Step 3: Write `values/default.yaml`** (empty env-wide override slot)

```yaml
{}
```

- [ ] **Step 4: Write `values/tatara-memory/common.yaml`** (release-wide override slot)

```yaml
{}
```

- [ ] **Step 5: Write `values/tatara-memory/default.yaml`** (release-and-env override slot)

```yaml
{}
```

- [ ] **Step 6: Write `values/tatara-memory/default.secrets.yaml`** as a SOPS-encryptable stub

```yaml
existingSecrets:
  pgPasswordSecret: "tatara-memory-pg-app"
```

Then encrypt:

```bash
sops -e -i values/tatara-memory/default.secrets.yaml
```

(If sops keys are not yet configured, leave the file plaintext for now and add a `TODO encrypt before commit` note via `.sops.yaml` configuration in step 7. The pre-commit gitleaks hook will fail if there is anything resembling a secret value.)

- [ ] **Step 7: Write `.sops.yaml`**

```yaml
creation_rules:
  - path_regex: \.secrets\.yaml$
    age: age1placeholder  # replace with actual age public key from ~/.config/sops/age/keys.txt
```

- [ ] **Step 8: Verify**

Run: `helmfile lint`
Expected: PASS (helm dep update may need to run first; see step 9).

- [ ] **Step 9: Update chart dependencies**

```bash
helm dep update charts/tatara-memory
```

Expected: downloads cnpg/cluster and neo4j subcharts into `charts/tatara-memory/charts/`. The local `lightrag` subchart is missing until Wave 5; expect a warning, not a failure.

- [ ] **Step 10: Commit**

```bash
git add helmfile.yaml.gotmpl values/ .sops.yaml charts/tatara-memory/Chart.lock
git commit -m "feat(helmfile): scaffold helmfile with cnpg/neo4j repos and tatara-memory release"
```

### Task 1.14: Scaffold the docs tree

**Files:**
- Create: `docs/superpowers/specs/` (already populated with the impl spec)
- Create: `docs/superpowers/plans/` (already populated with this plan)

- [ ] **Step 1: Verify both files exist**

```bash
ls docs/superpowers/specs/2026-05-24-tatara-memory-phase1-impl.md
ls docs/superpowers/plans/2026-05-24-tatara-memory-phase1.md
```

Expected: both files present.

- [ ] **Step 2: No commit needed** — both files were committed during planning.

### Task 1.15: Final wave-1 verification gate

- [ ] **Step 1: Run the full local gate**

```bash
pre-commit run --all-files
make lint test build
helm lint charts/tatara-memory
helmfile lint
```

Expected: every command PASS. Acceptable warnings:
- `helm lint`: missing `lightrag` subchart (lands Wave 5)
- `helmfile lint`: subchart warning

- [ ] **Step 2: Push the bootstrap branch (worktree → main)**

Wave-1 subagent stops here. The merge subagent (opus, Wave 6) integrates this worktree onto `main` and verifies the gate runs on `main`.
## Wave 2A — internal/obs

Subagent: sonnet. Worktree: `wt/wave2a-obs` off `main`. Independent of 2B and 2C. All tests use `testify/require`.

### Task 2A.1: obs — package skeleton and logger contract test

**Files:**
- Create: `internal/obs/obs.go`
- Test: `internal/obs/logger_test.go`

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestLogger_EmitsValidJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := obs.NewLogger(&buf, slog.LevelInfo)
	require.NotNil(t, logger)

	logger.Info("hello", slog.String("request_id", "abc"))

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "hello", got["msg"])
	require.Equal(t, "abc", got["request_id"])
	require.Equal(t, "INFO", got["level"])
	require.Contains(t, got, "time")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/... -run TestLogger_EmitsValidJSON -v`
Expected: FAIL with `undefined: obs.NewLogger`

- [ ] **Step 3: Write minimal implementation**

```go
package obs

import (
	"io"
	"log/slog"
)

func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(h)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/obs/... -run TestLogger_EmitsValidJSON -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obs/
git commit -m "feat(obs): add JSON slog logger factory"
```

### Task 2A.2: obs — default logger with stable field set

**Files:**
- Edit: `internal/obs/obs.go`
- Test: `internal/obs/logger_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestDefaultLogger_StableFields(t *testing.T) {
	var buf bytes.Buffer
	base := obs.NewLogger(&buf, slog.LevelInfo)
	req := obs.RequestLogger(base, obs.RequestFields{
		RequestID:  "rid-1",
		User:       "szymon",
		Route:      "/v1/memories",
		Method:     "POST",
		Status:     201,
		DurationMs: 12,
	})
	req.Info("request handled")

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	require.Equal(t, "rid-1", got["request_id"])
	require.Equal(t, "szymon", got["user"])
	require.Equal(t, "/v1/memories", got["route"])
	require.Equal(t, "POST", got["method"])
	require.EqualValues(t, 201, got["status"])
	require.EqualValues(t, 12, got["duration_ms"])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/... -run TestDefaultLogger_StableFields -v`
Expected: FAIL with `undefined: obs.RequestLogger`

- [ ] **Step 3: Write minimal implementation**

Append to `internal/obs/obs.go`:

```go
type RequestFields struct {
	RequestID  string
	User       string
	Route      string
	Method     string
	Status     int
	DurationMs int64
}

func RequestLogger(base *slog.Logger, f RequestFields) *slog.Logger {
	return base.With(
		slog.String("request_id", f.RequestID),
		slog.String("user", f.User),
		slog.String("route", f.Route),
		slog.String("method", f.Method),
		slog.Int("status", f.Status),
		slog.Int64("duration_ms", f.DurationMs),
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/obs/... -run TestDefaultLogger_StableFields -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obs/
git commit -m "feat(obs): add RequestLogger with stable field set"
```

### Task 2A.3: obs — prometheus registry factory test

**Files:**
- Create: `internal/obs/metrics.go`
- Test: `internal/obs/metrics_test.go`

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestPromRegistry_HasDefaultCollectors(t *testing.T) {
	reg := obs.PromRegistry()
	require.NotNil(t, reg)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	require.True(t, names["go_goroutines"], "expected go collector")
	require.True(t, names["process_cpu_seconds_total"], "expected process collector")

	_, ok := any(reg).(*prometheus.Registry)
	require.True(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/... -run TestPromRegistry_HasDefaultCollectors -v`
Expected: FAIL with `undefined: obs.PromRegistry`

- [ ] **Step 3: Write minimal implementation**

```go
package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

func PromRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return reg
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/obs/... -run TestPromRegistry_HasDefaultCollectors -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obs/
git commit -m "feat(obs): add prometheus registry with go and process collectors"
```

### Task 2A.4: obs — tracer no-op when endpoint empty

**Files:**
- Create: `internal/obs/tracing.go`
- Test: `internal/obs/tracing_test.go`

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestTracerProvider_NoopWhenEmpty(t *testing.T) {
	tp, shutdown, err := obs.TracerProvider(context.Background(), "", "tatara-memory")
	require.NoError(t, err)
	require.NotNil(t, tp)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	_, ok := tp.(noop.TracerProvider)
	require.True(t, ok, "expected noop tracer provider when endpoint is empty")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/... -run TestTracerProvider_NoopWhenEmpty -v`
Expected: FAIL with `undefined: obs.TracerProvider`

- [ ] **Step 3: Write minimal implementation**

```go
package obs

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type ShutdownFunc func(context.Context) error

func TracerProvider(ctx context.Context, endpoint, serviceName string) (trace.TracerProvider, ShutdownFunc, error) {
	if endpoint == "" {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, nil
	}
	return newOTLPProvider(ctx, endpoint, serviceName)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/obs/... -run TestTracerProvider_NoopWhenEmpty -v`
Expected: FAIL with `undefined: obs.newOTLPProvider`. Add the OTLP stub now so the package compiles even though the test only exercises the no-op branch.

Append to `internal/obs/tracing.go`:

```go
import (
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

func newOTLPProvider(ctx context.Context, endpoint, serviceName string) (trace.TracerProvider, ShutdownFunc, error) {
	exp, err := otlptrace.New(ctx, otlptracegrpc.NewClient(
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	))
	if err != nil {
		return nil, nil, err
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return nil, nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	return tp, tp.Shutdown, nil
}
```

Re-run: `go test ./internal/obs/... -run TestTracerProvider_NoopWhenEmpty -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obs/
git commit -m "feat(obs): add OTLP tracer provider with no-op fallback"
```

### Task 2A.5: obs — tracer constructs OTLP provider when endpoint is set

**Files:**
- Edit: `internal/obs/tracing_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestTracerProvider_OTLPWhenEndpointSet(t *testing.T) {
	ctx := context.Background()
	tp, shutdown, err := obs.TracerProvider(ctx, "localhost:4317", "tatara-memory")
	require.NoError(t, err)
	require.NotNil(t, tp)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(ctx)
	})

	_, isNoop := tp.(noop.TracerProvider)
	require.False(t, isNoop, "expected real provider when endpoint is set")
}
```

Add `"time"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/... -run TestTracerProvider_OTLPWhenEndpointSet -v`
Expected: FAIL (no real provider returned yet, or panics; type-assert fails)

- [ ] **Step 3: Write minimal implementation**

Already implemented in 2A.4 via `newOTLPProvider`. No additional code needed; the test should pass after the OTLP path runs.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/obs/... -run TestTracerProvider_OTLPWhenEndpointSet -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obs/
git commit -m "test(obs): cover OTLP tracer provider construction"
```

### Task 2A.6: obs — wire-up convenience constructor

**Files:**
- Edit: `internal/obs/obs.go`
- Test: `internal/obs/obs_test.go`

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func TestNew_AssemblesAllThree(t *testing.T) {
	var buf bytes.Buffer
	o, err := obs.New(context.Background(), obs.Config{
		LogWriter:    &buf,
		LogLevel:     slog.LevelInfo,
		ServiceName:  "tatara-memory",
		OTLPEndpoint: "",
	})
	require.NoError(t, err)
	require.NotNil(t, o.Logger)
	require.NotNil(t, o.Registry)
	require.NotNil(t, o.Tracer)
	require.NotNil(t, o.Shutdown)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/obs/... -run TestNew_AssemblesAllThree -v`
Expected: FAIL with `undefined: obs.New`

- [ ] **Step 3: Write minimal implementation**

```go
import (
	"context"
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	LogWriter    io.Writer
	LogLevel     slog.Level
	ServiceName  string
	OTLPEndpoint string
}

type Obs struct {
	Logger   *slog.Logger
	Registry *prometheus.Registry
	Tracer   trace.TracerProvider
	Shutdown ShutdownFunc
}

func New(ctx context.Context, cfg Config) (*Obs, error) {
	tp, shutdown, err := TracerProvider(ctx, cfg.OTLPEndpoint, cfg.ServiceName)
	if err != nil {
		return nil, err
	}
	return &Obs{
		Logger:   NewLogger(cfg.LogWriter, cfg.LogLevel),
		Registry: PromRegistry(),
		Tracer:   tp,
		Shutdown: shutdown,
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/obs/... -run TestNew_AssemblesAllThree -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obs/
git commit -m "feat(obs): add New constructor wiring logger, registry, tracer"
```

### Task 2A.7: obs — full package test sweep

- [ ] **Step 1: Run the full package suite**

Run: `go test ./internal/obs/... -v -count=1`
Expected: all 6 tests PASS.

- [ ] **Step 2: Lint**

Run: `golangci-lint run ./internal/obs/...`
Expected: no findings.

- [ ] **Step 3: Commit (only if anything moved)**

```bash
git status
# if clean, skip the commit
```

---

## Wave 2B — internal/auth

Subagent: sonnet. Worktree: `wt/wave2b-auth` off `main`. Independent of 2A and 2C. Uses `github.com/coreos/go-oidc/v3/oidc`, `github.com/golang-jwt/jwt/v5`, `github.com/go-chi/chi/v5`, `testify/require`.

### Task 2B.1: auth — package skeleton and Config struct

**Files:**
- Create: `internal/auth/auth.go`
- Test: `internal/auth/auth_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth"
)

func TestConfig_Validate(t *testing.T) {
	require.Error(t, (auth.Config{}).Validate())
	require.Error(t, (auth.Config{Issuer: "https://example/realms/master"}).Validate())
	require.NoError(t, (auth.Config{
		Issuer:   "https://example/realms/master",
		Audience: "tatara-memory",
	}).Validate())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestConfig_Validate -v`
Expected: FAIL with `undefined: auth.Config`

- [ ] **Step 3: Write minimal implementation**

```go
package auth

import "errors"

type Config struct {
	Issuer   string
	Audience string
}

func (c Config) Validate() error {
	if c.Issuer == "" {
		return errors.New("auth: issuer is required")
	}
	if c.Audience == "" {
		return errors.New("auth: audience is required")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/... -run TestConfig_Validate -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat(auth): add Config with issuer and audience"
```

### Task 2B.2: auth/testjwks — RSA keygen and JWKS server

**Files:**
- Create: `internal/auth/testjwks/testjwks.go`
- Test: `internal/auth/testjwks/testjwks_test.go`

- [ ] **Step 1: Write the failing test**

```go
package testjwks_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
)

func TestServer_ServesJWKS(t *testing.T) {
	srv := testjwks.NewServer(t)
	resp, err := http.Get(srv.JWKSURL())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var jwks struct {
		Keys []map[string]any `json:"keys"`
	}
	require.NoError(t, json.Unmarshal(body, &jwks))
	require.Len(t, jwks.Keys, 1)
	require.Equal(t, "RSA", jwks.Keys[0]["kty"])
	require.NotEmpty(t, jwks.Keys[0]["n"])
	require.NotEmpty(t, jwks.Keys[0]["e"])
	require.NotEmpty(t, jwks.Keys[0]["kid"])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/testjwks/... -run TestServer_ServesJWKS -v`
Expected: FAIL with `undefined: testjwks.NewServer`

- [ ] **Step 3: Write minimal implementation**

```go
package testjwks

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type Server struct {
	t      *testing.T
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
}

func NewServer(t *testing.T) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	s := &Server{t: t, key: key, kid: "test-kid-1"}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":   s.issuer,
			"jwks_uri": s.issuer + "/jwks.json",
		})
	})
	mux.HandleFunc("/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": s.kid,
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}},
		})
	})

	s.srv = httptest.NewServer(mux)
	s.issuer = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

func (s *Server) Issuer() string  { return s.issuer }
func (s *Server) JWKSURL() string { return s.issuer + "/jwks.json" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/testjwks/... -run TestServer_ServesJWKS -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat(auth/testjwks): add RSA-backed JWKS test server"
```

### Task 2B.3: auth/testjwks — token signing helper

**Files:**
- Edit: `internal/auth/testjwks/testjwks.go`
- Test: `internal/auth/testjwks/testjwks_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestServer_SignsValidToken(t *testing.T) {
	srv := testjwks.NewServer(t)
	token := srv.SignToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
		Extra:    map[string]any{"preferred_username": "szymon"},
	})
	require.NotEmpty(t, token)
	// JWT has 3 dot-separated parts
	parts := 0
	for _, c := range token {
		if c == '.' {
			parts++
		}
	}
	require.Equal(t, 2, parts)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/testjwks/... -run TestServer_SignsValidToken -v`
Expected: FAIL with `undefined: testjwks.Claims`

- [ ] **Step 3: Write minimal implementation**

Append to `internal/auth/testjwks/testjwks.go`:

```go
import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	Issuer    string
	Audience  []string
	Subject   string
	NotBefore time.Time
	IssuedAt  time.Time
	ExpiresAt time.Time
	Extra     map[string]any
}

func (s *Server) SignToken(t *testing.T, c Claims) string {
	t.Helper()
	now := time.Now()
	if c.IssuedAt.IsZero() {
		c.IssuedAt = now
	}
	if c.NotBefore.IsZero() {
		c.NotBefore = now
	}
	if c.ExpiresAt.IsZero() {
		c.ExpiresAt = now.Add(time.Hour)
	}

	claims := jwt.MapClaims{
		"iss": c.Issuer,
		"aud": c.Audience,
		"sub": c.Subject,
		"iat": c.IssuedAt.Unix(),
		"nbf": c.NotBefore.Unix(),
		"exp": c.ExpiresAt.Unix(),
	}
	for k, v := range c.Extra {
		claims[k] = v
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.kid
	signed, err := tok.SignedString(s.key)
	require.NoError(t, err)
	return signed
}

// SignTokenWithKey lets tests sign with a foreign key to simulate bad-signature cases.
func (s *Server) SignTokenWithKey(t *testing.T, key *rsa.PrivateKey, c Claims) string {
	t.Helper()
	tmp := *s
	tmp.key = key
	return tmp.SignToken(t, c)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/testjwks/... -run TestServer_SignsValidToken -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat(auth/testjwks): add token signing helpers"
```

### Task 2B.4: auth — Verifier accepts valid token

**Files:**
- Edit: `internal/auth/auth.go`
- Create: `internal/auth/verifier.go`
- Test: `internal/auth/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth"
	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
)

func TestVerifier_ValidToken(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	tok := srv.SignToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
		Extra:    map[string]any{"preferred_username": "szymon"},
	})

	claims, err := v.Verify(ctx, tok)
	require.NoError(t, err)
	require.Equal(t, "user-1", claims.Subject)
	require.Equal(t, "szymon", claims.PreferredUsername)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestVerifier_ValidToken -v`
Expected: FAIL with `undefined: auth.NewVerifier`

- [ ] **Step 3: Write minimal implementation**

```go
package auth

import (
	"context"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Verifier struct {
	cfg      Config
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
}

type Claims struct {
	Subject           string `json:"sub"`
	PreferredUsername string `json:"preferred_username"`
	Issuer            string `json:"iss"`
}

func NewVerifier(ctx context.Context, cfg Config) (*Verifier, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: discover issuer: %w", err)
	}
	v := provider.Verifier(&oidc.Config{ClientID: cfg.Audience})
	return &Verifier{cfg: cfg, provider: provider, verifier: v}, nil
}

func (v *Verifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	tok, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return nil, err
	}
	var c Claims
	if err := tok.Claims(&c); err != nil {
		return nil, fmt.Errorf("auth: decode claims: %w", err)
	}
	c.Issuer = tok.Issuer
	c.Subject = tok.Subject
	if c.Subject == "" {
		return nil, fmt.Errorf("auth: missing sub claim")
	}
	return &c, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/... -run TestVerifier_ValidToken -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat(auth): add OIDC verifier wrapping go-oidc"
```

### Task 2B.5: auth — Verifier rejects expired token

**Files:**
- Edit: `internal/auth/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestVerifier_ExpiredToken(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	tok := srv.SignToken(t, testjwks.Claims{
		Issuer:    srv.Issuer(),
		Audience:  []string{"tatara-memory"},
		Subject:   "user-1",
		IssuedAt:  time.Now().Add(-2 * time.Hour),
		NotBefore: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour),
	})

	_, err = v.Verify(ctx, tok)
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
}
```

Add `"time"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestVerifier_ExpiredToken -v`
Expected: PASS already (go-oidc rejects exp). If it does not, the verifier needs an explicit expiry check; the impl from 2B.4 delegates this to go-oidc.

- [ ] **Step 3: Confirm coverage**

If the test already passes, skip to step 5. No new code needed; this exercises the existing path.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/auth/... -run TestVerifier_ExpiredToken -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "test(auth): cover expired-token rejection"
```

### Task 2B.6: auth — Verifier rejects wrong issuer

**Files:**
- Edit: `internal/auth/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestVerifier_WrongIssuer(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	tok := srv.SignToken(t, testjwks.Claims{
		Issuer:   "https://evil.example/realms/master",
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
	})

	_, err = v.Verify(ctx, tok)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestVerifier_WrongIssuer -v`
Expected: PASS (go-oidc enforces issuer match).

- [ ] **Step 3: Confirm**

No new code needed.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/auth/... -run TestVerifier_WrongIssuer -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "test(auth): cover wrong-issuer rejection"
```

### Task 2B.7: auth — Verifier rejects wrong audience

**Files:**
- Edit: `internal/auth/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestVerifier_WrongAudience(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	tok := srv.SignToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"some-other-app"},
		Subject:  "user-1",
	})

	_, err = v.Verify(ctx, tok)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestVerifier_WrongAudience -v`
Expected: PASS (go-oidc enforces audience match).

- [ ] **Step 3: Confirm**

No new code needed.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/auth/... -run TestVerifier_WrongAudience -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "test(auth): cover wrong-audience rejection"
```

### Task 2B.8: auth — Verifier rejects bad signature

**Files:**
- Edit: `internal/auth/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestVerifier_BadSignature(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	foreign, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tok := srv.SignTokenWithKey(t, foreign, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
	})

	_, err = v.Verify(ctx, tok)
	require.Error(t, err)
}
```

Add `"crypto/rand"` and `"crypto/rsa"` imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestVerifier_BadSignature -v`
Expected: PASS (signature mismatch caught by go-oidc).

- [ ] **Step 3: Confirm**

No new code needed.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/auth/... -run TestVerifier_BadSignature -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "test(auth): cover bad-signature rejection"
```

### Task 2B.9: auth — Verifier requires sub claim

**Files:**
- Edit: `internal/auth/verifier_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestVerifier_MissingSubClaim(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	tok := srv.SignToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		// Subject deliberately empty
	})

	_, err = v.Verify(ctx, tok)
	require.Error(t, err)
	require.Contains(t, err.Error(), "sub")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestVerifier_MissingSubClaim -v`
Expected: PASS (the 2B.4 impl already enforces non-empty subject).

- [ ] **Step 3: Confirm**

No new code needed.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/auth/... -run TestVerifier_MissingSubClaim -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "test(auth): cover missing-sub-claim rejection"
```

### Task 2B.10: auth — chi middleware happy path

**Files:**
- Create: `internal/auth/middleware.go`
- Test: `internal/auth/middleware_test.go`

- [ ] **Step 1: Write the failing test**

```go
package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth"
	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
)

func TestMiddleware_ValidTokenInjectsClaims(t *testing.T) {
	srv := testjwks.NewServer(t)
	ctx := context.Background()

	v, err := auth.NewVerifier(ctx, auth.Config{
		Issuer:   srv.Issuer(),
		Audience: "tatara-memory",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(auth.Middleware(v))
	r.Get("/me", func(w http.ResponseWriter, req *http.Request) {
		c, ok := auth.ClaimsFromContext(req.Context())
		require.True(t, ok)
		_, _ = w.Write([]byte(c.Subject))
	})

	tok := srv.SignToken(t, testjwks.Claims{
		Issuer:   srv.Issuer(),
		Audience: []string{"tatara-memory"},
		Subject:  "user-1",
	})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "user-1", rec.Body.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestMiddleware_ValidTokenInjectsClaims -v`
Expected: FAIL with `undefined: auth.Middleware`

- [ ] **Step 3: Write minimal implementation**

```go
package auth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey struct{}

func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(*Claims)
	return c, ok
}

func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			claims, err := v.Verify(r.Context(), raw)
			if err != nil {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), ctxKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/auth/... -run TestMiddleware_ValidTokenInjectsClaims -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "feat(auth): add chi middleware injecting claims into context"
```

### Task 2B.11: auth — middleware rejects missing and invalid tokens

**Files:**
- Edit: `internal/auth/middleware_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMiddleware_MissingTokenReturns401(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(auth.Middleware(v))
	r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestMiddleware_InvalidTokenReturns401(t *testing.T) {
	srv := testjwks.NewServer(t)
	v, err := auth.NewVerifier(context.Background(), auth.Config{
		Issuer: srv.Issuer(), Audience: "tatara-memory",
	})
	require.NoError(t, err)

	r := chi.NewRouter()
	r.Use(auth.Middleware(v))
	r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/auth/... -run TestMiddleware -v`
Expected: PASS (impl from 2B.10 already covers both branches).

- [ ] **Step 3: Confirm**

No new code needed.

- [ ] **Step 4: Re-run full suite**

Run: `go test ./internal/auth/... -v -count=1`
Expected: all middleware and verifier tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/
git commit -m "test(auth): cover middleware 401 branches"
```

### Task 2B.12: auth — lint and final sweep

- [ ] **Step 1: Run full package**

Run: `go test ./internal/auth/... -v -count=1`
Expected: all PASS.

- [ ] **Step 2: Lint**

Run: `golangci-lint run ./internal/auth/...`
Expected: no findings.

- [ ] **Step 3: Commit (only if changes)**

```bash
git status
```

---

## Wave 2C — internal/lightrag

Subagent: sonnet. Worktree: `wt/wave2c-lightrag` off `main`. Independent of 2A and 2B. Wave 2 verification gate is `go test ./internal/...` green in this worktree. The HTTP client must instrument every call with `slog` plus the prometheus metrics named in the parent spec:

- `lightrag_calls_total{op,result}` (counter, `result` in `success|error`)
- `lightrag_call_duration_seconds{op}` (histogram)

The fake client lives in a subpackage so production code never imports it transitively.

### Task 2C.1: lightrag — domain types and Client interface

**Files:**
- Create: `internal/lightrag/types.go`
- Create: `internal/lightrag/client.go`
- Test: `internal/lightrag/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package lightrag_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func TestQueryMode_Valid(t *testing.T) {
	require.True(t, lightrag.QueryModeHybrid.Valid())
	require.True(t, lightrag.QueryModeLocal.Valid())
	require.True(t, lightrag.QueryModeGlobal.Valid())
	require.True(t, lightrag.QueryModeNaive.Valid())
	require.False(t, lightrag.QueryMode("bogus").Valid())
}

func TestClient_InterfaceMethods(t *testing.T) {
	// Compile-time check: any implementation must satisfy this method set.
	var _ lightrag.Client = (*stubClient)(nil)
}

type stubClient struct{ lightrag.Client }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestQueryMode_Valid -v`
Expected: FAIL with `undefined: lightrag.QueryModeHybrid`

- [ ] **Step 3: Write minimal implementation**

`internal/lightrag/types.go`:

```go
package lightrag

import "time"

type QueryMode string

const (
	QueryModeHybrid QueryMode = "hybrid"
	QueryModeLocal  QueryMode = "local"
	QueryModeGlobal QueryMode = "global"
	QueryModeNaive  QueryMode = "naive"
)

func (m QueryMode) Valid() bool {
	switch m {
	case QueryModeHybrid, QueryModeLocal, QueryModeGlobal, QueryModeNaive:
		return true
	}
	return false
}

type Document struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type InsertRequest struct {
	Documents []Document `json:"documents"`
}

type InsertResponse struct {
	IDs []string `json:"ids"`
}

type QueryRequest struct {
	Query string    `json:"query"`
	Mode  QueryMode `json:"mode"`
	TopK  int       `json:"top_k,omitempty"`
}

type Match struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

type QueryResponse struct {
	Matches []Match `json:"matches"`
}

type DescribeResponse struct {
	Response string   `json:"response"`
	Sources  []string `json:"sources"`
}

type Entity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

type EntityUpdate struct {
	Name        *string           `json:"name,omitempty"`
	Type        *string           `json:"type,omitempty"`
	Description *string           `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

type Edge struct {
	ID         string            `json:"id"`
	FromEntity string            `json:"from_entity"`
	ToEntity   string            `json:"to_entity"`
	Relation   string            `json:"relation"`
	Properties map[string]string `json:"properties,omitempty"`
}
```

`internal/lightrag/client.go`:

```go
package lightrag

import "context"

type Client interface {
	InsertDocument(ctx context.Context, req InsertRequest) (*InsertResponse, error)
	GetDocument(ctx context.Context, id string) (*Document, error)
	DeleteDocument(ctx context.Context, id string) error

	Query(ctx context.Context, req QueryRequest) (*QueryResponse, error)
	QueryDescribe(ctx context.Context, req QueryRequest) (*DescribeResponse, error)

	ListEntities(ctx context.Context, q string) ([]Entity, error)
	GetEntity(ctx context.Context, id string) (*Entity, error)
	UpdateEntity(ctx context.Context, id string, upd EntityUpdate) (*Entity, error)

	ListEdges(ctx context.Context) ([]Edge, error)
	CreateEdge(ctx context.Context, e Edge) (*Edge, error)
	DeleteEdge(ctx context.Context, id string) error

	Health(ctx context.Context) error
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestQueryMode_Valid -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): add domain types and Client interface"
```

### Task 2C.2: lightrag — HTTPClient skeleton and metrics

**Files:**
- Create: `internal/lightrag/http.go`
- Create: `internal/lightrag/metrics.go`
- Test: `internal/lightrag/metrics_test.go`

- [ ] **Step 1: Write the failing test**

```go
package lightrag_test

import (
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func TestNewHTTPClient_RegistersMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:    "http://lightrag.local",
		HTTPClient: http.DefaultClient,
		Registry:   reg,
	})
	require.NoError(t, err)
	require.NotNil(t, c)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	require.True(t, names["lightrag_calls_total"])
	require.True(t, names["lightrag_call_duration_seconds"])
}

func TestNewHTTPClient_RequiresBaseURL(t *testing.T) {
	_, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestNewHTTPClient -v`
Expected: FAIL with `undefined: lightrag.NewHTTPClient`

- [ ] **Step 3: Write minimal implementation**

`internal/lightrag/metrics.go`:

```go
package lightrag

import "github.com/prometheus/client_golang/prometheus"

type metrics struct {
	calls    *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		calls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "lightrag_calls_total",
			Help: "Count of LightRAG client calls by op and result.",
		}, []string{"op", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "lightrag_call_duration_seconds",
			Help:    "Duration of LightRAG client calls by op.",
			Buckets: prometheus.DefBuckets,
		}, []string{"op"}),
	}
	if reg != nil {
		reg.MustRegister(m.calls, m.duration)
	}
	return m
}

func (m *metrics) observe(op string, dur float64, err error) {
	result := "success"
	if err != nil {
		result = "error"
	}
	m.calls.WithLabelValues(op, result).Inc()
	m.duration.WithLabelValues(op).Observe(dur)
}
```

`internal/lightrag/http.go`:

```go
package lightrag

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type HTTPConfig struct {
	BaseURL    string
	HTTPClient *http.Client
	Logger     *slog.Logger
	Registry   prometheus.Registerer
}

type HTTPClient struct {
	base    string
	http    *http.Client
	log     *slog.Logger
	metrics *metrics
}

func NewHTTPClient(cfg HTTPConfig) (*HTTPClient, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("lightrag: BaseURL is required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &HTTPClient{
		base:    cfg.BaseURL,
		http:    cfg.HTTPClient,
		log:     cfg.Logger,
		metrics: newMetrics(cfg.Registry),
	}, nil
}

// do is the shared instrumented round-trip. Endpoint methods call it.
func (c *HTTPClient) do(ctx context.Context, op, method, path string, body io.Reader, out any) error {
	start := time.Now()
	err := c.roundTrip(ctx, method, path, body, out)
	dur := time.Since(start).Seconds()
	c.metrics.observe(op, dur, err)
	c.log.LogAttrs(ctx, levelFor(err), "lightrag_call",
		slog.String("op", op),
		slog.String("method", method),
		slog.String("path", path),
		slog.Float64("duration_s", dur),
		slog.Any("error", err),
	)
	return err
}

func levelFor(err error) slog.Level {
	if err != nil {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}
```

Stub `roundTrip` so the package compiles. It is fleshed out in 2C.3.

Append to `internal/lightrag/http.go`:

```go
func (c *HTTPClient) roundTrip(_ context.Context, _, _ string, _ io.Reader, _ any) error {
	return errors.New("lightrag: roundTrip not implemented")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestNewHTTPClient -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): add HTTPClient skeleton with metrics and logging"
```

### Task 2C.3: lightrag — real roundTrip via httptest

**Files:**
- Edit: `internal/lightrag/http.go`
- Test: `internal/lightrag/http_test.go`

- [ ] **Step 1: Write the failing test**

```go
package lightrag_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*lightrag.HTTPClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{BaseURL: srv.URL})
	require.NoError(t, err)
	return c, srv
}

func TestHTTPClient_InsertDocument(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/documents", r.URL.Path)
		var in lightrag.InsertRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Len(t, in.Documents, 1)
		require.Equal(t, "hello world", in.Documents[0].Content)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lightrag.InsertResponse{IDs: []string{"doc-1"}})
	})

	resp, err := c.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "hello world"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"doc-1"}, resp.IDs)
}

func TestHTTPClient_DeleteDocument(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/documents/doc-1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})

	require.NoError(t, c.DeleteDocument(context.Background(), "doc-1"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_InsertDocument -v`
Expected: FAIL with `undefined: c.InsertDocument` (and roundTrip stub error).

- [ ] **Step 3: Write minimal implementation**

Replace `roundTrip` and add the two endpoint methods.

```go
import (
	"bytes"
	"encoding/json"
	"fmt"
)

func (c *HTTPClient) roundTrip(ctx context.Context, method, path string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("lightrag: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("lightrag: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return &HTTPError{Status: resp.StatusCode, Body: string(buf), Path: path}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("lightrag: decode response: %w", err)
	}
	return nil
}

type HTTPError struct {
	Status int
	Body   string
	Path   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("lightrag: %s -> %d: %s", e.Path, e.Status, e.Body)
}

func (c *HTTPClient) InsertDocument(ctx context.Context, req InsertRequest) (*InsertResponse, error) {
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out InsertResponse
	if err := c.do(ctx, "insert_document", http.MethodPost, "/documents", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) DeleteDocument(ctx context.Context, id string) error {
	return c.do(ctx, "delete_document", http.MethodDelete, "/documents/"+id, nil, nil)
}

func encodeJSON(v any) (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(v); err != nil {
		return nil, fmt.Errorf("lightrag: encode body: %w", err)
	}
	return buf, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestHTTPClient -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): implement insert/delete document over HTTP"
```

### Task 2C.4: lightrag — GetDocument and HTTPError surfacing

**Files:**
- Edit: `internal/lightrag/http.go`
- Edit: `internal/lightrag/http_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestHTTPClient_GetDocument(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/documents/doc-1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.Document{ID: "doc-1", Content: "hi"})
	})

	doc, err := c.GetDocument(context.Background(), "doc-1")
	require.NoError(t, err)
	require.Equal(t, "doc-1", doc.ID)
	require.Equal(t, "hi", doc.Content)
}

func TestHTTPClient_GetDocument_404(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not found"}`))
	})

	_, err := c.GetDocument(context.Background(), "missing")
	require.Error(t, err)
	var he *lightrag.HTTPError
	require.ErrorAs(t, err, &he)
	require.Equal(t, 404, he.Status)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_GetDocument -v`
Expected: FAIL with `undefined: c.GetDocument`

- [ ] **Step 3: Write minimal implementation**

```go
func (c *HTTPClient) GetDocument(ctx context.Context, id string) (*Document, error) {
	var out Document
	if err := c.do(ctx, "get_document", http.MethodGet, "/documents/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_GetDocument -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): implement get document with HTTPError on 4xx"
```

### Task 2C.5: lightrag — Query and QueryDescribe

**Files:**
- Edit: `internal/lightrag/http.go`
- Edit: `internal/lightrag/http_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestHTTPClient_Query(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/query", r.URL.Path)
		var in lightrag.QueryRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, lightrag.QueryModeHybrid, in.Mode)
		require.Equal(t, "what is X", in.Query)

		_ = json.NewEncoder(w).Encode(lightrag.QueryResponse{
			Matches: []lightrag.Match{{ID: "m1", Score: 0.9, Text: "X is Y"}},
		})
	})

	resp, err := c.Query(context.Background(), lightrag.QueryRequest{
		Query: "what is X", Mode: lightrag.QueryModeHybrid,
	})
	require.NoError(t, err)
	require.Len(t, resp.Matches, 1)
	require.Equal(t, "m1", resp.Matches[0].ID)
}

func TestHTTPClient_QueryDescribe(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/query/describe", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.DescribeResponse{
			Response: "X is Y because Z",
			Sources:  []string{"doc-1", "doc-2"},
		})
	})

	resp, err := c.QueryDescribe(context.Background(), lightrag.QueryRequest{
		Query: "explain X", Mode: lightrag.QueryModeGlobal,
	})
	require.NoError(t, err)
	require.Equal(t, "X is Y because Z", resp.Response)
	require.Len(t, resp.Sources, 2)
}

func TestHTTPClient_Query_RejectsInvalidMode(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("server should not be called")
	})
	_, err := c.Query(context.Background(), lightrag.QueryRequest{Query: "x", Mode: "bogus"})
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_Query -v`
Expected: FAIL with `undefined: c.Query`

- [ ] **Step 3: Write minimal implementation**

```go
func (c *HTTPClient) Query(ctx context.Context, req QueryRequest) (*QueryResponse, error) {
	if !req.Mode.Valid() {
		return nil, fmt.Errorf("lightrag: invalid query mode %q", req.Mode)
	}
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out QueryResponse
	if err := c.do(ctx, "query", http.MethodPost, "/query", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) QueryDescribe(ctx context.Context, req QueryRequest) (*DescribeResponse, error) {
	if !req.Mode.Valid() {
		return nil, fmt.Errorf("lightrag: invalid query mode %q", req.Mode)
	}
	body, err := encodeJSON(req)
	if err != nil {
		return nil, err
	}
	var out DescribeResponse
	if err := c.do(ctx, "query_describe", http.MethodPost, "/query/describe", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_Query -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): implement query and query/describe endpoints"
```

### Task 2C.6: lightrag — entity endpoints

**Files:**
- Edit: `internal/lightrag/http.go`
- Edit: `internal/lightrag/http_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestHTTPClient_ListEntities(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/entities", r.URL.Path)
		require.Equal(t, "foo", r.URL.Query().Get("q"))
		_ = json.NewEncoder(w).Encode([]lightrag.Entity{
			{ID: "e1", Name: "foo", Type: "concept"},
		})
	})
	ents, err := c.ListEntities(context.Background(), "foo")
	require.NoError(t, err)
	require.Len(t, ents, 1)
	require.Equal(t, "foo", ents[0].Name)
}

func TestHTTPClient_GetEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/entities/e1", r.URL.Path)
		_ = json.NewEncoder(w).Encode(lightrag.Entity{ID: "e1", Name: "foo"})
	})
	e, err := c.GetEntity(context.Background(), "e1")
	require.NoError(t, err)
	require.Equal(t, "e1", e.ID)
}

func TestHTTPClient_UpdateEntity(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/entities/e1", r.URL.Path)
		var upd lightrag.EntityUpdate
		require.NoError(t, json.NewDecoder(r.Body).Decode(&upd))
		require.NotNil(t, upd.Name)
		require.Equal(t, "renamed", *upd.Name)
		_ = json.NewEncoder(w).Encode(lightrag.Entity{ID: "e1", Name: "renamed"})
	})

	name := "renamed"
	e, err := c.UpdateEntity(context.Background(), "e1", lightrag.EntityUpdate{Name: &name})
	require.NoError(t, err)
	require.Equal(t, "renamed", e.Name)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_(ListEntities|GetEntity|UpdateEntity) -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
import "net/url"

func (c *HTTPClient) ListEntities(ctx context.Context, q string) ([]Entity, error) {
	path := "/entities"
	if q != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	var out []Entity
	if err := c.do(ctx, "list_entities", http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) GetEntity(ctx context.Context, id string) (*Entity, error) {
	var out Entity
	if err := c.do(ctx, "get_entity", http.MethodGet, "/entities/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) UpdateEntity(ctx context.Context, id string, upd EntityUpdate) (*Entity, error) {
	body, err := encodeJSON(upd)
	if err != nil {
		return nil, err
	}
	var out Entity
	if err := c.do(ctx, "update_entity", http.MethodPatch, "/entities/"+id, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_(ListEntities|GetEntity|UpdateEntity) -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): implement list/get/update entity endpoints"
```

### Task 2C.7: lightrag — edge endpoints and health

**Files:**
- Edit: `internal/lightrag/http.go`
- Edit: `internal/lightrag/http_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestHTTPClient_ListEdges(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/edges", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]lightrag.Edge{
			{ID: "edge-1", FromEntity: "e1", ToEntity: "e2", Relation: "knows"},
		})
	})
	edges, err := c.ListEdges(context.Background())
	require.NoError(t, err)
	require.Len(t, edges, 1)
}

func TestHTTPClient_CreateEdge(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/edges", r.URL.Path)
		var in lightrag.Edge
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		require.Equal(t, "knows", in.Relation)
		in.ID = "edge-1"
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(in)
	})

	e, err := c.CreateEdge(context.Background(), lightrag.Edge{
		FromEntity: "e1", ToEntity: "e2", Relation: "knows",
	})
	require.NoError(t, err)
	require.Equal(t, "edge-1", e.ID)
}

func TestHTTPClient_DeleteEdge(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/edges/edge-1", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	require.NoError(t, c.DeleteEdge(context.Background(), "edge-1"))
}

func TestHTTPClient_Health(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, c.Health(context.Background()))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_(ListEdges|CreateEdge|DeleteEdge|Health) -v`
Expected: FAIL

- [ ] **Step 3: Write minimal implementation**

```go
func (c *HTTPClient) ListEdges(ctx context.Context) ([]Edge, error) {
	var out []Edge
	if err := c.do(ctx, "list_edges", http.MethodGet, "/edges", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *HTTPClient) CreateEdge(ctx context.Context, e Edge) (*Edge, error) {
	body, err := encodeJSON(e)
	if err != nil {
		return nil, err
	}
	var out Edge
	if err := c.do(ctx, "create_edge", http.MethodPost, "/edges", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) DeleteEdge(ctx context.Context, id string) error {
	return c.do(ctx, "delete_edge", http.MethodDelete, "/edges/"+id, nil, nil)
}

func (c *HTTPClient) Health(ctx context.Context) error {
	return c.do(ctx, "health", http.MethodGet, "/health", nil, nil)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_(ListEdges|CreateEdge|DeleteEdge|Health) -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag): implement edge endpoints and health probe"
```

### Task 2C.8: lightrag — confirm metrics increment under real call

**Files:**
- Edit: `internal/lightrag/metrics_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestHTTPClient_MetricsIncrement(t *testing.T) {
	reg := prometheus.NewRegistry()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := lightrag.NewHTTPClient(lightrag.HTTPConfig{
		BaseURL:  srv.URL,
		Registry: reg,
	})
	require.NoError(t, err)

	_, _ = c.Query(context.Background(), lightrag.QueryRequest{
		Query: "x", Mode: lightrag.QueryModeHybrid,
	})

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var calls, dur float64
	for _, mf := range mfs {
		switch mf.GetName() {
		case "lightrag_calls_total":
			for _, m := range mf.Metric {
				calls += m.GetCounter().GetValue()
			}
		case "lightrag_call_duration_seconds":
			for _, m := range mf.Metric {
				dur += float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	require.InDelta(t, 1, calls, 0.0001)
	require.InDelta(t, 1, dur, 0.0001)
}
```

Add `httptest` and `http` to imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_MetricsIncrement -v`
Expected: PASS (instrumentation already in place). If it fails, the `do` wrapper is not registering the error path.

- [ ] **Step 3: Confirm**

No new code needed.

- [ ] **Step 4: Re-run**

Run: `go test ./internal/lightrag/... -run TestHTTPClient_MetricsIncrement -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "test(lightrag): cover metrics increment on error response"
```

### Task 2C.9: lightrag/fake — package skeleton and InsertDocument

**Files:**
- Create: `internal/lightrag/fake/fake.go`
- Test: `internal/lightrag/fake/fake_test.go`

- [ ] **Step 1: Write the failing test**

```go
package fake_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
)

func TestFake_ImplementsClient(t *testing.T) {
	var _ lightrag.Client = fake.New()
}

func TestFake_InsertAndGetDocument(t *testing.T) {
	f := fake.New()
	resp, err := f.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "hello"}},
	})
	require.NoError(t, err)
	require.Len(t, resp.IDs, 1)

	doc, err := f.GetDocument(context.Background(), resp.IDs[0])
	require.NoError(t, err)
	require.Equal(t, "hello", doc.Content)
}

func TestFake_DeleteDocument(t *testing.T) {
	f := fake.New()
	resp, _ := f.InsertDocument(context.Background(), lightrag.InsertRequest{
		Documents: []lightrag.Document{{Content: "x"}},
	})
	require.NoError(t, f.DeleteDocument(context.Background(), resp.IDs[0]))
	_, err := f.GetDocument(context.Background(), resp.IDs[0])
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/fake/... -run TestFake -v`
Expected: FAIL with `undefined: fake.New`

- [ ] **Step 3: Write minimal implementation**

```go
package fake

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

type Client struct {
	mu       sync.Mutex
	docs     map[string]lightrag.Document
	entities map[string]lightrag.Entity
	edges    map[string]lightrag.Edge
	nextID   int
}

func New() *Client {
	return &Client{
		docs:     map[string]lightrag.Document{},
		entities: map[string]lightrag.Entity{},
		edges:    map[string]lightrag.Edge{},
	}
}

func (c *Client) nextStr(prefix string) string {
	c.nextID++
	return prefix + "-" + strconv.Itoa(c.nextID)
}

func (c *Client) InsertDocument(_ context.Context, req lightrag.InsertRequest) (*lightrag.InsertResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(req.Documents))
	for _, d := range req.Documents {
		if d.ID == "" {
			d.ID = c.nextStr("doc")
		}
		c.docs[d.ID] = d
		ids = append(ids, d.ID)
	}
	return &lightrag.InsertResponse{IDs: ids}, nil
}

func (c *Client) GetDocument(_ context.Context, id string) (*lightrag.Document, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.docs[id]
	if !ok {
		return nil, fmt.Errorf("fake: document %q not found", id)
	}
	return &d, nil
}

func (c *Client) DeleteDocument(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.docs[id]; !ok {
		return fmt.Errorf("fake: document %q not found", id)
	}
	delete(c.docs, id)
	return nil
}
```

Add stub methods to satisfy the interface (return empty values / not-implemented errors); they will be filled in 2C.10.

```go
func (c *Client) Query(context.Context, lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	return &lightrag.QueryResponse{}, nil
}
func (c *Client) QueryDescribe(context.Context, lightrag.QueryRequest) (*lightrag.DescribeResponse, error) {
	return &lightrag.DescribeResponse{}, nil
}
func (c *Client) ListEntities(context.Context, string) ([]lightrag.Entity, error) { return nil, nil }
func (c *Client) GetEntity(context.Context, string) (*lightrag.Entity, error) {
	return nil, fmt.Errorf("fake: not implemented")
}
func (c *Client) UpdateEntity(context.Context, string, lightrag.EntityUpdate) (*lightrag.Entity, error) {
	return nil, fmt.Errorf("fake: not implemented")
}
func (c *Client) ListEdges(context.Context) ([]lightrag.Edge, error) { return nil, nil }
func (c *Client) CreateEdge(context.Context, lightrag.Edge) (*lightrag.Edge, error) {
	return nil, fmt.Errorf("fake: not implemented")
}
func (c *Client) DeleteEdge(context.Context, string) error { return fmt.Errorf("fake: not implemented") }
func (c *Client) Health(context.Context) error             { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/fake/... -run TestFake -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag/fake): add in-memory client with document round-trip"
```

### Task 2C.10: lightrag/fake — entity, edge, and query round-trips

**Files:**
- Edit: `internal/lightrag/fake/fake.go`
- Edit: `internal/lightrag/fake/fake_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestFake_EntityRoundTrip(t *testing.T) {
	f := fake.New()
	f.SeedEntity(lightrag.Entity{ID: "e1", Name: "foo", Type: "concept"})

	got, err := f.GetEntity(context.Background(), "e1")
	require.NoError(t, err)
	require.Equal(t, "foo", got.Name)

	rename := "renamed"
	upd, err := f.UpdateEntity(context.Background(), "e1", lightrag.EntityUpdate{Name: &rename})
	require.NoError(t, err)
	require.Equal(t, "renamed", upd.Name)

	list, err := f.ListEntities(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestFake_EdgeRoundTrip(t *testing.T) {
	f := fake.New()
	e, err := f.CreateEdge(context.Background(), lightrag.Edge{
		FromEntity: "e1", ToEntity: "e2", Relation: "knows",
	})
	require.NoError(t, err)
	require.NotEmpty(t, e.ID)

	list, err := f.ListEdges(context.Background())
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, f.DeleteEdge(context.Background(), e.ID))
	list, _ = f.ListEdges(context.Background())
	require.Empty(t, list)
}

func TestFake_Query_ReturnsSeededMatches(t *testing.T) {
	f := fake.New()
	f.SeedMatches([]lightrag.Match{{ID: "m1", Score: 0.5, Text: "hi"}})

	resp, err := f.Query(context.Background(), lightrag.QueryRequest{
		Query: "x", Mode: lightrag.QueryModeHybrid,
	})
	require.NoError(t, err)
	require.Len(t, resp.Matches, 1)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/lightrag/fake/... -run TestFake_(EntityRoundTrip|EdgeRoundTrip|Query_ReturnsSeededMatches) -v`
Expected: FAIL (`fake: not implemented` and missing seeders).

- [ ] **Step 3: Write minimal implementation**

Replace the stub methods and add seeders.

```go
func (c *Client) SeedEntity(e lightrag.Entity) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entities[e.ID] = e
}

func (c *Client) SeedMatches(m []lightrag.Match) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.matches = m
}

func (c *Client) Query(_ context.Context, _ lightrag.QueryRequest) (*lightrag.QueryResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &lightrag.QueryResponse{Matches: append([]lightrag.Match(nil), c.matches...)}, nil
}

func (c *Client) QueryDescribe(_ context.Context, _ lightrag.QueryRequest) (*lightrag.DescribeResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return &lightrag.DescribeResponse{Response: c.describeResponse, Sources: append([]string(nil), c.describeSources...)}, nil
}

func (c *Client) ListEntities(_ context.Context, q string) ([]lightrag.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]lightrag.Entity, 0, len(c.entities))
	for _, e := range c.entities {
		if q == "" || containsFold(e.Name, q) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (c *Client) GetEntity(_ context.Context, id string) (*lightrag.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entities[id]
	if !ok {
		return nil, fmt.Errorf("fake: entity %q not found", id)
	}
	return &e, nil
}

func (c *Client) UpdateEntity(_ context.Context, id string, upd lightrag.EntityUpdate) (*lightrag.Entity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entities[id]
	if !ok {
		return nil, fmt.Errorf("fake: entity %q not found", id)
	}
	if upd.Name != nil {
		e.Name = *upd.Name
	}
	if upd.Type != nil {
		e.Type = *upd.Type
	}
	if upd.Description != nil {
		e.Description = *upd.Description
	}
	if upd.Properties != nil {
		e.Properties = upd.Properties
	}
	c.entities[id] = e
	return &e, nil
}

func (c *Client) ListEdges(_ context.Context) ([]lightrag.Edge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]lightrag.Edge, 0, len(c.edges))
	for _, e := range c.edges {
		out = append(out, e)
	}
	return out, nil
}

func (c *Client) CreateEdge(_ context.Context, e lightrag.Edge) (*lightrag.Edge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.ID == "" {
		e.ID = c.nextStr("edge")
	}
	c.edges[e.ID] = e
	return &e, nil
}

func (c *Client) DeleteEdge(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.edges[id]; !ok {
		return fmt.Errorf("fake: edge %q not found", id)
	}
	delete(c.edges, id)
	return nil
}
```

Update the struct and add the helper:

```go
import "strings"

type Client struct {
	mu               sync.Mutex
	docs             map[string]lightrag.Document
	entities         map[string]lightrag.Entity
	edges            map[string]lightrag.Edge
	matches          []lightrag.Match
	describeResponse string
	describeSources  []string
	nextID           int
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/lightrag/fake/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "feat(lightrag/fake): implement entity, edge, and query round-trips"
```

### Task 2C.11: lightrag — final sweep, lint, and interface conformance

**Files:**
- Edit: `internal/lightrag/client_test.go` (compile-time interface check on HTTPClient)

- [ ] **Step 1: Add the compile-time check**

```go
func TestHTTPClient_ImplementsClient(t *testing.T) {
	var _ lightrag.Client = (*lightrag.HTTPClient)(nil)
}
```

- [ ] **Step 2: Run full suite**

Run: `go test ./internal/lightrag/... -v -count=1`
Expected: all PASS.

- [ ] **Step 3: Lint**

Run: `golangci-lint run ./internal/lightrag/...`
Expected: no findings.

- [ ] **Step 4: Confirm wave 2 gate**

Run: `go test ./internal/... -count=1` (worktree only contains lightrag; this is fine)
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/lightrag/
git commit -m "test(lightrag): compile-time HTTPClient Client conformance"
```
## Wave 3A — internal/memory + internal/ingest

Runs in worktree `wt-wave3a` off `main`. Depends on Wave 2C's `internal/lightrag` package (Client interface + fake) being merged. All commits local.

### Task 3A.1: memory — domain types

**Files:**
- Create: `internal/memory/types.go`
- Test: `internal/memory/types_test.go`

- [ ] **Step 1: Write the failing test**

```go
package memory_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestMemoryZeroValue(t *testing.T) {
	var m memory.Memory
	require.Empty(t, m.ID)
	require.Empty(t, m.Text)
}

func TestQueryModeValid(t *testing.T) {
	cases := []struct {
		mode memory.QueryMode
		ok   bool
	}{
		{memory.QueryModeHybrid, true},
		{memory.QueryModeLocal, true},
		{memory.QueryModeGlobal, true},
		{memory.QueryModeNaive, true},
		{memory.QueryMode("bogus"), false},
	}
	for _, c := range cases {
		require.Equal(t, c.ok, c.mode.Valid(), "mode=%s", c.mode)
	}
}

func TestJobStatusTerminal(t *testing.T) {
	require.True(t, memory.JobStatusSucceeded.Terminal())
	require.True(t, memory.JobStatusFailed.Terminal())
	require.True(t, memory.JobStatusPartial.Terminal())
	require.False(t, memory.JobStatusQueued.Terminal())
	require.False(t, memory.JobStatusRunning.Terminal())
}

func TestIngestJobNow(t *testing.T) {
	j := memory.IngestJob{CreatedAt: time.Now()}
	require.False(t, j.CreatedAt.IsZero())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/... -run 'TestMemoryZeroValue|TestQueryModeValid|TestJobStatusTerminal|TestIngestJobNow' -v`
Expected: FAIL with "no Go files" or "undefined: memory.Memory".

- [ ] **Step 3: Write minimal implementation**

```go
package memory

import "time"

type Memory struct {
	ID         string                 `json:"id"`
	Text       string                 `json:"text"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

type Entity struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Type        string                 `json:"type"`
	Description string                 `json:"description,omitempty"`
	Properties  map[string]interface{} `json:"properties,omitempty"`
}

type Edge struct {
	ID         string                 `json:"id"`
	From       string                 `json:"from_entity"`
	To         string                 `json:"to_entity"`
	Relation   string                 `json:"relation"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

type QueryMode string

const (
	QueryModeHybrid QueryMode = "hybrid"
	QueryModeLocal  QueryMode = "local"
	QueryModeGlobal QueryMode = "global"
	QueryModeNaive  QueryMode = "naive"
)

func (m QueryMode) Valid() bool {
	switch m {
	case QueryModeHybrid, QueryModeLocal, QueryModeGlobal, QueryModeNaive:
		return true
	}
	return false
}

type Query struct {
	Mode QueryMode `json:"mode"`
	Text string    `json:"text"`
	TopK int       `json:"top_k,omitempty"`
}

type QueryMatch struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Text  string  `json:"text"`
}

type QueryResult struct {
	Matches []QueryMatch `json:"matches"`
}

type DescribeResult struct {
	Response string   `json:"response"`
	Sources  []string `json:"sources,omitempty"`
}

type JobStatus string

const (
	JobStatusQueued    JobStatus = "queued"
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusPartial   JobStatus = "partial"
)

func (s JobStatus) Terminal() bool {
	switch s {
	case JobStatusSucceeded, JobStatusFailed, JobStatusPartial:
		return true
	}
	return false
}

type IngestItemError struct {
	IdempotencyKey string `json:"idempotency_key"`
	Error          string `json:"error"`
}

type IngestJob struct {
	ID        string            `json:"id"`
	Status    JobStatus         `json:"status"`
	Total     int               `json:"total"`
	Done      int               `json:"done"`
	Failed    int               `json:"failed"`
	Errors    []IngestItemError `json:"errors,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type IngestItem struct {
	IdempotencyKey string                 `json:"idempotency_key"`
	Text           string                 `json:"text"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat(memory): domain types for Memory, Entity, Edge, Query, IngestJob"
```

### Task 3A.2: memory — lightrag translation helpers

**Files:**
- Create: `internal/memory/translate.go`
- Test: `internal/memory/translate_test.go`

- [ ] **Step 1: Write the failing test**

```go
package memory_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestToLightragInsert(t *testing.T) {
	m := memory.Memory{ID: "m1", Text: "hello", Metadata: map[string]interface{}{"src": "a"}}
	req := memory.ToLightragInsert(m)
	require.Equal(t, "m1", req.DocID)
	require.Equal(t, "hello", req.Content)
	require.Equal(t, "a", req.Metadata["src"])
}

func TestFromLightragQuery(t *testing.T) {
	resp := lightrag.QueryResponse{
		Matches: []lightrag.QueryMatch{
			{DocID: "m1", Score: 0.9, Text: "hi"},
			{DocID: "m2", Score: 0.5, Text: "ho"},
		},
	}
	got := memory.FromLightragQuery(resp)
	require.Len(t, got.Matches, 2)
	require.Equal(t, "m1", got.Matches[0].ID)
	require.InDelta(t, 0.9, got.Matches[0].Score, 1e-6)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/... -run 'TestToLightragInsert|TestFromLightragQuery' -v`
Expected: FAIL with "undefined: memory.ToLightragInsert".

- [ ] **Step 3: Write minimal implementation**

```go
package memory

import "github.com/szymonrychu/tatara-memory/internal/lightrag"

func ToLightragInsert(m Memory) lightrag.InsertRequest {
	return lightrag.InsertRequest{
		DocID:    m.ID,
		Content:  m.Text,
		Metadata: m.Metadata,
	}
}

func FromLightragQuery(r lightrag.QueryResponse) QueryResult {
	out := QueryResult{Matches: make([]QueryMatch, 0, len(r.Matches))}
	for _, m := range r.Matches {
		out.Matches = append(out.Matches, QueryMatch{
			ID:    m.DocID,
			Score: m.Score,
			Text:  m.Text,
		})
	}
	return out
}

func FromLightragEntity(e lightrag.Entity) Entity {
	return Entity{
		ID:          e.ID,
		Name:        e.Name,
		Type:        e.Type,
		Description: e.Description,
		Properties:  e.Properties,
	}
}

func ToLightragEntityPatch(e Entity) lightrag.EntityPatch {
	return lightrag.EntityPatch{
		Name:        e.Name,
		Type:        e.Type,
		Description: e.Description,
		Properties:  e.Properties,
	}
}

func FromLightragEdge(e lightrag.Edge) Edge {
	return Edge{
		ID:         e.ID,
		From:       e.From,
		To:         e.To,
		Relation:   e.Relation,
		Properties: e.Properties,
	}
}

func ToLightragEdgeCreate(e Edge) lightrag.EdgeCreate {
	return lightrag.EdgeCreate{
		From:       e.From,
		To:         e.To,
		Relation:   e.Relation,
		Properties: e.Properties,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat(memory): translation between domain and lightrag wire types"
```

### Task 3A.3: memory — Service create + get + delete

**Files:**
- Create: `internal/memory/service.go`
- Test: `internal/memory/service_test.go`

- [ ] **Step 1: Write the failing test**

```go
package memory_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestServiceCreateGetDelete(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New())

	m, err := svc.CreateMemory(ctx, memory.Memory{Text: "hello"})
	require.NoError(t, err)
	require.NotEmpty(t, m.ID)

	got, err := svc.GetMemory(ctx, m.ID)
	require.NoError(t, err)
	require.Equal(t, "hello", got.Text)

	require.NoError(t, svc.DeleteMemory(ctx, m.ID))

	_, err = svc.GetMemory(ctx, m.ID)
	require.ErrorIs(t, err, memory.ErrNotFound)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/... -run TestServiceCreateGetDelete -v`
Expected: FAIL with "undefined: memory.NewService".

- [ ] **Step 3: Write minimal implementation**

```go
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

var (
	ErrNotFound    = errors.New("memory: not found")
	ErrUpstream    = errors.New("memory: upstream error")
	ErrTransient   = errors.New("memory: transient upstream error")
)

type Service struct {
	lr  lightrag.Client
	now func() time.Time
}

func NewService(lr lightrag.Client) *Service {
	return &Service{lr: lr, now: time.Now}
}

func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func wrapUpstream(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, lightrag.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, lightrag.ErrTransient) {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}

func (s *Service) CreateMemory(ctx context.Context, m Memory) (Memory, error) {
	if m.ID == "" {
		m.ID = newID("mem")
	}
	m.CreatedAt = s.now()
	if err := s.lr.Insert(ctx, ToLightragInsert(m)); err != nil {
		return Memory{}, wrapUpstream(err)
	}
	return m, nil
}

func (s *Service) GetMemory(ctx context.Context, id string) (Memory, error) {
	doc, err := s.lr.GetDoc(ctx, id)
	if err != nil {
		return Memory{}, wrapUpstream(err)
	}
	return Memory{ID: doc.DocID, Text: doc.Content, Metadata: doc.Metadata}, nil
}

func (s *Service) DeleteMemory(ctx context.Context, id string) error {
	if err := s.lr.DeleteDoc(ctx, id); err != nil {
		return wrapUpstream(err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat(memory): Service Create/Get/Delete with upstream error mapping"
```

### Task 3A.4: memory — Service query + describe

**Files:**
- Edit: `internal/memory/service.go`
- Test: `internal/memory/service_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestServiceQuery(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	svc := memory.NewService(f)

	_, err := svc.CreateMemory(ctx, memory.Memory{Text: "alpha bravo"})
	require.NoError(t, err)

	res, err := svc.Query(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "alpha"})
	require.NoError(t, err)
	require.NotEmpty(t, res.Matches)

	_, err = svc.Query(ctx, memory.Query{Mode: memory.QueryMode("nope"), Text: "x"})
	require.Error(t, err)
}

func TestServiceDescribe(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New())
	_, err := svc.CreateMemory(ctx, memory.Memory{Text: "tatara is a smelting furnace"})
	require.NoError(t, err)

	r, err := svc.Describe(ctx, memory.Query{Mode: memory.QueryModeHybrid, Text: "what is tatara"})
	require.NoError(t, err)
	require.NotEmpty(t, r.Response)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/... -run 'TestServiceQuery|TestServiceDescribe' -v`
Expected: FAIL with "undefined: svc.Query".

- [ ] **Step 3: Write minimal implementation**

```go
func (s *Service) Query(ctx context.Context, q Query) (QueryResult, error) {
	if !q.Mode.Valid() {
		return QueryResult{}, fmt.Errorf("invalid mode: %s", q.Mode)
	}
	resp, err := s.lr.Query(ctx, lightrag.QueryRequest{
		Mode: string(q.Mode),
		Text: q.Text,
		TopK: q.TopK,
	})
	if err != nil {
		return QueryResult{}, wrapUpstream(err)
	}
	return FromLightragQuery(resp), nil
}

func (s *Service) Describe(ctx context.Context, q Query) (DescribeResult, error) {
	if !q.Mode.Valid() {
		return DescribeResult{}, fmt.Errorf("invalid mode: %s", q.Mode)
	}
	resp, err := s.lr.QueryDescribe(ctx, lightrag.QueryRequest{
		Mode: string(q.Mode),
		Text: q.Text,
		TopK: q.TopK,
	})
	if err != nil {
		return DescribeResult{}, wrapUpstream(err)
	}
	return DescribeResult{Response: resp.Response, Sources: resp.Sources}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat(memory): Service Query and Describe with mode validation"
```

### Task 3A.5: memory — Service entities + edges

**Files:**
- Edit: `internal/memory/service.go`
- Test: `internal/memory/service_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestServiceEntities(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedEntity(lightrag.Entity{ID: "e1", Name: "tatara", Type: "concept"})
	svc := memory.NewService(f)

	e, err := svc.GetEntity(ctx, "e1")
	require.NoError(t, err)
	require.Equal(t, "tatara", e.Name)

	got, err := svc.SearchEntities(ctx, "tatara")
	require.NoError(t, err)
	require.Len(t, got, 1)

	updated, err := svc.PatchEntity(ctx, "e1", memory.Entity{Description: "smelter"})
	require.NoError(t, err)
	require.Equal(t, "smelter", updated.Description)
}

func TestServiceEdges(t *testing.T) {
	ctx := context.Background()
	svc := memory.NewService(fake.New())

	edge, err := svc.CreateEdge(ctx, memory.Edge{From: "a", To: "b", Relation: "rel"})
	require.NoError(t, err)
	require.NotEmpty(t, edge.ID)

	list, err := svc.ListEdges(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, svc.DeleteEdge(ctx, edge.ID))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/memory/... -run 'TestServiceEntities|TestServiceEdges' -v`
Expected: FAIL with "undefined: svc.GetEntity".

- [ ] **Step 3: Write minimal implementation**

```go
func (s *Service) GetEntity(ctx context.Context, id string) (Entity, error) {
	e, err := s.lr.GetEntity(ctx, id)
	if err != nil {
		return Entity{}, wrapUpstream(err)
	}
	return FromLightragEntity(e), nil
}

func (s *Service) SearchEntities(ctx context.Context, q string) ([]Entity, error) {
	es, err := s.lr.SearchEntities(ctx, q)
	if err != nil {
		return nil, wrapUpstream(err)
	}
	out := make([]Entity, 0, len(es))
	for _, e := range es {
		out = append(out, FromLightragEntity(e))
	}
	return out, nil
}

func (s *Service) PatchEntity(ctx context.Context, id string, patch Entity) (Entity, error) {
	e, err := s.lr.UpdateEntity(ctx, id, ToLightragEntityPatch(patch))
	if err != nil {
		return Entity{}, wrapUpstream(err)
	}
	return FromLightragEntity(e), nil
}

func (s *Service) ListEdges(ctx context.Context) ([]Edge, error) {
	es, err := s.lr.ListEdges(ctx)
	if err != nil {
		return nil, wrapUpstream(err)
	}
	out := make([]Edge, 0, len(es))
	for _, e := range es {
		out = append(out, FromLightragEdge(e))
	}
	return out, nil
}

func (s *Service) CreateEdge(ctx context.Context, e Edge) (Edge, error) {
	created, err := s.lr.CreateEdge(ctx, ToLightragEdgeCreate(e))
	if err != nil {
		return Edge{}, wrapUpstream(err)
	}
	return FromLightragEdge(created), nil
}

func (s *Service) DeleteEdge(ctx context.Context, id string) error {
	if err := s.lr.DeleteEdge(ctx, id); err != nil {
		return wrapUpstream(err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/
git commit -m "feat(memory): Service entities (get/search/patch) and edges (list/create/delete)"
```

### Task 3A.6: ingest — JobStore interface + in-memory impl

**Files:**
- Create: `internal/ingest/store.go`
- Create: `internal/ingest/memstore.go`
- Test: `internal/ingest/memstore_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestMemStoreCreateGet(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()

	job := memory.IngestJob{ID: "j1", Status: memory.JobStatusQueued, Total: 2, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	items := []memory.IngestItem{
		{IdempotencyKey: "k1", Text: "a"},
		{IdempotencyKey: "k2", Text: "b"},
	}
	require.NoError(t, s.CreateJob(ctx, job, items))

	got, err := s.GetJob(ctx, "j1")
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, got.Status)
	require.Equal(t, 2, got.Total)
}

func TestMemStoreItemIdempotent(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()
	job := memory.IngestJob{ID: "j2", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.CreateJob(ctx, job, []memory.IngestItem{{IdempotencyKey: "dup", Text: "a"}}))

	dupJob := memory.IngestJob{ID: "j2", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	err := s.CreateJob(ctx, dupJob, []memory.IngestItem{{IdempotencyKey: "dup", Text: "a"}})
	require.ErrorIs(t, err, ingest.ErrJobExists)
}

func TestMemStoreClaimNextItem(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()
	job := memory.IngestJob{ID: "j3", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, s.CreateJob(ctx, job, []memory.IngestItem{{IdempotencyKey: "k", Text: "x"}}))

	item, ok, err := s.ClaimNextItem(ctx, "j3")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "k", item.IdempotencyKey)

	_, ok, err = s.ClaimNextItem(ctx, "j3")
	require.NoError(t, err)
	require.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run 'TestMemStore' -v`
Expected: FAIL with "undefined: ingest.NewMemStore".

- [ ] **Step 3: Write minimal implementation**

`internal/ingest/store.go`:

```go
package ingest

import (
	"context"
	"errors"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

var (
	ErrJobExists   = errors.New("ingest: job already exists")
	ErrJobNotFound = errors.New("ingest: job not found")
)

type JobStore interface {
	CreateJob(ctx context.Context, job memory.IngestJob, items []memory.IngestItem) error
	GetJob(ctx context.Context, id string) (memory.IngestJob, error)
	UpdateJob(ctx context.Context, job memory.IngestJob) error
	ClaimNextItem(ctx context.Context, jobID string) (memory.IngestItem, bool, error)
	MarkItemDone(ctx context.Context, jobID, idemKey string, runErr error) error
	ListRunningJobs(ctx context.Context) ([]string, error)
}
```

`internal/ingest/memstore.go`:

```go
package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type memItem struct {
	memory.IngestItem
	status string
	err    string
}

type memJobBundle struct {
	job   memory.IngestJob
	items []*memItem
}

type MemStore struct {
	mu   sync.Mutex
	jobs map[string]*memJobBundle
}

func NewMemStore() *MemStore {
	return &MemStore{jobs: make(map[string]*memJobBundle)}
}

func (s *MemStore) CreateJob(_ context.Context, j memory.IngestJob, items []memory.IngestItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[j.ID]; ok {
		return ErrJobExists
	}
	b := &memJobBundle{job: j}
	for _, it := range items {
		b.items = append(b.items, &memItem{IngestItem: it, status: "pending"})
	}
	s.jobs[j.ID] = b
	return nil
}

func (s *MemStore) GetJob(_ context.Context, id string) (memory.IngestJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[id]
	if !ok {
		return memory.IngestJob{}, ErrJobNotFound
	}
	return b.job, nil
}

func (s *MemStore) UpdateJob(_ context.Context, j memory.IngestJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[j.ID]
	if !ok {
		return ErrJobNotFound
	}
	j.UpdatedAt = time.Now()
	b.job = j
	return nil
}

func (s *MemStore) ClaimNextItem(_ context.Context, jobID string) (memory.IngestItem, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[jobID]
	if !ok {
		return memory.IngestItem{}, false, ErrJobNotFound
	}
	for _, it := range b.items {
		if it.status == "pending" {
			it.status = "running"
			return it.IngestItem, true, nil
		}
	}
	return memory.IngestItem{}, false, nil
}

func (s *MemStore) MarkItemDone(_ context.Context, jobID, key string, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}
	for _, it := range b.items {
		if it.IdempotencyKey == key {
			if runErr != nil {
				it.status = "failed"
				it.err = runErr.Error()
			} else {
				it.status = "done"
			}
			return nil
		}
	}
	return nil
}

func (s *MemStore) ListRunningJobs(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, b := range s.jobs {
		if b.job.Status == memory.JobStatusRunning {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "feat(ingest): JobStore interface and in-memory implementation"
```

### Task 3A.7: ingest — Enqueue with idempotency

**Files:**
- Create: `internal/ingest/enqueue.go`
- Test: `internal/ingest/enqueue_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ingest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func TestEnqueueAssignsKeys(t *testing.T) {
	ctx := context.Background()
	s := ingest.NewMemStore()
	e := ingest.NewEnqueuer(s)

	items := []memory.IngestItem{{Text: "a"}, {IdempotencyKey: "given", Text: "b"}}
	job, err := e.Enqueue(ctx, items)
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, job.Status)
	require.Equal(t, 2, job.Total)

	got, err := s.GetJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, job.ID, got.ID)
}

func TestEnqueueEmpty(t *testing.T) {
	ctx := context.Background()
	e := ingest.NewEnqueuer(ingest.NewMemStore())
	_, err := e.Enqueue(ctx, nil)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run TestEnqueue -v`
Expected: FAIL with "undefined: ingest.NewEnqueuer".

- [ ] **Step 3: Write minimal implementation**

```go
package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type Enqueuer struct {
	store JobStore
	now   func() time.Time
}

func NewEnqueuer(s JobStore) *Enqueuer {
	return &Enqueuer{store: s, now: time.Now}
}

func newID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + "_" + hex.EncodeToString(b[:])
}

func (e *Enqueuer) Enqueue(ctx context.Context, items []memory.IngestItem) (memory.IngestJob, error) {
	if len(items) == 0 {
		return memory.IngestJob{}, errors.New("ingest: empty items")
	}
	now := e.now()
	for i := range items {
		if items[i].IdempotencyKey == "" {
			items[i].IdempotencyKey = newID("itm")
		}
	}
	job := memory.IngestJob{
		ID:        newID("job"),
		Status:    memory.JobStatusQueued,
		Total:     len(items),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := e.store.CreateJob(ctx, job, items); err != nil {
		return memory.IngestJob{}, err
	}
	return job, nil
}

// GetJob delegates to the store so *Enqueuer satisfies httpapi.IngestService
// (Enqueue + GetJob). The handlers in internal/httpapi consume one combined
// interface; we keep the production wiring clean by exposing both methods on
// one type rather than building an adapter in cmd/.
func (e *Enqueuer) GetJob(ctx context.Context, id string) (memory.IngestJob, error) {
	return e.store.GetJob(ctx, id)
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "feat(ingest): Enqueuer assigns idempotency keys and creates queued jobs"
```

### Task 3A.8: ingest — Pool runs a single job to completion

**Files:**
- Create: `internal/ingest/pool.go`
- Test: `internal/ingest/pool_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/lightrag/fake"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}

func TestPoolDrainsJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	svc := memory.NewService(fake.New())
	pool := ingest.NewPool(store, svc, 2)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store)
	job, err := e.Enqueue(ctx, []memory.IngestItem{
		{Text: "a"}, {Text: "b"}, {Text: "c"},
	})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, err := store.GetJob(ctx, job.ID)
		return err == nil && j.Status == memory.JobStatusSucceeded
	}, "job did not reach succeeded")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, 3, j.Done)
	require.Equal(t, 0, j.Failed)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run TestPoolDrainsJob -v`
Expected: FAIL with "undefined: ingest.NewPool".

- [ ] **Step 3: Write minimal implementation**

```go
package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

const maxErrors = 50

type itemRunner interface {
	CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error)
}

type Pool struct {
	store   JobStore
	runner  itemRunner
	size    int
	notify  chan string
	stop    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	started bool
}

func NewPool(store JobStore, runner itemRunner, size int) *Pool {
	if size < 1 {
		size = 1
	}
	return &Pool{
		store:  store,
		runner: runner,
		size:   size,
		notify: make(chan string, 256),
		stop:   make(chan struct{}),
	}
}

func (p *Pool) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started {
		return
	}
	p.started = true
	for i := 0; i < p.size; i++ {
		p.wg.Add(1)
		go p.worker(ctx)
	}
}

func (p *Pool) Stop() {
	close(p.stop)
	p.wg.Wait()
}

func (p *Pool) Notify(jobID string) {
	select {
	case p.notify <- jobID:
	default:
	}
}

func (p *Pool) worker(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case <-p.stop:
			return
		case <-ctx.Done():
			return
		case jobID := <-p.notify:
			p.runJob(ctx, jobID)
		}
	}
}

func (p *Pool) runJob(ctx context.Context, jobID string) {
	j, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	if j.Status == memory.JobStatusQueued {
		j.Status = memory.JobStatusRunning
		j.UpdatedAt = time.Now()
		_ = p.store.UpdateJob(ctx, j)
	}
	for {
		item, ok, err := p.store.ClaimNextItem(ctx, jobID)
		if err != nil || !ok {
			break
		}
		runErr := p.processItem(ctx, item)
		_ = p.store.MarkItemDone(ctx, jobID, item.IdempotencyKey, runErr)

		cur, _ := p.store.GetJob(ctx, jobID)
		if runErr != nil {
			cur.Failed++
			if len(cur.Errors) < maxErrors {
				cur.Errors = append(cur.Errors, memory.IngestItemError{IdempotencyKey: item.IdempotencyKey, Error: runErr.Error()})
			}
		} else {
			cur.Done++
		}
		cur.UpdatedAt = time.Now()
		_ = p.store.UpdateJob(ctx, cur)
	}
	final, err := p.store.GetJob(ctx, jobID)
	if err != nil {
		return
	}
	switch {
	case final.Failed == 0:
		final.Status = memory.JobStatusSucceeded
	case final.Done == 0:
		final.Status = memory.JobStatusFailed
	default:
		final.Status = memory.JobStatusPartial
	}
	final.UpdatedAt = time.Now()
	_ = p.store.UpdateJob(ctx, final)
}

func (p *Pool) processItem(ctx context.Context, it memory.IngestItem) error {
	_, err := p.runner.CreateMemory(ctx, memory.Memory{
		ID:       it.IdempotencyKey,
		Text:     it.Text,
		Metadata: it.Metadata,
	})
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "feat(ingest): worker Pool drains job items via memory.Service"
```

### Task 3A.9: ingest — Pool state transitions on partial + failed

**Files:**
- Test: `internal/ingest/pool_test.go`

- [ ] **Step 1: Write the failing test**

```go
type failingRunner struct {
	fail map[string]bool
}

func (f *failingRunner) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	if f.fail[m.Text] {
		return memory.Memory{}, errors.New("boom")
	}
	return m, nil
}

func TestPoolPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	r := &failingRunner{fail: map[string]bool{"bad": true}}
	pool := ingest.NewPool(store, r, 1)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "ok"}, {Text: "bad"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusPartial, j.Status)
	require.Equal(t, 1, j.Done)
	require.Equal(t, 1, j.Failed)
	require.Len(t, j.Errors, 1)
}

func TestPoolAllFailed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	r := &failingRunner{fail: map[string]bool{"x": true, "y": true}}
	pool := ingest.NewPool(store, r, 1)
	pool.Start(ctx)
	defer pool.Stop()

	e := ingest.NewEnqueuer(store)
	job, err := e.Enqueue(ctx, []memory.IngestItem{{Text: "x"}, {Text: "y"}})
	require.NoError(t, err)
	pool.Notify(job.ID)

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, job.ID)
		return j.Status.Terminal()
	}, "job did not terminate")

	j, _ := store.GetJob(ctx, job.ID)
	require.Equal(t, memory.JobStatusFailed, j.Status)
}
```

Add `import "errors"` at the top of the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run 'TestPoolPartial|TestPoolAllFailed' -v`
Expected: PASS if Pool already handles transitions; otherwise FAIL with mismatched status.

- [ ] **Step 3: Write minimal implementation**

No code change required if Task 3A.8's transitions are correct. If a test fails, adjust the switch in `runJob`.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "test(ingest): cover partial and failed terminal states"
```

### Task 3A.10: ingest — Crash-safe resume on startup

**Files:**
- Edit: `internal/ingest/pool.go`
- Test: `internal/ingest/pool_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestPoolResumeRunningOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := ingest.NewMemStore()
	// Pre-populate a job already in running state with pending items.
	require.NoError(t, store.CreateJob(ctx, memory.IngestJob{
		ID: "resume1", Status: memory.JobStatusRunning, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, []memory.IngestItem{{IdempotencyKey: "k", Text: "ok"}}))

	pool := ingest.NewPool(store, &failingRunner{}, 1)
	pool.Start(ctx)
	defer pool.Stop()
	require.NoError(t, pool.Resume(ctx))

	waitFor(t, func() bool {
		j, _ := store.GetJob(ctx, "resume1")
		return j.Status.Terminal()
	}, "resumed job did not terminate")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run TestPoolResumeRunningOnStart -v`
Expected: FAIL with "undefined: pool.Resume".

- [ ] **Step 3: Write minimal implementation**

Add to `internal/ingest/pool.go`:

```go
func (p *Pool) Resume(ctx context.Context) error {
	ids, err := p.store.ListRunningJobs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		p.Notify(id)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "feat(ingest): crash-safe resume of running jobs on Pool startup"
```

### Task 3A.11: ingest — Idempotency replay returns existing job

**Files:**
- Edit: `internal/ingest/enqueue.go`
- Test: `internal/ingest/enqueue_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestEnqueueRejectsDuplicateBatchKey(t *testing.T) {
	ctx := context.Background()
	store := ingest.NewMemStore()
	e := ingest.NewEnqueuer(store)

	items := []memory.IngestItem{{IdempotencyKey: "k1", Text: "a"}, {IdempotencyKey: "k1", Text: "b"}}
	_, err := e.Enqueue(ctx, items)
	require.ErrorIs(t, err, ingest.ErrDuplicateKey)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run TestEnqueueRejectsDuplicateBatchKey -v`
Expected: FAIL with "undefined: ingest.ErrDuplicateKey".

- [ ] **Step 3: Write minimal implementation**

In `internal/ingest/enqueue.go`:

```go
var ErrDuplicateKey = errors.New("ingest: duplicate idempotency key in batch")

// in Enqueue, after assigning missing keys, before CreateJob:
seen := make(map[string]struct{}, len(items))
for _, it := range items {
	if _, ok := seen[it.IdempotencyKey]; ok {
		return memory.IngestJob{}, ErrDuplicateKey
	}
	seen[it.IdempotencyKey] = struct{}{}
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "feat(ingest): reject duplicate idempotency keys within a batch"
```

### Task 3A.12: ingest — Postgres schema migration

**Files:**
- Create: `internal/ingest/migrations/0001_jobs.sql`
- Create: `internal/ingest/migrate.go`
- Test: `internal/ingest/migrate_test.go`

- [ ] **Step 1: Write the failing test**

```go
package ingest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
)

func TestMigrationSQLExists(t *testing.T) {
	sql := ingest.MigrationSQL()
	require.NotEmpty(t, sql)
	require.Contains(t, sql, "CREATE TABLE")
	require.Contains(t, sql, "ingest_jobs")
	require.Contains(t, sql, "ingest_job_items")
	require.Contains(t, sql, "UNIQUE")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -run TestMigrationSQLExists -v`
Expected: FAIL with "undefined: ingest.MigrationSQL".

- [ ] **Step 3: Write minimal implementation**

`internal/ingest/migrations/0001_jobs.sql`:

```sql
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
```

`internal/ingest/migrate.go`:

```go
package ingest

import (
	"context"
	"database/sql"
	_ "embed"
)

//go:embed migrations/0001_jobs.sql
var migration0001 string

func MigrationSQL() string {
	return migration0001
}

func Migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, migration0001)
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/migrations/ internal/ingest/migrate.go internal/ingest/migrate_test.go
git commit -m "feat(ingest): postgres schema migration for jobs and job items"
```

### Task 3A.13: ingest — Postgres JobStore implementation

**Files:**
- Create: `internal/ingest/pgstore.go`
- Test: `internal/ingest/pgstore_test.go`

The pgstore test is build-tagged `integration` so unit `go test` stays driver-free. The store implementation itself uses `database/sql` only and is verified by interface conformance at compile time.

- [ ] **Step 1: Write the failing test**

```go
//go:build integration

package ingest_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func openPG(t *testing.T) *sql.DB {
	dsn := os.Getenv("TATARA_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TATARA_TEST_PG_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	require.NoError(t, db.PingContext(context.Background()))
	return db
}

func TestPGStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openPG(t)
	defer db.Close()
	require.NoError(t, ingest.Migrate(ctx, db))

	store := ingest.NewPGStore(db)
	job := memory.IngestJob{ID: "pgjob1", Status: memory.JobStatusQueued, Total: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	require.NoError(t, store.CreateJob(ctx, job, []memory.IngestItem{{IdempotencyKey: "k", Text: "a"}}))

	got, err := store.GetJob(ctx, "pgjob1")
	require.NoError(t, err)
	require.Equal(t, memory.JobStatusQueued, got.Status)

	item, ok, err := store.ClaimNextItem(ctx, "pgjob1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "k", item.IdempotencyKey)

	require.NoError(t, store.MarkItemDone(ctx, "pgjob1", "k", nil))
}

// Compile-time interface check (non-tagged).
var _ ingest.JobStore = (*ingest.PGStore)(nil)
```

Also add a non-tagged file `internal/ingest/pgstore_iface_test.go`:

```go
package ingest_test

import "github.com/szymonrychu/tatara-memory/internal/ingest"

var _ ingest.JobStore = (*ingest.PGStore)(nil)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ingest/... -v`
Expected: FAIL with "undefined: ingest.PGStore".

- [ ] **Step 3: Write minimal implementation**

```go
package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type PGStore struct {
	db *sql.DB
}

func NewPGStore(db *sql.DB) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) CreateJob(ctx context.Context, j memory.IngestJob, items []memory.IngestItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	errJSON, _ := json.Marshal(j.Errors)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ingest_jobs(id, status, total, done, failed, errors_json, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		j.ID, string(j.Status), j.Total, j.Done, j.Failed, string(errJSON), j.CreatedAt, j.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrJobExists
		}
		return err
	}
	for _, it := range items {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ingest_job_items(id, job_id, idempotency_key, status, error, created_at)
			VALUES ($1,$2,$3,'pending','',now())`,
			newID("itm"), j.ID, it.IdempotencyKey)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PGStore) GetJob(ctx context.Context, id string) (memory.IngestJob, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, status, total, done, failed, errors_json, created_at, updated_at
		FROM ingest_jobs WHERE id = $1`, id)
	var j memory.IngestJob
	var status, errJSON string
	if err := row.Scan(&j.ID, &status, &j.Total, &j.Done, &j.Failed, &errJSON, &j.CreatedAt, &j.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memory.IngestJob{}, ErrJobNotFound
		}
		return memory.IngestJob{}, err
	}
	j.Status = memory.JobStatus(status)
	_ = json.Unmarshal([]byte(errJSON), &j.Errors)
	return j, nil
}

func (s *PGStore) UpdateJob(ctx context.Context, j memory.IngestJob) error {
	errJSON, _ := json.Marshal(j.Errors)
	res, err := s.db.ExecContext(ctx, `
		UPDATE ingest_jobs SET status=$2, total=$3, done=$4, failed=$5, errors_json=$6, updated_at=$7
		WHERE id=$1`,
		j.ID, string(j.Status), j.Total, j.Done, j.Failed, string(errJSON), time.Now())
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrJobNotFound
	}
	return nil
}

func (s *PGStore) ClaimNextItem(ctx context.Context, jobID string) (memory.IngestItem, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return memory.IngestItem{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT id, idempotency_key FROM ingest_job_items
		WHERE job_id=$1 AND status='pending'
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, jobID)
	var id, key string
	if err := row.Scan(&id, &key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return memory.IngestItem{}, false, tx.Commit()
		}
		return memory.IngestItem{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_job_items SET status='running' WHERE id=$1`, id); err != nil {
		return memory.IngestItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return memory.IngestItem{}, false, err
	}
	return memory.IngestItem{IdempotencyKey: key}, true, nil
}

func (s *PGStore) MarkItemDone(ctx context.Context, jobID, key string, runErr error) error {
	status := "done"
	errStr := ""
	if runErr != nil {
		status = "failed"
		errStr = runErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE ingest_job_items SET status=$3, error=$4
		WHERE job_id=$1 AND idempotency_key=$2`,
		jobID, key, status, errStr)
	return err
}

func (s *PGStore) ListRunningJobs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM ingest_jobs WHERE status='running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps with code 23505; substring match avoids importing the driver pkg here.
	return contains(err.Error(), "23505") || contains(err.Error(), "duplicate key")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ingest/... -v` (integration-tagged test skips without PG; iface assertion compiles).
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ingest/
git commit -m "feat(ingest): postgres JobStore impl using database/sql + pgx"
```

### Task 3A.14: ingest — Wire pgx driver into go.mod

**Files:**
- Edit: `go.mod`, `go.sum`

- [ ] **Step 1: Write the failing test**

```bash
go test ./internal/ingest/... -v
```
Expected: PASS already (driver is imported only in the integration-tagged file; the unit suite does not need it). If the integration test file fails to compile because of the import, that's the failure.

- [ ] **Step 2: Run test to verify it fails**

Run: `go vet ./internal/ingest/...`
Expected: error about missing `github.com/jackc/pgx/v5/stdlib`.

- [ ] **Step 3: Write minimal implementation**

```bash
go get github.com/jackc/pgx/v5/stdlib@latest
go mod tidy
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go vet ./internal/ingest/... && go test ./internal/ingest/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(deps): add pgx/v5 stdlib driver for ingest postgres store"
```

---

## Wave 3B — internal/httpapi

Runs in worktree `wt-wave3b` off `main`. Depends on Wave 2B `internal/auth` (Verifier + Middleware + testjwks) and Wave 2A `internal/obs` (Logger, PromRegistry) being merged. Defines its own `MemoryService` interface locally so it does not depend on Wave 3A; tests use a stub. All commits local.

### Task 3B.1: httpapi — MemoryService interface + Router skeleton

**Files:**
- Create: `internal/httpapi/router.go`
- Create: `internal/httpapi/service.go`
- Test: `internal/httpapi/router_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestHealthz(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestHealthz -v`
Expected: FAIL with "undefined: httpapi.NewRouter".

- [ ] **Step 3: Write minimal implementation**

`internal/httpapi/service.go`:

```go
package httpapi

import (
	"context"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type MemoryService interface {
	CreateMemory(ctx context.Context, m memory.Memory) (memory.Memory, error)
	GetMemory(ctx context.Context, id string) (memory.Memory, error)
	DeleteMemory(ctx context.Context, id string) error
	Query(ctx context.Context, q memory.Query) (memory.QueryResult, error)
	Describe(ctx context.Context, q memory.Query) (memory.DescribeResult, error)
	GetEntity(ctx context.Context, id string) (memory.Entity, error)
	SearchEntities(ctx context.Context, q string) ([]memory.Entity, error)
	PatchEntity(ctx context.Context, id string, patch memory.Entity) (memory.Entity, error)
	ListEdges(ctx context.Context) ([]memory.Edge, error)
	CreateEdge(ctx context.Context, e memory.Edge) (memory.Edge, error)
	DeleteEdge(ctx context.Context, id string) error
}

type IngestService interface {
	Enqueue(ctx context.Context, items []memory.IngestItem) (memory.IngestJob, error)
	GetJob(ctx context.Context, id string) (memory.IngestJob, error)
}
```

`internal/httpapi/router.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Config struct {
	Service MemoryService
	Ingest  IngestService
	Verify  func(http.Handler) http.Handler // auth middleware, optional in tests
}

func NewRouter(cfg Config) *chi.Mux {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return r
}
```

Stub service in test file:

```go
package httpapi_test

import (
	"context"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type stubService struct {
	createErr error
	getMem    memory.Memory
	getErr    error
}

func (s *stubService) CreateMemory(_ context.Context, m memory.Memory) (memory.Memory, error) {
	if s.createErr != nil {
		return memory.Memory{}, s.createErr
	}
	m.ID = "mem_stub"
	return m, nil
}
func (s *stubService) GetMemory(_ context.Context, _ string) (memory.Memory, error) {
	return s.getMem, s.getErr
}
func (s *stubService) DeleteMemory(_ context.Context, _ string) error { return nil }
func (s *stubService) Query(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return memory.QueryResult{}, nil
}
func (s *stubService) Describe(_ context.Context, _ memory.Query) (memory.DescribeResult, error) {
	return memory.DescribeResult{}, nil
}
func (s *stubService) GetEntity(_ context.Context, _ string) (memory.Entity, error) {
	return memory.Entity{}, nil
}
func (s *stubService) SearchEntities(_ context.Context, _ string) ([]memory.Entity, error) {
	return nil, nil
}
func (s *stubService) PatchEntity(_ context.Context, _ string, _ memory.Entity) (memory.Entity, error) {
	return memory.Entity{}, nil
}
func (s *stubService) ListEdges(_ context.Context) ([]memory.Edge, error) { return nil, nil }
func (s *stubService) CreateEdge(_ context.Context, e memory.Edge) (memory.Edge, error) {
	e.ID = "edge_stub"
	return e, nil
}
func (s *stubService) DeleteEdge(_ context.Context, _ string) error { return nil }
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): chi router skeleton, MemoryService interface, healthz"
```

### Task 3B.2: httpapi — Error envelope + writer

**Files:**
- Create: `internal/httpapi/errors.go`
- Test: `internal/httpapi/errors_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestWriteErrorEnvelope(t *testing.T) {
	rr := httptest.NewRecorder()
	httpapi.WriteError(rr, 400, "bad input", "req-123")
	require.Equal(t, 400, rr.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Equal(t, "bad input", body["error"])
	require.Equal(t, "req-123", body["request_id"])
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestWriteErrorEnvelope -v`
Expected: FAIL with "undefined: httpapi.WriteError".

- [ ] **Step 3: Write minimal implementation**

```go
package httpapi

import (
	"encoding/json"
	"net/http"
)

type errorEnvelope struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id"`
}

func WriteError(w http.ResponseWriter, status int, msg, reqID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: msg, RequestID: reqID})
}

func WriteJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): JSON error envelope writer"
```

### Task 3B.3: httpapi — request-id middleware

**Files:**
- Create: `internal/httpapi/middleware.go`
- Test: `internal/httpapi/middleware_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestRequestIDMiddlewareGenerates(t *testing.T) {
	var seen string
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = httpapi.RequestIDFromContext(r.Context())
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	require.NotEmpty(t, seen)
	require.Equal(t, seen, rr.Header().Get("X-Request-Id"))
}

func TestRequestIDMiddlewarePassthrough(t *testing.T) {
	h := httpapi.RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("X-Request-Id", "client-supplied")
	h.ServeHTTP(rr, req)
	require.Equal(t, "client-supplied", rr.Header().Get("X-Request-Id"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestRequestIDMiddleware -v`
Expected: FAIL with "undefined: httpapi.RequestID".

- [ ] **Step 3: Write minimal implementation**

```go
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type ctxKey int

const requestIDKey ctxKey = 0

func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-Id", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): request-id middleware with passthrough and generation"
```

### Task 3B.4: httpapi — panic recovery + slog access log

**Files:**
- Edit: `internal/httpapi/middleware.go`
- Test: `internal/httpapi/middleware_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRecoverMiddlewareReturns500(t *testing.T) {
	h := httpapi.RequestID(httpapi.Recover(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("boom")
	})))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	require.Equal(t, 500, rr.Code)
	require.Contains(t, rr.Body.String(), "internal error")
}

func TestAccessLogMiddlewareCallsNext(t *testing.T) {
	called := false
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := httpapi.AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/x", nil))
	require.True(t, called)
	require.Equal(t, 204, rr.Code)
}
```

Add imports `"io"`, `"log/slog"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run 'TestRecoverMiddleware|TestAccessLog' -v`
Expected: FAIL with "undefined: httpapi.Recover".

- [ ] **Step 3: Write minimal implementation**

```go
import (
	"log/slog"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				WriteError(w, http.StatusInternalServerError, "internal error", RequestIDFromContext(r.Context()))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)
			logger.Info("http",
				"request_id", RequestIDFromContext(r.Context()),
				"route", r.URL.Path,
				"method", r.Method,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): panic-recovery and slog access-log middleware"
```

### Task 3B.5: httpapi — prometheus metrics middleware

**Files:**
- Edit: `internal/httpapi/middleware.go`
- Test: `internal/httpapi/middleware_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestMetricsMiddlewareCountsRequest(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := httpapi.NewMetrics(reg)
	h := m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/v1/memories", nil))

	mfs, err := reg.Gather()
	require.NoError(t, err)
	found := false
	for _, mf := range mfs {
		if mf.GetName() == "http_requests_total" {
			found = true
		}
	}
	require.True(t, found, "http_requests_total not registered")
}
```

Add import `"github.com/prometheus/client_golang/prometheus"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestMetricsMiddleware -v`
Expected: FAIL with "undefined: httpapi.NewMetrics".

- [ ] **Step 3: Write minimal implementation**

```go
import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	reqTotal *prometheus.CounterVec
	reqDur   *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		reqTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Count of HTTP requests by route, method, status.",
		}, []string{"route", "method", "status"}),
		reqDur: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_in_flight",
			Help: "In-flight HTTP requests.",
		}),
	}
	reg.MustRegister(m.reqTotal, m.reqDur, m.inFlight)
	return m
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Inc()
		defer m.inFlight.Dec()
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		m.reqTotal.WithLabelValues(r.URL.Path, r.Method, http.StatusText(rec.status)).Inc()
		m.reqDur.WithLabelValues(r.URL.Path).Observe(time.Since(start).Seconds())
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): prometheus metrics middleware (http_requests_total, duration, in_flight)"
```

### Task 3B.6: httpapi — wire middleware stack + /metrics endpoint

**Files:**
- Edit: `internal/httpapi/router.go`
- Test: `internal/httpapi/router_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestRouterServesMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, Registry: reg})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func TestRouterReadyz(t *testing.T) {
	r := httpapi.NewRouter(httpapi.Config{Service: &stubService{}, ReadyCheck: func(_ context.Context) error { return nil }})
	srv := httptest.NewServer(r)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}
```

Add imports `"context"`, `"github.com/prometheus/client_golang/prometheus"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run 'TestRouterServesMetrics|TestRouterReadyz' -v`
Expected: FAIL with 404 on /metrics or undefined field Registry.

- [ ] **Step 3: Write minimal implementation**

```go
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Config struct {
	Service    MemoryService
	Ingest     IngestService
	Verify     func(http.Handler) http.Handler
	Logger     *slog.Logger
	Registry   *prometheus.Registry
	ReadyCheck func(context.Context) error
}

func NewRouter(cfg Config) *chi.Mux {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Registry == nil {
		cfg.Registry = prometheus.NewRegistry()
	}
	metrics := NewMetrics(cfg.Registry)

	r := chi.NewRouter()
	r.Use(RequestID)
	r.Use(Recover)
	r.Use(AccessLog(cfg.Logger))
	r.Use(metrics.Middleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if cfg.ReadyCheck != nil {
			if err := cfg.ReadyCheck(req.Context()); err != nil {
				WriteError(w, http.StatusServiceUnavailable, "not ready", RequestIDFromContext(req.Context()))
				return
			}
		}
		w.WriteHeader(200)
	})
	r.Handle("/metrics", promhttp.HandlerFor(cfg.Registry, promhttp.HandlerOpts{}))

	r.Group(func(r chi.Router) {
		if cfg.Verify != nil {
			r.Use(cfg.Verify)
		}
		mountV1(r, cfg)
	})
	return r
}

func mountV1(r chi.Router, cfg Config) {
	// handlers added in later tasks
	_ = cfg
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): wire request-id/recover/log/metrics + /metrics and /readyz"
```

### Task 3B.7: httpapi — auth middleware wired with testjwks

**Files:**
- Edit: `internal/httpapi/router.go`
- Test: `internal/httpapi/auth_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestProtectedRouteRejectsMissingToken(t *testing.T) {
	tj := testjwks.Start(t)
	defer tj.Close()

	r := httpapi.NewRouter(httpapi.Config{
		Service: &stubService{},
		Verify:  tj.Middleware("tatara-memory"),
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/memories/m1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestProtectedRouteAcceptsValidToken(t *testing.T) {
	tj := testjwks.Start(t)
	defer tj.Close()

	r := httpapi.NewRouter(httpapi.Config{
		Service: &stubService{getMem: memory.Memory{ID: "m1", Text: "hi"}},
		Verify:  tj.Middleware("tatara-memory"),
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	tok := tj.SignToken(map[string]interface{}{"sub": "u1", "aud": "tatara-memory"})
	req, _ := http.NewRequest("GET", srv.URL+"/v1/memories/m1", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}
```

Add import for `memory`. Note this depends on Task 3B.8 mounting `GET /v1/memories/{id}` but the route must already 401 when auth is missing. Implement the GET endpoint within this task to satisfy the second assertion.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestProtectedRoute -v`
Expected: FAIL with 404 (route not mounted) or auth not enforced.

- [ ] **Step 3: Write minimal implementation**

Replace `mountV1` body in `router.go`:

```go
func mountV1(r chi.Router, cfg Config) {
	r.Route("/v1", func(r chi.Router) {
		r.Get("/memories/{id}", handleGetMemory(cfg))
	})
}

func handleGetMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		m, err := cfg.Service.GetMemory(r.Context(), id)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, m)
	}
}
```

Add `internal/httpapi/errmap.go`:

```go
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func mapServiceError(w http.ResponseWriter, r *http.Request, err error) {
	reqID := RequestIDFromContext(r.Context())
	switch {
	case errors.Is(err, memory.ErrNotFound):
		WriteError(w, http.StatusNotFound, "not found", reqID)
	case errors.Is(err, memory.ErrTransient):
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusServiceUnavailable, "upstream temporarily unavailable", reqID)
	case errors.Is(err, memory.ErrUpstream):
		WriteError(w, http.StatusBadGateway, "upstream error", reqID)
	case errors.Is(err, context.DeadlineExceeded):
		w.Header().Set("Retry-After", "5")
		WriteError(w, http.StatusServiceUnavailable, "request timed out", reqID)
	default:
		WriteError(w, http.StatusInternalServerError, "internal error", reqID)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): wire auth middleware, mount /v1/memories/{id}, error mapping"
```

### Task 3B.8: httpapi — /v1/memories CRUD (pattern)

**Files:**
- Create: `internal/httpapi/memories.go`
- Test: `internal/httpapi/memories_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func newSrv(t *testing.T, svc httpapi.MemoryService) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewRouter(httpapi.Config{Service: svc}))
}

func TestPostMemory201(t *testing.T) {
	srv := newSrv(t, &stubService{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"text": "hello"})
	resp, err := http.Post(srv.URL+"/v1/memories", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var m memory.Memory
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&m))
	require.NotEmpty(t, m.ID)
}

func TestPostMemoryBadJSON400(t *testing.T) {
	srv := newSrv(t, &stubService{})
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/memories", "application/json", bytes.NewReader([]byte("not-json")))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetMemoryNotFound(t *testing.T) {
	srv := newSrv(t, &stubService{getErr: memory.ErrNotFound})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/memories/missing")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestGetMemoryUpstream502(t *testing.T) {
	srv := newSrv(t, &stubService{getErr: errors.Join(memory.ErrUpstream, errors.New("lr down"))})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/memories/x")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestGetMemoryTransient503(t *testing.T) {
	srv := newSrv(t, &stubService{getErr: errors.Join(memory.ErrTransient, errors.New("timeout"))})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/memories/x")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.NotEmpty(t, resp.Header.Get("Retry-After"))
}

func TestDeleteMemory204(t *testing.T) {
	srv := newSrv(t, &stubService{})
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/memories/m1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run 'TestPostMemory|TestGetMemory|TestDeleteMemory' -v`
Expected: FAIL with 404 on POST and DELETE.

- [ ] **Step 3: Write minimal implementation**

`internal/httpapi/memories.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handlePostMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m memory.Memory
		if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		created, err := cfg.Service.CreateMemory(r.Context(), m)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, created)
	}
}

func handleDeleteMemory(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := cfg.Service.DeleteMemory(r.Context(), id); err != nil {
			mapServiceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

In `router.go` `mountV1`, add:

```go
r.Post("/memories", handlePostMemory(cfg))
r.Delete("/memories/{id}", handleDeleteMemory(cfg))
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): /v1/memories POST/GET/DELETE with status-code coverage"
```

### Task 3B.9: httpapi — /v1/queries and /v1/queries:describe

**Files:**
- Create: `internal/httpapi/queries.go`
- Test: `internal/httpapi/queries_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type queryStub struct {
	stubService
	qres memory.QueryResult
	dres memory.DescribeResult
	qerr error
}

func (q *queryStub) Query(_ context.Context, _ memory.Query) (memory.QueryResult, error) {
	return q.qres, q.qerr
}
func (q *queryStub) Describe(_ context.Context, _ memory.Query) (memory.DescribeResult, error) {
	return q.dres, q.qerr
}

func TestPostQuery200(t *testing.T) {
	svc := &queryStub{qres: memory.QueryResult{Matches: []memory.QueryMatch{{ID: "m1", Score: 0.9}}}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "alpha"})
	resp, err := http.Post(srv.URL+"/v1/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)

	var got memory.QueryResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got.Matches, 1)
}

func TestPostQueryInvalidMode400(t *testing.T) {
	srv := newSrv(t, &queryStub{})
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "nope", "text": "x"})
	resp, err := http.Post(srv.URL+"/v1/queries", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
}

func TestPostQueriesDescribe200(t *testing.T) {
	svc := &queryStub{dres: memory.DescribeResult{Response: "answer"}}
	srv := newSrv(t, svc)
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mode": "hybrid", "text": "x"})
	resp, err := http.Post(srv.URL+"/v1/queries:describe", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}
```

Add import `"context"` in the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestPostQuer -v`
Expected: FAIL with 404.

- [ ] **Step 3: Write minimal implementation**

`internal/httpapi/queries.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handlePostQuery(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q memory.Query
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if !q.Mode.Valid() {
			WriteError(w, http.StatusBadRequest, "invalid mode", RequestIDFromContext(r.Context()))
			return
		}
		res, err := cfg.Service.Query(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	}
}

func handlePostQueryDescribe(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var q memory.Query
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if !q.Mode.Valid() {
			WriteError(w, http.StatusBadRequest, "invalid mode", RequestIDFromContext(r.Context()))
			return
		}
		res, err := cfg.Service.Describe(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, res)
	}
}
```

In `mountV1`:

```go
r.Post("/queries", handlePostQuery(cfg))
r.Post("/queries:describe", handlePostQueryDescribe(cfg))
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): /v1/queries and /v1/queries:describe with mode validation"
```

### Task 3B.10: httpapi — /v1/entities (get, search, patch)

**Files:**
- Create: `internal/httpapi/entities.go`
- Test: `internal/httpapi/entities_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type entStub struct {
	stubService
	e memory.Entity
	q []memory.Entity
}

func (s *entStub) GetEntity(_ context.Context, _ string) (memory.Entity, error) { return s.e, nil }
func (s *entStub) SearchEntities(_ context.Context, _ string) ([]memory.Entity, error) {
	return s.q, nil
}
func (s *entStub) PatchEntity(_ context.Context, _ string, p memory.Entity) (memory.Entity, error) {
	return p, nil
}

func TestGetEntity200(t *testing.T) {
	srv := newSrv(t, &entStub{e: memory.Entity{ID: "e1", Name: "tatara"}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/entities/e1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func TestSearchEntities200(t *testing.T) {
	srv := newSrv(t, &entStub{q: []memory.Entity{{ID: "e1"}}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/entities?q=tat")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func TestSearchEntitiesMissingQ400(t *testing.T) {
	srv := newSrv(t, &entStub{})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/entities")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
}

func TestPatchEntity200(t *testing.T) {
	srv := newSrv(t, &entStub{})
	defer srv.Close()
	body, _ := json.Marshal(memory.Entity{Description: "smelter"})
	req, _ := http.NewRequest("PATCH", srv.URL+"/v1/entities/e1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run 'TestGetEntity|TestSearchEntities|TestPatchEntity' -v`
Expected: FAIL with 404.

- [ ] **Step 3: Write minimal implementation**

`internal/httpapi/entities.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handleGetEntity(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e, err := cfg.Service.GetEntity(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, e)
	}
}

func handleSearchEntities(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		if q == "" {
			WriteError(w, http.StatusBadRequest, "missing query parameter q", RequestIDFromContext(r.Context()))
			return
		}
		es, err := cfg.Service.SearchEntities(r.Context(), q)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"entities": es})
	}
}

func handlePatchEntity(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var patch memory.Entity
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		e, err := cfg.Service.PatchEntity(r.Context(), chi.URLParam(r, "id"), patch)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, e)
	}
}
```

In `mountV1` (order matters - put `/entities` before `/entities/{id}` only if chi requires; chi handles both fine):

```go
r.Get("/entities", handleSearchEntities(cfg))
r.Get("/entities/{id}", handleGetEntity(cfg))
r.Patch("/entities/{id}", handlePatchEntity(cfg))
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): /v1/entities get/search/patch endpoints"
```

### Task 3B.11: httpapi — /v1/edges (list, create, delete)

**Files:**
- Create: `internal/httpapi/edges.go`
- Test: `internal/httpapi/edges_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type edgeStub struct {
	stubService
	list []memory.Edge
}

func (s *edgeStub) ListEdges(_ context.Context) ([]memory.Edge, error) { return s.list, nil }
func (s *edgeStub) CreateEdge(_ context.Context, e memory.Edge) (memory.Edge, error) {
	e.ID = "edge_new"
	return e, nil
}
func (s *edgeStub) DeleteEdge(_ context.Context, _ string) error { return nil }

func TestListEdges200(t *testing.T) {
	srv := newSrv(t, &edgeStub{list: []memory.Edge{{ID: "e1"}}})
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/edges")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func TestCreateEdge201(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	body, _ := json.Marshal(memory.Edge{From: "a", To: "b", Relation: "rel"})
	resp, err := http.Post(srv.URL+"/v1/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestCreateEdgeMissingFields400(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	body, _ := json.Marshal(memory.Edge{From: "a"})
	resp, err := http.Post(srv.URL+"/v1/edges", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
}

func TestDeleteEdge204(t *testing.T) {
	srv := newSrv(t, &edgeStub{})
	defer srv.Close()
	req, _ := http.NewRequest("DELETE", srv.URL+"/v1/edges/e1", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run 'TestListEdges|TestCreateEdge|TestDeleteEdge' -v`
Expected: FAIL with 404.

- [ ] **Step 3: Write minimal implementation**

`internal/httpapi/edges.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

func handleListEdges(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		es, err := cfg.Service.ListEdges(r.Context())
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"edges": es})
	}
}

func handleCreateEdge(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var e memory.Edge
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if e.From == "" || e.To == "" || e.Relation == "" {
			WriteError(w, http.StatusBadRequest, "from_entity, to_entity, relation required", RequestIDFromContext(r.Context()))
			return
		}
		created, err := cfg.Service.CreateEdge(r.Context(), e)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusCreated, created)
	}
}

func handleDeleteEdge(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Service.DeleteEdge(r.Context(), chi.URLParam(r, "id")); err != nil {
			mapServiceError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
```

In `mountV1`:

```go
r.Get("/edges", handleListEdges(cfg))
r.Post("/edges", handleCreateEdge(cfg))
r.Delete("/edges/{id}", handleDeleteEdge(cfg))
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): /v1/edges list/create/delete with required-field validation"
```

### Task 3B.12: httpapi — /v1/memories:bulk + /v1/ingest-jobs/{id}

**Files:**
- Create: `internal/httpapi/ingest.go`
- Test: `internal/httpapi/ingest_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/httpapi"
	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type ingestStub struct {
	enq memory.IngestJob
	job memory.IngestJob
	err error
}

func (s *ingestStub) Enqueue(_ context.Context, _ []memory.IngestItem) (memory.IngestJob, error) {
	return s.enq, s.err
}
func (s *ingestStub) GetJob(_ context.Context, _ string) (memory.IngestJob, error) {
	return s.job, s.err
}

func newSrvIngest(t *testing.T, svc httpapi.MemoryService, ing httpapi.IngestService) *httptest.Server {
	t.Helper()
	return httptest.NewServer(httpapi.NewRouter(httpapi.Config{Service: svc, Ingest: ing}))
}

func TestBulkIngest202(t *testing.T) {
	ing := &ingestStub{enq: memory.IngestJob{ID: "job1", Status: memory.JobStatusQueued}}
	srv := newSrvIngest(t, &stubService{}, ing)
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{
		"items": []map[string]string{{"text": "a"}, {"text": "b"}},
	})
	resp, err := http.Post(srv.URL+"/v1/memories:bulk", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusAccepted, resp.StatusCode)

	var got memory.IngestJob
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Equal(t, "job1", got.ID)
}

func TestBulkIngestEmpty400(t *testing.T) {
	srv := newSrvIngest(t, &stubService{}, &ingestStub{err: errors.New("ingest: empty items")})
	defer srv.Close()

	body, _ := json.Marshal(map[string]interface{}{"items": []map[string]string{}})
	resp, err := http.Post(srv.URL+"/v1/memories:bulk", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
}

func TestGetJob200(t *testing.T) {
	ing := &ingestStub{job: memory.IngestJob{ID: "j1", Status: memory.JobStatusRunning, Total: 5, Done: 2}}
	srv := newSrvIngest(t, &stubService{}, ing)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/ingest-jobs/j1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run 'TestBulkIngest|TestGetJob' -v`
Expected: FAIL with 404.

- [ ] **Step 3: Write minimal implementation**

`internal/httpapi/ingest.go`:

```go
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/szymonrychu/tatara-memory/internal/memory"
)

type bulkRequest struct {
	Items []memory.IngestItem `json:"items"`
}

func handleBulkIngest(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bulkRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "invalid json", RequestIDFromContext(r.Context()))
			return
		}
		if len(req.Items) == 0 {
			WriteError(w, http.StatusBadRequest, "items must not be empty", RequestIDFromContext(r.Context()))
			return
		}
		job, err := cfg.Ingest.Enqueue(r.Context(), req.Items)
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusAccepted, job)
	}
}

func handleGetJob(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job, err := cfg.Ingest.GetJob(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			mapServiceError(w, r, err)
			return
		}
		WriteJSON(w, http.StatusOK, job)
	}
}
```

In `mountV1`:

```go
if cfg.Ingest != nil {
	r.Post("/memories:bulk", handleBulkIngest(cfg))
	r.Get("/ingest-jobs/{id}", handleGetJob(cfg))
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "feat(httpapi): /v1/memories:bulk and /v1/ingest-jobs/{id}"
```

### Task 3B.13: httpapi — end-to-end smoke with auth + all endpoints mounted

**Files:**
- Test: `internal/httpapi/e2e_test.go`

- [ ] **Step 1: Write the failing test**

```go
package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/szymonrychu/tatara-memory/internal/auth/testjwks"
	"github.com/szymonrychu/tatara-memory/internal/httpapi"
)

func TestE2EAllEndpointsAuthEnforced(t *testing.T) {
	tj := testjwks.Start(t)
	defer tj.Close()

	r := httpapi.NewRouter(httpapi.Config{
		Service: &queryStub{},
		Ingest:  &ingestStub{},
		Verify:  tj.Middleware("tatara-memory"),
	})
	srv := httptest.NewServer(r)
	defer srv.Close()

	endpoints := []struct {
		method, path string
		body         []byte
	}{
		{"POST", "/v1/memories", []byte(`{"text":"x"}`)},
		{"GET", "/v1/memories/m1", nil},
		{"DELETE", "/v1/memories/m1", nil},
		{"POST", "/v1/memories:bulk", []byte(`{"items":[{"text":"a"}]}`)},
		{"GET", "/v1/ingest-jobs/j1", nil},
		{"POST", "/v1/queries", []byte(`{"mode":"hybrid","text":"x"}`)},
		{"POST", "/v1/queries:describe", []byte(`{"mode":"hybrid","text":"x"}`)},
		{"GET", "/v1/entities/e1", nil},
		{"GET", "/v1/entities?q=t", nil},
		{"PATCH", "/v1/entities/e1", []byte(`{"description":"d"}`)},
		{"GET", "/v1/edges", nil},
		{"POST", "/v1/edges", []byte(`{"from_entity":"a","to_entity":"b","relation":"r"}`)},
		{"DELETE", "/v1/edges/e1", nil},
	}

	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			req, _ := http.NewRequest(ep.method, srv.URL+ep.path, bytes.NewReader(ep.body))
			if ep.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode, "expected 401 without token")
			require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

			var env map[string]string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
			require.NotEmpty(t, env["request_id"])
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -run TestE2EAllEndpointsAuthEnforced -v`
Expected: PASS if all routes from earlier tasks are mounted; FAIL with 404 for any missing route.

- [ ] **Step 3: Write minimal implementation**

If any subtest 404s, add the missing route in `mountV1`. The handlers exist already from earlier tasks.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/httpapi/
git commit -m "test(httpapi): e2e smoke - every /v1 route returns 401 envelope without token"
```

### Task 3B.14: httpapi — confirm full test suite green + run vet

**Files:** none

- [ ] **Step 1: Write the failing test**

n/a - verification step.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/... -v && go vet ./internal/httpapi/...`
Expected: PASS. If anything fails, fix in place and rerun.

- [ ] **Step 3: Write minimal implementation**

n/a.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS, zero vet output.

- [ ] **Step 5: Commit**

```bash
# No-op if nothing changed. If fixups were needed, commit them:
git status
git commit -am "chore(httpapi): wave 3B verification fixups" || true
```
## Wave 4A — cmd/tatara-memory wiring

Worktree: `git worktree add ../tatara-memory-wave4a wave4a/main-wiring` off `main`.
All tasks run inside that worktree. Local commits only; merge in Wave 6.

### Task 4A.1: config — flag/env parsing struct

**Files:**
- Create: `cmd/tatara-memory/config.go`
- Test: `cmd/tatara-memory/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, ":8080", cfg.HTTPAddr)
	require.Equal(t, "https://auth.szymonrichert.pl/realms/master", cfg.OIDCIssuer)
	require.Equal(t, "tatara-memory", cfg.OIDCAudience)
	require.Equal(t, 4, cfg.WorkerPoolSize)
	require.Equal(t, "info", cfg.LogLevel)
	require.Empty(t, cfg.OTLPEndpoint)
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	os.Clearenv()
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("PG_DSN", "postgres://u:p@db:5432/tm?sslmode=disable")
	t.Setenv("LIGHTRAG_BASE_URL", "http://lr:9621")
	t.Setenv("OIDC_ISSUER", "https://idp.example/realms/r")
	t.Setenv("OIDC_AUDIENCE", "svc")
	t.Setenv("WORKER_POOL_SIZE", "8")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("OTLP_ENDPOINT", "otel:4317")
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.HTTPAddr)
	require.Equal(t, "postgres://u:p@db:5432/tm?sslmode=disable", cfg.PGDSN)
	require.Equal(t, "http://lr:9621", cfg.LightRAGBaseURL)
	require.Equal(t, "https://idp.example/realms/r", cfg.OIDCIssuer)
	require.Equal(t, "svc", cfg.OIDCAudience)
	require.Equal(t, 8, cfg.WorkerPoolSize)
	require.Equal(t, "debug", cfg.LogLevel)
	require.Equal(t, "otel:4317", cfg.OTLPEndpoint)
}

func TestLoadConfig_FlagsBeatEnv(t *testing.T) {
	os.Clearenv()
	t.Setenv("HTTP_ADDR", ":9090")
	cfg, err := loadConfig([]string{"--http-addr", ":7777"})
	require.NoError(t, err)
	require.Equal(t, ":7777", cfg.HTTPAddr)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestLoadConfig`
Expected: FAIL with "undefined: loadConfig".

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
)

type config struct {
	HTTPAddr        string
	PGDSN           string
	LightRAGBaseURL string
	OIDCIssuer      string
	OIDCAudience    string
	WorkerPoolSize  int
	LogLevel        string
	OTLPEndpoint    string
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envIntOr(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("env %s: %w", key, err)
	}
	return n, nil
}

func loadConfig(args []string) (config, error) {
	wp, err := envIntOr("WORKER_POOL_SIZE", 4)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		HTTPAddr:        envOr("HTTP_ADDR", ":8080"),
		PGDSN:           envOr("PG_DSN", ""),
		LightRAGBaseURL: envOr("LIGHTRAG_BASE_URL", ""),
		OIDCIssuer:      envOr("OIDC_ISSUER", "https://auth.szymonrichert.pl/realms/master"),
		OIDCAudience:    envOr("OIDC_AUDIENCE", "tatara-memory"),
		WorkerPoolSize:  wp,
		LogLevel:        envOr("LOG_LEVEL", "info"),
		OTLPEndpoint:    envOr("OTLP_ENDPOINT", ""),
	}

	fs := flag.NewFlagSet("tatara-memory", flag.ContinueOnError)
	fs.StringVar(&cfg.HTTPAddr, "http-addr", cfg.HTTPAddr, "HTTP listen address")
	fs.StringVar(&cfg.PGDSN, "pg-dsn", cfg.PGDSN, "Postgres DSN")
	fs.StringVar(&cfg.LightRAGBaseURL, "lightrag-base-url", cfg.LightRAGBaseURL, "LightRAG base URL")
	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL")
	fs.StringVar(&cfg.OIDCAudience, "oidc-audience", cfg.OIDCAudience, "OIDC audience")
	fs.IntVar(&cfg.WorkerPoolSize, "worker-pool-size", cfg.WorkerPoolSize, "Ingest worker pool size")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "Log level (debug|info|warn|error)")
	fs.StringVar(&cfg.OTLPEndpoint, "otlp-endpoint", cfg.OTLPEndpoint, "OTLP endpoint (empty disables tracing)")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/config.go cmd/tatara-memory/config_test.go
git commit -m "feat(main): flag and env config loader"
```

### Task 4A.2: config — validation

**Files:**
- Modify: `cmd/tatara-memory/config.go`
- Modify: `cmd/tatara-memory/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestLoadConfig_ValidateRequired(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{})
	require.NoError(t, err)
	require.Error(t, cfg.validate())

	cfg.PGDSN = "postgres://x"
	cfg.LightRAGBaseURL = "http://lr"
	require.NoError(t, cfg.validate())
}

func TestLoadConfig_ValidatePoolSize(t *testing.T) {
	os.Clearenv()
	cfg, err := loadConfig([]string{"--worker-pool-size", "0"})
	require.NoError(t, err)
	cfg.PGDSN = "x"
	cfg.LightRAGBaseURL = "y"
	require.Error(t, cfg.validate())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestLoadConfig_Validate`
Expected: FAIL with "cfg.validate undefined".

- [ ] **Step 3: Write minimal implementation**

Add to `config.go`:

```go
func (c config) validate() error {
	if c.PGDSN == "" {
		return fmt.Errorf("pg-dsn is required")
	}
	if c.LightRAGBaseURL == "" {
		return fmt.Errorf("lightrag-base-url is required")
	}
	if c.WorkerPoolSize < 1 {
		return fmt.Errorf("worker-pool-size must be >= 1")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/config.go cmd/tatara-memory/config_test.go
git commit -m "feat(main): validate required config fields"
```

### Task 4A.3: app — runtime struct skeleton

**Files:**
- Create: `cmd/tatara-memory/app.go`
- Test: `cmd/tatara-memory/app_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApp_NewAndShutdown(t *testing.T) {
	a, err := newAppForTest(t)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, a.shutdown(ctx))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestApp_NewAndShutdown`
Expected: FAIL with "newAppForTest undefined".

- [ ] **Step 3: Write minimal implementation**

`app.go`:

```go
package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/szymonrychu/tatara-memory/internal/ingest"
	"github.com/szymonrychu/tatara-memory/internal/lightrag"
)

type app struct {
	log     *slog.Logger
	reg     *prometheus.Registry
	db      *sql.DB
	lrc     lightrag.Client
	pool    *ingest.Pool
	server  *http.Server
	stopOTL func(context.Context) error
}

func (a *app) shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if a.server != nil {
		_ = a.server.Shutdown(shutdownCtx)
	}
	if a.pool != nil {
		a.pool.Stop()
	}
	if a.db != nil {
		_ = a.db.Close()
	}
	if a.stopOTL != nil {
		_ = a.stopOTL(shutdownCtx)
	}
	return nil
}
```

`app_test.go` helper:

```go
package main

import (
	"net/http"
	"testing"

	"github.com/szymonrychu/tatara-memory/internal/obs"
)

func newAppForTest(t *testing.T) (*app, error) {
	t.Helper()
	return &app{
		log:    obs.Logger("info"),
		reg:    obs.PromRegistry(),
		server: &http.Server{Addr: ":0"},
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/app.go cmd/tatara-memory/app_test.go
git commit -m "feat(main): app runtime struct and shutdown"
```

### Task 4A.4: health — /healthz always-200

**Files:**
- Create: `cmd/tatara-memory/health.go`
- Test: `cmd/tatara-memory/health_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthz(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	healthzHandler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ok", rr.Body.String())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestHealthz`
Expected: FAIL with "healthzHandler undefined".

- [ ] **Step 3: Write minimal implementation**

```go
package main

import "net/http"

func healthzHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/health.go cmd/tatara-memory/health_test.go
git commit -m "feat(main): healthz handler"
```

### Task 4A.5: health — /readyz with db ping + lightrag health

**Files:**
- Modify: `cmd/tatara-memory/health.go`
- Modify: `cmd/tatara-memory/health_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakePinger struct{ err error }

func (f fakePinger) PingContext(ctx context.Context) error { return f.err }

type fakeHealther struct{ err error }

func (f fakeHealther) Health(ctx context.Context) error { return f.err }

func TestReadyz_OK(t *testing.T) {
	h := readyzHandler(fakePinger{}, fakeHealther{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rr.Code)
}

func TestReadyz_DBDown(t *testing.T) {
	h := readyzHandler(fakePinger{err: errors.New("db gone")}, fakeHealther{})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Contains(t, rr.Body.String(), "db")
}

func TestReadyz_LightRAGDown(t *testing.T) {
	h := readyzHandler(fakePinger{}, fakeHealther{err: errors.New("lr gone")})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Contains(t, rr.Body.String(), "lightrag")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestReadyz`
Expected: FAIL with "readyzHandler undefined".

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"context"
	"encoding/json"
	"net/http"
)

type pinger interface {
	PingContext(ctx context.Context) error
}

type healther interface {
	Health(ctx context.Context) error
}

func readyzHandler(db pinger, lr healther) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result := map[string]string{"db": "ok", "lightrag": "ok"}
		status := http.StatusOK
		if err := db.PingContext(r.Context()); err != nil {
			result["db"] = err.Error()
			status = http.StatusServiceUnavailable
		}
		if err := lr.Health(r.Context()); err != nil {
			result["lightrag"] = err.Error()
			status = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	})
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/health.go cmd/tatara-memory/health_test.go
git commit -m "feat(main): readyz handler covering db and lightrag"
```

### Task 4A.6: wire — observability + db open

**Files:**
- Modify: `cmd/tatara-memory/app.go`
- Modify: `cmd/tatara-memory/app_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildObsAndDB(t *testing.T) {
	cfg := config{
		HTTPAddr:        ":0",
		PGDSN:           "postgres://user:pass@127.0.0.1:1/db?sslmode=disable",
		LightRAGBaseURL: "http://127.0.0.1:9999",
		OIDCIssuer:      "https://example/realms/r",
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "info",
	}
	logger, reg, stop, err := buildObs(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, reg)
	require.NotNil(t, stop)
	require.NoError(t, stop(context.Background()))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestBuildObs`
Expected: FAIL with "buildObs undefined".

- [ ] **Step 3: Write minimal implementation**

Append to `app.go`:

```go
func buildObs(ctx context.Context, cfg config) (*slog.Logger, *prometheus.Registry, func(context.Context) error, error) {
	logger := obs.Logger(cfg.LogLevel)
	reg := obs.PromRegistry()
	stop, err := obs.TracerProvider(ctx, cfg.OTLPEndpoint)
	if err != nil {
		return nil, nil, nil, err
	}
	return logger, reg, stop, nil
}

func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	return db, nil
}
```

Add imports: `"github.com/szymonrychu/tatara-memory/internal/obs"` and the postgres driver `_ "github.com/lib/pq"`.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/app.go cmd/tatara-memory/app_test.go
git commit -m "feat(main): observability and db builders"
```

### Task 4A.7: wire — newApp end-to-end with fakes

**Files:**
- Modify: `cmd/tatara-memory/app.go`
- Modify: `cmd/tatara-memory/app_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestNewApp_WithFakes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/.well-known/openid-configuration":
			_, _ = w.Write([]byte(`{"issuer":"` + r.Host + `","jwks_uri":"http://x/jwks"}`))
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: srv.URL,
		OIDCIssuer:      srv.URL,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "info",
	}
	a, err := newAppWithDeps(context.Background(), cfg, fakeDeps{})
	require.NoError(t, err)
	require.NotNil(t, a.server)
	require.NoError(t, a.shutdown(context.Background()))
}
```

Add `fakeDeps` and supporting fakes in `app_test.go`:

```go
type fakeDeps struct{}

func (fakeDeps) openDB(dsn string) (*sql.DB, error) {
	return sql.OpenDB(fakeConnector{}), nil
}

type fakeConnector struct{}

func (fakeConnector) Connect(_ context.Context) (driver.Conn, error) { return nil, driver.ErrBadConn }
func (fakeConnector) Driver() driver.Driver                          { return nil }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestNewApp_WithFakes`
Expected: FAIL with "newAppWithDeps undefined".

- [ ] **Step 3: Write minimal implementation**

Append to `app.go`:

```go
type deps struct {
	openDBFn func(string) (*sql.DB, error)
}

func newAppWithDeps(ctx context.Context, cfg config, d interface{ openDB(string) (*sql.DB, error) }) (*app, error) {
	logger, reg, stop, err := buildObs(ctx, cfg)
	if err != nil {
		return nil, err
	}
	db, err := d.openDB(cfg.PGDSN)
	if err != nil {
		return nil, err
	}
	lrc := lightrag.NewClient(cfg.LightRAGBaseURL, reg, logger)
	store := ingest.NewPostgresStore(db)
	memSvc := memory.NewService(lrc, logger)
	pool := ingest.NewPool(cfg.WorkerPoolSize, store, memSvc, logger)
	pool.Start(ctx)
	verifier, err := auth.NewVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience)
	if err != nil {
		return nil, err
	}
	router := httpapi.NewRouter(httpapi.Deps{
		Logger:   logger,
		Registry: reg,
		Verifier: verifier,
		Memory:   memSvc,
		Pool:     pool,
	})
	router.Method(http.MethodGet, "/healthz", healthzHandler())
	router.Method(http.MethodGet, "/readyz", readyzHandler(db, lrc))
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return &app{
		log:     logger,
		reg:     reg,
		db:      db,
		lrc:     lrc,
		pool:    pool,
		server:  srv,
		stopOTL: stop,
	}, nil
}

func newApp(ctx context.Context, cfg config) (*app, error) {
	return newAppWithDeps(ctx, cfg, realDeps{})
}

type realDeps struct{}

func (realDeps) openDB(dsn string) (*sql.DB, error) { return openDB(dsn) }
```

Add imports for `auth`, `memory`, `httpapi`.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/app.go cmd/tatara-memory/app_test.go
git commit -m "feat(main): wire obs, db, lightrag, memory, ingest, auth, router"
```

### Task 4A.8: serve — listen with graceful shutdown loop

**Files:**
- Create: `cmd/tatara-memory/serve.go`
- Test: `cmd/tatara-memory/serve_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServe_GracefulShutdown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: mux}
	ln, err := newListener(srv.Addr)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- serve(srv, ln) }()

	time.Sleep(50 * time.Millisecond)
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
	require.NoError(t, <-errCh)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestServe_GracefulShutdown`
Expected: FAIL with "newListener undefined".

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"errors"
	"net"
	"net/http"
)

func newListener(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

func serve(srv *http.Server, ln net.Listener) error {
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/serve.go cmd/tatara-memory/serve_test.go
git commit -m "feat(main): listener and serve loop with graceful close"
```

### Task 4A.9: main — signal handling + entrypoint

**Files:**
- Modify: `cmd/tatara-memory/main.go`

- [ ] **Step 1: Write the failing test**

Test lives in 4A.10 (integration). For this task, write a unit test for `waitForSignal`:

`cmd/tatara-memory/main_test.go`:

```go
package main

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWaitForSignal_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	require.NoError(t, waitForSignal(ctx))
}

func TestWaitForSignal_SIGTERM(t *testing.T) {
	ctx := context.Background()
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
	}()
	require.NoError(t, waitForSignal(ctx))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestWaitForSignal`
Expected: FAIL with "waitForSignal undefined".

- [ ] **Step 3: Write minimal implementation**

Rewrite `cmd/tatara-memory/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/szymonrychu/tatara-memory/internal/version"
)

func waitForSignal(ctx context.Context) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(ch)
	select {
	case <-ch:
	case <-ctx.Done():
	}
	return nil
}

func run(ctx context.Context, args []string) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	a, err := newApp(ctx, cfg)
	if err != nil {
		return err
	}
	a.log.Info("starting", "version", version.Version, "addr", cfg.HTTPAddr)

	ln, err := newListener(cfg.HTTPAddr)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- serve(a.server, ln) }()

	select {
	case err := <-errCh:
		return err
	case <-waitChan(ctx):
	}

	a.log.Info("shutdown signal received")
	return a.shutdown(context.Background())
}

func waitChan(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		_ = waitForSignal(ctx)
		close(done)
	}()
	return done
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/main.go cmd/tatara-memory/main_test.go
git commit -m "feat(main): signal-driven run loop"
```

### Task 4A.10: integration — end-to-end wiring smoke

**Files:**
- Create: `cmd/tatara-memory/integration_test.go`

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type alwaysOKConnector struct{}

func (alwaysOKConnector) Connect(_ context.Context) (driver.Conn, error) { return okConn{}, nil }
func (alwaysOKConnector) Driver() driver.Driver                          { return okDriver{} }

type okDriver struct{}

func (okDriver) Open(string) (driver.Conn, error) { return okConn{}, nil }

type okConn struct{}

func (okConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (okConn) Close() error                        { return nil }
func (okConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }
func (okConn) Ping(context.Context) error          { return nil }

type fakeAppDeps struct{}

func (fakeAppDeps) openDB(string) (*sql.DB, error) {
	return sql.OpenDB(alwaysOKConnector{}), nil
}

func TestApp_EndToEnd(t *testing.T) {
	lr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/.well-known/openid-configuration" {
			_, _ = w.Write([]byte(`{"issuer":"` + r.Host + `","jwks_uri":"http://x/jwks"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(lr.Close)

	cfg := config{
		HTTPAddr:        "127.0.0.1:0",
		PGDSN:           "fake",
		LightRAGBaseURL: lr.URL,
		OIDCIssuer:      lr.URL,
		OIDCAudience:    "tatara-memory",
		WorkerPoolSize:  1,
		LogLevel:        "info",
	}
	a, err := newAppWithDeps(context.Background(), cfg, fakeAppDeps{})
	require.NoError(t, err)
	ln, err := newListener(cfg.HTTPAddr)
	require.NoError(t, err)
	go func() { _ = serve(a.server, ln) }()

	deadline := time.Now().Add(2 * time.Second)
	var resp *http.Response
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + ln.Addr().String() + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get("http://" + ln.Addr().String() + "/readyz")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Graceful shutdown drains.
	go func() { _ = syscall.Kill(syscall.Getpid(), syscall.SIGTERM) }()
	require.NoError(t, a.shutdown(context.Background()))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/tatara-memory/ -run TestApp_EndToEnd`
Expected: PASS if all prior tasks landed. If FAIL, fix wiring before moving on.

- [ ] **Step 3: Write minimal implementation**

No new code; this test exercises the assembled pieces. If readyz reports failure under the fake postgres `Ping`, switch `alwaysOKConnector` to return the appropriate ping success path (already covered above).

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tatara-memory/integration_test.go
git commit -m "test(main): end-to-end wiring and graceful shutdown"
```

### Task 4A.11: go.mod — add lib/pq driver

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: Write the failing test**

```bash
go build ./cmd/tatara-memory/
```
Expected: FAIL with "no required module provides package github.com/lib/pq".

- [ ] **Step 2: Run test to verify it fails**

Run: `go build ./cmd/tatara-memory/`
Expected: build error referencing `lib/pq`.

- [ ] **Step 3: Write minimal implementation**

```bash
go get github.com/lib/pq@v1.10.9
go mod tidy
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./cmd/tatara-memory/ && go test ./cmd/tatara-memory/...`
Expected: build succeeds, tests PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add lib/pq postgres driver"
```

### Task 4A.12: verification — full lint and test sweep

**Files:**
- None (verification only)

- [ ] **Step 1: Write the failing test**

N/A — verification gate.

- [ ] **Step 2: Run test to verify it fails**

N/A.

- [ ] **Step 3: Write minimal implementation**

Run the verification sweep.

- [ ] **Step 4: Run test to verify it passes**

```bash
make lint test build
```
Expected: all green. If `make build` warns about missing `-ldflags`, leave as-is; defaults are fine.

- [ ] **Step 5: Commit**

No commit; merge-ready state for Wave 6.

```bash
git log --oneline -n 15
```

## Wave 4B — chart bodies

Worktree: `git worktree add ../tatara-memory-wave4b wave4b/chart-bodies` off `main`.
All tasks operate in `charts/tatara-memory/`. Local commits only.

Conventions:
- Release name in tests: `tm`.
- Common labels macro `tatara-memory.labels` from `_helpers.tpl` (added in Wave 1).
- Selector labels macro `tatara-memory.selectorLabels`.
- A new template macro `tatara-memory.envConfig` produces kebab-case key/value pairs from camelCase scalars.

### Task 4B.1: helpers — envConfig mapping macro

**Files:**
- Modify: `charts/tatara-memory/templates/_helpers.tpl`
- Modify: `charts/tatara-memory/values.yaml`
- Create: `charts/tatara-memory/tests/helpers_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: helpers envConfig
templates:
  - configmap.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: renders kebab-case keys from camelCase scalars
    set:
      httpAddr: ":8080"
      lightragBaseUrl: "http://tm-lightrag:9621"
      oidcIssuer: "https://auth.szymonrichert.pl/realms/master"
      oidcAudience: "tatara-memory"
      workerPoolSize: 4
      logLevel: "info"
      otlpEndpoint: ""
      pgHost: "tm-database-cluster-rw"
      pgPort: 5432
      pgDb: "tatara_memory"
      pgUser: "tatara_memory"
    asserts:
      - equal:
          path: data.http-addr
          value: ":8080"
      - equal:
          path: data.lightrag-base-url
          value: "http://tm-lightrag:9621"
      - equal:
          path: data.oidc-issuer
          value: "https://auth.szymonrichert.pl/realms/master"
      - equal:
          path: data.oidc-audience
          value: "tatara-memory"
      - equal:
          path: data.worker-pool-size
          value: "4"
      - equal:
          path: data.log-level
          value: "info"
      - equal:
          path: data.pg-host
          value: "tm-database-cluster-rw"
      - equal:
          path: data.pg-port
          value: "5432"
      - equal:
          path: data.pg-db
          value: "tatara_memory"
      - equal:
          path: data.pg-user
          value: "tatara_memory"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/helpers_test.yaml`
Expected: FAIL (configmap.yaml not yet written / macro missing).

- [ ] **Step 3: Write minimal implementation**

Append to `_helpers.tpl`:

```yaml
{{/*
Map camelCase values.* scalars to kebab-case ConfigMap keys.
Strict: values.yaml carries only scalars; this macro is the single mapping point.
*/}}
{{- define "tatara-memory.envConfig" -}}
http-addr: {{ .Values.httpAddr | quote }}
lightrag-base-url: {{ .Values.lightragBaseUrl | quote }}
oidc-issuer: {{ .Values.oidcIssuer | quote }}
oidc-audience: {{ .Values.oidcAudience | quote }}
worker-pool-size: {{ .Values.workerPoolSize | quote }}
log-level: {{ .Values.logLevel | quote }}
otlp-endpoint: {{ .Values.otlpEndpoint | quote }}
pg-host: {{ .Values.pgHost | quote }}
pg-port: {{ .Values.pgPort | quote }}
pg-db: {{ .Values.pgDb | quote }}
pg-user: {{ .Values.pgUser | quote }}
{{- end -}}
```

Replace `values.yaml` with strict camelCase scalars (no lists, no nested env):

```yaml
image:
  repository: harbor.szymonrichert.pl/tatara/tatara-memory
  tag: ""
  pullPolicy: IfNotPresent

replicaCount: 1

httpAddr: ":8080"
lightragBaseUrl: "http://tm-lightrag:9621"
oidcIssuer: "https://auth.szymonrichert.pl/realms/master"
oidcAudience: "tatara-memory"
workerPoolSize: 4
logLevel: "info"
otlpEndpoint: ""

pgHost: "tm-database-cluster-rw"
pgPort: 5432
pgDb: "tatara_memory"
pgUser: "tatara_memory"

# Secrets are provided by external Secret objects (cnpg-managed, SOPS,
# or ExternalSecret). Set existingSecret to override the default
# generated by templates/secret.yaml.
existingSecret: ""
pgPasswordSecret: "tm-database-cluster-app"
pgPasswordKey: "password"
oidcClientSecret: ""

service:
  type: ClusterIP
  port: 8080

ingress:
  enabled: false
  className: nginx
  host: ""
  clusterIssuer: letsencrypt-prod
  tlsSecretName: ""

serviceMonitor:
  enabled: true
  interval: "30s"
  scrapeTimeout: "10s"

networkPolicy:
  enabled: true

resources:
  requests:
    cpu: 100m
    memory: 256Mi
  limits:
    memory: 512Mi
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS (after configmap.yaml in next task; if helm-unittest needs the template now, write a minimal configmap.yaml first using `{{ include "tatara-memory.envConfig" . | nindent 2 }}`).

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/_helpers.tpl \
        charts/tatara-memory/values.yaml \
        charts/tatara-memory/tests/helpers_test.yaml
git commit -m "feat(chart): envConfig helper and strict scalar values"
```

### Task 4B.2: configmap — kebab-case keys

**Files:**
- Create: `charts/tatara-memory/templates/configmap.yaml`
- Create: `charts/tatara-memory/tests/configmap_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: configmap
templates:
  - configmap.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: renders ConfigMap with kebab-case keys
    asserts:
      - isKind:
          of: ConfigMap
      - equal:
          path: metadata.name
          value: tm-tatara-memory
      - isNotNullOrEmpty:
          path: data.http-addr
      - isNotNullOrEmpty:
          path: data.lightrag-base-url
      - isNotNullOrEmpty:
          path: data.oidc-issuer
      - isNotNullOrEmpty:
          path: data.oidc-audience
      - isNotNullOrEmpty:
          path: data.worker-pool-size
      - isNotNullOrEmpty:
          path: data.log-level
      - isNotNullOrEmpty:
          path: data.pg-host
      - isNotNullOrEmpty:
          path: data.pg-port
      - isNotNullOrEmpty:
          path: data.pg-db
      - isNotNullOrEmpty:
          path: data.pg-user
  - it: rejects any camelCase keys leaking through
    asserts:
      - isNull:
          path: data.httpAddr
      - isNull:
          path: data.lightragBaseUrl
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/configmap_test.yaml`
Expected: FAIL ("could not find template" or rendering error).

- [ ] **Step 3: Write minimal implementation**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
data:
  {{- include "tatara-memory.envConfig" . | nindent 2 }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/configmap.yaml \
        charts/tatara-memory/tests/configmap_test.yaml
git commit -m "feat(chart): configmap with kebab-case keys"
```

### Task 4B.3: secret — kebab-case stub

**Files:**
- Create: `charts/tatara-memory/templates/secret.yaml`
- Create: `charts/tatara-memory/tests/secret_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: secret
templates:
  - secret.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: renders Secret with kebab-case keys when existingSecret is empty
    set:
      existingSecret: ""
    asserts:
      - isKind:
          of: Secret
      - equal:
          path: metadata.name
          value: tm-tatara-memory
      - isNotNullOrEmpty:
          path: data.pg-password
      - isNotNullOrEmpty:
          path: data.oidc-client-secret
  - it: skips Secret when existingSecret is set
    set:
      existingSecret: my-external-secret
    asserts:
      - hasDocuments:
          count: 0
  - it: never embeds plaintext password literal
    asserts:
      - notMatchRegex:
          path: data.pg-password
          pattern: "^plaintext"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/secret_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if not .Values.existingSecret }}
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
type: Opaque
data:
  # pg-password is a placeholder; real value lands from the cnpg
  # cluster's app secret (see deployment.yaml secretRef ordering)
  # or from a SOPS-encrypted values file at deploy time.
  pg-password: {{ "" | b64enc | quote }}
  oidc-client-secret: {{ .Values.oidcClientSecret | b64enc | quote }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/secret.yaml \
        charts/tatara-memory/tests/secret_test.yaml
git commit -m "feat(chart): secret stub with kebab-case keys and existingSecret bypass"
```

### Task 4B.4: serviceaccount — plain

**Files:**
- Create: `charts/tatara-memory/templates/serviceaccount.yaml`
- Create: `charts/tatara-memory/tests/serviceaccount_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: serviceaccount
templates:
  - serviceaccount.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: renders a plain ServiceAccount with no annotations
    asserts:
      - isKind:
          of: ServiceAccount
      - equal:
          path: metadata.name
          value: tm-tatara-memory
      - isNull:
          path: metadata.annotations
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/serviceaccount_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/serviceaccount.yaml \
        charts/tatara-memory/tests/serviceaccount_test.yaml
git commit -m "feat(chart): plain serviceaccount"
```

### Task 4B.5: service — ClusterIP on app port

**Files:**
- Create: `charts/tatara-memory/templates/service.yaml`
- Create: `charts/tatara-memory/tests/service_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: service
templates:
  - service.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: renders a Service exposing http on the app port
    asserts:
      - isKind:
          of: Service
      - equal:
          path: spec.type
          value: ClusterIP
      - equal:
          path: spec.ports[0].port
          value: 8080
      - equal:
          path: spec.ports[0].targetPort
          value: http
      - equal:
          path: spec.ports[0].name
          value: http
      - equal:
          path: spec.selector["app.kubernetes.io/name"]
          value: tatara-memory
      - equal:
          path: spec.selector["app.kubernetes.io/instance"]
          value: tm
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/service_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
spec:
  type: {{ .Values.service.type }}
  ports:
    - name: http
      port: {{ .Values.service.port }}
      targetPort: http
      protocol: TCP
  selector:
    {{- include "tatara-memory.selectorLabels" . | nindent 4 }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/service.yaml \
        charts/tatara-memory/tests/service_test.yaml
git commit -m "feat(chart): clusterip service on http port"
```

### Task 4B.6: deployment — envFrom-only wiring

**Files:**
- Create: `charts/tatara-memory/templates/deployment.yaml`
- Create: `charts/tatara-memory/tests/deployment_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: deployment
templates:
  - deployment.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: renders one container wired via envFrom only
    set:
      image.tag: "1.2.3"
    asserts:
      - isKind:
          of: Deployment
      - equal:
          path: spec.template.spec.containers[0].name
          value: tatara-memory
      - equal:
          path: spec.template.spec.containers[0].image
          value: harbor.szymonrichert.pl/tatara/tatara-memory:1.2.3
      - isNull:
          path: spec.template.spec.containers[0].env
      - equal:
          path: spec.template.spec.containers[0].envFrom[0].configMapRef.name
          value: tm-tatara-memory
      - equal:
          path: spec.template.spec.containers[0].envFrom[1].secretRef.name
          value: tm-tatara-memory
      - equal:
          path: spec.template.spec.containers[0].ports[0].containerPort
          value: 8080
      - equal:
          path: spec.template.spec.containers[0].ports[0].name
          value: http
      - equal:
          path: spec.template.spec.containers[0].livenessProbe.httpGet.path
          value: /healthz
      - equal:
          path: spec.template.spec.containers[0].readinessProbe.httpGet.path
          value: /readyz
      - equal:
          path: spec.template.spec.serviceAccountName
          value: tm-tatara-memory
  - it: uses existingSecret name when provided
    set:
      existingSecret: external-tm-secret
      image.tag: "1.2.3"
    asserts:
      - equal:
          path: spec.template.spec.containers[0].envFrom[1].secretRef.name
          value: external-tm-secret
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/deployment_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
spec:
  replicas: {{ .Values.replicaCount }}
  selector:
    matchLabels:
      {{- include "tatara-memory.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "tatara-memory.selectorLabels" . | nindent 8 }}
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
    spec:
      serviceAccountName: {{ include "tatara-memory.fullname" . }}
      containers:
        - name: tatara-memory
          image: "{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
          envFrom:
            - configMapRef:
                name: {{ include "tatara-memory.fullname" . }}
            - secretRef:
                name: {{ default (include "tatara-memory.fullname" .) .Values.existingSecret }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            {{- toYaml .Values.resources | nindent 12 }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/deployment.yaml \
        charts/tatara-memory/tests/deployment_test.yaml
git commit -m "feat(chart): deployment with envFrom-only wiring"
```

### Task 4B.7: ingress — cert-manager + nginx

**Files:**
- Create: `charts/tatara-memory/templates/ingress.yaml`
- Create: `charts/tatara-memory/tests/ingress_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: ingress
templates:
  - ingress.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: skipped when ingress.enabled is false
    set:
      ingress.enabled: false
    asserts:
      - hasDocuments:
          count: 0
  - it: renders an Ingress with cert-manager and nginx annotations
    set:
      ingress.enabled: true
      ingress.host: tm.example.test
      ingress.clusterIssuer: letsencrypt-prod
    asserts:
      - isKind:
          of: Ingress
      - equal:
          path: spec.ingressClassName
          value: nginx
      - equal:
          path: metadata.annotations["cert-manager.io/cluster-issuer"]
          value: letsencrypt-prod
      - equal:
          path: spec.rules[0].host
          value: tm.example.test
      - equal:
          path: spec.rules[0].http.paths[0].backend.service.name
          value: tm-tatara-memory
      - equal:
          path: spec.rules[0].http.paths[0].backend.service.port.name
          value: http
      - equal:
          path: spec.tls[0].hosts[0]
          value: tm.example.test
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/ingress_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.ingress.enabled }}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
  annotations:
    cert-manager.io/cluster-issuer: {{ .Values.ingress.clusterIssuer | quote }}
spec:
  ingressClassName: {{ .Values.ingress.className }}
  rules:
    - host: {{ .Values.ingress.host | quote }}
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: {{ include "tatara-memory.fullname" . }}
                port:
                  name: http
  tls:
    - hosts:
        - {{ .Values.ingress.host | quote }}
      secretName: {{ default (printf "%s-tls" (include "tatara-memory.fullname" .)) .Values.ingress.tlsSecretName }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/ingress.yaml \
        charts/tatara-memory/tests/ingress_test.yaml
git commit -m "feat(chart): cert-manager + nginx ingress"
```

### Task 4B.8: servicemonitor — prometheus-operator scrape

**Files:**
- Create: `charts/tatara-memory/templates/servicemonitor.yaml`
- Create: `charts/tatara-memory/tests/servicemonitor_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: servicemonitor
templates:
  - servicemonitor.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: skipped when disabled
    set:
      serviceMonitor.enabled: false
    asserts:
      - hasDocuments:
          count: 0
  - it: renders a ServiceMonitor selecting deployment labels
    set:
      serviceMonitor.enabled: true
      serviceMonitor.interval: "15s"
      serviceMonitor.scrapeTimeout: "10s"
    asserts:
      - isKind:
          of: ServiceMonitor
      - equal:
          path: spec.endpoints[0].port
          value: http
      - equal:
          path: spec.endpoints[0].path
          value: /metrics
      - equal:
          path: spec.endpoints[0].interval
          value: 15s
      - equal:
          path: spec.endpoints[0].scrapeTimeout
          value: 10s
      - equal:
          path: spec.selector.matchLabels["app.kubernetes.io/name"]
          value: tatara-memory
      - equal:
          path: spec.selector.matchLabels["app.kubernetes.io/instance"]
          value: tm
      - equal:
          path: spec.namespaceSelector.matchNames[0]
          value: tatara
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/servicemonitor_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.serviceMonitor.enabled }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
spec:
  namespaceSelector:
    matchNames:
      - {{ .Release.Namespace }}
  selector:
    matchLabels:
      {{- include "tatara-memory.selectorLabels" . | nindent 6 }}
  endpoints:
    - port: http
      path: /metrics
      interval: {{ .Values.serviceMonitor.interval }}
      scrapeTimeout: {{ .Values.serviceMonitor.scrapeTimeout }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/servicemonitor.yaml \
        charts/tatara-memory/tests/servicemonitor_test.yaml
git commit -m "feat(chart): servicemonitor scraping /metrics"
```

### Task 4B.9: networkpolicy — ingress from nginx + monitoring

**Files:**
- Create: `charts/tatara-memory/templates/networkpolicy.yaml`
- Create: `charts/tatara-memory/tests/networkpolicy_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: networkpolicy ingress
templates:
  - networkpolicy.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: skipped when disabled
    set:
      networkPolicy.enabled: false
    asserts:
      - hasDocuments:
          count: 0
  - it: allows ingress from ingress-nginx and monitoring namespaces
    set:
      networkPolicy.enabled: true
    asserts:
      - isKind:
          of: NetworkPolicy
      - equal:
          path: spec.policyTypes[0]
          value: Ingress
      - equal:
          path: spec.policyTypes[1]
          value: Egress
      - equal:
          path: spec.ingress[0].from[0].namespaceSelector.matchLabels["kubernetes.io/metadata.name"]
          value: ingress-nginx
      - equal:
          path: spec.ingress[0].from[1].namespaceSelector.matchLabels["kubernetes.io/metadata.name"]
          value: monitoring
      - equal:
          path: spec.ingress[0].ports[0].port
          value: 8080
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/networkpolicy_test.yaml`
Expected: FAIL.

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.networkPolicy.enabled }}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "tatara-memory.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "tatara-memory.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      {{- include "tatara-memory.selectorLabels" . | nindent 6 }}
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: ingress-nginx
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
      ports:
        - port: 8080
          protocol: TCP
  egress:
    - to:
        - podSelector:
            matchLabels:
              cnpg.io/cluster: {{ include "tatara-memory.fullname" . }}-database-cluster
      ports:
        - port: 5432
          protocol: TCP
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: neo4j
      ports:
        - port: 7687
          protocol: TCP
    - to:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: lightrag
      ports:
        - port: 9621
          protocol: TCP
    - to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - port: 53
          protocol: UDP
        - port: 53
          protocol: TCP
    - to:
        - namespaceSelector: {}
      ports:
        - port: 443
          protocol: TCP
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/templates/networkpolicy.yaml \
        charts/tatara-memory/tests/networkpolicy_test.yaml
git commit -m "feat(chart): networkpolicy ingress allowlist"
```

### Task 4B.10: networkpolicy — egress allowlist asserts

**Files:**
- Modify: `charts/tatara-memory/tests/networkpolicy_test.yaml`

- [ ] **Step 1: Write the failing test**

Append to the suite:

```yaml
  - it: allows egress to postgres on 5432
    set:
      networkPolicy.enabled: true
    asserts:
      - equal:
          path: spec.egress[0].to[0].podSelector.matchLabels["cnpg.io/cluster"]
          value: tm-tatara-memory-database-cluster
      - equal:
          path: spec.egress[0].ports[0].port
          value: 5432
  - it: allows egress to neo4j on 7687
    set:
      networkPolicy.enabled: true
    asserts:
      - equal:
          path: spec.egress[1].to[0].podSelector.matchLabels["app.kubernetes.io/name"]
          value: neo4j
      - equal:
          path: spec.egress[1].ports[0].port
          value: 7687
  - it: allows egress to lightrag on 9621
    set:
      networkPolicy.enabled: true
    asserts:
      - equal:
          path: spec.egress[2].to[0].podSelector.matchLabels["app.kubernetes.io/name"]
          value: lightrag
      - equal:
          path: spec.egress[2].ports[0].port
          value: 9621
  - it: allows DNS egress
    set:
      networkPolicy.enabled: true
    asserts:
      - equal:
          path: spec.egress[3].ports[0].port
          value: 53
      - equal:
          path: spec.egress[3].ports[0].protocol
          value: UDP
  - it: allows https egress for OIDC issuer
    set:
      networkPolicy.enabled: true
    asserts:
      - equal:
          path: spec.egress[4].ports[0].port
          value: 443
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/networkpolicy_test.yaml`
Expected: PASS (template covered both ingress and egress; if any path differs, fix the template).

- [ ] **Step 3: Write minimal implementation**

No change unless test fails; in that case adjust the egress block in `networkpolicy.yaml`.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/tests/networkpolicy_test.yaml
git commit -m "test(chart): assert egress allowlist for postgres, neo4j, lightrag, dns, oidc"
```

### Task 4B.11: deployment — secret leak guard

**Files:**
- Modify: `charts/tatara-memory/tests/deployment_test.yaml`
- Modify: `charts/tatara-memory/tests/secret_test.yaml`

- [ ] **Step 1: Write the failing test**

Add to `deployment_test.yaml`:

```yaml
  - it: never declares inline env
    asserts:
      - notExists:
          path: spec.template.spec.containers[0].env
  - it: passes no plaintext password through env
    documentSelector:
      path: kind
      value: Deployment
    asserts:
      - notMatchRegex:
          path: spec.template.spec.containers[0].envFrom
          pattern: "password"
```

Add to `secret_test.yaml`:

```yaml
  - it: does not reference plaintext literal in template source
    set:
      existingSecret: ""
      oidcClientSecret: ""
    asserts:
      - notMatchRegex:
          path: data.pg-password
          pattern: "(?i)(secret|password|admin|root)"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/deployment_test.yaml -f tests/secret_test.yaml`
Expected: PASS (template already complies). If FAIL, remove offending content from templates.

- [ ] **Step 3: Write minimal implementation**

No change unless tests fail.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/tests/deployment_test.yaml \
        charts/tatara-memory/tests/secret_test.yaml
git commit -m "test(chart): guard against inline env and plaintext secret leakage"
```

### Task 4B.12: render — every template parses

**Files:**
- Create: `charts/tatara-memory/tests/render_all_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: render all templates
templates:
  - configmap.yaml
  - secret.yaml
  - serviceaccount.yaml
  - service.yaml
  - deployment.yaml
  - ingress.yaml
  - servicemonitor.yaml
  - networkpolicy.yaml
release:
  name: tm
  namespace: tatara
tests:
  - it: every template renders without error with ingress and monitor enabled
    set:
      ingress.enabled: true
      ingress.host: tm.example.test
      serviceMonitor.enabled: true
      networkPolicy.enabled: true
      image.tag: "1.0.0"
    asserts:
      - hasDocuments:
          count: 1
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/render_all_test.yaml`
Expected: PASS once all prior tasks land. If any template fails to render, fix it now.

- [ ] **Step 3: Write minimal implementation**

No new code unless rendering fails. If kubeconform/lint reveals schema issues, patch the offending template.

- [ ] **Step 4: Run test to verify it passes**

```bash
helm dep update charts/tatara-memory
helm lint charts/tatara-memory
helm template tm charts/tatara-memory \
  --namespace tatara \
  --set ingress.enabled=true \
  --set ingress.host=tm.example.test \
  --set image.tag=1.0.0 | kubeconform -strict -summary
helm unittest charts/tatara-memory
```
Expected: lint clean, kubeconform clean, all unittests PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/tests/render_all_test.yaml
git commit -m "test(chart): smoke render all templates with ingress and monitor on"
```

### Task 4B.13: helmfile — release entry for tatara-memory

**Files:**
- Modify: `helmfile.yaml.gotmpl`

- [ ] **Step 1: Write the failing test**

```bash
helmfile -f helmfile.yaml.gotmpl lint
```
Expected: FAIL or no-op if no release exists yet. Add a snapshot expectation:

```bash
helmfile -f helmfile.yaml.gotmpl template --skip-deps | grep "kind: Deployment" | grep tatara-memory
```
Expected: empty output (no release yet).

- [ ] **Step 2: Run test to verify it fails**

Run the grep above.
Expected: no matches.

- [ ] **Step 3: Write minimal implementation**

Add to `helmfile.yaml.gotmpl`:

```yaml
releases:
  - name: tatara-memory
    chart: ./charts/tatara-memory
    namespace: tatara
    labels:
      purpose: tatara
      application: tatara-memory
    values:
      - values/tatara-memory/common.yaml
      - values/tatara-memory/{{`{{ .Environment.Name }}`}}.yaml
    secrets:
      - values/tatara-memory/{{`{{ .Environment.Name }}`}}.secrets.yaml
    missingFileHandler: Warn
```

If `repositories`, `environments`, and `helmDefaults` blocks already exist from Wave 1, only append the `releases` entry.

Create empty placeholder values files so `missingFileHandler: Warn` stays quiet:

```bash
mkdir -p values/tatara-memory
: > values/tatara-memory/common.yaml
: > values/tatara-memory/default.yaml
```

- [ ] **Step 4: Run test to verify it passes**

```bash
helm dep update charts/tatara-memory
helmfile -f helmfile.yaml.gotmpl lint
helmfile -f helmfile.yaml.gotmpl template --skip-deps | grep -c "kind: Deployment"
```
Expected: lint clean; at least one Deployment rendered.

- [ ] **Step 5: Commit**

```bash
git add helmfile.yaml.gotmpl values/tatara-memory/common.yaml values/tatara-memory/default.yaml
git commit -m "feat(helmfile): register tatara-memory release"
```

### Task 4B.14: verification — full chart sweep

**Files:**
- None (verification only)

- [ ] **Step 1: Write the failing test**

N/A — verification gate.

- [ ] **Step 2: Run test to verify it fails**

N/A.

- [ ] **Step 3: Write minimal implementation**

Run the full sweep.

- [ ] **Step 4: Run test to verify it passes**

```bash
helm dep update charts/tatara-memory
helm lint charts/tatara-memory
helm unittest charts/tatara-memory
helm template tm charts/tatara-memory --namespace tatara \
  --set ingress.enabled=true --set ingress.host=tm.example.test \
  --set image.tag=1.0.0 | kubeconform -strict -summary
helmfile -f helmfile.yaml.gotmpl lint
```
Expected: all green. Merge-ready state for Wave 6.

- [ ] **Step 5: Commit**

No commit; ready for Wave 6 merge.

```bash
git log --oneline -n 20
```
## Wave 5 — charts/tatara-memory/charts/lightrag

Runs in parallel with Wave 4. Single sonnet subagent in a dedicated git
worktree off `main`. Local commits only; integration back to `main` is
handled by the wave merge subagent.

Hard constraints carried into every task:

- Chart name is `lightrag` (parent chart `tatara-memory` namespaces it).
- Image pinned to
  `ghcr.io/hkuds/lightrag:v1.4.16@sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d`.
  Helper renders `repository:tag@digest`.
- `values.yaml` holds camelCase scalars only. No `env:` arrays, no
  lists, no `extraEnv`.
- Workload reads config via `envFrom: [configMapRef, secretRef]`.
  CamelCase value -> kebab-case key in ConfigMap/Secret. Mapping lives
  in `_helpers.tpl`.
- Genuinely list-shaped data renders into a templated ConfigMap and is
  read at runtime (no list values).
- All commits use Conventional Commits. Scope: `lightrag`.

### Task 5.1: scaffold — `helm create lightrag` and strip defaults

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/Chart.yaml`
- Create: `charts/tatara-memory/charts/lightrag/.helmignore`
- Create: `charts/tatara-memory/charts/lightrag/values.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/.gitkeep`
- Delete: every template under `charts/tatara-memory/charts/lightrag/templates/` produced by `helm create` (tests/, NOTES.txt, hpa.yaml, deployment.yaml, service.yaml, serviceaccount.yaml, ingress.yaml, _helpers.tpl)

- [ ] **Step 1: Write the failing test**

```yaml
# charts/tatara-memory/charts/lightrag/tests/chart_metadata_test.yaml
suite: chart metadata
templates:
  - Chart.yaml
tests:
  - it: chart name is the unprefixed subchart name
    documentSelector:
      path: name
      value: lightrag
    asserts:
      - equal:
          path: name
          value: lightrag
      - equal:
          path: type
          value: application
      - matchRegex:
          path: version
          pattern: "^0\\."
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd charts/tatara-memory/charts/lightrag && helm unittest . -f tests/chart_metadata_test.yaml`
Expected: FAIL with "no Chart.yaml found" or "chart not found".

- [ ] **Step 3: Write minimal implementation**

```bash
cd charts/tatara-memory/charts
helm create lightrag
# strip the boilerplate before any real work
rm -rf lightrag/templates/tests
rm -f  lightrag/templates/NOTES.txt
rm -f  lightrag/templates/hpa.yaml
rm -f  lightrag/templates/deployment.yaml
rm -f  lightrag/templates/service.yaml
rm -f  lightrag/templates/serviceaccount.yaml
rm -f  lightrag/templates/ingress.yaml
rm -f  lightrag/templates/_helpers.tpl
rm -f  lightrag/values.yaml
mkdir -p lightrag/tests
touch    lightrag/tests/.gitkeep
```

Then write `charts/tatara-memory/charts/lightrag/Chart.yaml`:

```yaml
apiVersion: v2
name: lightrag
description: LightRAG server (PGKVStorage + PGVectorStorage + Neo4JStorage). Local subchart of tatara-memory.
type: application
version: 0.1.0
appVersion: "1.4.16"
```

And write `charts/tatara-memory/charts/lightrag/values.yaml`:

```yaml
nameOverride: ""
fullnameOverride: ""

replicaCount: 1

image:
  repository: ghcr.io/hkuds/lightrag
  tag: v1.4.16
  digest: sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d
  pullPolicy: IfNotPresent
  pullSecretName: regcred

# LightRAG runtime configuration. Each scalar becomes a kebab-case
# key in the rendered ConfigMap and is exported into the pod via
# envFrom (UPPER_SNAKE upstream env name derived from the kebab key).
llmBinding: openai
llmModel: gpt-4.1-mini
embeddingBinding: openai
embeddingModel: text-embedding-3-small
embeddingDim: "1536"
kvStorage: PGKVStorage
vectorStorage: PGVectorStorage
graphStorage: Neo4JStorage
docStatusStorage: PGDocStatusStorage
neo4jUri: neo4j://tatara-neo4j:7687
maxAsync: "8"
maxParallelInsert: "8"
embeddingFuncMaxAsync: "8"
postgresHost: tatara-lightrag-database-cluster-rw
postgresPort: "5432"
postgresDatabase: lightrag
postgresUser: lightrag

# Secret references. When existingSecret is set the chart references it
# directly; when empty, the chart renders its own Secret using value.
secrets:
  openai:
    existingSecret: ""
    existingSecretKey: api-key
    value: ""
  postgres:
    existingSecret: tatara-lightrag-database-cluster-app
    existingSecretKey: password
    value: ""
  neo4j:
    existingSecret: tatara-neo4j-password
    existingSecretKey: password
    value: ""

service:
  type: ClusterIP
  port: 9621

ingress:
  enabled: false
  className: nginx
  host: ""
  pathPrefix: /lightrag
  tlsSecretName: ""
  certManagerClusterIssuer: letsencrypt-prod
  whitelistSourceRange: ""

persistence:
  enabled: true
  storageClass: rook-ceph
  accessMode: ReadWriteOnce
  size: 10Gi
  existingClaim: ""

resources:
  requestsCpu: 200m
  requestsMemory: 512Mi
  limitsMemory: 1Gi

serviceAccount:
  create: true
  name: ""

networkPolicy:
  enabled: true
  postgresPort: 5432
  neo4jPort: 7687

serviceMonitor:
  enabled: false
  path: /metrics
  interval: 30s
  scrapeTimeout: 10s
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd charts/tatara-memory/charts/lightrag && helm unittest . -f tests/chart_metadata_test.yaml`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): scaffold subchart with pinned image values"
```

### Task 5.2: `_helpers.tpl` — names, labels, camelCase to kebab-case map, image ref

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/_helpers.tpl`
- Create: `charts/tatara-memory/charts/lightrag/tests/helpers_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
# charts/tatara-memory/charts/lightrag/tests/helpers_test.yaml
suite: helpers
templates:
  - templates/_helpers.tpl
tests:
  - it: helpers file alone renders nothing
    asserts:
      - hasDocuments:
          count: 0
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory/charts/lightrag -f tests/helpers_test.yaml`
Expected: FAIL with "template: templates/_helpers.tpl: no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{/*
Subchart name. Parent chart already namespaces; we use just "lightrag".
*/}}
{{- define "lightrag.name" -}}
{{- default "lightrag" .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lightrag.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "lightrag.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "lightrag.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "lightrag.labels" -}}
helm.sh/chart: {{ include "lightrag.chart" . }}
{{ include "lightrag.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: lightrag
app.kubernetes.io/part-of: tatara-memory
{{- end }}

{{- define "lightrag.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lightrag.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Pinned image: repository:tag@digest. Digest must be present.
*/}}
{{- define "lightrag.image" -}}
{{- $r := required "image.repository required" .Values.image.repository -}}
{{- $t := required "image.tag required" .Values.image.tag -}}
{{- $d := required "image.digest required (must be sha256:...)" .Values.image.digest -}}
{{- printf "%s:%s@%s" $r $t $d -}}
{{- end }}

{{- define "lightrag.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "lightrag.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end }}

{{/*
Non-secret config keys. camelCase values.yaml key -> kebab-case
ConfigMap key. The deployment mounts the ConfigMap via envFrom with
no prefix, so upstream LightRAG env names map 1:1 by uppercasing
and replacing "-" with "_" downstream. We render kebab-case here
because that is the project-wide convention.
*/}}
{{- define "lightrag.configKeys" -}}
llm-binding: {{ .Values.llmBinding | quote }}
llm-model: {{ .Values.llmModel | quote }}
embedding-binding: {{ .Values.embeddingBinding | quote }}
embedding-model: {{ .Values.embeddingModel | quote }}
embedding-dim: {{ .Values.embeddingDim | quote }}
kv-storage: {{ .Values.kvStorage | quote }}
vector-storage: {{ .Values.vectorStorage | quote }}
graph-storage: {{ .Values.graphStorage | quote }}
doc-status-storage: {{ .Values.docStatusStorage | quote }}
neo4j-uri: {{ .Values.neo4jUri | quote }}
max-async: {{ .Values.maxAsync | quote }}
max-parallel-insert: {{ .Values.maxParallelInsert | quote }}
embedding-func-max-async: {{ .Values.embeddingFuncMaxAsync | quote }}
postgres-host: {{ .Values.postgresHost | quote }}
postgres-port: {{ .Values.postgresPort | quote }}
postgres-database: {{ .Values.postgresDatabase | quote }}
postgres-user: {{ .Values.postgresUser | quote }}
{{- end }}

{{/*
Secret resolution: when a secrets.<x>.existingSecret is set, point at
it; otherwise point at our own rendered Secret with the canonical key.
*/}}
{{- define "lightrag.secretName" -}}
{{- include "lightrag.fullname" . -}}
{{- end }}

{{- define "lightrag.openaiSecretName" -}}
{{- default (include "lightrag.secretName" .) .Values.secrets.openai.existingSecret -}}
{{- end }}
{{- define "lightrag.openaiSecretKey" -}}
{{- default "lightrag-openai-api-key" .Values.secrets.openai.existingSecretKey -}}
{{- end }}

{{- define "lightrag.postgresSecretName" -}}
{{- default (include "lightrag.secretName" .) .Values.secrets.postgres.existingSecret -}}
{{- end }}
{{- define "lightrag.postgresSecretKey" -}}
{{- default "postgres-password" .Values.secrets.postgres.existingSecretKey -}}
{{- end }}

{{- define "lightrag.neo4jSecretName" -}}
{{- default (include "lightrag.secretName" .) .Values.secrets.neo4j.existingSecret -}}
{{- end }}
{{- define "lightrag.neo4jSecretKey" -}}
{{- default "neo4j-password" .Values.secrets.neo4j.existingSecretKey -}}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `helm unittest charts/tatara-memory/charts/lightrag -f tests/helpers_test.yaml`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add helpers with pinned image ref and kebab-case config map"
```

### Task 5.3: `serviceaccount.yaml` — minimal SA

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/serviceaccount.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/serviceaccount_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: serviceaccount
templates:
  - templates/serviceaccount.yaml
tests:
  - it: renders by default
    asserts:
      - isKind:
          of: ServiceAccount
      - equal:
          path: metadata.name
          value: RELEASE-NAME-lightrag
  - it: skips when serviceAccount.create=false
    set:
      serviceAccount:
        create: false
    asserts:
      - hasDocuments:
          count: 0
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory/charts/lightrag -f tests/serviceaccount_test.yaml`
Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.serviceAccount.create -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "lightrag.serviceAccountName" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add ServiceAccount template"
```

### Task 5.4: `configmap.yaml` — kebab-case non-secret config

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/configmap.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/configmap_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: configmap
templates:
  - templates/configmap.yaml
tests:
  - it: renders all kebab-case lightrag config keys
    asserts:
      - isKind:
          of: ConfigMap
      - equal:
          path: metadata.name
          value: RELEASE-NAME-lightrag
      - isNotNullOrEmpty:
          path: data.llm-binding
      - isNotNullOrEmpty:
          path: data.llm-model
      - isNotNullOrEmpty:
          path: data.embedding-binding
      - isNotNullOrEmpty:
          path: data.embedding-model
      - isNotNullOrEmpty:
          path: data.embedding-dim
      - isNotNullOrEmpty:
          path: data.kv-storage
      - isNotNullOrEmpty:
          path: data.vector-storage
      - isNotNullOrEmpty:
          path: data.graph-storage
      - isNotNullOrEmpty:
          path: data.doc-status-storage
      - isNotNullOrEmpty:
          path: data.neo4j-uri
      - isNotNullOrEmpty:
          path: data.max-async
      - isNotNullOrEmpty:
          path: data.max-parallel-insert
      - isNotNullOrEmpty:
          path: data.embedding-func-max-async
      - isNotNullOrEmpty:
          path: data.postgres-host
      - isNotNullOrEmpty:
          path: data.postgres-port
      - isNotNullOrEmpty:
          path: data.postgres-database
      - isNotNullOrEmpty:
          path: data.postgres-user
      - equal:
          path: data.kv-storage
          value: PGKVStorage
  - it: rejects unexpected camelCase keys (sanity guard)
    asserts:
      - isNull:
          path: data.llmBinding
      - isNull:
          path: data.kvStorage
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory/charts/lightrag -f tests/configmap_test.yaml`
Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "lightrag.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
data:
  {{- include "lightrag.configKeys" . | nindent 2 }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add ConfigMap with kebab-case runtime keys"
```

### Task 5.5: `secret.yaml` — only rendered when no existingSecret provided

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/secret.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/secret_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: secret
templates:
  - templates/secret.yaml
tests:
  - it: no Secret rendered when all three existingSecret values are set (defaults)
    asserts:
      - hasDocuments:
          count: 0

  - it: renders a Secret when openai existingSecret is empty and value is supplied
    set:
      secrets:
        openai:
          existingSecret: ""
          existingSecretKey: lightrag-openai-api-key
          value: sk-test-not-real
    asserts:
      - isKind:
          of: Secret
      - equal:
          path: type
          value: Opaque
      - isNotNullOrEmpty:
          path: data.lightrag-openai-api-key
      # Plaintext value must not appear; only base64 in data.
      - notMatchRegex:
          path: stringData
          pattern: "sk-test-not-real"

  - it: kebab-case keys only, no camelCase leakage
    set:
      secrets:
        openai:
          existingSecret: ""
          value: sk-x
        postgres:
          existingSecret: ""
          value: pw-x
        neo4j:
          existingSecret: ""
          value: pw-y
    asserts:
      - isNotNullOrEmpty:
          path: data.lightrag-openai-api-key
      - isNotNullOrEmpty:
          path: data.postgres-password
      - isNotNullOrEmpty:
          path: data.neo4j-password
      - isNull:
          path: data.openaiApiKey
      - isNull:
          path: data.postgresPassword
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- $needOpenai   := and (not .Values.secrets.openai.existingSecret)   (ne (.Values.secrets.openai.value   | toString) "") -}}
{{- $needPostgres := and (not .Values.secrets.postgres.existingSecret) (ne (.Values.secrets.postgres.value | toString) "") -}}
{{- $needNeo4j    := and (not .Values.secrets.neo4j.existingSecret)    (ne (.Values.secrets.neo4j.value    | toString) "") -}}
{{- if or $needOpenai $needPostgres $needNeo4j -}}
apiVersion: v1
kind: Secret
metadata:
  name: {{ include "lightrag.secretName" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
type: Opaque
data:
  {{- if $needOpenai }}
  lightrag-openai-api-key: {{ .Values.secrets.openai.value | b64enc | quote }}
  {{- end }}
  {{- if $needPostgres }}
  postgres-password: {{ .Values.secrets.postgres.value | b64enc | quote }}
  {{- end }}
  {{- if $needNeo4j }}
  neo4j-password: {{ .Values.secrets.neo4j.value | b64enc | quote }}
  {{- end }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add optional Secret with kebab-case credential keys"
```

### Task 5.6: `pvc.yaml` — local working directory storage

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/pvc.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/pvc_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: pvc
templates:
  - templates/pvc.yaml
tests:
  - it: renders by default
    asserts:
      - isKind:
          of: PersistentVolumeClaim
      - equal:
          path: metadata.name
          value: RELEASE-NAME-lightrag-data
      - equal:
          path: spec.accessModes[0]
          value: ReadWriteOnce
      - equal:
          path: spec.resources.requests.storage
          value: 10Gi
      - equal:
          path: spec.storageClassName
          value: rook-ceph
  - it: skipped when persistence.enabled=false
    set:
      persistence:
        enabled: false
    asserts:
      - hasDocuments:
          count: 0
  - it: skipped when persistence.existingClaim is set
    set:
      persistence:
        existingClaim: some-other-pvc
    asserts:
      - hasDocuments:
          count: 0
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if and .Values.persistence.enabled (not .Values.persistence.existingClaim) -}}
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ include "lightrag.fullname" . }}-data
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
spec:
  accessModes:
  - {{ .Values.persistence.accessMode }}
  storageClassName: {{ .Values.persistence.storageClass }}
  resources:
    requests:
      storage: {{ .Values.persistence.size }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add PVC template for local working directory"
```

### Task 5.7: `service.yaml` — ClusterIP on 9621

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/service.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/service_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: service
templates:
  - templates/service.yaml
tests:
  - it: renders a ClusterIP service on port 9621 named http
    asserts:
      - isKind:
          of: Service
      - equal:
          path: spec.type
          value: ClusterIP
      - equal:
          path: spec.ports[0].port
          value: 9621
      - equal:
          path: spec.ports[0].name
          value: http
      - equal:
          path: spec.ports[0].targetPort
          value: http
      - equal:
          path: spec.selector["app.kubernetes.io/name"]
          value: lightrag
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "lightrag.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
spec:
  type: {{ .Values.service.type }}
  selector:
    {{- include "lightrag.selectorLabels" . | nindent 4 }}
  ports:
  - name: http
    port: {{ .Values.service.port }}
    targetPort: http
    protocol: TCP
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add Service template"
```

### Task 5.8: `deployment.yaml` — pinned digest, envFrom, RWO-safe Recreate, probes

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/deployment.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/deployment_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: deployment
templates:
  - templates/deployment.yaml
tests:
  - it: uses pinned digest image string
    asserts:
      - equal:
          path: spec.template.spec.containers[0].image
          value: ghcr.io/hkuds/lightrag:v1.4.16@sha256:67ccf8d9f74eb29da872bf8b3e6513605f5ac601fb0509c5b0ca16d98d2d307d
      - matchRegex:
          path: spec.template.spec.containers[0].image
          pattern: "@sha256:[0-9a-f]{64}$"

  - it: uses envFrom with both configmap and secret refs and no env array
    asserts:
      - equal:
          path: spec.template.spec.containers[0].envFrom[0].configMapRef.name
          value: RELEASE-NAME-lightrag
      - contains:
          path: spec.template.spec.containers[0].envFrom
          content:
            secretRef:
              name: RELEASE-NAME-lightrag
      - isNull:
          path: spec.template.spec.containers[0].env

  - it: uses Recreate strategy because PVC is RWO
    asserts:
      - equal:
          path: spec.strategy.type
          value: Recreate

  - it: container port 9621 named http
    asserts:
      - equal:
          path: spec.template.spec.containers[0].ports[0].containerPort
          value: 9621
      - equal:
          path: spec.template.spec.containers[0].ports[0].name
          value: http

  - it: probes target the http port
    asserts:
      - equal:
          path: spec.template.spec.containers[0].startupProbe.tcpSocket.port
          value: http
      - equal:
          path: spec.template.spec.containers[0].livenessProbe.tcpSocket.port
          value: http
      - equal:
          path: spec.template.spec.containers[0].readinessProbe.tcpSocket.port
          value: http

  - it: existing secrets are used when set (default values)
    asserts:
      - contains:
          path: spec.template.spec.containers[0].envFrom
          content:
            secretRef:
              name: RELEASE-NAME-lightrag
        not: true

  - it: imagePullSecrets honoured when pullSecretName set
    asserts:
      - equal:
          path: spec.template.spec.imagePullSecrets[0].name
          value: regcred

  - it: does not leak plaintext secret values
    set:
      secrets:
        openai:
          value: sk-leak-canary
    asserts:
      - notMatchRegex:
          path: spec.template.spec
          pattern: "sk-leak-canary"
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

When `existingSecret` is configured for any of the three credential
secrets the workload needs the env values to come from there, not from
the chart-rendered Secret. The simplest envFrom-only pattern is: render
each external secret into the same kebab-case keys via a stub
ExternalSecret-style projection, OR resolve everything into a single
envFrom Secret that we own. We pick the latter: when any of
`secrets.*.existingSecret` is set, the rendered Secret stays empty and
the deployment additionally references each existing secret via
`envFrom: secretRef`. All three are kebab-case-keyed by contract.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "lightrag.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
spec:
  replicas: {{ .Values.replicaCount }}
  # PVC is RWO with replicaCount=1; Recreate avoids the
  # "new pod cannot attach because old still holds the volume" deadlock.
  strategy:
    type: Recreate
  selector:
    matchLabels:
      {{- include "lightrag.selectorLabels" . | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "lightrag.selectorLabels" . | nindent 8 }}
      annotations:
        checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
    spec:
      serviceAccountName: {{ include "lightrag.serviceAccountName" . }}
      {{- if .Values.image.pullSecretName }}
      imagePullSecrets:
      - name: {{ .Values.image.pullSecretName }}
      {{- end }}
      containers:
      - name: lightrag
        image: {{ include "lightrag.image" . | quote }}
        imagePullPolicy: {{ .Values.image.pullPolicy }}
        ports:
        - name: http
          containerPort: 9621
          protocol: TCP
        envFrom:
        - configMapRef:
            name: {{ include "lightrag.fullname" . }}
        - secretRef:
            name: {{ include "lightrag.openaiSecretName" . }}
        {{- $pg := include "lightrag.postgresSecretName" . }}
        {{- if ne $pg (include "lightrag.openaiSecretName" .) }}
        - secretRef:
            name: {{ $pg }}
        {{- end }}
        {{- $n4 := include "lightrag.neo4jSecretName" . }}
        {{- if and (ne $n4 (include "lightrag.openaiSecretName" .)) (ne $n4 $pg) }}
        - secretRef:
            name: {{ $n4 }}
        {{- end }}
        startupProbe:
          tcpSocket:
            port: http
          periodSeconds: 5
          failureThreshold: 60
        livenessProbe:
          tcpSocket:
            port: http
          periodSeconds: 30
          failureThreshold: 3
        readinessProbe:
          tcpSocket:
            port: http
          periodSeconds: 10
          failureThreshold: 3
        resources:
          requests:
            cpu: {{ .Values.resources.requestsCpu }}
            memory: {{ .Values.resources.requestsMemory }}
          limits:
            memory: {{ .Values.resources.limitsMemory }}
        volumeMounts:
        - name: data
          mountPath: /app/data
      volumes:
      - name: data
        {{- if .Values.persistence.enabled }}
        persistentVolumeClaim:
          claimName: {{ .Values.persistence.existingClaim | default (printf "%s-data" (include "lightrag.fullname" .)) }}
        {{- else }}
        emptyDir: {}
        {{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add Deployment with pinned digest and envFrom-only config"
```

### Task 5.9: `ingress.yaml` — disabled by default, sub_filter rewrites preserved

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/ingress.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/ingress_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: ingress
templates:
  - templates/ingress.yaml
tests:
  - it: disabled by default
    asserts:
      - hasDocuments:
          count: 0

  - it: renders when enabled with host and tls
    set:
      ingress:
        enabled: true
        className: nginx
        host: lightrag.example.com
        pathPrefix: /lightrag
        tlsSecretName: lightrag-tls
    asserts:
      - isKind:
          of: Ingress
      - equal:
          path: spec.ingressClassName
          value: nginx
      - equal:
          path: spec.rules[0].host
          value: lightrag.example.com
      - equal:
          path: spec.tls[0].secretName
          value: lightrag-tls
      - matchRegex:
          path: metadata.annotations["nginx.ingress.kubernetes.io/configuration-snippet"]
          pattern: "sub_filter '\"/webui'"
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.ingress.enabled -}}
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ include "lightrag.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
  annotations:
    cert-manager.io/cluster-issuer: {{ .Values.ingress.certManagerClusterIssuer | quote }}
    nginx.ingress.kubernetes.io/rewrite-target: /$2
    nginx.ingress.kubernetes.io/use-regex: "true"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "120"
    {{- with .Values.ingress.whitelistSourceRange }}
    nginx.ingress.kubernetes.io/whitelist-source-range: {{ . | quote }}
    {{- end }}
    nginx.ingress.kubernetes.io/configuration-snippet: |
      sub_filter_once off;
      sub_filter_types text/html application/javascript;
      sub_filter '"/webui' '"{{ .Values.ingress.pathPrefix }}/webui';
      sub_filter '"/auth-status' '"{{ .Values.ingress.pathPrefix }}/auth-status';
      sub_filter '"/login' '"{{ .Values.ingress.pathPrefix }}/login';
      sub_filter '"/query' '"{{ .Values.ingress.pathPrefix }}/query';
      sub_filter '"/documents' '"{{ .Values.ingress.pathPrefix }}/documents';
      sub_filter '"/graph' '"{{ .Values.ingress.pathPrefix }}/graph';
      sub_filter '"/health' '"{{ .Values.ingress.pathPrefix }}/health';
      sub_filter '"/docs' '"{{ .Values.ingress.pathPrefix }}/docs';
      sub_filter '`/auth-status' '`{{ .Values.ingress.pathPrefix }}/auth-status';
      sub_filter '`/login' '`{{ .Values.ingress.pathPrefix }}/login';
      sub_filter '`/query' '`{{ .Values.ingress.pathPrefix }}/query';
      sub_filter '`/documents' '`{{ .Values.ingress.pathPrefix }}/documents';
      sub_filter '`/graph' '`{{ .Values.ingress.pathPrefix }}/graph';
      sub_filter '`/graphs' '`{{ .Values.ingress.pathPrefix }}/graphs';
      sub_filter '`/health' '`{{ .Values.ingress.pathPrefix }}/health';
      sub_filter '`/docs' '`{{ .Values.ingress.pathPrefix }}/docs';
spec:
  ingressClassName: {{ .Values.ingress.className | quote }}
  rules:
  - host: {{ .Values.ingress.host | quote }}
    http:
      paths:
      - path: {{ .Values.ingress.pathPrefix }}(/|$)(.*)
        pathType: ImplementationSpecific
        backend:
          service:
            name: {{ include "lightrag.fullname" . }}
            port:
              name: http
  tls:
  - hosts:
    - {{ .Values.ingress.host | quote }}
    secretName: {{ .Values.ingress.tlsSecretName | quote }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add Ingress template, disabled by default"
```

### Task 5.10: `networkpolicy.yaml` — explicit egress, ingress from tatara-memory

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/networkpolicy.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/networkpolicy_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: networkpolicy
templates:
  - templates/networkpolicy.yaml
tests:
  - it: renders by default
    asserts:
      - isKind:
          of: NetworkPolicy
      - equal:
          path: spec.podSelector.matchLabels["app.kubernetes.io/name"]
          value: lightrag
      - contains:
          path: spec.policyTypes
          content: Ingress
      - contains:
          path: spec.policyTypes
          content: Egress

  - it: ingress allows tatara-memory pod within same namespace
    asserts:
      - equal:
          path: spec.ingress[0].from[0].podSelector.matchLabels["app.kubernetes.io/name"]
          value: tatara-memory
      - equal:
          path: spec.ingress[0].ports[0].port
          value: 9621

  - it: egress permits postgres 5432 and neo4j 7687 in-cluster
    asserts:
      - contains:
          path: spec.egress[0].ports
          content:
            port: 5432
            protocol: TCP
      - contains:
          path: spec.egress[1].ports
          content:
            port: 7687
            protocol: TCP

  - it: egress permits DNS and external https
    asserts:
      - contains:
          path: spec.egress[2].ports
          content:
            port: 53
            protocol: UDP
      - contains:
          path: spec.egress[3].ports
          content:
            port: 443
            protocol: TCP

  - it: skipped when networkPolicy.enabled=false
    set:
      networkPolicy:
        enabled: false
    asserts:
      - hasDocuments:
          count: 0
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.networkPolicy.enabled -}}
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: {{ include "lightrag.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
spec:
  podSelector:
    matchLabels:
      {{- include "lightrag.selectorLabels" . | nindent 6 }}
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app.kubernetes.io/name: tatara-memory
    ports:
    - port: 9621
      protocol: TCP
  egress:
  # PostgreSQL (cnpg-cluster in-namespace).
  - to:
    - podSelector:
        matchLabels:
          cnpg.io/cluster: tatara-lightrag-database-cluster
    ports:
    - port: {{ .Values.networkPolicy.postgresPort }}
      protocol: TCP
  # Neo4j in-namespace.
  - to:
    - podSelector:
        matchLabels:
          app.kubernetes.io/name: neo4j
    ports:
    - port: {{ .Values.networkPolicy.neo4jPort }}
      protocol: TCP
  # DNS (cluster).
  - ports:
    - port: 53
      protocol: UDP
    - port: 53
      protocol: TCP
  # Outbound HTTPS for OpenAI / Anthropic API.
  - ports:
    - port: 443
      protocol: TCP
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add NetworkPolicy with scoped ingress and egress"
```

### Task 5.11: `servicemonitor.yaml` — prometheus-operator scrape, opt-in

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/templates/servicemonitor.yaml`
- Create: `charts/tatara-memory/charts/lightrag/tests/servicemonitor_test.yaml`

LightRAG upstream serves on port 9621 over the same HTTP listener;
v1.4.x does not consistently ship `/metrics`. We render a ServiceMonitor
that scrapes the http port at the configured path, default disabled so
that operators opt in once they have metrics enabled upstream.

- [ ] **Step 1: Write the failing test**

```yaml
suite: servicemonitor
templates:
  - templates/servicemonitor.yaml
tests:
  - it: disabled by default
    asserts:
      - hasDocuments:
          count: 0

  - it: renders when enabled and targets http port
    set:
      serviceMonitor:
        enabled: true
        path: /metrics
        interval: 30s
        scrapeTimeout: 10s
    asserts:
      - isKind:
          of: ServiceMonitor
      - equal:
          path: spec.endpoints[0].port
          value: http
      - equal:
          path: spec.endpoints[0].path
          value: /metrics
      - equal:
          path: spec.endpoints[0].interval
          value: 30s
      - equal:
          path: spec.endpoints[0].scrapeTimeout
          value: 10s
      - equal:
          path: spec.selector.matchLabels["app.kubernetes.io/name"]
          value: lightrag
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "no such template".

- [ ] **Step 3: Write minimal implementation**

```yaml
{{- if .Values.serviceMonitor.enabled -}}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: {{ include "lightrag.fullname" . }}
  namespace: {{ .Release.Namespace }}
  labels:
    {{- include "lightrag.labels" . | nindent 4 }}
spec:
  selector:
    matchLabels:
      {{- include "lightrag.selectorLabels" . | nindent 6 }}
  namespaceSelector:
    matchNames:
    - {{ .Release.Namespace }}
  endpoints:
  - port: http
    path: {{ .Values.serviceMonitor.path | quote }}
    interval: {{ .Values.serviceMonitor.interval }}
    scrapeTimeout: {{ .Values.serviceMonitor.scrapeTimeout }}
{{- end }}
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "feat(lightrag): add opt-in ServiceMonitor for prom-operator"
```

### Task 5.12: full-render smoke — every template renders against default values

**Files:**
- Create: `charts/tatara-memory/charts/lightrag/tests/render_all_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
suite: render-all-smoke
tests:
  - it: deployment renders and uses pinned digest
    template: templates/deployment.yaml
    asserts:
      - hasDocuments:
          count: 1
      - matchRegex:
          path: spec.template.spec.containers[0].image
          pattern: "@sha256:[0-9a-f]{64}$"

  - it: service renders
    template: templates/service.yaml
    asserts:
      - hasDocuments:
          count: 1

  - it: configmap renders with kebab-case keys only
    template: templates/configmap.yaml
    asserts:
      - hasDocuments:
          count: 1
      - isNotNullOrEmpty:
          path: data.kv-storage

  - it: pvc renders by default
    template: templates/pvc.yaml
    asserts:
      - hasDocuments:
          count: 1

  - it: serviceaccount renders by default
    template: templates/serviceaccount.yaml
    asserts:
      - hasDocuments:
          count: 1

  - it: networkpolicy renders by default
    template: templates/networkpolicy.yaml
    asserts:
      - hasDocuments:
          count: 1

  - it: ingress not rendered by default
    template: templates/ingress.yaml
    asserts:
      - hasDocuments:
          count: 0

  - it: servicemonitor not rendered by default
    template: templates/servicemonitor.yaml
    asserts:
      - hasDocuments:
          count: 0

  - it: secret not rendered when all existingSecret are set (defaults)
    template: templates/secret.yaml
    asserts:
      - hasDocuments:
          count: 0
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory/charts/lightrag -f tests/render_all_test.yaml`
Expected: FAIL only if any earlier template was missed. If all earlier
tasks shipped, this should pass on the first run; treat that as the
gate that proves the chart renders cohesively.

- [ ] **Step 3: Write minimal implementation**

No new templates. This task is the integration smoke test.

- [ ] **Step 4: Run test to verify it passes**

Run: `helm unittest charts/tatara-memory/charts/lightrag` (all suites).
Expected: PASS across every suite.

Also run: `helm lint charts/tatara-memory/charts/lightrag`
Expected: 1 chart(s) linted, 0 chart(s) failed.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/charts/lightrag/
git commit -m "test(lightrag): add full-render smoke covering all templates"
```

### Task 5.13: parent chart wiring — `dependencies` entry, `helm dep update`, parent lint

**Files:**
- Edit: `charts/tatara-memory/Chart.yaml`
- Create: `charts/tatara-memory/tests/lightrag_dependency_test.yaml`

- [ ] **Step 1: Write the failing test**

```yaml
# charts/tatara-memory/tests/lightrag_dependency_test.yaml
suite: parent chart wires lightrag
templates:
  - Chart.yaml
tests:
  - it: parent depends on local lightrag subchart
    documentSelector:
      path: name
      value: tatara-memory
    asserts:
      - contains:
          path: dependencies
          content:
            name: lightrag
            version: 0.1.0
            repository: file://charts/lightrag
```

- [ ] **Step 2: Run test to verify it fails**

Run: `helm unittest charts/tatara-memory -f tests/lightrag_dependency_test.yaml`
Expected: FAIL because `dependencies` either lacks a `lightrag` entry
or points at a different repository.

- [ ] **Step 3: Write minimal implementation**

Add (or update) the `dependencies` section in
`charts/tatara-memory/Chart.yaml`. The wave-1 chart already lists
cnpg-cluster and neo4j; we add `lightrag`:

```yaml
dependencies:
  - name: cluster
    alias: cnpg-cluster
    version: 0.6.1
    repository: https://cloudnative-pg.github.io/charts
  - name: neo4j
    version: 2026.4.0
    repository: https://helm.neo4j.com/neo4j
  - name: lightrag
    version: 0.1.0
    repository: file://charts/lightrag
```

Then refresh and lint:

```bash
helm dep update charts/tatara-memory
helm lint        charts/tatara-memory
helm unittest    charts/tatara-memory
helm unittest    charts/tatara-memory/charts/lightrag
```

- [ ] **Step 4: Run test to verify it passes**

Run the four commands above. Expected:

- `helm dep update`: writes `charts/tatara-memory/Chart.lock`, vendors
  `cluster-0.6.1.tgz`, `neo4j-2026.4.0.tgz`. Local `lightrag` is used
  in-place (no tgz).
- `helm lint`: `1 chart(s) linted, 0 chart(s) failed`.
- `helm unittest charts/tatara-memory`: dependency suite PASS.
- `helm unittest charts/tatara-memory/charts/lightrag`: all suites PASS.

- [ ] **Step 5: Commit**

```bash
git add charts/tatara-memory/Chart.yaml \
        charts/tatara-memory/Chart.lock \
        charts/tatara-memory/tests/lightrag_dependency_test.yaml
git commit -m "feat(tatara-memory): wire lightrag local subchart dependency"
```
## Wave 6 — Integration merge (single opus subagent)

Runs after each prior wave completes in worktrees. Owns the merge to `main`. Cannot be parallelised — the merge subagent is the serialisation point.

### Task 6.1: Merge a wave's worktrees back to main

Repeat this loop once per wave (1 → 2 → 3 → 4+5 → ...). For waves with multiple parallel subagents (2, 3, 4+5), each subagent has its own worktree; the merge is done in order: 2A → 2B → 2C, then 3A → 3B, etc.

**Files:**
- Modify: numerous (whatever each worktree changed)

- [ ] **Step 1: Confirm the wave's subagents are done**

For each worktree the wave spawned, confirm the subagent ran its
final verification gate. The wave's last-task verification commands
in this plan are the contract.

- [ ] **Step 2: Switch to main**

```bash
cd ~/Documents/tatara/tatara-memory
git checkout main
git pull --ff-only origin main || true
```

- [ ] **Step 3: Merge first worktree**

For worktree branch `wave-NX-<name>` (e.g. `wave-2a-obs`):

```bash
git merge --no-ff wave-2a-obs -m "merge: wave 2A (internal/obs)"
```

If conflicts arise, resolve in this order of preference:
1. `go.mod` / `go.sum`: take the union; run `go mod tidy` after.
2. `Chart.yaml` `dependencies:` list: take the union, preserve sort order.
3. `cmd/tatara-memory/main.go` imports: take the union.
4. `Makefile`: take the union of targets.
5. Code conflicts: stop, investigate, do not paper over.

- [ ] **Step 4: Run the integration gate after the merge**

```bash
go mod tidy
make lint test build
helm lint charts/tatara-memory
helm unittest charts/tatara-memory || true   # may have no tests yet; acceptable warning
helmfile lint
```

Expected: every command PASS. Fix anything that broke. **Do not amend a previous wave's commits** — fix forward with a new commit on `main`.

- [ ] **Step 5: Repeat for the next worktree in the wave**

Merge the next worktree, re-run the gate. Continue until every worktree in the wave is merged.

- [ ] **Step 6: Tag the wave boundary**

```bash
git tag wave-N-merged
```

(Where N is the wave number. Tags make rollback boundaries explicit.)

- [ ] **Step 7: Clean up worktrees**

```bash
for wt in $(git worktree list --porcelain | awk '/^worktree/ {print $2}' | grep -v "^$(pwd)$"); do
  git worktree remove "$wt"
done
git branch -D wave-2a-obs wave-2b-auth wave-2c-lightrag  # adjust per wave
```

- [ ] **Step 8: Push main**

```bash
git push origin main
git push origin wave-N-merged
```

### Task 6.2: Final integration check after all waves merged

After waves 1 through 5 are all on `main`:

- [ ] **Step 1: Full local gate**

```bash
go mod tidy
make all
helm dep update charts/tatara-memory
helm lint charts/tatara-memory
helm unittest charts/tatara-memory
helmfile lint
```

Expected: every command PASS, including the lightrag subchart now present.

- [ ] **Step 2: Build the production image from main**

```bash
make image
docker run --rm harbor.szymonrichert.pl/tatara-memory:$(git describe --tags --always --dirty)
```

Expected: container prints version line and exits 0.

- [ ] **Step 3: No commit needed** — verification only.

---

## Wave 7 — Local deploy and smoke test (sequential, from main)

Runs after Wave 6's final integration check passes. Single human-driven session; not a subagent task. Build/deploy from `main` only.

### Task 7.1: Push the image to harbor

- [ ] **Step 1: Push**

```bash
make push
```

Expected: image present at `harbor.szymonrichert.pl/tatara-memory:<version>`.

### Task 7.2: Prepare the secrets file

**Files:**
- Modify: `values/tatara-memory/default.secrets.yaml`

- [ ] **Step 1: Decrypt or create the secrets file**

```bash
sops values/tatara-memory/default.secrets.yaml
```

- [ ] **Step 2: Populate keys**

```yaml
existingSecrets:
  pgPasswordSecret: "tatara-memory-pg-app"
  pgPasswordKey: "password"
  oidcClientSecretSecret: "tatara-memory-oidc"
  oidcClientSecretKey: "client-secret"

# Lightrag subchart secret references (passed through to lightrag subchart values)
lightrag:
  secrets:
    openaiApiKeySecret: "tatara-memory-lightrag-openai"
    openaiApiKeyKey: "api-key"
    neo4jPasswordSecret: "tatara-memory-neo4j-password"
    neo4jPasswordKey: "password"
    postgresPasswordSecret: "tatara-memory-lightrag-pg-app"
    postgresPasswordKey: "password"
```

- [ ] **Step 3: Save and verify SOPS encryption**

```bash
grep -q "sops:" values/tatara-memory/default.secrets.yaml
```

Expected: present (file is encrypted).

### Task 7.3: Pre-create external secrets

The chart references several existing-secret names. They must exist before helmfile apply.

- [ ] **Step 1: Create the OIDC client secret**

```bash
CLIENT_SECRET=$(kubectl -n keycloak exec deploy/keycloak -- /opt/keycloak/bin/kcadm.sh \
  get clients -r master -q clientId=tatara-memory --fields secret \
  --format json | jq -r '.[0].value // .[0].secret')

kubectl create namespace tatara-memory --dry-run=client -o yaml | kubectl apply -f -
kubectl -n tatara-memory create secret generic tatara-memory-oidc \
  --from-literal=client-secret="$CLIENT_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f -
```

- [ ] **Step 2: Create the lightrag OpenAI key secret**

```bash
kubectl -n tatara-memory create secret generic tatara-memory-lightrag-openai \
  --from-literal=api-key="$OPENAI_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
```

- [ ] **Step 3: Neo4j password secret**

```bash
kubectl -n tatara-memory create secret generic tatara-memory-neo4j-password \
  --from-literal=password="$(openssl rand -base64 32)" \
  --dry-run=client -o yaml | kubectl apply -f -
```

(cnpg cluster passwords are auto-created by the cnpg/cluster chart on first deploy; no action required.)

### Task 7.4: Helmfile diff and apply

- [ ] **Step 1: Diff**

```bash
cd ~/Documents/tatara/tatara-memory
helmfile diff
```

Expected: full creation diff for the namespace (cnpg cluster, neo4j sts, lightrag deploy, tatara-memory deploy, ingress, etc.).

- [ ] **Step 2: Apply**

```bash
helmfile apply
```

Expected: helmfile reports release `tatara-memory` deployed; pods reach Ready (allow 2-3 minutes for cnpg + neo4j init).

- [ ] **Step 3: Verify pod health**

```bash
kubectl -n tatara-memory get pods
kubectl -n tatara-memory logs deploy/tatara-memory --tail=50
```

Expected: tatara-memory pod Ready, log lines are JSON with `request_id`, `level`, `time`, `msg`. No ERROR. Readyz passes.

### Task 7.5: Smoke test the API

- [ ] **Step 1: Mint a bearer token**

```bash
TOKEN=$(curl -s -X POST \
  "https://auth.szymonrichert.pl/realms/master/protocol/openid-connect/token" \
  -d "client_id=tatara-memory" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=client_credentials" \
  -d "scope=tatara" | jq -r .access_token)

echo "$TOKEN" | cut -c1-60   # sanity check
```

Expected: a JWT starting with `eyJ...`.

- [ ] **Step 2: POST a memory**

```bash
curl -sX POST https://tatara-memory.szymonrichert.pl/v1/memories \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text":"Tatara is a Japanese clay furnace used for smelting iron sand into tamahagane steel.","idempotency_key":"smoke-001"}' | jq .
```

Expected: 201, body `{"id":"..."}`. Capture `id` into `$MEMID`.

- [ ] **Step 3: GET the memory**

```bash
curl -s https://tatara-memory.szymonrichert.pl/v1/memories/$MEMID \
  -H "Authorization: Bearer $TOKEN" | jq .
```

Expected: 200, body contains the original text.

- [ ] **Step 4: Query**

```bash
curl -sX POST https://tatara-memory.szymonrichert.pl/v1/queries \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text":"What is tatara?","mode":"hybrid"}' | jq .
```

Expected: 200, body with `matches` array; at least one match references the seeded memory.

- [ ] **Step 5: Bulk ingest + job status**

```bash
JOB_ID=$(curl -sX POST https://tatara-memory.szymonrichert.pl/v1/memories:bulk \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"items":[
        {"text":"Iron sand is collected from riverbeds.","idempotency_key":"smoke-bulk-1"},
        {"text":"Tamahagane is the resulting high-carbon steel.","idempotency_key":"smoke-bulk-2"},
        {"text":"Master smiths forge tamahagane into katana blades.","idempotency_key":"smoke-bulk-3"}]}' \
  | jq -r .job_id)

# Poll until terminal
for i in {1..30}; do
  STATUS=$(curl -s https://tatara-memory.szymonrichert.pl/v1/ingest-jobs/$JOB_ID \
    -H "Authorization: Bearer $TOKEN" | jq -r .status)
  echo "[$i] status=$STATUS"
  case "$STATUS" in succeeded|failed|partial) break ;; esac
  sleep 2
done
```

Expected: terminal status `succeeded`; counts `done=3 failed=0`.

- [ ] **Step 6: DELETE the memory**

```bash
curl -sX DELETE https://tatara-memory.szymonrichert.pl/v1/memories/$MEMID \
  -H "Authorization: Bearer $TOKEN" -o /dev/null -w "%{http_code}\n"
```

Expected: `204`.

### Task 7.6: Verify Prometheus scrape

- [ ] **Step 1: Check the ServiceMonitor target**

In Grafana / Prometheus UI, confirm target `tatara-memory/tatara-memory` is `up=1`.

- [ ] **Step 2: Query a few key metrics**

```
http_requests_total{route="/v1/memories",method="POST"}
ingest_jobs_total{terminal_state="succeeded"}
lightrag_calls_total{op="query",result="ok"}
```

Expected: non-zero values matching the smoke-test traffic from Task 7.5.

### Task 7.7: Verify log shape and absence of errors

- [ ] **Step 1: Tail logs and check JSON shape**

```bash
kubectl -n tatara-memory logs deploy/tatara-memory --tail=200 | jq -s 'length, (.[0] | keys)'
```

Expected: every line parses as JSON; expected keys include `time`, `level`, `msg`, `request_id`, `user` (where applicable), `route`, `method`, `status`, `duration_ms`.

- [ ] **Step 2: Confirm no ERROR lines from smoke test**

```bash
kubectl -n tatara-memory logs deploy/tatara-memory --tail=500 | jq -r 'select(.level=="ERROR")'
```

Expected: empty output.

### Task 7.8: Update platform docs and ship the phase

- [ ] **Step 1: Update `~/Documents/tatara/ROADMAP.md`**

Change Phase 1 status from `planned` to `shipped <date>`; add a one-line summary of what landed.

- [ ] **Step 2: Update `~/Documents/tatara/MEMORY.md`**

Add any non-obvious decisions or dead-ends that came out of implementation. Examples (only if true at ship time):

- `2026-05-24 - lightrag pinned to v1.4.16; v1.5.0 rc1/rc2 skipped.`
- `2026-05-24 - neo4j chart moved to CalVer; 2026.4.0 supersedes old 5.x line.`
- Any incident / surprise that future-Claude should remember.

- [ ] **Step 3: Commit the docs repo updates**

```bash
cd ~/Documents/tatara
git add ROADMAP.md MEMORY.md
git commit -m "docs: mark phase 1 (tatara-memory) shipped"
git push origin main
```

- [ ] **Step 4: Tag the release on the tatara-memory repo**

```bash
cd ~/Documents/tatara/tatara-memory
git tag v0.1.0
git push origin v0.1.0
```

Phase 1 is shipped.
