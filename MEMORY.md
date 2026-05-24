# MEMORY.md

Component-local memory for tatara-memory. Cross-repo decisions live in
`~/Documents/tatara/MEMORY.md`.

Format: `YYYY-MM-DD - decision/finding`

---

## Decisions

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

## Open questions

*(nothing yet)*
