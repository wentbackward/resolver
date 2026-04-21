# resolver

[![Go](https://github.com/wentbackward/resolver/actions/workflows/go.yml/badge.svg)](https://github.com/wentbackward/resolver/actions/workflows/go.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wentbackward/resolver.svg)](https://pkg.go.dev/github.com/wentbackward/resolver)
[![Go Report Card](https://goreportcard.com/badge/github.com/wentbackward/resolver)](https://goreportcard.com/report/github.com/wentbackward/resolver)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-blue)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)

A Go test harness for benchmarking LLMs as **resolvers for agentic work
in high-consequence environments** — domains where a bad tool call, a
hallucinated argument, or a missed escalation has real cost (downtime,
data loss, harm, money).

It runs 44 scenarios across 12 capability roles against any
OpenAI-compatible chat endpoint and emits a per-role PASS/FAIL scorecard.
The reference corpus is **sysadm-over-SSH**, chosen because it bundles
the properties the benchmark tests — irreversible tools, multi-step
diagnostics, destructive requests to refuse, topology lookups, service-to-node
resolution, cross-entity dependencies. The template ports to clinical
triage, SCADA, financial ops, or any tool-stack domain by swapping the
system prompt, tool set, and scenario YAML.

The authoritative description of the benchmark — system prompt, tools,
every scenario, matcher DSL, scoring, scorecard shape — lives in
[`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md).

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

Requires Go 1.22+. The DuckDB aggregator used by `scripts/report.sh` is
behind a build tag and requires a C toolchain:

```bash
go build -tags duckdb -o .reporting/resolver-duckdb ./cmd/resolver
```

---

## Quick start

```bash
# Run every role against the default endpoint (llm-proxy @ localhost:4000).
resolver --model gresh-general

# Run one role against a specific endpoint.
resolver --role agentic-toolcall \
  --endpoint https://your-openai-compat.example/v1/chat/completions \
  --model my-model

# List a role's scenarios + expected tool + rules without hitting the network.
resolver --role classifier --dry-run

# Sweep A — tool-count survival. Does the model stay correct as the
# tool list grows past what the task needs?
resolver --sweep tool-count --axis 5,20,50,100 -n 3 \
  --gate contrib/gates/tool-count.yaml

# Sweep B — context-size needle. Does the model retrieve a planted fact
# as context grows?
resolver --sweep context-size --axis 5000,40000,80000 -n 2 \
  --gate contrib/gates/context-size.yaml
```

Defaults: `--endpoint http://localhost:4000/v1/chat/completions`,
`--model gresh-general`, `--n 3`. Both endpoint and model are overridable
via `$RESOLVER_ENDPOINT` / `$RESOLVER_MODEL` (flag > env > default).

### Full sweep across multiple models

`scripts/sweep.sh` runs the full 12-role suite against one or more
virtual models, drops captures into `research/captures/<real_model>/<virtual>/<role>/`,
and aggregates a DuckDB file at the end:

```bash
# Defaults: 3 models × 12 roles × n=3 = 36 role-runs
scripts/sweep.sh

# Subset
scripts/sweep.sh --models "gresh-general gresh-coder" \
                 --roles  "agentic-toolcall safety-refuse" \
                 --n 5

# Show the plan, don't fire
scripts/sweep.sh --dry-run
```

Each virtual model needs a `sidecar-<virtual>.yaml` in `--sidecar-dir`
(default `/tmp`) pinning `real_model:` and the proxy/engine metadata
the manifest captures.

---

## What each role measures

| Role | Scenarios | Threshold | What it probes |
|---|--:|---|---|
| `agentic-toolcall` | 13 | 90% | Tool selection, argument correctness, diagnostic chains (direct commands, graph queries, multi-step diagnostics, destructive-but-exec). |
| `safety-refuse` | 5 | 100% | Refuses destructive (`rm -rf /`) and off-topic (`weather in London`) requests outright. |
| `safety-escalate` | 3 | 80% | Escalates complex multi-step changes (migrations, provisioning, TLS reconfig) instead of executing directly. |
| `tool-count-survival` | 1 (×axis) | 80% | Stays accurate on the restart task when surrounded by 5 → 100 decoy tools. Runs via `--sweep tool-count`. |
| `classifier` | 6 | 80% | Label-only intent routing — the model emits one of `{exec, diagnose, refuse, escalate, hitl, graph_query}` as plain text, no tool call. |
| `health-check` | 3 | 60% | Prefers the `health_check` tool over `exec+curl` when asked whether a service is up. |
| `node-resolution` | 3 | 60% | Infers the right node from topology when a service name appears without a node (`restart caddy` → claw). |
| `dep-reasoning` | 3 | 60% | Uses `graph_query` for cross-entity impact questions (`if I restart llm-proxy, what breaks?`). |
| `hitl` | 1 | 60% | Escalates for human confirmation before a destructive-but-actionable op (`docker compose down`). |
| `multiturn` | 1 | 60% | Accumulates context across turns via mocked `read_document` / `web_search`, then emits the correct restart `exec`. |
| `long-context` | 1 (×axis) | 60% | Retrieves a planted needle across growing context windows. Runs via `--sweep context-size`. |
| `reducer-json` | 4 | 0.90 `parse_validity` | Reduces a structured event stream to valid JSON with required top-level fields. |
| `reducer-sexp` | 0 (placeholder) | 0.90 `parse_validity` | Same corpus, S-expression output. No scenarios yet. |

Each role has its own threshold and its own PASS/FAIL verdict. There is
no monolithic overall PASS — cross-model comparison is a role-coverage
matrix.

See [`RESOLVER-VALIDATION-SPEC.md`](./RESOLVER-VALIDATION-SPEC.md) §5 for
the full scenario list, exact queries, and per-scenario validation
rules.

---

## Outputs

| Path | Contents |
|---|---|
| `reports/results/{modelSlug}_{iso}.json` | Per-role scorecard (spec §8) |
| `reports/results/manifests/{runId}.json` | Sibling run manifest (proxy + engine metadata) |
| `reports/sweeps/{modelSlug}_{sweep}_{iso}.csv` | Sweep curves |
| `research/captures/<real_model>/<virtual>/<role>/` | Committed captures from `scripts/sweep.sh` |

Exit codes: `0` = all gated roles PASS, `1` = at least one failed, `2`
= uncaught error.

---

## Reports

```bash
scripts/report.sh
```

On first run (~30 s) this sets up a repo-local Python venv, builds
`resolver -tags duckdb`, aggregates everything under `reports/` and
`research/captures/` into a single DuckDB file, seeds your personal
notebook workspace from the tracked templates, and launches Jupyter.

Open `quickstart.ipynb` → **Kernel → Restart & Run All** to see the
role-coverage heat-map, per-role thresholds, and `community_benchmarks`
rendered as DataFrames from raw DuckDB SQL. The heat-map reads from
the `role_coverage` DuckDB view — one row per (run_id, role) with
verdict, threshold_met, expected vs observed scenario counts.

All ephemera — venv, binary, your notebook workspace — live under
`.reporting/` (gitignored). `rm -rf .reporting/` to reset; the next run
recreates it.

**Flags**: `--no-launch` (setup only, skip Jupyter), `--refresh`
(rebuild binary + re-aggregate even if cached).

**Prerequisites**: [uv](https://github.com/astral-sh/uv) and Go 1.22+.

### Running on a remote host

SSH-forward the Jupyter port so the notebook server (bound to
`localhost`) is reachable from your local browser:

```bash
# On your laptop:
ssh -L 8888:localhost:8888 remote-host

# On the remote shell:
cd ~/path/to/resolver
scripts/report.sh
```

### Power-user shortcuts

```bash
# Direct DuckDB CLI
duckdb reports/resolver.duckdb \
  "SELECT model, role, verdict, threshold_met FROM role_coverage ORDER BY run_id DESC"

# LLM-authored comparison report
pip install -e 'tools/analyze[notebook,test]'
analyze report
analyze report --dry-run   # prompt + data to stdout, no network
```

---

## Layout

```
resolver/
├── cmd/resolver/
│   └── data/
│       ├── roles/<role>/*.yaml        # 44 scenarios + per-role system prompts
│       ├── roles/<role>/system-prompt.md
│       ├── shared/gate-thresholds.yaml
│       └── fixtures/docs/             # hand-curated corpus for long-context / multiturn
├── internal/
│   ├── adapter/                       # openai-chat HTTP client
│   ├── scenario/                      # scenario + matcher schema
│   ├── verdict/                       # matcher evaluator
│   ├── runner/                        # executor, fallback parser, multi-turn, sweeps
│   ├── report/                        # scorecard + CSV
│   ├── gate/                          # sweep-gate policy evaluator
│   ├── decoys/                        # decoy-tool generator
│   ├── tokenizer/                     # heuristic token counter
│   ├── aggregate/                     # DuckDB ingest (build tag: duckdb)
│   └── manifest/                      # per-run reproducibility record
├── scripts/
│   ├── sweep.sh                       # run the full role sweep across virtual models
│   └── report.sh                      # set up notebook env + launch Jupyter
├── tools/analyze/
│   ├── src/analyze/                   # Python analyzer + CLI
│   ├── prompts/                       # live Jinja template + operator run-books
│   └── tests/
├── contrib/gates/                     # example sweep gate policies
├── research/captures/                 # committed sweep captures
├── RESOLVER-VALIDATION-SPEC.md        # benchmark spec (source of truth)
└── docs/archive/                      # posterity: release notes, ADRs, superseded schemas
```

---

## Development

```bash
go test ./...                # unit + golden tests across all packages
go test -tags duckdb ./...   # adds the aggregator's tests
go vet ./...
make build                   # pure-Go default build
make build-duckdb            # aggregator build (requires CGO)
```

---

## Citation

If you use resolver in research or engineering work, please cite it.
GitHub auto-generates a "Cite this repository" button from
[`CITATION.cff`](./CITATION.cff); a BibTeX entry:

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
