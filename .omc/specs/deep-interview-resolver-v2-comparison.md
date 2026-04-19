# Deep Interview Spec: Resolver v2 — Cross-Model Comparison

## Metadata
- Interview ID: resolver-v2-comparison-2026-04-19
- Rounds: 5
- Final Ambiguity Score: 15%
- Type: brownfield (v1 harness shipped at commit `2408196`)
- Generated: 2026-04-19
- Threshold: 20%
- Status: PASSED

## Clarity Breakdown
| Dimension | Score | Weight | Weighted |
|---|---|---|---|
| Goal Clarity | 0.92 | 0.35 | 0.32 |
| Constraint Clarity | 0.85 | 0.25 | 0.21 |
| Success Criteria | 0.80 | 0.25 | 0.20 |
| Context Clarity | 0.80 | 0.15 | 0.12 |
| **Total Clarity** | | | **0.85** |
| **Ambiguity** | | | **0.15** |

## Goal

Extend the v1 resolver harness so that scorecards from many runs — across models, proxy configurations, and vLLM engine configurations — can be compared rigorously. Scope:

1. **Metadata capture.** Record the proxy-side real-model plus a user-declared run-config sidecar (MTP, thinking mode, tool parser, reasoning parser, context size) into `manifest.json`. Unknown values stay explicitly unknown (not hallucinated).
2. **Aggregation store.** A DuckDB single-file store ingests every scorecard + manifest under `reports/`. JSON stays the canonical source of truth; DuckDB is a derivable view for analytics.
3. **Analyzer.** A small Python package in `tools/analyze/` exposes (a) Jupyter-notebook templates, (b) a CLI that calls an LLM (Claude or similar) to produce an opinionated markdown analysis of the aggregate data, and (c) a curated prompt living in version control so the "opinionated" part is inspectable and tunable.
4. **Community-benchmark reference.** A hand-authored `reports/community-benchmarks.yaml` lets analysts join public leaderboard scores (BFCL, tau-bench, RULER, MMLU, GPQA) against resolver runs by model name. AI-driven scraping fills in missing rows on demand; append-only since historical scores don't change.
5. **AI-orchestrated run protocol.** A prompt doc tells an AI CLI how to (a) SSH to the host running llm-proxy + vLLM, (b) read the proxy config + vLLM recipe, (c) build a `run.yaml` sidecar, (d) invoke `resolver --run-config run.yaml`. Remote models (HF inference, Anthropic, etc.) gracefully degrade to "unknown" on probe-only fields.

The v1 harness and spec §7 scorecard shape are frozen contracts — nothing in v2 changes what a single scorecard looks like. All new work is **additive**.

## Constraints

- **Tech split:** Go for benchmark execution + scorecard/manifest/DuckDB aggregation. Python (single `tools/analyze/` package) for data exploration + LLM-authored analysis. Reuses the existing `llm-proxy` infrastructure for the reporter LLM by default.
- **Store format:** DuckDB single file at `reports/resolver.duckdb`. Append-only per run; rebuildable from `reports/results/*.json` + `reports/sweeps/*.csv` + manifests. Never the canonical source.
- **Metadata capture protocol:**
  - **Runtime probe** (Go): `GET {endpoint_origin}/v1/models` for resolvedRealModel; optional `GET {endpoint_origin}/v1/proxy/config` when the proxy surfaces it. Failures fall back to "unknown".
  - **Sidecar YAML** (Go): new `--run-config PATH` flag parses a user-authored YAML with fields: `real_model`, `backend_port`, `thinking`, `mtp`, `context_size`, `tool_parser`, `reasoning_parser`, `quantization`, `proxy_recipe_path`, `vllm_recipe_path`, `notes`. All fields optional. Verbatim into `manifest.runConfig`.
  - **AI orchestration prompt** (docs): `docs/prompts/run-benchmark.md` describing how an AI CLI should build the sidecar from remote recipe files.
- **Reporter LLM:** default = `gresh-general` at the configured llm-proxy endpoint (dogfooding). Override via `--reporter-model` on the Python analyzer CLI. Never runs the reporter LLM against the model under test — two separate endpoints by design.
- **Community-benchmark schema:** YAML list of records, one per `(model, benchmark)` pair. Fields: `model, benchmark, metric, value, source_url, as_of, notes`. Metrics at v2 launch:
  - `bfcl_overall` (Berkeley Function-Calling)
  - `tau_bench_retail_pass_at_1`
  - `tau_bench_airline_pass_at_1`
  - `ruler_32k`, `ruler_128k`
  - `mmlu`
  - `gpqa_diamond`
- **Reproducibility verification:** a thin `resolver --tier 1 -n N` rerun helper that runs Tier 1 N times in sequence, emits N scorecards, and the Python analyzer reports per-query variance (stddev of score and elapsedMs). Confirms the "v1 is very consistent" claim quantitatively.
- **Spec parity:** scorecard meta keys stay byte-identical to spec §7 — all new metadata lands in `manifest.json` only. Tier thresholds may move from Go code (`scenario.GatedTiers()`) to a YAML that ships embedded *and* can be overridden, but default values stay `90/80/60/60/60`.
- **No database server / web UI:** DuckDB is embedded; Python notebooks + one markdown report are the consumption surface. Shareability is "copy the `reports/` directory".

## Non-Goals

- **Not** a separate web dashboard or hosted service.
- **Not** changing v1 scorecard `meta` keys (byte parity with spec §7 preserved).
- **Not** adding the Anthropic / openclaw / hf-serverless adapters (still explicit v3).
- **Not** integrating every community benchmark — the v2 launch set is capped at 7 metrics.
- **Not** live scraping on every run (scrapes are on-demand, AI-driven, cached append-only).
- **Not** LLM-as-judge for in-scenario verdicts (still deferred to v3+); the reporter LLM analyses *aggregate results*, not individual scenarios.
- **Not** a migration tool for existing scorecards — v1 scorecards are readable as-is by the DuckDB ingester because the schema stayed additive.

## Acceptance Criteria

### Metadata capture (Go)
- [ ] `--run-config PATH` flag parses a YAML whose fields are merged into `manifest.runConfig` (new field on the manifest schema; `manifestVersion` bumped to 2 with backwards-compat rule: v1 manifests remain readable by the aggregator).
- [ ] On each run, the openai-chat adapter probes `GET {endpoint_origin}/v1/models` (timeout 5s, non-fatal on failure). The first model's `id`/`root` field is stored in `manifest.resolvedRealModel`; on failure, stored as `"unknown"`.
- [ ] Manifest schema documented in `docs/manifest-schema.md` with field definitions, required-ness, and explicit semantics for `"unknown"`.
- [ ] Unit test: sidecar YAML with all fields populated round-trips through `manifest.runConfig` with no data loss; missing fields remain absent (not emitted as empty strings).

### Aggregator (Go — new command or subcommand)
- [ ] `resolver aggregate` (or equivalent subcommand name) walks `reports/results/`, `reports/sweeps/`, and their `manifests/` siblings; upserts each `runId` into `reports/resolver.duckdb` with tables: `runs`, `queries`, `sweep_rows`, `run_config`, `community_benchmarks` (the last imported from YAML).
- [ ] Schema is a Go struct tagged for DuckDB; migration function creates tables on first run; second run is idempotent.
- [ ] Table view exposes a `comparison` virtual view joining `runs ⋈ queries` with one row per `(runId, queryId)` — the unit a notebook naturally pivots on.
- [ ] `resolver aggregate --dry-run` prints the set of new runIds that would be ingested without writing.
- [ ] Unit test: ingesting the same directory twice yields the same row counts (idempotent).
- [ ] Unit test: reading v1-shaped manifests (no `runConfig`, no `resolvedRealModel`) works without error.

### Python analyzer (`tools/analyze/`)
- [ ] `tools/analyze/pyproject.toml` declares deps: `duckdb`, `pandas` (or `polars`), `jupyter`, `openai` (as a generic chat client pointing at llm-proxy).
- [ ] `tools/analyze/cli.py report` reads `reports/resolver.duckdb`, joins `community-benchmarks.yaml`, builds a structured prompt (data tables + columns explained), POSTs to the configured reporter LLM via llm-proxy's OpenAI endpoint, writes `reports/analysis-{ts}.md`.
- [ ] Prompt template lives at `docs/prompts/compare-models.md` — version-controlled, markdown, referenced by the CLI. Editing the prompt changes the report without code changes.
- [ ] `tools/analyze/notebooks/quickstart.ipynb` demonstrates: load DuckDB, show all runs, group by model, plot tier accuracy, show sweep curves.
- [ ] `tools/analyze/notebooks/reproducibility.ipynb` runs the variance check against N repeat runs of the same config.
- [ ] Integration test: run the CLI against a seeded fixture DuckDB + a stub LLM endpoint; assert the emitted markdown contains the expected sections (ranking, variance, benchmark join).

### Community benchmarks
- [ ] `reports/community-benchmarks.yaml` exists with the v2 launch-set metrics (`bfcl_overall`, `tau_bench_retail_pass_at_1`, `tau_bench_airline_pass_at_1`, `ruler_32k`, `ruler_128k`, `mmlu`, `gpqa_diamond`) populated for at least 3 seed models as examples (e.g., Qwen3.6-35B, Llama-3.1-70B-Instruct, Claude-Sonnet-4.6).
- [ ] Schema documented at `docs/community-benchmarks-schema.md`; analyzer joins by case-insensitive `model` name or alias list.
- [ ] `docs/prompts/scrape-community-benchmarks.md` exists — an AI-CLI prompt that describes how to discover current values on official leaderboards + append to the YAML (append-only contract; historical rows never edited).

### AI orchestration prompt
- [ ] `docs/prompts/run-benchmark.md` describes the complete run protocol: SSH to llm-proxy host → `cat` the proxy recipe + the vLLM compose/recipe file → build sidecar YAML → invoke `resolver --run-config run.yaml` → commit the scorecard+manifest+run-config as a triplet. Handles the "remote model, some fields unknown" case explicitly.
- [ ] Prompt is linked from README under a new "Running benchmarks across stacks" section.

### Threshold tweakability
- [ ] `scenario.GatedTiers()` hardcoded defaults moved to `cmd/resolver/data/tier1/gate-thresholds.yaml` (embedded). A `--thresholds PATH` flag overrides the embedded defaults.
- [ ] Embedded defaults preserve existing behaviour (90/80/60/60/60, same labels with `>` phrasing for spec §7 parity).
- [ ] A cross-model table built by the analyzer shows PASS/FAIL per gated check per model, with the threshold values as column headers — so a user tweaking thresholds can see who would pass/fail.

### Reproducibility verification
- [ ] Existing `-n N` seed flag already supports repeats for sweeps. Add equivalent for `--tier 1`: `resolver --tier 1 -n 5` runs the suite 5 times and writes 5 scorecards with shared run_config.
- [ ] Python notebook computes per-query stddev of score (0/0.5/1 mapped) + elapsedMs; reports are considered "consistent" if stddev ≤ 5% across N=5 at temperature=0.

### General
- [ ] All new Go code has `go test ./...` coverage including happy path + one error path.
- [ ] Python tests via `pytest tools/analyze/tests/`. Run by a convenience make/just target.
- [ ] README updated with a "v2: Comparing models" section linking the analyzer, benchmark YAML, run prompt, reproducibility notebook.
- [ ] Manifest schema v2 bump documented; v1 manifests still ingestable.

## Assumptions Exposed & Resolved

| Assumption | Challenge | Resolution |
|---|---|---|
| The harness should call vLLM directly to capture MTP / tool parser | Those are startup flags, not queryable over HTTP; probe would partially succeed at best | Sidecar YAML is the canonical record for startup-flag fields; runtime probe only covers what the API exposes |
| Community-benchmark data should be scraped on every run | Leaderboard URLs change; scraping is brittle; historical values never change | Hand-curated YAML is canonical; AI-driven scrape fills missing rows on demand; append-only |
| The analysis step should be a built-in Go subcommand (`resolver analyze`) | Go is a bad host for data-science iteration + LLM clients | Analysis lives in `tools/analyze/` as Python; Go stays focused on benchmark execution + aggregation |
| v1 scorecard shape needs to change to carry richer metadata | Any shape change breaks spec §7 byte parity + historical comparability | All new metadata lives in sibling `manifest.json`; scorecard meta keys frozen |
| The reporter LLM is the same as the model under test | That biases analysis toward "the model says it did well" | Reporter LLM is a separate endpoint selection; defaults to llm-proxy's `gresh-general` with a `--reporter-model` override; never evaluates itself |
| Storage should be a database server (Postgres / similar) | Overkill for local single-operator use; breaks the "copy `reports/` and run" portability | DuckDB single file; JSON stays canonical; the DB is derivable |

## Technical Context

**Repo state (2026-04-19, commit `2408196`):**
- v1 harness in Go 1.24.2. 9 packages under `internal/`. 63 files, 7,395 insertions.
- Manifest schema v1: `runId, model, resolvedRealModel (scaffolded but unused), adapter, tokenizerMode, endpoint, tier, sweep, seeds, parallel, scenarioHashes, startedAt, finishedAt, goVersion, commitSha, hostName, manifestVersion`.
- Scorecard spec §7 byte-exact after the tier-key ordering fix (`Summary.MarshalJSON`).
- Per-scenario verdict rules already tweakable via YAML (`correct_if` / `partial_if`); tier-level thresholds hardcoded in `internal/scenario/scenario.go:32`.
- Live Tier 1 smoke against llm-proxy's `gresh-general` scores 94%/0%/100%/100%/100% across the five gated checks.
- No DuckDB dependency yet; closest neighbour is `gopkg.in/yaml.v3` (the only external Go dep).

**Sibling projects relevant to v2:**
- `../llm-proxy/` — Go service. Exposes `/v1/models` today. A `/v1/proxy/config` surface would make runtime probing more useful; optional to add there in v2.1.
- `../sysadm/` and `../sysadm-v2/` — the agents this benchmark measures. Not modified.

**Proposed v2 additions** (directory-level):
```
resolver/
├── cmd/resolver/
│   └── main.go                # +--run-config, +aggregate subcommand, +--thresholds
├── internal/
│   ├── manifest/
│   │   └── manifest.go        # +RunConfig field, +probe integration, bump to v2
│   ├── aggregate/             # NEW: DuckDB ingester
│   │   ├── schema.go
│   │   ├── ingest.go
│   │   └── ingest_test.go
│   └── scenario/
│       └── thresholds.go      # NEW: load gate-thresholds.yaml (embedded + override)
├── cmd/resolver/data/tier1/
│   └── gate-thresholds.yaml   # NEW: embedded defaults
├── tools/
│   └── analyze/               # NEW: Python package
│       ├── pyproject.toml
│       ├── src/analyze/
│       │   ├── __init__.py
│       │   ├── cli.py
│       │   ├── db.py
│       │   └── report.py
│       ├── notebooks/
│       │   ├── quickstart.ipynb
│       │   └── reproducibility.ipynb
│       └── tests/
├── reports/
│   ├── community-benchmarks.yaml  # NEW: seed set of public benchmark scores
│   └── resolver.duckdb            # generated, gitignored
└── docs/
    ├── manifest-schema.md          # NEW
    ├── community-benchmarks-schema.md  # NEW
    └── prompts/
        ├── run-benchmark.md        # NEW: AI-CLI run orchestration
        ├── compare-models.md       # NEW: analysis prompt template
        └── scrape-community-benchmarks.md  # NEW
```

## Ontology (Key Entities)

| Entity | Type | Fields | Relationships |
|---|---|---|---|
| Scorecard (v1) | core | meta{model,endpoint,timestamp,queryCount,nodeVersion}, summary, results | produced_by Resolver; ingested_by Aggregator |
| Manifest (v2) | core | runId, model, resolvedRealModel, runConfig, tokenizerMode, seeds, scenarioHashes, goVersion, commitSha, manifestVersion | sibling_to Scorecard; ingested_by Aggregator |
| RunConfig | new core | real_model, backend_port, thinking, mtp, context_size, tool_parser, reasoning_parser, quantization, proxy_recipe_path, vllm_recipe_path, notes | embedded_in Manifest |
| Aggregator | new core | command, db_path | reads {Scorecard, Manifest}; writes DuckDB |
| DuckDB store | new core | tables: runs, queries, sweep_rows, run_config, community_benchmarks | written_by Aggregator; read_by PythonAnalyzer |
| PythonAnalyzer | new core | cli, notebooks, prompt_template | reads DuckDB, CommunityBenchmarksYAML; produces AnalysisReport |
| AnalysisReport | new core | timestamp, markdown, reporter_model | written_by PythonAnalyzer |
| CommunityBenchmarksYAML | new supporting | records[{model,benchmark,metric,value,source_url,as_of,notes}] | joined_into DuckDB by Aggregator |
| Gate | core | label, tiers, threshold | sourced_from embedded YAML (v2) or --thresholds override |
| Reproducibility check | new supporting | n_repeats, stddev_tolerance | notebook-hosted verification |
| AIOrchestrationPrompt | new supporting | path=docs/prompts/run-benchmark.md | guides AI_CLI to build RunConfig |

## Ontology Convergence

| Round | Entity Count | New | Stable | Stability Ratio |
|---|---|---|---|---|
| 1 | 6 | 6 | - | N/A |
| 2 | 7 | 1 (DuckDB) | 6 | 85.7% |
| 3 | 9 | 2 (RunConfig, AIOrchestrationPrompt) | 7 | 77.8% |
| 4 | 10 | 1 (PythonAnalyzer) | 9 | 90.0% |
| 5 | 11 | 1 (CommunityBenchmarksYAML) | 10 | 90.9% |

Domain model stabilized after round 4. Round 5 added the benchmark registry as a distinct entity (previously implicit).

## Interview Transcript

<details>
<summary>Full Q&A (5 rounds)</summary>

### Round 1 — Goal Clarity
**Q:** What's the concrete end-artifact this v2 work should produce?
**A:** JSON as source of truth; aggregator builds a store for spreadsheet/Jupyter/LLM analysis; an LLM-authored opinionated analysis sits on top. (Also: should retest reproducibility claim.)
**Ambiguity:** 42%

### Round 2 — Constraint Clarity
**Q:** What store format should the aggregator emit?
**A:** DuckDB single file.
**Ambiguity:** 36%

### Round 3 — Constraint Clarity
**Q:** How should the resolver capture proxy + vLLM settings per run?
**A:** Option 2 (probe + sidecar YAML) + an AI CLI that reads proxy config + vLLM recipe to build the sidecar; unknown is a valid value for remote/HF-hosted models.
**Ambiguity:** 30%

### Round 4 — Success Criteria (Contrarian)
**Q:** How should the LLM-authored "opinionated analysis" step actually work?
**A:** Separate small Python tool in `tools/analyze/`.
**Ambiguity:** 20%

### Round 5 — Constraint Clarity (Simplifier)
**Q:** How should community-benchmark data land in our reporting?
**A:** Hand-curated YAML with append-only semantics; AI-driven scrape fills missing rows when needed; metrics must be identified now.
**Ambiguity:** 15%

</details>
