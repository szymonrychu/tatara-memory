# Memory retrieval-quality eval harness

This directory holds the versioned data for the memory retrieval-quality
eval harness (issue #41). It measures the single most important behaviour
of the platform: whether `POST /queries` surfaces the memories a correct
retrieval must return. Today retrieval quality is unmeasured, because
`QueryMatch.Score` is hard-coded to 0 (lightrag v1.4.16 exposes no
per-match ranking field; see `MEMORY.md` 2026-05-27). This harness scores
retrieval by **match order + content hit** instead of by Score.

## Layout

```
eval/
  seed/*.json      fixed corpus the harness bulk-ingests before querying
  golden/*.json    (query, mode, expected) cases scored against retrieval
  README.md        this file
```

Data lives as JSON read at runtime, never as lists in `values.yaml`
(hard rule 6). The format is the contract; both sets are meant to grow.

## Seed corpus format (`seed/*.json`)

A JSON array of memory items, each mirroring `memory.IngestItem`
(`internal/memory/types.go`). The harness submits the union of every file
under `seed/` to `POST /memories:bulk`.

```json
[
  {
    "idempotency_key": "eval-seed-go-version",
    "text": "Hard rule: use the newest stable Go ...",
    "metadata": { "source": "CLAUDE.md", "type": "hard-rule" }
  }
]
```

- `idempotency_key` (required, unique): stable key so re-running the
  harness re-ingests idempotently.
- `text` (required): the memory body. Retrieval returns chunks of this
  text, so golden `expected` substrings should be distinctive phrases
  drawn verbatim from here.
- `metadata` (optional): free-form string map; `source`/`type` are used
  here only for provenance.

The seed is drawn from real platform memories (CLAUDE.md hard rules,
MEMORY.md decisions/findings, deploy gates) so the harness is
self-contained and reproducible.

## Golden case format (`golden/*.json`)

A JSON array of cases. The harness runs every case across the files under
`golden/`.

```json
[
  {
    "name": "query-score-from-rank",
    "query": "How is QueryMatch.Score populated for scored retrieval?",
    "mode": "local",
    "top_k": 10,
    "expected": ["retrieval rank", "1/(rank+1)"]
  }
]
```

- `name` (required, unique): stable case identifier for logs/trends.
- `query` (required): the retrieval text sent to `POST /queries:data`
  (the structured, score-ranked path).
- `mode` (required): one of `hybrid`, `local`, `global`, `naive`
  (`memory.QueryMode`).
- `top_k` (optional): per-case retrieval depth. Omitted/0 defaults to 10;
  capped at 500 (matches the service clamp in `internal/httpapi/queries.go`).
- `expected` (required, non-empty): substrings and/or memory IDs that a
  correct retrieval MUST surface. An expected entry counts as a hit when
  it is a substring of a returned `Match.Text` OR matches a `Match.ID`.

## Metrics

The eval queries `/queries:data`, whose `Match.Score` is derived from
LightRAG's chunk retrieval order (`1/(rank+1)`; LightRAG v1.4.16 exposes
no per-chunk relevance field). Matches are ranked by `Score` descending
before scoring, so recall@k and MRR reflect that retrieval order rather
than the order matches happened to arrive in:

- **recall@k**: fraction of a case's `expected` entries found within the
  top `k` score-ranked matches.
- **MRR**: reciprocal rank (`1/rank`) of the first score-ranked match that
  satisfies any expected entry; 0 if none.

The runner aggregates mean recall@k and mean MRR across all cases and
exits non-zero when aggregate recall@k falls below a configurable floor,
so it can gate in CI.

## Running

The runner (`cmd/eval`) seeds this corpus into a live tatara-memory
deployment, runs the golden cases, and scores retrieval. It is opt-in,
like the `-tags integration` suite, and is NOT part of unit `make test`:

```
MEMORY_BASE_URL=https://memory.example MEMORY_TOKEN=<oidc-token> make eval
```

`make eval` fails fast with a clear message if `MEMORY_BASE_URL` is unset.
It exits non-zero when aggregate recall@k falls below the floor, so CI can
gate on it.

### Configuration

Every flag has an env fallback; the flag wins when both are set.

| Flag | Env | Default | Meaning |
| --- | --- | --- | --- |
| `-base-url` | `MEMORY_BASE_URL` | (required) | tatara-memory base URL |
| `-token` | `MEMORY_TOKEN` | "" | pre-fetched OIDC bearer token |
| `-recall-floor` | `EVAL_RECALL_FLOOR` | `0.7` | min mean recall@k before non-zero exit |
| `-k` | `EVAL_K` | `10` | k for recall@k |
| `-golden-dir` | `EVAL_GOLDEN_DIR` | (embedded) | override dir of golden `*.json` |
| `-seed-dir` | `EVAL_SEED_DIR` | (embedded) | override dir of seed `*.json` |
| `-metrics-file` | `EVAL_METRICS_FILE` | "" | optional Prometheus textfile of aggregate scores |
| `-job-timeout` | `EVAL_JOB_TIMEOUT` | `5m` | max wait for the seed ingest job |

The token is never logged. Output is slog JSON: one INFO line per case
(name, query, mode, recall@k, mrr, hits) and a final aggregate line
(cases, k, mean recall@k, mean MRR, floor, pass).

## Adding cases

1. If the answer is not already covered, add a seed item to `seed/*.json`
   with a distinctive phrase.
2. Add a golden case to `golden/*.json` whose `expected` entries are
   substrings of that phrase (or a memory ID).
3. Keep JSON minimal and stable; one logical group per file is fine.
