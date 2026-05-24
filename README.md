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
make chart-lint      # helm lint via mise (avoids homebrew helm shadow)
make helmfile-lint   # helmfile lint via mise
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
