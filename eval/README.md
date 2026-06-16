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
    "name": "query-score-zero",
    "query": "Why is QueryMatch.Score always zero?",
    "mode": "local",
    "top_k": 10,
    "expected": ["Score remains 0", "per-match ranking field"]
  }
]
```

- `name` (required, unique): stable case identifier for logs/trends.
- `query` (required): the retrieval text sent to `POST /queries`.
- `mode` (required): one of `hybrid`, `local`, `global`, `naive`
  (`memory.QueryMode`).
- `top_k` (optional): per-case retrieval depth. Omitted/0 defaults to 10;
  capped at 500 (matches the service clamp in `internal/httpapi/queries.go`).
- `expected` (required, non-empty): substrings and/or memory IDs that a
  correct retrieval MUST surface. An expected entry counts as a hit when
  it is a substring of a returned `Match.Text` OR matches a `Match.ID`.

## Metrics

Because `Score` is unavailable, retrieval is scored purely by the order
and content of the returned `Matches`:

- **recall@k**: fraction of a case's `expected` entries found within the
  first `k` matches.
- **MRR**: reciprocal rank (`1/rank`) of the first match that satisfies
  any expected entry; 0 if none.

The runner aggregates mean recall@k and mean MRR across all cases and
exits non-zero when aggregate recall@k falls below a configurable floor,
so it can gate in CI.

## Running

The runner (`cmd/eval`, added in a later subtask) needs a live, seeded
tatara-memory deployment. It is opt-in, like the `-tags integration`
suite, and is not part of unit `make test`:

```
MEMORY_BASE_URL=https://memory.example MEMORY_TOKEN=<oidc-token> make eval
```

## Adding cases

1. If the answer is not already covered, add a seed item to `seed/*.json`
   with a distinctive phrase.
2. Add a golden case to `golden/*.json` whose `expected` entries are
   substrings of that phrase (or a memory ID).
3. Keep JSON minimal and stable; one logical group per file is fine.
