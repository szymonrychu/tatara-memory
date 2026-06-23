# Code-graph retrieval-quality eval harness

This directory holds the versioned data and runner for the code-graph
retrieval-quality eval harness (issue #49). It mirrors the memory eval
(`eval/`, issue #41) for the platform's *second* major retrieval surface:
the code graph served by `/code/*` and backed by the recursive-CTE walks in
`internal/codegraph/pgstore.go`. That surface is what the autonomous loop and
the brainstorm/deep-research turns use to locate where a fix belongs and gauge
blast radius (the `code_*` MCP tools); before this harness a regression in CTE
traversal or in edge attribution degraded agent navigation silently, with no
number to track.

Unlike the memory eval, the code seed is a small **synthetic** graph pushed via
`POST /code-graph:bulk`. Because that endpoint is a synchronous, file-granular
*replace* scoped to a dedicated repo slug, the harness is self-contained and
idempotent and pollutes no real repo's graph - there is no teardown problem.
(A snapshot-seeded variant that also guards analyzer extraction quality is a
deliberate, separately-scoped follow-up; see ROADMAP.)

## Layout

```
eval/codegraph/
  seed/*.json      synthetic fixture graph, pushed before the cases run
  golden/*.json    (kind, args, expected) traversal cases scored against /code/*
  README.md        this file
```

Data lives as JSON read at runtime (embedded into the binary), never as lists
in `values.yaml` (hard rule 6). The format is the contract; both sets are meant
to grow.

## Seed fixture format (`seed/*.json`)

Each file is one `codegraph.GraphPush` (`internal/codegraph/types.go`) **minus**
`repo` (the runner sets the slug). Multiple files are merged (union of files,
entities, edges). The fixture spans the discriminative relation classes -
`calls`, `defines`, `imports`, `depends_on`, `value_ref` - so each traversal
type has something to find, including an `imports`/`depends_on` edge so the
known Go import/depends_on extraction recall-hole shows up as a measurable miss
once a real corpus replaces the fixture.

```json
{
  "files": ["app/service.go"],
  "entities": [
    { "id": "go:func:app/service.Run", "name": "Run", "type": "function", "file_path": "app/service.go" }
  ],
  "edges": [
    { "from": "go:func:app/service.Run", "to": "go:func:app/service.Helper", "relation": "calls", "src_file": "app/service.go" }
  ]
}
```

Every entity's `file_path` (when set) and every edge's `src_file` must be one of
`files`, matching the service-side scope check; the loader validates this so a
malformed fixture fails at load time rather than as a 400 from the deployment.

## Golden case format (`golden/*.json`)

A JSON array of cases. Each case names a `kind` (one `/code/*` endpoint) plus the
args that endpoint reads, and the `expected` entity IDs (or `from->to` edge keys
for `file-imports`) a correct response must surface.

```json
[
  { "name": "callees-run-depth1", "kind": "callees", "id": "go:func:app/service.Run", "depth": 1,
    "expected": ["go:func:app/service.Helper", "go:func:app/util.Validate"] },
  { "name": "path-main-to-format-calls", "kind": "path", "from": "go:func:app/main.Main",
    "to": "go:func:app/util.Format", "relation": "calls",
    "expected": ["go:func:app/main.Main", "go:func:app/service.Run", "go:func:app/service.Helper", "go:func:app/util.Format"] }
]
```

- `name` (required, unique): stable case identifier for logs/trends.
- `kind` (required): one of `search`, `entity`, `neighbors`, `callers`,
  `callees`, `dependents`, `dependencies`, `resource-graph`, `file-imports`,
  `path`.
- args per kind: `search` -> `q` (+ optional `type`); `entity`/`callers`/
  `callees`/`dependents`/`dependencies`/`resource-graph` -> `id` (+ optional
  `depth`); `neighbors` -> `id` + `relation` (+ optional `direction`, `depth`);
  `file-imports` -> `path`; `path` -> `from` + `to` (+ optional `relation`,
  `max_depth`).
- `expected` (required, non-empty): for a **set** case, the complete expected
  result set (so precision is meaningful); for a **ranked** case, the entries
  that must appear within the top k.

## Scoring (two modes, because the surface has two shapes)

- **Ranked** (`search`, `path`): the endpoint returns an ordered list, so it is
  scored by **recall@k + MRR**, reusing the memory eval's primitives but matching
  on entity ID / symbol name instead of a memory `Match.Text` substring.
- **Set** (every traversal): `callers`/`callees`/`dependents`/`dependencies`/
  `path`-less walks return an exact set (the CTE `ORDER BY id, depth`), so they
  are scored by **precision / recall / F1** of the returned entity-ID set against
  the expected set. A regression that drops an edge shows up as a recall miss; a
  phantom-node / over-walk regression shows up as a precision drop that rank-only
  recall would hide.

All four metrics are computed for every case; the aggregate **mean recall@k** is
the gate (the runner exits non-zero below the floor, so CI can gate, identical to
`cmd/eval`). This harness lands and gates independently of #48: the code surface
already has a real ranking signal (`Search` orders by match class; `code_important`
by degree), unlike memory's hard-coded `QueryMatch.Score=0`.

## Running

The runner (`cmd/codegraph-eval`) pushes the fixture into a live tatara-memory
deployment, runs the golden cases, and scores them. It is opt-in and NOT part of
unit `make test`:

```
MEMORY_BASE_URL=https://memory.example MEMORY_TOKEN=<oidc-token> make codegraph-eval
```

`make codegraph-eval` fails fast if `MEMORY_BASE_URL` is unset, and exits
non-zero when aggregate recall@k falls below the floor.

### Configuration

Every flag has an env fallback; the flag wins when both are set.

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `-base-url` | `MEMORY_BASE_URL` | (required) | tatara-memory base URL |
| `-token` | `MEMORY_TOKEN` | "" | pre-fetched OIDC bearer token |
| `-repo` | `CODEGRAPH_EVAL_REPO` | `eval/codegraph-fixture` | synthetic fixture repo slug |
| `-recall-floor` | `EVAL_RECALL_FLOOR` | `0.7` | min mean recall@k before non-zero exit |
| `-k` | `EVAL_K` | `10` | k for recall@k on ranked cases |
| `-golden-dir` | `EVAL_GOLDEN_DIR` | (embedded) | override dir of golden `*.json` |
| `-seed-dir` | `EVAL_SEED_DIR` | (embedded) | override dir of seed `*.json` |
| `-metrics-file` | `EVAL_METRICS_FILE` | "" | optional Prometheus textfile of aggregate scores |

The token is never logged. Output is slog JSON: one INFO line per case (name,
kind, mode, recall@k, mrr, precision, f1, hits) and a final aggregate line.

The metrics textfile mirrors the memory eval's: `codegraph_eval_recall_at_k`,
`_mrr`, `_precision`, `_f1`, `_cases`, `_recall_floor`, so the same `/metrics`
plumbing can carry these too.

## Adding cases

1. If the entities/edges a case needs are not in the fixture, add them to
   `seed/*.json` (keep IDs canonical and `src_file`/`file_path` in `files`).
2. Add a golden case whose `expected` is the complete result set for that
   endpoint and args.
3. Keep JSON minimal and stable; the fixture should stay a minimal,
   discriminative graph, not grow into a corpus.
