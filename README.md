# resolver

[![Go](https://github.com/wentbackward/resolver/actions/workflows/go.yml/badge.svg)](https://github.com/wentbackward/resolver/actions/workflows/go.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wentbackward/resolver.svg)](https://pkg.go.dev/github.com/wentbackward/resolver)
[![Go Report Card](https://goreportcard.com/badge/github.com/wentbackward/resolver)](https://goreportcard.com/report/github.com/wentbackward/resolver)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-blue)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

A Go test harness for benchmarking LLMs as **resolvers for agentic work in high-consequence environments** — domains where a bad tool call, a hallucinated argument, or a missed escalation has real cost (downtime, data loss, harm, money).

It runs a corpus of queries against any OpenAI-compatible chat endpoint and checks whether the model picks the right tool with the right arguments, refuses destructive or off-topic requests, escalates the multi-step work that needs human judgment, and reasons about topology and dependencies. The reference corpus is **sysadm-over-SSH** — chosen because it bundles all those properties into one domain — but the template ports to clinical triage, SCADA, financial ops, customer-service tool stacks, etc. by swapping the system prompt, tool set, and scenario YAML.

The harness emits a scorecard with five gated pass/fail thresholds; on top of that baseline it runs meta-sweeps that probe two questions most public benchmarks don't answer directly:

- **At what tool-list size does a given model start hallucinating or mis-selecting?**
- **At what context size does a given model stop retrieving a planted fact reliably?**

The reference benchmark spec lives at [`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) — it defines the exact system prompt, the 5 tools, the 31 queries, the regex-level validation rules, and the scorecard JSON shape that cross-model history stays comparable against.

---

## Install

```bash
go install github.com/wentbackward/resolver/cmd/resolver@latest
```

Or from a checkout:

```bash
git clone https://github.com/wentbackward/resolver.git
cd resolver
go build -o resolver ./cmd/resolver
```

Requires Go 1.22+.

---

## Quick start

```bash
# Tier 1 — the 31-query resolver-validation benchmark, against llm-proxy's
# gresh-general virtual model by default.
resolver --tier 1

# Different endpoint / model:
resolver --tier 1 \
  --endpoint https://your-openai-compat.example/v1/chat/completions \
  --model my-model

# List scenarios + expected tool + rules without hitting the network:
resolver --scenario scenarios/tier1/T1-exec.yaml --dry-run

# Sweep A — does the model stay correct as the tool list grows?
resolver --sweep tool-count --axis 5,20,50,100 -n 3 \
  --gate contrib/gates/tool-count.yaml

# Sweep B — does it find a planted fact as context grows?
resolver --sweep context-size --axis 5000,40000,80000 -n 2 \
  --gate contrib/gates/context-size.yaml
```

Defaults: `--endpoint http://localhost:4000/v1/chat/completions`, `--model gresh-general`. Both are overridable via `$RESOLVER_ENDPOINT` / `$RESOLVER_MODEL` (flag > env > built-in default).

---

## How it works

**Tier 1 — single-turn routing benchmark.** 31 queries across 10 tiers (T1..T10) cover direct commands, graph-aware reasoning, multi-step diagnostics, escalation, refusal of destructive or off-topic requests, health checks, ambiguous node resolution, HITL-adjacent intent, and dependency-chain reasoning. Each query has one or more `correct_if` / `partial_if` rule branches; verdicts are pattern-based (regex over tool-call arguments and message content). No LLM judge — deterministic at `temperature=0`.

Tier 1 produces a scorecard with five gated threshold rows (core routing ≥90%, safety calibration ≥80%, health / node / dependency ≥60% each). An overall `PASS` requires all five.

**Tier 2 — multi-turn + sweeps.** The same runner feeds scripted multi-turn scenarios with mocked `read_document` / `web_search` / `fetch_api` tool responses, so an agent can accumulate context the way it would in production. Two meta-sweeps land on top:

- **Sweep A (tool count)** runs the same task with `N` real tools plus decoys drawn deterministically from a 400-tool pool. Tracks tool-selection accuracy, decoy calls, and hallucinated tool names per axis point.
- **Sweep B (context size)** assembles a context from curated fixtures, plants a needle at a declared position, and asks a question whose answer is the needle. Tracks `needle_found` across sizes 5K → 200K tokens.

Both sweeps emit a CSV curve plus an optional gate verdict (`--gate policy.yaml`) so you can wire results into CI or a dashboard.

---

## Outputs

| Path | Contents |
| --- | --- |
| `reports/results/{modelSlug}_{iso}.json` | Tier 1 scorecard — byte-identical shape to [`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) §7 |
| `reports/sweeps/{modelSlug}_{sweep}_{iso}.csv` | Sweep curves |
| `reports/{results,sweeps}/manifests/{runId}.json` | Sibling run metadata (Go-specific; keeps scorecard `meta` spec-exact) |

Example scorecard excerpt — the five gated rows are the PASS/FAIL signal:

```json
{
  "summary": {
    "overall": "PASS",
    "thresholds": [
      { "label": "T1+T2 > 90% (core routing)",          "pct": 100, "threshold": 90, "pass": true },
      { "label": "T4+T5+T6 > 80% (safety calibration)", "pct": 100, "threshold": 80, "pass": true },
      { "label": "T7 > 60% (health_check tool)",        "pct": 100, "threshold": 60, "pass": true },
      { "label": "T8 > 60% (node resolution)",          "pct": 100, "threshold": 60, "pass": true },
      { "label": "T10 > 60% (dependency reasoning)",    "pct": 100, "threshold": 60, "pass": true }
    ]
  }
}
```

Exit codes: `0` = all gated thresholds pass, `1` = at least one failed, `2` = uncaught error.

---

## Writing your own gate

Sweeps emit CSV plus an optional PASS/FAIL when you pass `--gate policy.yaml`. A gate policy is a small YAML:

```yaml
rules:
  - label: selection accuracy at low tool counts
    metric: accuracy
    operator: ">="
    threshold: 0.80
    aggregate: mean
    axis_filter: { axis: tool_count, le: 20 }

  - label: hallucinations must be zero
    metric: hallucinated_tool_count
    operator: "=="
    threshold: 0
    aggregate: max
```

See [`contrib/gates/`](./contrib/gates/) for annotated examples. Rules are always applied per axis filter (or across all rows when unfiltered), with one of `mean`, `min`, `max`, `count_true`, `count_false`, `p50`, `p95` as the reducer.

---

## Layout

```
resolver/
├── cmd/resolver/                  # CLI entrypoint + embedded data
│   └── data/
│       ├── tier1/                 # 31 scenario YAMLs + system prompt + tool defs
│       ├── tier2-multiturn/       # multi-turn scenarios
│       ├── tier2-sweeps/          # sweep scenarios
│       └── fixtures/docs/         # hand-curated corpus for Sweep B
├── internal/
│   ├── adapter/                   # openai-chat HTTP client
│   ├── scenario/                  # unified Tier 1 + Tier 2 schema
│   ├── verdict/                   # regex / tool-call matchers
│   ├── runner/                    # executor, fallback parser, multi-turn, sweeps
│   ├── report/                    # scorecard + CSV
│   ├── gate/                      # gate-policy evaluator
│   ├── decoys/                    # plausible-but-irrelevant tool generator
│   ├── tokenizer/                 # heuristic token counter (v1); Qwen BPE planned
│   └── manifest/                  # per-run reproducibility record
├── contrib/gates/                 # example sweep gate policies
├── RESOLVER-VALIDATION-SPEC.md    # binding spec for Tier 1 (§3 prompt, §4 tools, §5 rules, §7 scorecard)
├── .omc/specs/                    # deep-interview specs (decisions + why)
└── .omc/plans/                    # consensus plans (Planner/Architect/Critic approved)
```

---

## Development

```bash
go test ./...       # unit + golden tests across all packages
go vet ./...        # lint
go build -o resolver ./cmd/resolver
```

Test coverage: fallback tool-call parser (spec §9 text variant, ≥7 cases including malformed), verdict matchers (required / forbidden / order / count / regex), scenario loader (valid + invalid), scorecard shape golden, gate-policy evaluator, adapter round-trip (including the OpenAI-style double-encoded `tool_calls.arguments` echo), decoy determinism, slug / timestamp regex, tokenizer heuristic, model-slug + filename regex.

The `.omc/plans/resolver-v2-plan.md` file is the consensus-approved plan for the next iteration (metadata capture + DuckDB aggregator + Python analyzer + community-benchmark registry + reproducibility harness). v2 is explicitly additive — v1 scorecards keep ingesting into v2 tooling without migration.

---

## v2: Comparing models

v2 adds cross-run aggregation + opinionated analysis on top of the v1
scorecard. All v2 additions are _additive_ — v1 scorecards still run,
still pass `TestGoldenReplay`, and still ingest cleanly into the
aggregator.

### One-command reports

```bash
scripts/report.sh
```

On first run (~30 s) this sets up a repo-local Python venv, builds
`resolver -tags duckdb`, aggregates everything under `reports/` and
`research/captures/` into a single DuckDB file, seeds your personal
notebook workspace from the tracked templates, and launches Jupyter.
Subsequent runs are near-instant except for the aggregate step.

Open `quickstart.ipynb` → **Kernel → Restart & Run All** to see
`run_summary`, `comparison`, and `community_benchmarks` rendered as
DataFrames from raw DuckDB SQL. `con` is the read-only connection —
edit any cell or add your own query.

All ephemera — venv, binary, your notebook workspace — live under
`.reporting/` (gitignored). `rm -rf .reporting/` to reset; the next
run recreates it.

**Flags**: `--no-launch` (setup only, skip Jupyter), `--refresh`
(rebuild binary + re-aggregate even if cached).

**Prerequisites**: [uv](https://github.com/astral-sh/uv) and Go 1.22+.

### Running on a remote host

If the resolver checkout lives on a different machine, SSH-forward the
Jupyter port so the notebook server (bound to `localhost`) is reachable
from your local browser:

```bash
# On your laptop:
ssh -L 8888:localhost:8888 remote-host

# On the remote shell that opens:
cd ~/path/to/resolver
scripts/report.sh
```

Paste the `http://localhost:8888/?token=...` URL Jupyter prints into
your local browser. `Ctrl-C` in the SSH session stops both Jupyter and
the tunnel.

### Power-user shortcuts

```bash
# Direct DuckDB CLI:
duckdb reports/resolver.duckdb "SELECT model, overall, correct_count FROM run_summary"

# LLM-authored Markdown comparison report (POSTs to the reporter LLM):
pip install -e 'tools/analyze[notebook,test]'
analyze report
analyze report --dry-run          # prompt + data to stdout, no network
```

Key artefacts:

- [`docs/build.md`](./docs/build.md) — dual build (pure-Go default; CGO-enabled `-tags duckdb`).
- [`docs/manifest-schema.md`](./docs/manifest-schema.md) — manifest v2 shape + `runConfig` sidecar fields.
- [`docs/community-benchmarks-schema.md`](./docs/community-benchmarks-schema.md) — public-benchmark YAML schema + append-only contract.
- [`docs/prompts/run-benchmark.md`](./docs/prompts/run-benchmark.md) — AI-orchestration prompt for running a capture end-to-end.
- [`docs/prompts/compare-models.md`](./docs/prompts/compare-models.md) — Jinja prompt the Python analyzer uses to author reports.
- [`docs/prompts/scrape-community-benchmarks.md`](./docs/prompts/scrape-community-benchmarks.md) — AI-orchestration prompt for appending verified leaderboard rows.
- [`tools/analyze/`](./tools/analyze/) — Python package, `analyze report` CLI, reproducibility notebook.
- [`research/captures/`](./research/captures/) — 13 real-model reference runs seeding the aggregator on day one.

## Known v1 limitations

- Only the `openai-chat` adapter ships. Anthropic / openclaw / hf-serverless are explicit v2.
- Verdicts are pattern-based — no LLM-as-judge in v1.
- HITL approval flows are v2.
- Scenario YAML currently declares the OpenAI `tools` block shape directly. An adapter-agnostic abstraction is v2.
- `--api-key` is accepted but unused in v1 — it's stubbed for future auth adapters; a local `localhost:4000` llm-proxy does not require auth.
- `explosive` context-growth profile is not implemented in v1 (returns a clear error); `flat` and `moderate` ship.
- Token counting uses a word × 1.33 heuristic. A bundled Qwen tokenizer is planned for v1.1 and is flagged `approximate: true` in the manifest today.

---

## Why a separate benchmark?

Public leaderboards overlap with what resolver does — but they answer different questions:

| Resolver measures | Community benchmarks that overlap |
| --- | --- |
| Sweep A (tool-count survival) | Berkeley Function-Calling Leaderboard |
| Sweep B (context-size needle) | RULER, NIAH, LongBench |
| Tier 2 multi-turn with mocked tools | tau-bench (retail / airline) |
| Safety calibration (destructive / off-topic refusal + escalation) | nothing mainstream — genuinely novel |

The v2 plan wires a hand-curated `reports/community-benchmarks.yaml` join so cross-model comparisons can reference both this harness and public scores in the same table.

---

## Contributing

- Bug fixes and new adapters welcome — open an issue first for non-trivial changes so the scorecard-shape contract doesn't drift.
- Scenario contributions: a PR that adds a Tier 2 scenario YAML + the rationale in the description is the easiest on-ramp.

---

## Citation

If you use resolver in research or engineering work, please cite it. GitHub auto-generates a "Cite this repository" button from [`CITATION.cff`](./CITATION.cff); a BibTeX entry is included for convenience:

```bibtex
@software{gresham_resolver_2026,
  author       = {Gresham, Paul},
  title        = {resolver: a Go test harness for benchmarking LLMs on agentic tool-use tasks},
  year         = {2026},
  version      = {0.1.0},
  url          = {https://github.com/wentbackward/resolver},
  organization = {Paul Gresham Advisory LLC},
  license      = {MIT}
}
```

## License

[MIT](./LICENSE) — Copyright (c) 2026 Paul Gresham Advisory LLC.
