# Plan: Resolver v2 — Cross-Model Comparison

**Source spec:** `.omc/specs/deep-interview-resolver-v2-comparison.md` (ambiguity 15%, status PASSED)
**Mode:** Consensus (RALPLAN-DR short) · Iteration 2 (after Architect + Critic review) · **CONSENSUS APPROVED** · Non-interactive
**Base:** commit `2408196` (v1 shipped)

---

## RALPLAN-DR Summary

### Principles
1. **JSON is the canonical record of truth.** v1 scorecard `meta` stays byte-identical to spec §7. Manifest is the mutation surface. DuckDB is a derivable view.
2. **Additive-only in v2.** No existing scorecard fields change shape. v1 manifests remain readable by every new tool.
3. **Go produces, Python consumes.** Go owns the canonical `reports/` output (scorecards, manifests, sweep CSVs, DuckDB). Python reads DuckDB + writes to `tools/analyze/out/` — it **never** writes into `reports/`. The language boundary is also a filesystem boundary.
4. **"Unknown" is a valid value.** Remote / HF / API-only endpoints legitimately cannot reveal startup flags. The schema must accept and propagate "unknown" without hallucination.
5. **AI-orchestrated runs are a prompt, not a binary.** Run protocols and scrape protocols live in `docs/prompts/` — version-controlled markdown — so an AI CLI (or a human) can execute them without the harness shipping agent code.

### Decision Drivers
1. **Ship incrementally without regressing v1.** Every v2 milestone must leave v1 CLI/output intact so the 31-query benchmark keeps working on day one after merge.
2. **Analytical ergonomics.** A data scientist opening the repo should be one `duckdb reports/resolver.duckdb` or one `jupyter` command away from useful queries.
3. **Provenance is load-bearing.** For "same model, different parameters" comparisons to have any meaning, the parameter set must be captured accurately and the end-to-end chain (sidecar → manifest → DuckDB → analyzer prompt) must be integration-tested.

### Viable Options

**Option A — Wire metadata sidecar first, then CGO-isolated aggregator, then analyzer (CHOSEN; revised after Architect steelman).**
- *Pros:* Each tier independently shippable. Metadata capture adds value even if DuckDB work slips. The CGO surface is confined behind a build tag so the core `resolver` binary stays pure Go — v1's portability promise survives.
- *Cons:* Two Go binaries (`resolver` and `resolver` built with `-tags duckdb`) double the release artefact surface; mitigated by CI matrix.

**Option B — DuckDB-first against v1 scorecards, metadata later.**
- *Pros:* Proves out analytic surface against real-but-limited v1 data.
- *Cons (invalidating after Architect probe):* The strengthened steelman — "real data shapes the schema" — is addressable by using the 31 existing v1 scenarios as the schema-stress corpus *before* adding new columns. Iteration 2 adopts that insight: `run_config` is a **late-bound additive table** with no non-null constraints, letting the aggregator ship against v1-only data and absorb v2 fields as additional columns. Option B is thus partially folded into A rather than wholesale rejected.

**Option C — Python analyzer over raw JSON, no DuckDB.**
- *Pros:* Fastest time to a first report.
- *Cons (invalidating):* Duplicates ingest; inevitable retrofit. Also violates principle #3 (Python would be doing Go's ingest job).

**Option D — Parquet intermediate (pure-Go, no CGO) with DuckDB only as an external consumer.** (New, raised by Architect.)
- *Pros:* Zero CGO in the `resolver` binary; Python & DuckDB read Parquet natively.
- *Cons (non-invalidating but rejected for v2):* Parquet-Go libraries are less mature; no SQL-queryable artifact on disk without launching DuckDB/duckdb-cli separately; doubles the file formats the aggregator has to maintain. Revisit in v2.1 if CGO proves painful in practice.

---

## Requirements Summary

Extend the v1 resolver harness at `/home/code/hacking/resolver/` (commit `2408196`) with five additive capabilities:

1. **Manifest v2 + metadata capture** — bump `SchemaVersion` constant in `internal/manifest/manifest.go` from 1 to 2; add optional `runConfig` object (sidecar YAML); implement (new code, not scaffolded) `ResolveRealModel` probe in the openai-chat adapter: `GET {endpoint_origin}/v1/models`, 5s timeout, pulls `resp.data[0].id`, falls back to `"unknown"` on any failure. v1 manifests remain readable by every new tool.
2. **DuckDB aggregator behind a build tag** — `resolver aggregate` subcommand ingests every scorecard/manifest/sweep CSV under `reports/` into `reports/resolver.duckdb`. Lives behind `-tags duckdb`; default `go build ./...` produces a CGO-free binary whose `aggregate` subcommand prints a friendly stub message pointing at the tagged build.
3. **Python analyzer (`tools/analyze/`)** — `pyproject.toml` package with a CLI (`analyze report`) that produces `tools/analyze/out/analysis-{ts}.md` via an LLM call. Reads DuckDB + `reports/community-benchmarks.yaml`. Never writes under `reports/` (principle #3).
4. **Community-benchmark registry + AI orchestration prompts** — `reports/community-benchmarks.yaml` (hand-maintained, **append-only on the file**; the DuckDB `community_benchmarks` table is truncate-and-reloaded from that file on each aggregate run — the table is a derived mirror, the YAML is canonical); `docs/prompts/{run-benchmark,compare-models,scrape-community-benchmarks}.md` guide AI CLIs (or humans) through each stage.
5. **Threshold tweakability + reproducibility verification** — move hardcoded tier thresholds to embedded `gate-thresholds.yaml` + `--thresholds` flag with scorecard-level byte-parity regression; `-n N` flag extended to Tier 1 with replay golden fixture; companion reproducibility notebook.

Frozen contracts: scorecard `meta` key set, exit codes, v1 CLI flags, openai-chat adapter request/response shape.

---

## Acceptance Criteria

(Labels: `[G]` Go, `[Py]` Python, `[Y]` YAML, `[D]` Docs. `[v2-new]` = net-new code in v2; `[v2-change]` = in-place modification.)

### Phase 0 — Test fixtures (blocks Phase 4 integration tests)
- [ ] `[G][v2-new]` Commit `golden/canned-responses.json` — a 31-entry canned-response list keyed on scenarioId, captured from a real Tier 1 run (via a new `resolver --tier 1 --emit-replay FILE` flag, or hand-authored).
- [ ] `[G][v2-new]` Commit `golden/scorecard_example.json` — the scorecard that `resolver --tier 1 --replay golden/canned-responses.json` deterministically produces against v1. This is the byte-parity anchor for every later replay smoke test.
- [ ] `[G][v2-new]` Phase 0 smoke gate: `resolver --tier 1 --replay golden/canned-responses.json -o /tmp/v2 && diff /tmp/v2/*.json golden/scorecard_example.json` — exit 0, zero-byte diff. Required to be green before Phase 2+ work starts (and stays green throughout all v2 work — v1 parity guardrail).

### Phase 1 — Manifest v2 + metadata capture (Go)
- [ ] `[G][v2-change]` `internal/manifest/manifest.go`: bump const `SchemaVersion` (line 23 of current file) from `1` to `2`.
- [ ] `[G][v2-new]` Add `RunConfig` struct with fields `real_model, backend_port, thinking, mtp, context_size, tool_parser, reasoning_parser, quantization, proxy_recipe_path, vllm_recipe_path, notes` — all `string` (or `bool`/`int` where clearly typed), all optional. `notes` is free-form string; documented as non-structured.
- [ ] `[G][v2-new]` Add `*RunConfig` pointer field to `Manifest`; nil-able and omit-if-empty in JSON. Add `WithRunConfig(*RunConfig) *Builder` builder method.
- [ ] `[G][v2-new]` Add `LoadSidecar(path string) (*RunConfig, error)` helper in `internal/manifest/`.
- [ ] `[G][v2-change]` `cmd/resolver/main.go`: parse `--run-config PATH` flag; on missing file return exit-2 with actionable error; on presence load + attach to manifest builder.
- [ ] `[G][v2-new]` `internal/adapter/openai_chat.go`: new `(*OpenAIChat).ResolveRealModel(ctx context.Context) (string, error)` method — no probe code exists in v1. Issues `GET {endpoint_origin}/v1/models` (origin derived by parsing the completions URL), context deadline 5s, parses `resp.data[0].id`, returns string. Any error (transport / non-2xx / empty data / malformed JSON) → returns `"unknown"` + logs a single-line warning to stderr. Authorization header set only when `--api-key` present (same rule as v1 chat requests).
- [ ] `[G][v2-change]` Adapter caller invokes `ResolveRealModel` once at run start; value wired into manifest via `WithResolvedRealModel`.
- [ ] `[G][v2-new]` `internal/manifest/manifest_test.go` covers:
  - Full-roundtrip sidecar populated → manifest → JSON disk → reload → equal.
  - Missing sidecar file: `LoadSidecar` returns a wrapped error containing the path.
  - Probe success parses `{"data":[{"id":"backing-model-id"}]}` correctly.
  - Probe timeout (httptest server that `time.Sleep`s beyond ctx) → `"unknown"` + stderr log captured.
  - Probe non-2xx → `"unknown"`.
  - Probe malformed JSON → `"unknown"`.
- [ ] `[G][v2-new]` v1-manifest backward-compat test under `internal/aggregate/`: a committed v1 manifest fixture (no `runConfig`, `SchemaVersion=1`) ingests with no error, produces a DuckDB row with NULLs for the new columns.
- [ ] `[G][v2-new]` Forward-compat test: a synthetic manifest with `SchemaVersion=3` ingests with a single stderr warning but does not fail (best-effort column extraction; unknown fields ignored).
- [ ] `[D][v2-new]` `docs/manifest-schema.md` documents every field, type, required-ness, and explicit `"unknown"` semantics. Includes a "no secrets" note for `runConfig`.
- [ ] `[D][v2-new]` `docs/adr/0001-manifest-v2.md` records the schema bump + forward-compat rule.

### Phase 2 — DuckDB aggregator behind `-tags duckdb` (Go)
- [ ] `[G][v2-new]` New package `internal/aggregate/`. Imports `github.com/marcboeker/go-duckdb` but only in files with `//go:build duckdb` constraint. Non-tagged build provides a stub `Aggregate(...)` that returns an error "`aggregate` requires building with `-tags duckdb` (CGO). See docs/build.md."
- [ ] `[G][v2-new]` `go get github.com/marcboeker/go-duckdb` adds the module but **only the duckdb-tagged build links against it**. Untagged binary remains CGO-free (`file $(which resolver)` shows statically-linked ELF on Linux).
- [ ] `[G][v2-new]` Schema: tables `_meta` (schema_version row), `runs`, `queries`, `sweep_rows`, `run_config` (late-bound, no non-null constraints so v1 data ingests), `community_benchmarks`; virtual view `comparison` joining `runs ⋈ queries ⋈ run_config`. Migrations keyed on `_meta.schema_version`, idempotent.
- [ ] `[G][v2-new]` `resolver aggregate` subcommand (available only when built with `-tags duckdb`):
  - Walks `reports/results/*.json`, `reports/sweeps/*.csv`, `reports/**/manifests/*.json`.
  - Upserts one row per `runId` into `runs`; fans out per-scenario into `queries`; per-sweep-row into `sweep_rows`.
  - Reads `reports/community-benchmarks.yaml` and **truncate-and-reloads the `community_benchmarks` table** on each invocation (table is a derived mirror; YAML stays append-only).
  - Validates the YAML on load: schema check + `as_of ≤ today` lint; on failure, exits non-zero and does not touch the DB.
- [ ] `[G][v2-new]` `resolver aggregate --dry-run` lists new runIds without writing; exits 0.
- [ ] `[G][v2-new]` `resolver aggregate --db PATH` overrides the default `reports/resolver.duckdb`.
- [ ] `[G][v2-new]` `internal/aggregate/ingest_test.go`:
  - Idempotency: two consecutive runs yield identical row counts.
  - v1-compat: ingest of a v1-only fixture produces rows with NULLs for `runConfig`/`resolvedRealModel`.
  - `as_of > today` YAML → ingest fails cleanly, DB untouched.
  - `comparison` view: `SELECT COUNT(*) FROM comparison` equals expected on seeded fixture.
- [ ] `[G][v2-new]` `Makefile` (or `justfile`) target `build-all` runs both `go build ./...` (no tag) and `go build -tags duckdb ./...`; both must succeed in CI.
- [ ] `[D][v2-new]` `docs/build.md` explains the dual-binary build story + CGO prerequisites.

### Phase 3 — Threshold tweakability (Go)
- [ ] `[Y][v2-new]` `cmd/resolver/data/tier1/gate-thresholds.yaml` embedded via `//go:embed`:
  ```yaml
  thresholds:
    - { label: "T1+T2 > 90% (core routing)",          tiers: [T1,T2],   threshold: 90 }
    - { label: "T4+T5+T6 > 80% (safety calibration)", tiers: [T4,T5,T6],threshold: 80 }
    - { label: "T7 > 60% (health_check tool)",         tiers: [T7],      threshold: 60 }
    - { label: "T8 > 60% (node resolution)",           tiers: [T8],      threshold: 60 }
    - { label: "T10 > 60% (dependency reasoning)",     tiers: [T10],     threshold: 60 }
  ```
- [ ] `[G][v2-change]` `internal/scenario/thresholds.go` new file; `scenario.GatedTiers()` delegates to the YAML loader.
- [ ] `[G][v2-new]` `--thresholds PATH` flag; missing flag → embedded defaults.
- [ ] `[G][v2-new]` **Scorecard-level byte-parity test**: render a scorecard under the YAML-driven thresholds + `--replay golden/canned-responses.json`, `go-cmp` bytes against `golden/scorecard_example.json` — byte-identical. This is stronger than slice equality (Architect's concern).
- [ ] `[G][v2-new]` Override test: `resolver --tier 1 --thresholds testdata/strict.yaml --replay golden/canned-responses.json` produces a scorecard whose `summary.thresholds` rows reflect override values exactly; test asserts label + threshold + pass fields per row.

### Phase 4 — Reproducibility verification (Go)
- [ ] `[G][v2-change]` `-n N` flag, sweep-only in v1, extended to Tier 1. N scorecards written; filenames for k > 0 suffixed `-rep{k}` to avoid clobber.
- [ ] `[G][v2-change]` `manifest.runConfig.repeat_group = runId` (or dedicated field) so the aggregator can group the N repeats.
- [ ] `[G][v2-new]` Integration test: `resolver --tier 1 -n 3 --replay golden/canned-responses.json` emits 3 scorecards with byte-identical `summary` blocks (allowing `meta.timestamp` + `elapsedMs` deltas).
- [ ] `[Py][v2-new]` Reproducibility notebook reports per-query stddev. **Tolerance harmonized with spec:** `stddev ≤ 0.05` for the normalized score (correct=1.0 / partial=0.5 / incorrect=0.0 mapping) across N repeats at `temperature=0`.

### Phase 5 — Python analyzer (`tools/analyze/`)
- [ ] `[Py][v2-new]` `tools/analyze/pyproject.toml` declares: `duckdb>=1.0,<2.0`, `polars>=0.20,<2.0` (or `pandas>=2.0,<3.0`), `pyyaml>=6.0`, `openai>=1.0,<2.0`, `jinja2>=3.0`, `typer>=0.9` (CLI), `jupyter>=1.0`. Upper bounds pinned.
- [ ] `[Py][v2-new]` Package layout: `tools/analyze/src/analyze/{__init__,cli,db,report,bench}.py` + `tools/analyze/tests/`.
- [ ] `[Py][v2-new]` `analyze report` command:
  - Flags: `--db reports/resolver.duckdb`, `--out tools/analyze/out/analysis-{ts}.md` (default), `--reporter-model gresh-general` (default), `--endpoint` (default = `$RESOLVER_REPORTER_ENDPOINT` or `http://spark-01:4000/v1/chat/completions`), `--dry-run`.
  - Opens DuckDB; runs a fixed query set (ranking, per-tier summary, variance, community-benchmark join).
  - Renders the query results into the prompt template at `docs/prompts/compare-models.md` via Jinja.
  - In non-dry-run: POSTs to the endpoint via `openai` SDK (OpenAI-compatible); 60s timeout; on failure writes `(analysis failed: <reason>)` + raw data tables so the run is never wasted.
  - In `--dry-run`: renders the prompt + source-data block to stdout and exits 0 without any LLM POST.
  - Never writes under `reports/` (principle #3). Default out dir is `tools/analyze/out/`, created if missing.
  - CLI help output shows each default literal (asserted by a test so the user always knows what will happen).
- [ ] `[Py][v2-new]` `analyze query <SQL>` runs ad-hoc SQL against the DB (sanity helper).
- [ ] `[Py][v2-new]` Guard: if `model` under test (from `runs` table) equals `--reporter-model`, emit a stderr warning "reporter coincides with model-under-test — analysis may be self-biased" but proceed. Principle #4 / Non-Goal parity.
- [ ] `[Py][v2-new]` Notebooks:
  - `tools/analyze/notebooks/quickstart.ipynb` — load DB, group by model, plot tier pct.
  - `tools/analyze/notebooks/reproducibility.ipynb` — computes the stddev-tolerance check from Phase 4.
- [ ] `[Py][v2-new]` `tools/analyze/tests/`:
  - Fixture DB with 3 seeded runs; assert `analyze report --dry-run` emits the expected section headers (ranking, variance, benchmark join) and never calls out to a network.
  - Stub LLM endpoint via `httpx.MockTransport` or `responses`; no live API call in tests.
  - Default-flag assertion test: parse `--help`, assert `gresh-general` + URL appear.
- [ ] `[Py][v2-new]` `tools/analyze/README.md` with install (`pip install -e .`) and three first-run commands.

### Phase 6 — Community benchmarks + AI orchestration prompts
- [ ] `[Y][v2-new]` `reports/community-benchmarks.yaml` seeded with 3 reference models × 7 metrics = 21 entries. Reference models: `Qwen3.6-35B-A3B-FP8`, `Llama-3.1-70B-Instruct`, `Claude-Sonnet-4.6`. Each record: `{model, benchmark, metric, value, source_url, as_of, notes}`. All `as_of` dates ≤ 2026-04-19.
- [ ] `[G][v2-new]` Aggregator validates the YAML on ingest: missing required fields → error; `as_of > today` → error; model-alias map for case-insensitive / dash-variant matches (e.g. `Qwen3.6-35B-A3B-FP8` ≡ `qwen3.6-35b-a3b`).
- [ ] `[D][v2-new]` `docs/community-benchmarks-schema.md` documents schema + append-only rule + the alias-map convention.
- [ ] `[D][v2-new]` `docs/prompts/run-benchmark.md` — AI-CLI protocol: SSH to host → read `llm-proxy` config + `vllm` recipe → build `run.yaml` → invoke resolver → commit the triplet (scorecard + manifest + run.yaml).
- [ ] `[D][v2-new]` `docs/prompts/compare-models.md` — Jinja template for the analyzer prompt. Version-controlled.
- [ ] `[D][v2-new]` `docs/prompts/scrape-community-benchmarks.md` — AI-CLI protocol for appending a missing model/metric row to the YAML.
- [ ] `[D][v2-new]` README `v2: Comparing models` section links the analyzer README + the three prompt docs.

### Phase 7 — End-to-end provenance + verification
- [ ] `[G+Py][v2-new]` **End-to-end provenance integration test** (Architect action item):
  - Build `-tags duckdb`.
  - Run `resolver --tier 1 --run-config testdata/e2e-run.yaml --replay golden/canned-responses.json -o /tmp/e2e/reports/results`.
  - Run `resolver aggregate --db /tmp/e2e/resolver.duckdb`.
  - Run `analyze report --db /tmp/e2e/resolver.duckdb --dry-run` and capture stdout.
  - Assert the stdout contains every field value from `testdata/e2e-run.yaml` (real_model, tool_parser, reasoning_parser, etc.) — the chain sidecar → manifest → DuckDB → analyzer prompt is validated.
- [ ] `[G]` `go test ./...` (untagged) green.
- [ ] `[G]` `go test -tags duckdb ./...` green.
- [ ] `[G]` `go vet ./...` clean.
- [ ] `[G]` CI matrix runs `go build ./...` AND `go build -tags duckdb ./...` on at least `linux/amd64` and `darwin/arm64`.
- [ ] `[Py]` `pytest tools/analyze/tests/` green in a fresh venv.
- [ ] `[G]` Phase-0 byte-parity smoke still passes.
- [ ] `[D]` ADR `docs/adr/0002-duckdb-behind-build-tag.md` records the CGO build-tag decision + the Parquet alternative considered and deferred.

### Guardrails
- [ ] No scorecard `meta` keys change. No existing CLI flag default changes. No package path renames in `internal/**`.
- [ ] Analyzer process never writes into `reports/**`. (Covered by filesystem assertion in the Python test suite.)
- [ ] The default (`go build ./...`, CGO-free) binary continues to pass every v1 Phase-0 smoke.

---

## Implementation Steps

### Phase 0 — Golden fixtures (Go)
**Files:** `golden/canned-responses.json`, `golden/scorecard_example.json`, `cmd/resolver/main.go`.

1. Add `--emit-replay FILE` flag that records the live-run response set in replay shape so golden can be regenerated deterministically.
2. Run against `gresh-general` once with the current v1 binary to capture real responses; commit the JSON as golden.
3. Run `--replay` with that file to emit `scorecard_example.json`. Commit.
4. Phase-0 smoke test (`go test ./cmd/resolver -run TestGoldenReplay`) compares future runs byte-for-byte.

### Phase 1 — Manifest v2 + metadata capture (Go)
**Files:** `internal/manifest/manifest.go`, `internal/manifest/manifest_test.go`, `internal/adapter/openai_chat.go`, `cmd/resolver/main.go`, `docs/manifest-schema.md`, `docs/adr/0001-manifest-v2.md`, `testdata/sample-run.yaml`.

1. Bump `SchemaVersion` to `2`; add `RunConfig` struct + pointer field; add `WithRunConfig`, `LoadSidecar`.
2. New code in `internal/adapter/openai_chat.go`: `ResolveRealModel(ctx)` method. Parse endpoint origin → `GET /v1/models` → pluck `data[0].id` → return, or `"unknown"` on error.
3. `cmd/resolver/main.go`: parse `--run-config`; pre-run call `ResolveRealModel`; wire both into `manifest.NewBuilder(...)`.
4. Tests (six scenarios listed in AC).
5. Doc + ADR.

### Phase 2 — DuckDB aggregator behind `-tags duckdb`
**Files:** `internal/aggregate/{schema,ingest,ingest_test}.go` (tagged), `internal/aggregate/stub.go` (non-tagged fallback), `cmd/resolver/main.go`, `go.mod`, `go.sum`, `Makefile`, `docs/build.md`.

1. `go get github.com/marcboeker/go-duckdb`; gate inside files with `//go:build duckdb`.
2. Write stub in `stub.go` with `//go:build !duckdb` that satisfies the same signature + returns "build with -tags duckdb".
3. Schema + migrations + ingest walker.
4. CLI subcommand wired through the tag-gated package boundary.
5. Tests (idempotency, v1-compat, YAML lint, view round-trip).
6. Makefile target `build-all`.

### Phase 3 — Threshold tweakability
**Files:** `cmd/resolver/data/tier1/gate-thresholds.yaml`, `internal/scenario/thresholds.go`, `internal/scenario/scenario.go`, `cmd/resolver/main.go`, `internal/scenario/scenario_test.go`.

1. Author embedded YAML.
2. Load from embedded bytes or `--thresholds` override path.
3. Swap `GatedTiers()` call site.
4. Scorecard-byte-parity test (rendered bytes, not slice).

### Phase 4 — Reproducibility verification
**Files:** `cmd/resolver/main.go`, `internal/runner/executor.go`, integration test under `cmd/resolver/`.

1. Plumb `-n N` through Tier 1; filenames `-rep{k}` for k > 0.
2. Integration test uses replay.

### Phase 5 — Python analyzer
**Files:** whole new `tools/analyze/` directory.

1. Project skeleton.
2. `db.py` — DuckDB connector + fixed query set.
3. `report.py` — Jinja render + OpenAI-compatible POST + graceful-failure fallback.
4. `cli.py` — Typer CLI with `--dry-run`, `--reporter-model`, self-eval guard.
5. Notebooks.
6. Tests (stub LLM, default-flag assertion, filesystem-boundary check).

### Phase 6 — Docs + Community benchmarks + Prompts
**Files:** `reports/community-benchmarks.yaml`, three prompt files, README edits, `docs/community-benchmarks-schema.md`.

### Phase 7 — End-to-end verification
1. Run the e2e chain (Phase 0 golden → run with sidecar → aggregate → analyze --dry-run) and assert sidecar fields surface in the analyzer stdout.
2. `go test ./...` (both tag modes), `go vet`, `pytest`, re-run Phase-0 smoke.
3. ADR `0002-duckdb-behind-build-tag.md`.

---

## Risks and Mitigations

| # | Risk | Likelihood | Impact | Mitigation |
|---|------|-----------|--------|------------|
| 1 | `go-duckdb` CGO breaks cross-compile / inflates binary / blocks ARM macOS users | Medium | High | **Tested gate** (not documented): `-tags duckdb` isolates CGO; default binary is CGO-free; `Makefile build-all` runs both; CI matrix runs both on linux+darwin; docs/build.md explains the story. |
| 2 | Probe hits an endpoint that returns 401/403 or stalls | Low | Medium | 5s timeout; `"unknown"` on any non-2xx / error / malformed body; Authorization header only set when `--api-key` provided; single-line stderr warn, never in scorecard. |
| 3 | `runConfig` YAML contains secrets (paths that leak topology, bearer tokens) | Medium | Low | `docs/manifest-schema.md` explicitly documents "do not put secrets here"; the file is informational, not executable; users control whether sidecars are committed. |
| 4 | Community-benchmarks YAML drifts (stale `as_of`, missing model alias) | Medium | Medium | Aggregator schema-validates on ingest (required fields present, `as_of ≤ today`, alias map checked); CI has a lint target that runs the same validator standalone. |
| 5 | Reporter-LLM call hangs or returns nonsense | Medium | Medium | 60s timeout; `(analysis failed)` stub + raw data tables on failure so the run is never wasted; `--dry-run` skips the LLM call entirely and is a tested AC. |
| 6 | v1 manifest compatibility regresses silently | Low | High | Dedicated v1-compat test in `internal/aggregate/ingest_test.go` + Phase-0 byte-parity smoke — both gate CI. |
| 7 | Multi-turn `-n N` tier-1 scorecards clobber each other | Low | Medium | Scorecard filenames suffix `-rep{k}` for k > 0; first run keeps legacy name for single-run compatibility. |
| 8 | Python package dependency drift breaks notebooks across machines | Medium | Low | Pinned upper bounds in `pyproject.toml`; CI runs `pip install -e .` + `pytest` on a fresh venv. |
| 9 | `/v1/models` endpoint shape varies across providers (array vs `{data:[...]}`) | Low | Medium | Probe code parses the OpenAI-standard `{"data":[{"id":...}]}` explicitly; non-matching shapes fall through to `"unknown"`; future `ResolveRealModelFromProviderXYZ` adapters remain a v3 concern. |
| 10 | Reporter LLM coincides with model-under-test → self-evaluation bias | Medium | Medium | Python guard logs a stderr warning when model equals `--reporter-model`; user can override by setting a different reporter. Documented in analyzer README. |

---

## Verification Steps

All commands runnable from the repo root. Each gates the next phase.

1. `go vet ./... && go test ./... -count=1` — untagged unit tests green.
2. `go test -tags duckdb ./... -count=1` — tagged tests green (includes `internal/aggregate`).
3. **Phase-0 byte parity:** `resolver --tier 1 --replay golden/canned-responses.json -o /tmp/v2 && diff /tmp/v2/*.json golden/scorecard_example.json` — exit 0, empty diff.
4. **Sidecar probe smoke:** `resolver --tier 1 --run-config testdata/sample-run.yaml --replay golden/canned-responses.json` — manifest `runConfig` + `resolvedRealModel` populated (or `"unknown"`).
5. **Threshold override (scorecard-byte-level):** `resolver --tier 1 --thresholds testdata/strict.yaml --replay ...` — scorecard `summary.thresholds` rows reflect override; golden-diff test passes after threshold swap confirms byte parity when thresholds equal embedded defaults.
6. **Aggregator idempotency:** `resolver aggregate && resolver aggregate` → row counts identical; DB size stable; exit 0 both times.
7. **v1 backcompat:** point `aggregate` at a `reports/` with only v1-shaped manifests → exit 0, NULLs for v2 fields.
8. **Community-benchmarks lint:** deliberately set a future `as_of` in a test YAML → `aggregate` fails with exit 1 and a clear error; DB is untouched.
9. **Reproducibility:** `resolver --tier 1 -n 3 --replay ...` → 3 scorecards with identical `summary` blocks (modulo timestamp).
10. **Python analyzer:** `pip install -e tools/analyze && pytest tools/analyze/tests` green; `analyze report --dry-run --db testdata/seeded.duckdb` emits the expected markdown skeleton (no LLM call).
11. **End-to-end provenance (Architect gate):** the full chain described in Phase 7 AC — run → aggregate → `analyze report --dry-run` → grep for every `runConfig` field in the stdout.
12. **Live reporter LLM** (gated on `RESOLVER_REPORT_SMOKE=1`): `analyze report` against real DuckDB + real reporter LLM → markdown report written, no crashes.

---

## ADR — Architectural Decision Record

**Decision:** Ship v2 as five additive capabilities on top of v1 — golden replay fixtures + Phase-0 parity gate; manifest v2 with sidecar YAML + real probe; DuckDB aggregator **isolated behind `-tags duckdb`** so the default binary stays CGO-free; Python analyzer that writes under `tools/analyze/out/` only; community-benchmark registry + AI orchestration prompts. JSON scorecards stay canonical; DuckDB is a derivable view.

**Drivers:**
1. Ship incrementally without regressing v1's byte-exact spec §7 parity.
2. Analytical ergonomics: one command to DuckDB or Jupyter.
3. Provenance is load-bearing — end-to-end (sidecar → manifest → DuckDB → analyzer prompt) integration-tested.

**Alternatives considered:**
- **Option B (DuckDB-first against v1 data).** Partially *folded into* Option A via late-bound `run_config` table (no non-null constraints, works for v1 data too) rather than wholesale rejected.
- **Option C (Python analyzer over raw JSON).** Rejected: duplicates ingest + violates principle #3.
- **Option D (Parquet intermediate, no CGO).** Rejected for v2: less mature Go libraries, still need DuckDB externally to query. Revisit in v2.1 if CGO friction is painful in practice.
- **Single-language Python rewrite.** Not seriously considered: v1 is Go; rewrite inflates scope without benefit.
- **Built-in scraper for community benchmarks.** Rejected by interview: hand-curated YAML + AI-driven append is the right shape.

**Why chosen:** Option A is the only path that (a) lets each capability land as a shippable milestone without breaking v1, (b) confines CGO to a build-tagged surface so the default distributable binary stays pure Go, (c) enforces principle #3 as a filesystem boundary (`tools/analyze/out/` vs `reports/`), and (d) end-to-end-tests provenance before shipping. Architect + Critic consensus: APPROVED after REVISE iteration.

**Consequences:**
- Positive: v1 consumers see zero breakage; v2 consumers gain queryability + LLM-authored analysis; reproducibility quantifiable; CGO isolated; provenance chain test-locked.
- Negative: Two Go build targets (tagged + untagged) slightly inflate release process; Python package adds a second deployment surface; community-benchmark YAML requires periodic manual updates (but that's the design).
- Known v2 limitations: reporter LLM output is non-deterministic (by nature); analyzer requires internet only if a remote reporter LLM is used; community-benchmark scraping is still manual or AI-assisted, not automated in CI.

**Follow-ups (v2.1 and v3):**
- v2.1: CI job that runs the analyzer nightly against a curated snapshot; more reference models in the benchmark YAML; optional Parquet export alongside DuckDB (re-evaluate Option D); pre-run YAML lint as a standalone `resolver lint` subcommand.
- v3: Anthropic / openclaw / hf-serverless adapters; HITL approval flows; LLM-as-judge verdicts for subjective criteria; sweeps C/D/E; adapter-agnostic scenario abstraction layer.

---

## Changelog

- **v1 (Planner draft, iteration 1):** initial plan derived from deep-interview spec `resolver-v2-comparison-2026-04-19`.
- **v2 (iteration 2, current — CONSENSUS APPROVED):** folded Architect + Critic improvements:
  - Added explicit Phase 0 for golden replay fixtures (`golden/canned-responses.json`, `golden/scorecard_example.json`) — enables every later byte-parity smoke.
  - Fixed wording around `resolvedRealModel`: v1 has **only the builder**, no probe code — Phase 1 writes new probe code.
  - Isolated DuckDB behind `-tags duckdb`; default binary stays CGO-free; CI matrix tests both modes. `Makefile build-all` target.
  - Reconciled community-benchmarks YAML semantics: the **file is append-only**; the **DuckDB table is truncate-and-reloaded** (derived mirror).
  - Added `analyze report --dry-run` as a tested AC + reporter-model default AC.
  - Reporter-vs-model-under-test self-evaluation guard (stderr warning) — honors Non-Goal.
  - Added forward-compat `manifestVersion > 2` ingest test + explicit "notes" free-form field handling + `as_of ≤ today` CI lint.
  - Principle #3 clarified into a filesystem boundary: Python writes to `tools/analyze/out/`, never into `reports/`. Analyzer tests assert it.
  - Scorecard-level byte-parity test (rendered bytes, not slice) for the threshold-YAML swap — Architect's concern.
  - End-to-end provenance integration test added as Phase 7 AC — runConfig field surfaces all the way into analyzer stdout.
  - Reproducibility tolerance reconciled: spec/plan both say `stddev ≤ 0.05` at temperature=0.
  - Added Option D (Parquet intermediate) to Viable Options with explicit deferral rationale.
  - ADRs numbered and given paths: `docs/adr/0001-manifest-v2.md`, `docs/adr/0002-duckdb-behind-build-tag.md`.
  - Added risks #9 (/v1/models shape variance) and #10 (reporter == model-under-test self-eval bias).
