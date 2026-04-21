# Community-benchmarks schema

Reference data from public leaderboards, joined into the v2 aggregator
by `real_model` so cross-model comparisons can cite resolver findings
alongside published numbers from BFCL, RULER, tau-bench, MMLU, GPQA, etc.

File: [`reports/community-benchmarks.yaml`](../reports/community-benchmarks.yaml)

## Schema

Each entry is a record keyed on `(model, benchmark, metric)` — the three
identifiers that collectively name "this score". Required fields:

| Field | Type | Notes |
|---|---|---|
| `model` | string | The **real** model identifier (e.g. `Qwen/Qwen3.6-35B-A3B-FP8`). Matches `runs.resolved_real_model` in the DuckDB. |
| `benchmark` | string | The leaderboard name (e.g. `bfcl`, `ruler`, `tau-bench`, `mmlu`, `gpqa`). Lowercase recommended. |
| `metric` | string | The specific metric within that benchmark (e.g. `overall`, `32k`, `retail`, `5shot`, `diamond`). |
| `value` | float | The score, typically 0–1 (accuracy) or 0–100 (%). Use the leaderboard's native scale. |
| `source_url` | string | A canonical URL where the number can be verified (leaderboard page, paper, model card). Required. |
| `as_of` | date | ISO `YYYY-MM-DD` — the date the number was last confirmed. **Must be ≤ today** (aggregator rejects future dates). |
| `notes` | string | Optional — free-form. Seed rows carry a `"seed — verify via source_url"` marker until an operator confirms. |

## Append-only contract

Historical rows **must not change**. A score reported on a specific date
is immutable:

- If a new eval produces a different number, **append a new row** with a
  newer `as_of` date — don't overwrite the old one.
- If a score is withdrawn or disputed, append a row with the updated
  value and reference the retraction in `notes`.
- The `(model, benchmark, metric, as_of)` tuple is the de-facto primary
  key for the file; the aggregator enforces `(model, benchmark, metric)`
  uniqueness so adding a newer `as_of` row supersedes the old one in the
  DuckDB mirror. Future work: teach the aggregator to keep both rows and
  expose a "latest" view.

This contract exists because downstream consumers (the Python analyzer,
external citations) need to refer to a specific published score at a
specific date. Mutating the file in place would invalidate every prior
citation.

## Validation

The aggregator validates on ingest:

- Every field except `notes` is required.
- `as_of` must parse as `YYYY-MM-DD` and must be ≤ today (UTC).
- Non-conformant entries fail the ingest with a clear error; the DuckDB
  file is **not** touched. Fix the YAML and rerun `resolver aggregate`.

## How to add a row

Three reliable routes:

1. **Hand-edit** the YAML with a new entry. Include the `source_url` and
   today's `as_of`. The aggregator's lint will catch typos.
2. **AI-assisted refresh** — follow
   [`tools/analyze/prompts/scrape-community-benchmarks.md`](../../tools/analyze/prompts/scrape-community-benchmarks.md)
   with an AI CLI (Claude Code, Codex, etc.). The prompt walks the AI
   through discovering current values on official leaderboards and
   appending rows.
3. **From a new resolver capture** — if you captured a run against a
   model whose public benchmark data you already have, just append the
   corresponding rows in the same commit.

## Mapping to resolver runs

The aggregator's join uses `community_benchmarks.model =
runs.resolved_real_model` (case-insensitive on the Go side). If your
llm-proxy routes a virtual like `gresh-general` to
`Qwen/Qwen3.6-35B-A3B-FP8`, the row above lands on every
`gresh-general` run that carries that `resolved_real_model`. Rows for
models **not** present in the resolver runs are still ingested — they
just won't show up in the analyzer's run-scoped join.
